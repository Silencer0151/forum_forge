// Package forum embeds the static assets, HTML templates, and SQL migrations
// that the forum binary needs at runtime. Embedding here (rather than reading
// from disk) keeps the deployable artifact a single self-contained binary.
//
// The exported FSes are rooted at the embed source directory; pass them through
// fs.Sub before handing to consumers that expect a directory-rooted FS
// (e.g. the renderer wants templates rooted at "layout/", "pages/", "partials/").
package forum

import "embed"

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// MigrationsFS returns the embedded migration files. The returned FS is rooted
// at "migrations/" — callers needing the files at the FS root should use fs.Sub.
func MigrationsFS() embed.FS { return migrationsFS }

// TemplatesFS returns the embedded HTML templates. The returned FS is rooted at
// "templates/" — callers should use fs.Sub before handing to render.New.
func TemplatesFS() embed.FS { return templatesFS }

// StaticFS returns the embedded static assets (CSS, JS, images). The returned
// FS is rooted at "static/" — callers should use fs.Sub before serving via http.FileServer.
func StaticFS() embed.FS { return staticFS }
