package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

const (
	recentPostsLimit         = 10
	defaultProfilePostsPerPage = 20
)

// UserHandlers handles user profile and post-history pages.
type UserHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewUserHandler constructs a UserHandlers.
func NewUserHandler(st store.Store, r *render.Renderer) *UserHandlers {
	return &UserHandlers{store: st, renderer: r}
}

// ProfilePostRow bundles a post with its thread for the profile recent-posts list.
type ProfilePostRow struct {
	Post   *model.Post
	Thread *model.Thread
}

// ProfilePage serves GET /u/{username}: user profile with avatar, stats, recent posts.
func (h *UserHandlers) ProfilePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := middleware.UserFromContext(ctx)
	username := r.PathValue("username")

	profileUser, err := h.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get user by username", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	recentResult, err := h.store.ListPostsByAuthor(ctx, profileUser.ID, store.PageRequest{Page: 1, PerPage: recentPostsLimit})
	if err != nil {
		slog.Error("list recent posts by author", "user_id", profileUser.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	recentPosts := make([]ProfilePostRow, 0, len(recentResult.Items))
	for _, pwa := range recentResult.Items {
		thread, err := h.store.GetThreadByID(ctx, pwa.Post.ThreadID)
		if err != nil {
			slog.Warn("get thread for recent post", "thread_id", pwa.Post.ThreadID, "error", err)
			continue
		}
		recentPosts = append(recentPosts, ProfilePostRow{Post: pwa.Post, Thread: thread})
	}

	isOwnProfile := viewer != nil && viewer.ID == profileUser.ID
	canMod := viewer != nil && viewer.Role.CanModerate()
	canSendPM := viewer != nil && !isOwnProfile && !profileUser.Banned

	data := BaseData(r)
	data["Title"] = profileUser.DisplayNameOrUsername() + " - Profile"
	data["ProfileUser"] = profileUser
	data["RecentPosts"] = recentPosts
	data["TotalPosts"] = recentResult.Total
	data["IsOwnProfile"] = isOwnProfile
	data["CanMod"] = canMod
	data["CanSendPM"] = canSendPM

	if err := h.renderer.Render(w, "profile.html", data); err != nil {
		slog.Error("render profile page", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// UserPostsPage serves GET /u/{username}/posts: paginated post history with thread context.
func (h *UserHandlers) UserPostsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := middleware.UserFromContext(ctx)
	username := r.PathValue("username")

	profileUser, err := h.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get user by username for posts page", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	page := pageFromQuery(r)
	postsResult, err := h.store.ListPostsByAuthor(ctx, profileUser.ID, store.PageRequest{Page: page, PerPage: defaultProfilePostsPerPage})
	if err != nil {
		slog.Error("list posts by author for posts page", "user_id", profileUser.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	posts := make([]ProfilePostRow, 0, len(postsResult.Items))
	for _, pwa := range postsResult.Items {
		thread, err := h.store.GetThreadByID(ctx, pwa.Post.ThreadID)
		if err != nil {
			slog.Warn("get thread for post history", "thread_id", pwa.Post.ThreadID, "error", err)
			continue
		}
		posts = append(posts, ProfilePostRow{Post: pwa.Post, Thread: thread})
	}

	baseURL := "/u/" + username + "/posts"

	data := BaseData(r)
	data["Title"] = profileUser.DisplayNameOrUsername() + " - Post History"
	data["ProfileUser"] = profileUser
	data["Posts"] = posts
	data["Pagination"] = buildPagination(postsResult.Page, postsResult.TotalPages, baseURL, "#posts-content")
	data["IsOwnProfile"] = viewer != nil && viewer.ID == profileUser.ID

	if render.IsHTMXRequest(r) {
		if err := h.renderer.RenderContent(w, "user_posts.html", data); err != nil {
			slog.Error("render user posts content htmx", "username", username, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	if err := h.renderer.Render(w, "user_posts.html", data); err != nil {
		slog.Error("render user posts page", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// BanUser handles POST /u/{username}/ban: bans a user (mod/admin only).
func (h *UserHandlers) BanUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := middleware.UserFromContext(ctx)
	if viewer == nil || !viewer.Role.CanModerate() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := r.PathValue("username")
	target, err := h.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get user for ban", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if target.Role.CanAdmin() {
		http.Error(w, "Cannot ban an admin", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))

	if err := h.store.BanUser(ctx, target.ID, reason); err != nil {
		slog.Error("ban user", "user_id", target.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.DeleteUserSessions(ctx, target.ID); err != nil {
		slog.Warn("delete user sessions on ban", "user_id", target.ID, "error", err)
	}

	slog.Info("user banned", "target_id", target.ID, "target_username", target.Username, "mod_id", viewer.ID, "reason", reason)
	http.Redirect(w, r, "/u/"+username, http.StatusSeeOther)
}

// UnbanUser handles POST /u/{username}/unban: unbans a user (mod/admin only).
func (h *UserHandlers) UnbanUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := middleware.UserFromContext(ctx)
	if viewer == nil || !viewer.Role.CanModerate() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := r.PathValue("username")
	target, err := h.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get user for unban", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.UnbanUser(ctx, target.ID); err != nil {
		slog.Error("unban user", "user_id", target.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	slog.Info("user unbanned", "target_id", target.ID, "target_username", target.Username, "mod_id", viewer.ID)
	http.Redirect(w, r, "/u/"+username, http.StatusSeeOther)
}
