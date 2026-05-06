package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
)

// Email template names registered with the Mailer. Kept as constants so the
// handler and the email-template loader cannot drift apart silently.
const (
	emailTemplateVerify = "verify"
	emailTemplateReset  = "reset"
)

// AuthHandlers serves the standalone (email+password) auth pages. The Service
// holds the persistence + crypto logic; this struct is just the HTTP shell.
type AuthHandlers struct {
	svc      *auth.Service
	tokens   *auth.TokenService
	mailer   *auth.Mailer
	renderer *render.Renderer
	cfg      AuthConfig
}

// AuthConfig configures the handler set.
type AuthConfig struct {
	// Secure marks session cookies HTTPS-only. Set true when BASE_URL is
	// https or the server runs behind a TLS-terminating proxy.
	Secure bool
	// BaseURL is the canonical externally-visible URL of the forum, used to
	// build absolute links in outbound emails (verification, reset). Should
	// match FORUM_BASE_URL from spec.md §14.
	BaseURL string
}

// NewAuth constructs an AuthHandlers value. tokens and mailer are required —
// the verify/forgot/reset flows will panic on nil at call time, so wire them
// up explicitly. Tests that don't exercise those flows can pass the same
// no-op instances built by auth.NewTokenService and auth.NewMailer.
func NewAuth(svc *auth.Service, tokens *auth.TokenService, mailer *auth.Mailer, renderer *render.Renderer, cfg AuthConfig) *AuthHandlers {
	return &AuthHandlers{
		svc:      svc,
		tokens:   tokens,
		mailer:   mailer,
		renderer: renderer,
		cfg:      cfg,
	}
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
	d["Notice"] = ""
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

// Register creates a new account, opens a session, and dispatches the email
// verification link in the background. Failure to send the email is logged
// but does not block the response — the user can request a new verification
// email later.
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

	h.sendVerificationEmail(r.Context(), res.User)

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

// ── verify email ─────────────────────────────────────────────────────────────

// VerifyEmail handles GET /auth/verify?token=X. On success it marks the user
// as verified, deletes the token, and redirects to the home page with a
// "verified" query flag the layout can show as a flash. On failure it renders
// a generic error page so an attacker can't distinguish bad tokens from
// expired ones.
func (h *AuthHandlers) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	rawToken := r.URL.Query().Get("token")
	if _, err := h.tokens.ConsumeVerification(r.Context(), rawToken); err != nil {
		if errors.Is(err, auth.ErrTokenInvalid) {
			h.renderVerifyResult(w, r, false)
			return
		}
		slog.Error("auth: verify", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.renderVerifyResult(w, r, true)
}

func (h *AuthHandlers) renderVerifyResult(w http.ResponseWriter, r *http.Request, ok bool) {
	data := BaseData(r)
	data["Title"] = "Email Verification"
	data["Verified"] = ok
	if ok {
		data["Notice"] = "Your email is now verified."
	} else {
		w.WriteHeader(http.StatusBadRequest)
		data["Error"] = "This verification link is invalid or has expired."
	}
	h.render(w, "verify_result.html", data)
}

// ── forgot password ──────────────────────────────────────────────────────────

// ForgotPasswordPage renders the form that asks for the user's email.
func (h *AuthHandlers) ForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	if middleware.UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := BaseData(r)
	data["Title"] = "Forgot Password"
	data["Email"] = ""
	data["Submitted"] = false
	h.render(w, "forgot_password.html", data)
}

// ForgotPassword handles POST /auth/forgot-password. It always renders the
// "if an account exists, an email is on its way" confirmation, regardless of
// whether the email maps to a real user, to prevent enumeration. When a real
// user is found, it generates a reset token and dispatches the email.
func (h *AuthHandlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.PostFormValue("email")))

	user, err := h.tokens.FindUserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, auth.ErrUserNotFound) {
		slog.Error("auth: forgot-password lookup", "error", err)
	}
	if user != nil && !user.Banned {
		h.sendResetEmail(r.Context(), user)
	}

	data := BaseData(r)
	data["Title"] = "Forgot Password"
	data["Email"] = email
	data["Submitted"] = true
	h.render(w, "forgot_password.html", data)
}

// ── reset password ───────────────────────────────────────────────────────────

