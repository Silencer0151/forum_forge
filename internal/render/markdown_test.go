package render

import (
	"strings"
	"testing"
)

func TestRenderMarkdown_Basic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "bold",
			input:    "**hello**",
			contains: "<strong>hello</strong>",
		},
		{
			name:     "italic",
			input:    "*world*",
			contains: "<em>world</em>",
		},
		{
			name:     "heading",
			input:    "# Title",
			contains: "<h1",
		},
		{
			name:     "link",
			input:    "[click](https://example.com)",
			contains: `href="https://example.com"`,
		},
		{
			name:     "table",
			input:    "| A | B |\n|---|---|\n| 1 | 2 |",
			contains: "<table>",
		},
		{
			name:     "strikethrough",
			input:    "~~deleted~~",
			contains: "<del>deleted</del>",
		},
		{
			name:     "task list unchecked",
			input:    "- [ ] todo",
			contains: `type="checkbox"`,
		},
		{
			name:     "task list checked",
			input:    "- [x] done",
			contains: "checked",
		},
		{
			name:     "code block",
			input:    "```\nfoo()\n```",
			contains: "<code>",
		},
		{
			name:     "autolink",
			input:    "visit https://example.com today",
			contains: `href="https://example.com"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderMarkdown(tt.input)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("RenderMarkdown(%q) = %q; want it to contain %q", tt.input, got, tt.contains)
			}
		})
	}
}

func TestRenderMarkdown_XSSStripped(t *testing.T) {
	xssPayloads := []struct {
		name    string
		input   string
		blocked string
	}{
		{
			name:    "script tag",
			input:   "<script>alert('xss')</script>",
			blocked: "<script>",
		},
		{
			name:    "onerror attribute",
			input:   `<img src=x onerror="alert(1)">`,
			blocked: "onerror",
		},
		{
			name:    "javascript href",
			input:   `[click](javascript:alert(1))`,
			blocked: "javascript:",
		},
		{
			name:    "iframe",
			input:   `<iframe src="https://evil.com"></iframe>`,
			blocked: "<iframe",
		},
		{
			name:    "onclick attribute",
			input:   `<div onclick="alert(1)">click me</div>`,
			blocked: "onclick",
		},
		{
			name:    "data URI script",
			input:   `<img src="data:text/html,<script>alert(1)</script>">`,
			blocked: "<script>",
		},
	}

	for _, tt := range xssPayloads {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderMarkdown(tt.input)
			if strings.Contains(got, tt.blocked) {
				t.Errorf("RenderMarkdown(%q) = %q; dangerous content %q was not stripped", tt.input, got, tt.blocked)
			}
		})
	}
}

func TestRenderBBCode_EscapesHTML(t *testing.T) {
	input := `[b]Hello[/b] <script>alert(1)</script>`
	got := RenderBBCode(input)
	if strings.Contains(got, "<script>") {
		t.Errorf("RenderBBCode output contains unescaped <script>: %q", got)
	}
	if strings.Contains(got, "<b>") {
		t.Errorf("RenderBBCode output contains rendered <b> tag: %q", got)
	}
	if !strings.Contains(got, "[b]Hello[/b]") {
		t.Errorf("RenderBBCode output should contain literal BBCode tags: %q", got)
	}
}
