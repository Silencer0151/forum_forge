// Package server wires the HTTP router, applies route-specific configuration,
// and runs the server under one of the three TLS modes from spec.md §12.1
// (autocert, manual, none). It owns no business logic — handlers live in
// internal/handler and are mounted from registerRoutes.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/config"
	"github.com/nitro/forum_forge/internal/handler"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second

	// globalRateLimit / globalRateWindow form a permissive baseline limit
	// that protects against scrapers and runaway clients without affecting
	// normal use. Action-specific limits (post/thread create, PMs, reports)
	// live in their handlers and use additional Limiter instances.
	globalRateLimit  = 600
	globalRateWindow = time.Minute
)

// Server is the composed HTTP server: router + dependencies + lifecycle.
type Server struct {
	cfg           *config.Config
	store         store.Store
	renderer      *render.Renderer
	staticFS      fs.FS
	mux           *http.ServeMux
	handler       http.Handler
	authenticator auth.Authenticator

	authHandlers        *handler.AuthHandlers
	categoryHandlers    *handler.CategoryHandlers
	threadHandlers      *handler.ThreadHandlers
	postHandlers        *handler.PostHandlers
	userHandlers        *handler.UserHandlers
	settingsHandlers    *handler.SettingsHandlers
	pmHandlers          *handler.PMHandlers
	draftHandlers       *handler.DraftHandlers
	moderationHandlers  *handler.ModerationHandlers
	adminHandlers       *handler.AdminHandlers
	searchHandlers      *handler.SearchHandlers
	attachmentHandlers  *handler.AttachmentHandlers
}

// AuthDeps bundles the auth-flow building blocks the server needs but does
// not own. The caller (cmd/forum) constructs the mailer + token service so
// it can wire SMTP config and email templates from environment + embed.FS.
//
// Authenticator is the spec.md §4.2 adapter the auth middleware dispatches
// through. cmd/forum picks standalone or kanoogi based on FORUM_AUTH_MODE
// and hands the result to the server.
type AuthDeps struct {
	Service       *auth.Service
	Tokens        *auth.TokenService
	Mailer        *auth.Mailer
	Authenticator auth.Authenticator
}

// New builds a Server with all routes registered and wrapped in the standard
// middleware chain. It does not start listening; call Run for that.
//
// authDeps.Authenticator is the spec.md §4.2 adapter; if nil, New falls back
// to authDeps.Service (the standalone implementation) so existing callers
// that pre-date task 3.3 keep working without source changes.
func New(cfg *config.Config, st store.Store, r *render.Renderer, staticFS fs.FS, authDeps AuthDeps) *Server {
	authenticator := authDeps.Authenticator
	if authenticator == nil {
		authenticator = authDeps.Service
	}
	s := &Server{
		cfg:           cfg,
		store:         st,
		renderer:      r,
		staticFS:      staticFS,
		mux:           http.NewServeMux(),
		authenticator: authenticator,
	}

	s.authHandlers = handler.NewAuth(
		authDeps.Service, authDeps.Tokens, authDeps.Mailer, r,
		handler.AuthConfig{Secure: s.secureCookies(), BaseURL: cfg.BaseURL},
	)
	spamCfg := handler.SpamConfig{
		CaptchaProvider: cfg.CaptchaProvider,
		CaptchaSiteKey:  cfg.CaptchaSiteKey,
		CaptchaSecret:   cfg.CaptchaSecret,
	}
	s.categoryHandlers = handler.NewCategory(st, r)
	s.threadHandlers = handler.NewThreadHandler(st, r, spamCfg)
	s.postHandlers = handler.NewPostHandler(st, r, spamCfg)
	s.userHandlers = handler.NewUserHandler(st, r)
	s.settingsHandlers = handler.NewSettingsHandler(st, r, authDeps.Service, cfg.UploadPath, handler.SettingsConfig{
		Secure:         s.secureCookies(),
		IntegratedMode: cfg.AuthMode == "integrated",
	})
	s.pmHandlers = handler.NewPMHandler(st, r)
	s.draftHandlers = handler.NewDraftHandler(st, r)
	s.moderationHandlers = handler.NewModerationHandler(st, r)
	s.adminHandlers = handler.NewAdminHandler(st, r)
	s.searchHandlers = handler.NewSearchHandler(st, r)
	s.attachmentHandlers = handler.NewAttachmentHandler(st, cfg.UploadPath)
	s.postHandlers.SetAttachmentHandlers(s.attachmentHandlers)
	s.threadHandlers.SetAttachmentHandlers(s.attachmentHandlers)

	s.registerRoutes()
	s.handler = s.wrapMiddleware(s.mux)
	return s
}

