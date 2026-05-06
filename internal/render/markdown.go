package render

import (
	"bytes"
	"html"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	rendererhtml "github.com/yuin/goldmark/renderer/html"
)

var (
	mdParser goldmark.Markdown
	sanitize *bluemonday.Policy
)

func init() {
	mdParser = goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.Linkify,
			extension.TaskList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			rendererhtml.WithHardWraps(),
			rendererhtml.WithXHTML(),
		),
	)
	sanitize = bluemonday.UGCPolicy()
	// Allow disabled checkboxes produced by the task-list extension.
	sanitize.AllowAttrs("type", "disabled", "checked").OnElements("input")
}

// RenderMarkdown converts markdown source to sanitized HTML.
func RenderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := mdParser.Convert([]byte(src), &buf); err != nil {
		return "<p>" + html.EscapeString(src) + "</p>"
	}
	return sanitize.Sanitize(buf.String())
}

// RenderBBCode is a stub that escapes BBCode and returns it as plaintext HTML.
func RenderBBCode(src string) string {
	return "<p>" + html.EscapeString(src) + "</p>"
}
