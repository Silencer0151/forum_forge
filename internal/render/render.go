package render

import (
	"crypto/md5"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const htmxRequestHeader = "HX-Request"

// Renderer loads and executes HTML templates.
type Renderer struct {
	templates map[string]*template.Template
	funcMap   template.FuncMap
}

// New creates a Renderer by parsing templates from fsys.
// fsys should be rooted at the templates/ directory so paths like
// "layout/base.html", "pages/home.html", "partials/foo.html" resolve correctly.
// It is valid to call New with an FS that contains no template files.
func New(fsys fs.FS) (*Renderer, error) {
	r := &Renderer{
		templates: make(map[string]*template.Template),
	}
	r.funcMap = r.buildFuncMap()
	if err := r.load(fsys); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Renderer) buildFuncMap() template.FuncMap {
	return template.FuncMap{
		"formatTime":  FormatTime,
		"truncate":    Truncate,
		"pluralize":   Pluralize,
		"markdown":    func(s string) template.HTML { return template.HTML(RenderMarkdown(s)) },
		"gravatar":    GravatarURL,
		"formatBytes": FormatBytes,
		"isImage":     IsImageContentType,
		// safehtml marks a pre-sanitized string as trusted HTML so html/template
		// does not escape it. Only use this for content that has already been
		// HTML-escaped at its source (e.g. search snippets, which escape the
		// body before wrapping matches in <mark>).
		"safehtml":  func(s string) template.HTML { return template.HTML(s) },
		"hasPrefix": strings.HasPrefix,
	}
}

func (r *Renderer) load(fsys fs.FS) error {
	layouts, err := fs.Glob(fsys, "layout/*.html")
	if err != nil {
		return fmt.Errorf("globbing layouts: %w", err)
	}
	partials, err := fs.Glob(fsys, "partials/*.html")
	if err != nil {
		return fmt.Errorf("globbing partials: %w", err)
	}
	pages, err := fs.Glob(fsys, "pages/*.html")
	if err != nil {
		return fmt.Errorf("globbing pages: %w", err)
	}

	// Each full page is compiled with all layouts + all partials + the page file.
	for _, page := range pages {
		name := filepath.Base(page)
		files := make([]string, 0, len(layouts)+len(partials)+1)
		files = append(files, layouts...)
		files = append(files, partials...)
		files = append(files, page)

		tmpl, err := template.New(name).Funcs(r.funcMap).ParseFS(fsys, files...)
		if err != nil {
			return fmt.Errorf("parsing page %s: %w", name, err)
		}
		r.templates["page:"+name] = tmpl
	}

	// Each partial is compiled as a standalone template.
	for _, partial := range partials {
		name := filepath.Base(partial)
		tmpl, err := template.New(name).Funcs(r.funcMap).ParseFS(fsys, partial)
		if err != nil {
			return fmt.Errorf("parsing partial %s: %w", name, err)
		}
		r.templates["partial:"+name] = tmpl
	}

	return nil
}

// IsHTMXRequest reports whether the request was made by HTMX.
func IsHTMXRequest(r *http.Request) bool {
	return r.Header.Get(htmxRequestHeader) == "true"
}

// Render writes a full-page response using the named page template inside the
// base layout. name should match a file in templates/pages/ (e.g. "home.html").
func (r *Renderer) Render(w http.ResponseWriter, name string, data any) error {
	tmpl, ok := r.templates["page:"+name]
	if !ok {
		return fmt.Errorf("render: page template %q not found", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tmpl.ExecuteTemplate(w, "base", data)
}

// RenderPartial writes an HTMX-targeted partial response without a layout
// wrapper. name should match a file in templates/partials/ (e.g. "thread_row.html").
// The template inside the file is expected to use {{define "<name-without-ext>"}}.
func (r *Renderer) RenderPartial(w http.ResponseWriter, name string, data any) error {
	tmpl, ok := r.templates["partial:"+name]
	if !ok {
		return fmt.Errorf("render: partial template %q not found", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templateName := strings.TrimSuffix(name, ".html")
	return tmpl.ExecuteTemplate(w, templateName, data)
}

// RenderError renders the error.html page with the given HTTP status code. It
// sets the status on the response writer before executing the template so the
// browser receives the correct code. data may be nil; StatusCode, Message, and
// Title keys are injected automatically when absent.
func (r *Renderer) RenderError(w http.ResponseWriter, status int, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["StatusCode"]; !ok {
		data["StatusCode"] = status
	}
	if _, ok := data["Message"]; !ok {
		data["Message"] = http.StatusText(status)
	}
	if _, ok := data["Title"]; !ok {
		data["Title"] = fmt.Sprintf("%d %s", status, http.StatusText(status))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	tmpl, ok := r.templates["page:error.html"]
	if !ok {
		fmt.Fprintf(w, "%d %s\n", status, http.StatusText(status))
		return
	}
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		fmt.Fprintf(w, "\n<!-- render error: %v -->", err)
	}
}

// RenderContent writes just the {{define "content"}} block of a page template,
// without the base layout wrapper. Used for HTMX responses that replace a
// page section (e.g., paginated thread or subcategory content).
func (r *Renderer) RenderContent(w http.ResponseWriter, name string, data any) error {
	tmpl, ok := r.templates["page:"+name]
	if !ok {
		return fmt.Errorf("render: page template %q not found", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tmpl.ExecuteTemplate(w, "content", data)
}

// FormatTime formats t as a human-readable string in the server's local time
// zone. Database values are stored in UTC; converting to local on display
// keeps the UI aligned with the operator's "wall clock" rather than UTC,
// which is the surprising default that users read as "the timestamp is wrong".
func FormatTime(t time.Time) string {
	return t.Local().Format("Jan 2, 2006 3:04 PM")
}

// Truncate shortens s to at most n characters, appending "..." if cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if idx := strings.LastIndex(s[:n], " "); idx > 0 {
		return s[:idx] + "..."
	}
	return s[:n] + "..."
}

// Pluralize returns "n singular" or "n plural" depending on n.
func Pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// GravatarURL returns a Gravatar image URL for the given email address.
func GravatarURL(email string) string {
	h := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
	return fmt.Sprintf("https://www.gravatar.com/avatar/%x?d=identicon&s=80", h)
}

// FormatBytes formats a byte count as a human-readable string (e.g. "1.2 MB").
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// IsImageContentType reports whether the MIME type represents an image.
func IsImageContentType(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}