// Handler returns the wrapped http.Handler (mux + middleware chain). Useful
// for tests that drive the router with httptest.NewServer or for embedding
// the forum into another mux (see spec.md §12.5 "library import" mode).
func (s *Server) Handler() http.Handler { return s.handler }

// wrapMiddleware composes the per-request middleware in spec.md §5.2 order.
// Outer→inner: Recovery (catch panics, render 500) → Security (static
// hardening headers — CSP, X-Frame-Options, etc.) → Proxy (normalize
// r.RemoteAddr/scheme, set HSTS) → Auth (resolve user from session) → Flash
// (pop flash cookie into context) → Logging (so user_id is available) →
// RateLimit (per user/IP, after auth so the key reflects the real identity) →
// CSRF (reject missing/mismatched tokens on unsafe methods) → mux.
//
// Security sits above Proxy so that error pages rendered by Recovery still
// receive hardening headers; HSTS is left to Proxy because it must only be
// sent over actual HTTPS responses.
func (s *Server) wrapMiddleware(h http.Handler) http.Handler {
	limiter := middleware.NewLimiter(globalRateLimit, globalRateWindow)
	renderer := s.renderer
	return middleware.Chain(
		middleware.Recovery(func(w http.ResponseWriter, r *http.Request) {
			renderer.RenderError(w, http.StatusInternalServerError, nil)
		}),
		middleware.Security(middleware.SecurityConfig{}),
		middleware.Proxy(middleware.ProxyConfig{
			TrustProxy: s.cfg.TrustProxy,
			HSTSValue:  s.hstsValue(),
		}),
		middleware.Auth(s.authenticator),
		middleware.FlashMiddleware,
		middleware.Logging(slog.Default()),
		middleware.RateLimit(limiter),
		middleware.CSRF(middleware.CSRFConfig{Secure: s.secureCookies()}),
	)(h)
}

// secureCookies decides whether to mark cookies Secure. Self-terminating TLS
// modes always serve HTTPS; behind a proxy we trust X-Forwarded-Proto. In
// plain mode (TLS_MODE=none, no trust_proxy) cookies must NOT be Secure or
// browsers won't send them back over HTTP.
func (s *Server) secureCookies() bool {
	if s.cfg.TLSMode == "autocert" || s.cfg.TLSMode == "manual" {
		return true
	}
	return s.cfg.TrustProxy && strings.HasPrefix(strings.ToLower(s.cfg.BaseURL), "https://")
}

// hstsValue is the Strict-Transport-Security value to send. Empty in plain
// dev mode so a developer hitting localhost on http isn't locked into https
// for the whole apex domain by an over-eager browser.
func (s *Server) hstsValue() string {
	if s.cfg.TLSMode == "autocert" || s.cfg.TLSMode == "manual" {
		return middleware.DefaultHSTS
	}
	if s.cfg.TrustProxy {
		return middleware.DefaultHSTS
	}
	return ""
}

// Run starts the HTTP server in the configured TLS mode and blocks until ctx
// is canceled. On cancellation it triggers a graceful shutdown.
func (s *Server) Run(ctx context.Context) error {
	go s.runDraftCleanup(ctx)
	go s.runAttachmentCleanup(ctx)

	switch s.cfg.TLSMode {
	case "autocert":
		return s.runAutocert(ctx)
	case "manual":
		return s.runManual(ctx)
	case "none":
		return s.runPlain(ctx)
	default:
		return fmt.Errorf("unsupported TLS mode %q", s.cfg.TLSMode)
	}
}

