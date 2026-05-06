package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/render"
)

// AuthHandlers serves the standalone (email+password) auth pages. The Service
// holds the persistence + crypto logic; this struct is just the HTTP shell.
type AuthHandlers struct {
	svc      *auth.Service
	renderer *render.Renderer
	secure   bool
}

// AuthConfig configures the handler set.
type AuthConfig struct {
	// Secure marks session cookies HTTPS-only. Set true when BASE_URL is
	// https or the server runs behind a TLS-terminating proxy.
	Secure bool
}

// NewAuth constructs an AuthHandlers value.
func NewAuth(svc *auth.Service, renderer *render.Renderer, cfg AuthConfig) *AuthHandlers {
	return &AuthHandlers{svc: svc, renderer: renderer, secure: cfg.Secure}
}

// LoginPage renders the login form.
func (h *AuthHandlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	if middleware.UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := newLoginData(r)
	data["Next"] = sanitizeNext(r.URL.Query().Get("next"))
	h.render(w, "login.html", data)
}

// newLoginData seeds every template-referenced key so html/template never
// renders "<no value>" for an absent field on first paint.
func newLoginData(r *http.Request) map[string]any {
	d := BaseData(r)
	d["Title"] = "Log In"
	d["Email"] = ""
	d["Next"] = ""
	d["Error"] = ""
	return d
}

// newRegisterData mirrors newLoginData for the register page.
func newRegisterData(r *http.Request) map[string]any {
	d := BaseData(r)
	d["Title"] = "Register"
	d["Username"] = ""
	d["Email"] = ""
	d["Error"] = ""
	d["FieldErrors"] = map[string]string{}
	return d
}

// Login validates credentials and starts a session.
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	in := auth.LoginInput{
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
	}
	next := sanitizeNext(r.PostFormValue("next"))

	res, err := h.svc.Login(r.Context(), in)
	if err != nil {
		h.renderLoginError(w, r, in, next, err)
		return
	}

	h.setSessionCookie(w, res.Session.ID)
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// RegisterPage renders the registration form.
func (h *AuthHandlers) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if middleware.UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.render(w, "register.html", newRegisterData(r))
}

// Register creates a new account and starts a session.
func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	in := auth.RegisterInput{
		Username: r.PostFormValue("username"),
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
	}

	res, err := h.svc.Register(r.Context(), in)
	if err != nil {
		h.renderRegisterError(w, r, in, err)
		return
	}

	h.setSessionCookie(w, res.Session.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout destroys the session and clears the cookie.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if sess := middleware.SessionFromContext(r.Context()); sess != nil {
		if err := h.svc.Logout(r.Context(), sess.ID); err != nil {
			slog.Error("auth: logout", "error", err)
		}
	} else if cookie, err := r.Cookie(middleware.SessionCookieName); err == nil && cookie.Value != "" {
		// Fall back to the cookie value when the session never made it
		// into the context (e.g. user changed device clocks, or the
		// session was already expired but the cookie is still around).
		_ = h.svc.Logout(r.Context(), cookie.Value)
	}
	h.clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *AuthHandlers) renderLoginError(w http.ResponseWriter, r *http.Request, in auth.LoginInput, next string, err error) {
	data := newLoginData(r)
	data["Email"] = in.Email
	data["Next"] = next

	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		data["Error"] = "Invalid email or password."
	case errors.Is(err, auth.ErrUserBanned):
		data["Error"] = "This account has been banned."
	default:
		slog.Error("auth: login", "error", err)
		data["Error"] = "Something went wrong. Please try again."
	}
	w.WriteHeader(http.StatusUnauthorized)
	h.render(w, "login.html", data)
}

func (h *AuthHandlers) renderRegisterError(w http.ResponseWriter, r *http.Request, in auth.RegisterInput, err error) {
	data := newRegisterData(r)
	data["Username"] = in.Username
	data["Email"] = in.Email

	status := http.StatusBadRequest
	var fieldErrs auth.FieldErrors
	switch {
	case errors.As(err, &fieldErrs):
		data["FieldErrors"] = map[string]string(fieldErrs)
	case errors.Is(err, auth.ErrEmailTaken):
		data["FieldErrors"] = map[string]string{"email": "An account with that email already exists."}
		status = http.StatusConflict
	case errors.Is(err, auth.ErrUsernameTaken):
		data["FieldErrors"] = map[string]string{"username": "That username is taken."}
		status = http.StatusConflict
	default:
		slog.Error("auth: register", "error", err)
		data["Error"] = "Something went wrong. Please try again."
		status = http.StatusInternalServerError
	}

	w.WriteHeader(status)
	h.render(w, "register.html", data)
}

func (h *AuthHandlers) render(w http.ResponseWriter, name string, data map[string]any) {
	if err := h.renderer.Render(w, name, data); err != nil {
		slog.Error("auth: render", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func (h *AuthHandlers) setSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.SessionLifetime.Seconds()),
	})
}

func (h *AuthHandlers) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// sanitizeNext accepts only relative same-host paths to prevent open redirects.
// Any absolute URL or scheme is silently dropped.
func sanitizeNext(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	return u.RequestURI()
}

