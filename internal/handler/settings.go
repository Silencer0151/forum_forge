package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
	"github.com/nitro/forum_forge/internal/upload"
)

const (
	maxDisplayNameLen = 50
	maxSignatureLen   = 256
	maxAvatarURLLen   = 500
)

// SettingsConfig holds deployment-level options the settings handler needs.
type SettingsConfig struct {
	// Secure marks session cookies HTTPS-only. Should match AuthConfig.Secure.
	Secure bool
	// IntegratedMode disables avatar and password editing when AUTH_MODE=integrated,
	// because those are managed by the external identity provider.
	IntegratedMode bool
}

// SettingsHandlers serves GET and POST /settings.
type SettingsHandlers struct {
	store     store.Store
	renderer  *render.Renderer
	authSvc   *auth.Service
	uploadDir string
	cfg       SettingsConfig
}

// NewSettingsHandler constructs a SettingsHandlers.
func NewSettingsHandler(st store.Store, r *render.Renderer, authSvc *auth.Service, uploadDir string, cfg SettingsConfig) *SettingsHandlers {
	return &SettingsHandlers{
		store:     st,
		renderer:  r,
		authSvc:   authSvc,
		uploadDir: uploadDir,
		cfg:       cfg,
	}
}

// SettingsPage serves GET /settings. Requires authentication.
func (h *SettingsHandlers) SettingsPage(w http.ResponseWriter, r *http.Request) {
	viewer := middleware.UserFromContext(r.Context())
	if viewer == nil {
		http.Redirect(w, r, "/auth/login?next=/settings", http.StatusSeeOther)
		return
	}

	data := h.newData(r, viewer)
	if r.URL.Query().Get("saved") != "" {
		data["Notice"] = "Your settings have been saved."
	}
	h.render(w, "settings.html", data)
}

// UpdateSettings handles POST /settings. A hidden field "section" distinguishes
// the profile section (display name, signature, avatar) from the password section.
func (h *SettingsHandlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	viewer := middleware.UserFromContext(r.Context())
	if viewer == nil {
		http.Redirect(w, r, "/auth/login?next=/settings", http.StatusSeeOther)
		return
	}

	// ParseMultipartForm to support avatar file uploads. The limit is the avatar
	// cap plus a generous allowance for other form fields.
	if err := r.ParseMultipartForm(upload.MaxAvatarBytes + 1<<20); err != nil {
		// Fall back to url-encoded form (e.g. password section with no file field).
		if formErr := r.ParseForm(); formErr != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}

	switch r.FormValue("section") {
	case "password":
		h.handlePasswordChange(w, r, viewer)
	default:
		h.handleProfileUpdate(w, r, viewer)
	}
}

// handleProfileUpdate saves display name, signature, and avatar changes.
func (h *SettingsHandlers) handleProfileUpdate(w http.ResponseWriter, r *http.Request, viewer *model.User) {
	ctx := r.Context()

	// Re-fetch the user so we apply changes on top of the current DB state.
	user, err := h.store.GetUserByID(ctx, viewer.ID)
	if err != nil {
		slog.Error("settings: get user for profile update", "user_id", viewer.ID, "error", err)
		h.renderError(w, r, viewer, "Something went wrong. Please try again.")
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	signature := strings.TrimSpace(r.FormValue("signature"))
	avatarURL := strings.TrimSpace(r.FormValue("avatar_url"))

	if len(displayName) > maxDisplayNameLen {
		h.renderError(w, r, user, fmt.Sprintf("Display name must be %d characters or fewer.", maxDisplayNameLen))
		return
	}
	if len(signature) > maxSignatureLen {
		h.renderError(w, r, user, fmt.Sprintf("Signature must be %d characters or fewer.", maxSignatureLen))
		return
	}

	// Avatar: integrated mode locks avatar to the identity provider.
	if !h.cfg.IntegratedMode {
		// File upload takes priority over URL input.
		if file, _, fileErr := r.FormFile("avatar"); fileErr == nil {
			defer file.Close()
			result, upErr := upload.SaveAvatar(file, user.ID, h.uploadDir)
			if upErr != nil {
				h.renderError(w, r, user, "Avatar upload failed: "+upErr.Error())
				return
			}
			user.AvatarURL = result.URL
		} else if avatarURL != "" {
			if len(avatarURL) > maxAvatarURLLen {
				h.renderError(w, r, user, "Avatar URL is too long.")
				return
			}
			user.AvatarURL = avatarURL
		}
	}

	user.DisplayName = displayName
	user.Signature = signature

	if err := h.store.UpdateUser(ctx, user); err != nil {
		slog.Error("settings: update profile", "user_id", user.ID, "error", err)
		h.renderError(w, r, user, "Could not save settings. Please try again.")
		return
	}

	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// handlePasswordChange verifies the current password, hashes the new one,
// invalidates all existing sessions, and redirects the user to log in again.
func (h *SettingsHandlers) handlePasswordChange(w http.ResponseWriter, r *http.Request, viewer *model.User) {
	ctx := r.Context()

	user, err := h.store.GetUserByID(ctx, viewer.ID)
	if err != nil {
		slog.Error("settings: get user for password change", "user_id", viewer.ID, "error", err)
		h.renderError(w, r, viewer, "Something went wrong. Please try again.")
		return
	}

	currentPwd := r.FormValue("current_password")
	newPwd := r.FormValue("new_password")
	confirmPwd := r.FormValue("confirm_password")

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPwd)); err != nil {
		h.renderError(w, r, user, "Current password is incorrect.")
		return
	}
	if newPwd != confirmPwd {
		h.renderError(w, r, user, "New passwords do not match.")
		return
	}
	if len(newPwd) < auth.MinPasswordLength {
		h.renderError(w, r, user, fmt.Sprintf("New password must be at least %d characters.", auth.MinPasswordLength))
		return
	}

	hash, err := h.authSvc.HashPassword(newPwd)
	if err != nil {
		slog.Error("settings: hash new password", "user_id", user.ID, "error", err)
		h.renderError(w, r, user, "Could not update password. Please try again.")
		return
	}

	user.PasswordHash = hash
	if err := h.store.UpdateUser(ctx, user); err != nil {
		slog.Error("settings: save new password", "user_id", user.ID, "error", err)
		h.renderError(w, r, user, "Could not save password. Please try again.")
		return
	}

	// Invalidate all sessions so other devices are logged out.
	if err := h.store.DeleteUserSessions(ctx, user.ID); err != nil {
		slog.Warn("settings: delete sessions after password change", "user_id", user.ID, "error", err)
	}

	// Clear the browser cookie — the session no longer exists server-side.
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *SettingsHandlers) newData(r *http.Request, user *model.User) map[string]any {
	data := BaseData(r)
	data["Title"] = "Settings"
	data["SettingsUser"] = user
	data["Error"] = ""
	data["Notice"] = ""
	data["IntegratedMode"] = h.cfg.IntegratedMode
	return data
}

func (h *SettingsHandlers) renderError(w http.ResponseWriter, r *http.Request, user *model.User, msg string) {
	data := h.newData(r, user)
	data["Error"] = msg
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	h.render(w, "settings.html", data)
}

func (h *SettingsHandlers) render(w http.ResponseWriter, name string, data map[string]any) {
	if err := h.renderer.Render(w, name, data); err != nil {
		slog.Error("settings: render", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