// runDraftCleanup deletes drafts older than 30 days once per day.
func (s *Server) runDraftCleanup(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-30 * 24 * time.Hour)
			if err := s.store.CleanupOldDrafts(context.Background(), cutoff); err != nil {
				slog.Warn("draft cleanup", "error", err)
			}
		}
	}
}

// runAttachmentCleanup deletes orphaned attachment records (no associated post)
// older than 24 hours once per day.
func (s *Server) runAttachmentCleanup(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-24 * time.Hour)
			if err := s.store.DeleteOrphanedAttachments(context.Background(), cutoff); err != nil {
				slog.Warn("attachment cleanup", "error", err)
			}
		}
	}
}

// ── route registration ────────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	// Static files. The handler strips the /static/ prefix before delegating
	// to the embedded FS so paths like /static/css/style.css resolve to css/style.css.
	staticHandler := http.StripPrefix("/static/",
		cacheStatic(http.FileServer(http.FS(s.staticFS))))
	s.mux.Handle("GET /static/", staticHandler)

	// User-uploaded files (avatars, etc.) served from the upload directory on disk.
	uploadsHandler := http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.cfg.UploadPath)))
	s.mux.Handle("GET /uploads/", uploadsHandler)

	// Attachments: served with correct Content-Type and Content-Disposition via DB lookup.
	s.mux.HandleFunc("GET /attachments/{id}", s.attachmentHandlers.ServeAttachment)

	// Health check (referenced from spec.md §9.2 for Docker readiness).
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok"))
	})

	// Forum index and category pages (Task 4.1).
	s.mux.HandleFunc("GET /{$}", s.categoryHandlers.Home)
	s.mux.HandleFunc("GET /c/{category_slug}", s.categoryHandlers.Category)

	// Thread listing and creation (Task 4.2).
	s.mux.HandleFunc("GET /c/{category_slug}/{sub_slug}", s.threadHandlers.SubcategoryPage)
	s.mux.HandleFunc("GET /t/new", s.threadHandlers.NewThreadPage)
	s.mux.HandleFunc("POST /t/new", s.threadHandlers.CreateThread)

	// Thread view and replies (Task 4.3).
	s.mux.HandleFunc("GET /t/{thread_id}", s.postHandlers.ThreadPage)
	s.mux.HandleFunc("POST /t/{thread_id}/reply", s.postHandlers.ReplyToThread)

	// Posts (Task 4.4 — HTMX-enhanced edit, delete, react).
	s.mux.HandleFunc("GET /p/{post_id}", s.postHandlers.GetPostView)
	s.mux.HandleFunc("GET /p/{post_id}/edit", s.postHandlers.GetEditForm)
	s.mux.HandleFunc("PUT /p/{post_id}", s.postHandlers.UpdatePost)
	s.mux.HandleFunc("DELETE /p/{post_id}", s.postHandlers.DeletePost)
	s.mux.HandleFunc("POST /p/{post_id}", s.postHandlers.PostMethodOverride)
	s.mux.HandleFunc("POST /p/{post_id}/react", s.postHandlers.ReactToPost)
	s.mux.HandleFunc("POST /p/{post_id}/quote", s.postHandlers.QuotePost)
	s.mux.HandleFunc("GET /p/{post_id}/report", s.postHandlers.GetReportForm)
	s.mux.HandleFunc("POST /p/{post_id}/report", s.postHandlers.ReportPost)

	// Users (Task 5.1).
	s.mux.HandleFunc("GET /u/{username}", s.userHandlers.ProfilePage)
	s.mux.HandleFunc("GET /u/{username}/posts", s.userHandlers.UserPostsPage)
	s.mux.HandleFunc("POST /u/{username}/ban", s.userHandlers.BanUser)
	s.mux.HandleFunc("POST /u/{username}/unban", s.userHandlers.UnbanUser)
	s.mux.HandleFunc("GET /settings", s.settingsHandlers.SettingsPage)
	s.mux.HandleFunc("POST /settings", s.settingsHandlers.UpdateSettings)

	// Private messages (Task 5.3).
	s.mux.HandleFunc("GET /pm", s.pmHandlers.InboxPage)
	s.mux.HandleFunc("GET /pm/new", s.pmHandlers.NewMessagePage)
	s.mux.HandleFunc("POST /pm/new", s.pmHandlers.SendMessage)
	s.mux.HandleFunc("GET /pm/{pm_id}", s.pmHandlers.ViewMessage)
	s.mux.HandleFunc("POST /pm/{pm_id}/delete", s.pmHandlers.DeleteMessage)

	// Search (Task 7.2).
	s.mux.HandleFunc("GET /search", s.searchHandlers.SearchPage)

	// Auth (standalone email+password — see internal/handler/auth.go).
	// Task 3.3 will route these through an AuthAdapter so integrated mode
	// can swap a different implementation without touching server.go.
	s.mux.HandleFunc("GET /auth/login", s.authHandlers.LoginPage)
	s.mux.HandleFunc("POST /auth/login", s.authHandlers.Login)
	s.mux.HandleFunc("GET /auth/register", s.authHandlers.RegisterPage)
	s.mux.HandleFunc("POST /auth/register", s.authHandlers.Register)
	s.mux.HandleFunc("POST /auth/logout", s.authHandlers.Logout)
	s.mux.HandleFunc("GET /auth/verify", s.authHandlers.VerifyEmail)
	s.mux.HandleFunc("GET /auth/forgot-password", s.authHandlers.ForgotPasswordPage)
	s.mux.HandleFunc("POST /auth/forgot-password", s.authHandlers.ForgotPassword)
	s.mux.HandleFunc("GET /auth/reset-password", s.authHandlers.ResetPasswordPage)
	s.mux.HandleFunc("POST /auth/reset-password", s.authHandlers.ResetPassword)

	// Moderation (Task 6.1).
	s.mux.HandleFunc("GET /mod/reports", s.moderationHandlers.ReportsQueue)
	s.mux.HandleFunc("POST /mod/reports/{id}/review", s.moderationHandlers.ReviewReport)
	s.mux.HandleFunc("POST /mod/threads/{id}/lock", s.moderationHandlers.LockThread)
	s.mux.HandleFunc("POST /mod/threads/{id}/pin", s.moderationHandlers.PinThread)
	s.mux.HandleFunc("POST /mod/threads/{id}/delete", s.moderationHandlers.DeleteThread)
	s.mux.HandleFunc("POST /mod/users/{id}/ban", s.moderationHandlers.BanUserByID)

	// Admin (Task 6.3).
	s.mux.HandleFunc("GET /admin/settings", s.adminHandlers.AdminSettingsPage)
	s.mux.HandleFunc("POST /admin/settings", s.adminHandlers.UpdateAdminSettings)
	s.mux.HandleFunc("GET /admin/categories", s.adminHandlers.AdminCategoriesPage)
	s.mux.HandleFunc("POST /admin/categories", s.adminHandlers.UpdateAdminCategories)

	// Drafts (Task 5.4 — auto-save endpoints).
	s.mux.HandleFunc("GET /drafts", s.draftHandlers.GetDraft)
	s.mux.HandleFunc("POST /drafts", s.draftHandlers.UpsertDraft)
	s.mux.HandleFunc("DELETE /drafts/{id}", s.draftHandlers.DeleteDraft)
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (s *Server) notImplemented(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not Implemented", http.StatusNotImplemented)
}

