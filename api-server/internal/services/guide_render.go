package services

import (
	"bytes"
	"errors"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Guide content formats an administrator can author in.
const (
	GuideFormatMarkdown = "markdown"
	GuideFormatHTML     = "html"
)

// ErrGuideFormat is returned for a format other than markdown or html.
var ErrGuideFormat = errors.New("format must be markdown or html")

var guideMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	// Raw HTML inside Markdown is allowed through goldmark on purpose: every
	// byte of output, Markdown-derived or not, passes the sanitizer below.
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// guidePolicy is the single allow-list guide HTML must satisfy before it is
// stored or previewed. It starts from bluemonday's UGC policy (no script,
// style, iframe, event handlers or javascript: URLs) and adds only the
// presentational pieces guides need.
var guidePolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[a-zA-Z0-9_+-]+$`)).OnElements("code")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|right|center)$`)).OnElements("td", "th")
	p.AllowElements("kbd", "mark", "details", "summary")
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

// RenderGuide turns guide source into HTML that is safe to inject into the
// portal. The browser never sanitizes; it only displays what this returns.
func RenderGuide(format, source string) (string, error) {
	var raw string
	switch format {
	case GuideFormatMarkdown:
		var buf bytes.Buffer
		if err := guideMarkdown.Convert([]byte(source), &buf); err != nil {
			return "", err
		}
		raw = buf.String()
	case GuideFormatHTML:
		raw = source
	default:
		return "", ErrGuideFormat
	}
	return guidePolicy.Sanitize(raw), nil
}
