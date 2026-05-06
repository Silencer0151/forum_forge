package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// minimalFS builds an in-memory FS that mimics the templates/ directory layout.
func minimalFS() fstest.MapFS {
	return fstest.MapFS{
		"layout/base.html": {
			Data: []byte(`{{define "base"}}<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>{{end}}`),
		},
		"pages/home.html": {
			Data: []byte(`{{define "content"}}<main>{{.Title}}</main>{{end}}`),
		},
		"partials/snippet.html": {
			Data: []byte(`<span>{{.Text}}</span>`),
		},
	}
}

func TestNew_EmptyFS(t *testing.T) {
	empty := fstest.MapFS{}
	r, err := New(empty)
	if err != nil {
		t.Fatalf("New with empty FS returned error: %v", err)
	}
	if r == nil {
		t.Fatal("New returned nil renderer")
	}
}

func TestNew_LoadsTemplates(t *testing.T) {
	r, err := New(minimalFS())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, ok := r.templates["page:home.html"]; !ok {
		t.Error("expected page:home.html template to be loaded")
	}
	if _, ok := r.templates["partial:snippet.html"]; !ok {
		t.Error("expected partial:snippet.html template to be loaded")
	}
}

func TestRender_ProducesHTML(t *testing.T) {
	r, err := New(minimalFS())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	data := struct{ Title string }{Title: "Forum Home"}
	if err := r.Render(w, "home.html", data); err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("response missing DOCTYPE: %q", body)
	}
	if !strings.Contains(body, "Forum Home") {
		t.Errorf("response missing title text: %q", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q; want text/html", ct)
	}
}

func TestRender_UnknownTemplate(t *testing.T) {
	r, err := New(minimalFS())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := httptest.NewRecorder()
	if err := r.Render(w, "nonexistent.html", nil); err == nil {
		t.Error("expected error for unknown template, got nil")
	}
}

func TestRenderPartial_ProducesFragment(t *testing.T) {
	r, err := New(minimalFS())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	data := struct{ Text string }{Text: "hello"}
	if err := r.RenderPartial(w, "snippet.html", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "hello") {
		t.Errorf("partial missing text: %q", body)
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("partial should not include full page layout: %q", body)
	}
}

func TestRenderPartial_UnknownTemplate(t *testing.T) {
	r, err := New(minimalFS())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := httptest.NewRecorder()
	if err := r.RenderPartial(w, "missing.html", nil); err == nil {
		t.Error("expected error for unknown partial, got nil")
	}
}

func TestIsHTMXRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"htmx request", "true", true},
		{"non-htmx request", "", false},
		{"wrong value", "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("HX-Request", tt.header)
			}
			if got := IsHTMXRequest(req); got != tt.want {
				t.Errorf("IsHTMXRequest = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	ts := time.Date(2024, 3, 15, 9, 5, 0, 0, time.UTC)
	got := FormatTime(ts)
	if !strings.Contains(got, "2024") {
		t.Errorf("FormatTime(%v) = %q; expected year 2024", ts, got)
	}
	if !strings.Contains(got, "Mar") {
		t.Errorf("FormatTime(%v) = %q; expected month Mar", ts, got)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"short", 10, "short"},
		{"hello world foo bar", 12, "hello world..."},
		{"nospacehere", 6, "nospac..."},
		{"exactly10!", 10, "exactly10!"},
	}
	for _, tt := range tests {
		got := Truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q; want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		n        int
		singular string
		plural   string
		want     string
	}{
		{1, "reply", "replies", "1 reply"},
		{0, "reply", "replies", "0 replies"},
		{5, "reply", "replies", "5 replies"},
	}
	for _, tt := range tests {
		got := Pluralize(tt.n, tt.singular, tt.plural)
		if got != tt.want {
			t.Errorf("Pluralize(%d, %q, %q) = %q; want %q", tt.n, tt.singular, tt.plural, got, tt.want)
		}
	}
}

func TestGravatarURL(t *testing.T) {
	url := GravatarURL("test@example.com")
	if !strings.HasPrefix(url, "https://www.gravatar.com/avatar/") {
		t.Errorf("GravatarURL = %q; expected Gravatar URL", url)
	}
	// Same email should produce same URL regardless of case/whitespace.
	if GravatarURL("TEST@EXAMPLE.COM") != GravatarURL("test@example.com") {
		t.Error("GravatarURL should be case-insensitive")
	}
	if GravatarURL("  test@example.com  ") != GravatarURL("test@example.com") {
		t.Error("GravatarURL should trim whitespace")
	}
}

func TestMarkdownTemplateFunc(t *testing.T) {
	fsys := fstest.MapFS{
		"layout/base.html": {
			Data: []byte(`{{define "base"}}{{block "content" .}}{{end}}{{end}}`),
		},
		"pages/md.html": {
			Data: []byte(`{{define "content"}}{{markdown .Body}}{{end}}`),
		},
	}
	r, err := New(fsys)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	data := struct{ Body string }{Body: "**bold text**"}
	if err := r.Render(w, "md.html", data); err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<strong>bold text</strong>") {
		t.Errorf("markdown func output missing expected HTML: %q", body)
	}
}