// cacheStatic adds a sensible Cache-Control header for embedded static files.
// Content-hash cache busting can come later; an hour-long cache is safe for now
// and prevents the browser from re-fetching CSS on every page load.
func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// ── TLS mode runners ──────────────────────────────────────────────────────────

func (s *Server) runPlain(ctx context.Context) error {
	srv := s.newHTTPServer(s.cfg.ListenAddr)
	slog.Info("listening (plain http)", "addr", s.cfg.ListenAddr)
	return runWithShutdown(ctx, srv, srv.ListenAndServe)
}

func (s *Server) runManual(ctx context.Context) error {
	srv := s.newHTTPServer(s.cfg.ListenAddr)
	srv.TLSConfig = hardenedTLSConfig()

	// Optional :80 redirect-to-HTTPS listener. Runs concurrently and shuts down
	// alongside the TLS listener.
	var redirSrv *http.Server
	if s.cfg.RedirectHTTP {
		redirSrv = s.newRedirectServer(":80")
	}

	slog.Info("listening (manual TLS)",
		"addr", s.cfg.ListenAddr, "cert", s.cfg.TLSCertFile, "redirect_http", s.cfg.RedirectHTTP)

	listenFn := func() error {
		return srv.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	}
	return runMultiServer(ctx, srv, redirSrv, listenFn)
}

