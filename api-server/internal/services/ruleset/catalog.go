package ruleset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	catalogRepo  = "MetaCubeX/meta-rules-dat"
	catalogRef   = "meta"
	catalogTTL   = 12 * time.Hour
	maxDownload  = 32 << 20
	maxEntries   = 50000
	fetchTimeout = 20 * time.Second
)

// CatalogEntry is one rule set published in MetaCubeX meta-rules-dat.
type CatalogEntry struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Behavior string `json:"behavior"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

// Catalog lists MetaCubeX rule sets through GitHub's tree API, which is the
// only listing that is not truncated for this repository; results are cached
// because unauthenticated GitHub calls are rate limited.
type Catalog struct {
	client  *http.Client
	baseURL string
	cdnBase string

	mu      sync.Mutex
	entries []CatalogEntry
	fetched time.Time
}

func NewCatalog() *Catalog {
	return &Catalog{
		client:  &http.Client{Timeout: fetchTimeout},
		baseURL: "https://api.github.com",
		cdnBase: "https://cdn.jsdelivr.net/gh/" + catalogRepo + "@" + catalogRef + "/geo",
	}
}

func (c *Catalog) Entries(ctx context.Context) ([]CatalogEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries != nil && time.Since(c.fetched) < catalogTTL {
		return c.entries, nil
	}
	root, err := c.tree(ctx, catalogRef)
	if err != nil {
		return c.stale(err)
	}
	geoSHA := ""
	for _, e := range root {
		if e.Path == "geo" && e.Type == "tree" {
			geoSHA = e.SHA
		}
	}
	if geoSHA == "" {
		return c.stale(errors.New("geo directory not found"))
	}
	geo, err := c.tree(ctx, geoSHA)
	if err != nil {
		return c.stale(err)
	}
	var out []CatalogEntry
	for _, dir := range geo {
		if dir.Type != "tree" || (dir.Path != "geosite" && dir.Path != "geoip") {
			continue
		}
		files, err := c.tree(ctx, dir.SHA)
		if err != nil {
			return c.stale(err)
		}
		behavior := "domain"
		if dir.Path == "geoip" {
			behavior = "ipcidr"
		}
		for _, f := range files {
			name, ok := strings.CutSuffix(f.Path, ".mrs")
			if f.Type != "blob" || !ok {
				continue
			}
			out = append(out, CatalogEntry{
				Kind: dir.Path, Name: name, Behavior: behavior, Size: f.Size,
				URL: fmt.Sprintf("%s/%s/%s.mrs", c.cdnBase, dir.Path, name),
			})
		}
	}
	slices.SortFunc(out, func(a, b CatalogEntry) int {
		if a.Kind != b.Kind {
			return strings.Compare(b.Kind, a.Kind)
		}
		return strings.Compare(a.Name, b.Name)
	})
	c.entries, c.fetched = out, time.Now()
	return out, nil
}

// stale serves the previous listing when a refresh fails, so a rate-limited
// GitHub does not empty the picker.
func (c *Catalog) stale(err error) ([]CatalogEntry, error) {
	if c.entries != nil {
		return c.entries, nil
	}
	return nil, err
}

type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

func (c *Catalog) tree(ctx context.Context, sha string) ([]treeEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/git/trees/%s", c.baseURL, catalogRepo, url.PathEscape(sha)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "proxima-vpn-admin")
	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github tree %s: %s", sha, res.Status)
	}
	var body struct {
		Tree      []treeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxDownload)).Decode(&body); err != nil {
		return nil, err
	}
	return body.Tree, nil
}

// allowedHosts are the rule-set mirrors the admin viewer may fetch. The list is
// closed so the endpoint cannot be used to make the server request arbitrary
// URLs (internal addresses included).
var allowedHosts = map[string]bool{
	"cdn.jsdelivr.net":              true,
	"fastly.jsdelivr.net":           true,
	"testingcf.jsdelivr.net":        true,
	"gcore.jsdelivr.net":            true,
	"raw.githubusercontent.com":     true,
	"github.com":                    true,
	"objects.githubusercontent.com": true,
}

// ErrHostNotAllowed is returned for URLs outside allowedHosts.
var ErrHostNotAllowed = errors.New("only jsDelivr and GitHub rule-set URLs can be viewed")

// Contents is a decoded rule set ready for display.
type Contents struct {
	Behavior  string   `json:"behavior"`
	Format    string   `json:"format"`
	Count     int      `json:"count"`
	Entries   []string `json:"entries"`
	Truncated bool     `json:"truncated"`
}

// Fetcher downloads and decodes rule sets from allowed mirrors.
type Fetcher struct {
	client *http.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{client: &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 || req.URL.Scheme != "https" || !allowedHosts[req.URL.Hostname()] {
				return ErrHostNotAllowed
			}
			return nil
		},
	}}
}

// Fetch downloads rawURL and decodes it as format (mrs, yaml or text; empty
// picks by extension).
func (f *Fetcher) Fetch(ctx context.Context, rawURL, format string) (Contents, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !allowedHosts[u.Hostname()] || u.User != nil {
		return Contents{}, ErrHostNotAllowed
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Contents{}, err
	}
	req.Header.Set("User-Agent", "proxima-vpn-admin")
	res, err := f.client.Do(req)
	if err != nil {
		return Contents{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Contents{}, fmt.Errorf("download failed: %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxDownload+1))
	if err != nil {
		return Contents{}, err
	}
	if len(data) > maxDownload {
		return Contents{}, errors.New("rule set is larger than 32 MiB")
	}
	if format == "" {
		format = formatFromPath(u.Path)
	}
	return Decode(data, format)
}

func formatFromPath(p string) string {
	switch {
	case strings.HasSuffix(p, ".mrs"):
		return "mrs"
	case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
		return "yaml"
	default:
		return "text"
	}
}

// Decode turns rule-set bytes in format into display entries.
func Decode(data []byte, format string) (Contents, error) {
	var out Contents
	switch format {
	case "mrs":
		behavior, count, entries, err := DecodeMRS(data)
		if err != nil {
			return Contents{}, err
		}
		out = Contents{Behavior: behavior, Count: int(count), Entries: entries}
	case "yaml":
		var doc struct {
			Payload []string `yaml:"payload"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return Contents{}, fmt.Errorf("yaml: %w", err)
		}
		out = Contents{Entries: doc.Payload, Count: len(doc.Payload)}
	case "text":
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				out.Entries = append(out.Entries, line)
			}
		}
		out.Count = len(out.Entries)
	default:
		return Contents{}, fmt.Errorf("unknown format %q", format)
	}
	out.Format = format
	if len(out.Entries) > maxEntries {
		out.Entries, out.Truncated = out.Entries[:maxEntries], true
	}
	if out.Entries == nil {
		out.Entries = []string{}
	}
	return out, nil
}