// ResetPasswordPage renders the new-password form. The token is carried as a
// hidden field so the user can't lose it by typing into the URL bar.
func (h *AuthHandlers) ResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	rawToken := r.URL.Query().Get("token")
	if _, err := h.tokens.ValidateReset(r.Context(), rawToken); err != nil {
		if errors.Is(err, auth.ErrTokenInvalid) {
			h.renderResetInvalid(w, r)
			return
		}
		slog.Error("auth: reset validate", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data := h.newResetData(r, rawToken)
	h.render(w, "reset_password.html", data)
}

// ResetPassword handles POST /auth/reset-password.
func (h *AuthHandlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rawToken := r.PostFormValue("token")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")

	if password != confirm {
		w.WriteHeader(http.StatusBadRequest)
		data := h.newResetData(r, rawToken)
		data["Error"] = "Passwords do not match."
		h.render(w, "reset_password.html", data)
		return
	}
	if len(password) < auth.MinPasswordLength {
		w.WriteHeader(http.StatusBadRequest)
		data := h.newResetData(r, rawToken)
		data["Error"] = "Password must be at least 8 characters."
		h.render(w, "reset_password.html", data)
		return
	}

	hash, err := h.svc.HashPassword(password)
	if err != nil {
		slog.Error("auth: reset hash", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if _, err := h.tokens.ConsumeReset(r.Context(), rawToken, hash); err != nil {
		if errors.Is(err, auth.ErrTokenInvalid) {
			h.renderResetInvalid(w, r)
			return
		}
		slog.Error("auth: reset consume", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Sessions were invalidated server-side; clear the user's cookie too.
	h.clearSessionCookie(w)
	http.Redirect(w, r, "/auth/login?reset=1", http.StatusSeeOther)
}

func (h *AuthHandlers) newResetData(r *http.Request, token string) map[string]any {
	d := BaseData(r)
	d["Title"] = "Reset Password"
	d["Token"] = token
	d["Error"] = ""
	return d
}

func (h *AuthHandlers) renderResetInvalid(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	data := BaseData(r)
	data["Title"] = "Reset Password"
	data["Error"] = "This reset link is invalid or has expired. Request a new one."
	h.render(w, "reset_invalid.html", data)
}

// ── email dispatch helpers ───────────────────────────────────────────────────

func (h *AuthHandlers) sendVerificationEmail(ctx context.Context, u *model.User) {
	rawToken, err := h.tokens.IssueVerification(ctx, u.ID)
	if err != nil {
		slog.Error("auth: issue verify token", "error", err, "user_id", u.ID)
		return
	}
	verifyURL := h.buildAbsoluteURL("/auth/verify", url.Values{"token": {rawToken}})
	if err := h.mailer.Send(ctx, emailTemplateVerify, u.Email, "Verify your ForumForge email", map[string]any{
		"Username":  u.Username,
		"VerifyURL": verifyURL,
	}); err != nil {
		slog.Error("auth: send verify email", "error", err, "user_id", u.ID)
	}
}

func (h *AuthHandlers) sendResetEmail(ctx context.Context, u *model.User) {
	rawToken, err := h.tokens.IssueReset(ctx, u.ID)
	if err != nil {
		slog.Error("auth: issue reset token", "error", err, "user_id", u.ID)
		return
	}
	resetURL := h.buildAbsoluteURL("/auth/reset-password", url.Values{"token": {rawToken}})
	if err := h.mailer.Send(ctx, emailTemplateReset, u.Email, "Reset your ForumForge password", map[string]any{
		"Username": u.Username,
		"ResetURL": resetURL,
	}); err != nil {
		slog.Error("auth: send reset email", "error", err, "user_id", u.ID)
	}
}

// buildAbsoluteURL joins BaseURL with path and encoded query so emails always
// carry a clickable link. Falls back to a relative URL if BaseURL is missing.
func (h *AuthHandlers) buildAbsoluteURL(path string, q url.Values) string {
	base := strings.TrimRight(h.cfg.BaseURL, "/")
	if base == "" {
		return path + "?" + q.Encode()
	}
	return base + path + "?" + q.Encode()
}

// ── shared helpers ───────────────────────────────────────────────────────────

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
		Secure:   h.cfg.Secure,
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
		Secure:   h.cfg.Secure,
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