func (s *Server) runAutocert(ctx context.Context) error {
	mgr := &autocert.Manager{
		Cache:      autocert.DirCache(s.cfg.TLSCertDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(s.cfg.TLSDomains...),
	}

	srv := s.newHTTPServer(s.cfg.ListenAddr)
	srv.TLSConfig = mgr.TLSConfig()
	srv.TLSConfig.MinVersion = tls.VersionTLS12
	srv.TLSConfig.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256}

	// :80 listener handles ACME http-01 challenges and redirects everything else
	// to https://<primary domain>.
	primary := ""
	if len(s.cfg.TLSDomains) > 0 {
		primary = s.cfg.TLSDomains[0]
	}
	redirHandler := mgr.HTTPHandler(redirectToHTTPSHandler(primary))
	redirSrv := &http.Server{
		Addr:              ":80",
		Handler:           redirHandler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	slog.Info("listening (autocert)",
		"addr", s.cfg.ListenAddr, "domains", s.cfg.TLSDomains, "cert_dir", s.cfg.TLSCertDir)

	listenFn := func() error { return srv.ListenAndServeTLS("", "") }
	return runMultiServer(ctx, srv, redirSrv, listenFn)
}

// ── server lifecycle helpers ──────────────────────────────────────────────────

func (s *Server) newHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func (s *Server) newRedirectServer(addr string) *http.Server {
	target := s.cfg.BaseURL
	return &http.Server{
		Addr:              addr,
		Handler:           redirectToHTTPSHandler(stripScheme(target)),
		ReadHeaderTimeout: readHeaderTimeout,
	}
}

// runWithShutdown runs srv in a goroutine and shuts it down when ctx is canceled.
func runWithShutdown(ctx context.Context, srv *http.Server, listen func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- listen() }()

	select {
	case <-ctx.Done():
		return shutdown(srv)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// runMultiServer runs the primary TLS server plus an optional :80 redirect/
// challenge server. It returns when either fails or ctx is canceled.
func runMultiServer(ctx context.Context, primary, secondary *http.Server, listen func() error) error {
	primaryErr := make(chan error, 1)
	go func() { primaryErr <- listen() }()

	secondaryErr := make(chan error, 1)
	if secondary != nil {
		go func() { secondaryErr <- secondary.ListenAndServe() }()
	}

	select {
	case <-ctx.Done():
		err := shutdown(primary)
		if secondary != nil {
			if sErr := shutdown(secondary); sErr != nil && err == nil {
				err = sErr
			}
		}
		return err
	case err := <-primaryErr:
		if secondary != nil {
			_ = shutdown(secondary)
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-secondaryErr:
		_ = shutdown(primary)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("redirect listener: %w", err)
	}
}

func shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown %s: %w", srv.Addr, err)
	}
	return nil
}

// hardenedTLSConfig returns the spec.md §12.2 TLS config for self-terminating modes.
func hardenedTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
	}
}

// redirectToHTTPSHandler 301-redirects every request to https://host{path}.
// If host is empty it reuses r.Host, which is the safe behavior for autocert
// where the request reaches us on a domain we already trust.
func redirectToHTTPSHandler(host string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://"
		if host != "" {
			target += host
		} else {
			target += r.Host
		}
		target += r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

func stripScheme(rawURL string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(rawURL, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(rawURL, prefix), "/")
		}
	}
	return strings.TrimSuffix(rawURL, "/")
}
