package ruleset

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// The fixtures are MetaCubeX meta-rules-dat files fetched together; each .list
// is the plain-text twin of its .mrs. The .list may repeat a domain both exact
// and as "+.", which the binary set stores once, so expected entries are
// normalized the same way before comparing.
func readList(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

func normalizeDomains(entries []string) []string {
	wild := map[string]bool{}
	for _, e := range entries {
		if d, ok := strings.CutPrefix(e, "+."); ok {
			wild[d] = true
		}
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e, "+.") && wild[e] {
			continue
		}
		out = append(out, e)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func TestDecodeMRSMatchesPlainList(t *testing.T) {
	for _, tc := range []struct{ name, behavior string }{
		{"geosite-naver", "domain"},
		{"geosite-kakao", "domain"},
		{"geoip-kr", "ipcidr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tc.name + ".mrs")
			if err != nil {
				t.Fatal(err)
			}
			behavior, count, got, err := DecodeMRS(data)
			if err != nil {
				t.Fatalf("DecodeMRS: %v", err)
			}
			if behavior != tc.behavior {
				t.Fatalf("behavior = %s, want %s", behavior, tc.behavior)
			}
			want := readList(t, "testdata/"+tc.name+".list")
			if tc.behavior == "domain" {
				want = normalizeDomains(want)
				got = normalizeDomains(got)
			} else {
				slices.Sort(want)
				slices.Sort(got)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("decoded %d entries, list has %d; first got %v want %v", len(got), len(want), got[:min(3, len(got))], want[:min(3, len(want))])
			}
			if int(count) != len(want) {
				t.Errorf("declared count %d, entries %d", count, len(want))
			}
		})
	}
}

func TestDecodeMRSRejectsGarbage(t *testing.T) {
	if _, _, _, err := DecodeMRS([]byte("not zstd at all")); err == nil {
		t.Fatal("garbage input must fail")
	}
}
