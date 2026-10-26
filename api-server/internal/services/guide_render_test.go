package services

import (
	"strings"
	"testing"
)

func TestRenderGuide_MarkdownKeepsStructure(t *testing.T) {
	out, err := RenderGuide(GuideFormatMarkdown, "## Install\n\n- step one\n- step two\n\n[site](https://example.com) ![shot](https://example.com/a.png)\n\n| a | b |\n|---|---|\n| 1 | 2 |\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h2", "<ul>", "<li>step one</li>", `href="https://example.com"`, `<img src="https://example.com/a.png"`, "<table>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered markdown missing %q:\n%s", want, out)
		}
	}
}

func TestRenderGuide_StripsScriptVectors(t *testing.T) {
	payloads := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<iframe src="https://evil.example"></iframe>`,
		`<div style="background:url(javascript:alert(1))" onclick="alert(1)">y</div>`,
		`<svg onload=alert(1)><circle/></svg>`,
		`<a href="JaVaScRiPt:alert(1)">z</a>`,
		`<object data="x.swf"></object><embed src="x.swf">`,
		`<form action="https://evil.example"><input name=q></form>`,
	}
	forbidden := []string{"<script", "onerror", "onclick", "onload", "javascript:", "<iframe", "style=", "<svg", "<object", "<embed", "<form", "<input"}
	for _, format := range []string{GuideFormatHTML, GuideFormatMarkdown} {
		for _, p := range payloads {
			out, err := RenderGuide(format, p)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(out)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Errorf("%s payload %q left %q in output: %s", format, p, bad, out)
				}
			}
		}
	}
}

func TestRenderGuide_RejectsUnknownFormat(t *testing.T) {
	if _, err := RenderGuide("bbcode", "x"); err != ErrGuideFormat {
		t.Fatalf("want ErrGuideFormat, got %v", err)
	}
}
