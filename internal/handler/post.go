package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

const defaultPostsPerPage = 20

// PostHandlers handles thread-view and reply endpoints.
type PostHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewPostHandler constructs a PostHandlers.
func NewPostHandler(st store.Store, r *render.Renderer) *PostHandlers {
	return &PostHandlers{store: st, renderer: r}
}

// PostDisplayRow bundles everything the post partial needs to render one post.
type PostDisplayRow struct {
	Post           *model.Post
	Author         *model.User
	DeletedByName  string
	ReactionCounts []store.ReactionCount
	UserReacted    map[model.ReactionType]bool
	Depth          int
	CanEdit        bool
	CanDelete      bool
	CanReport      bool
	CanReact       bool
}

// ThreadPage serves GET /t/{thread_id}: paginated posts with reply form.
func (h *PostHandlers) ThreadPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)

	threadID, err := strconv.ParseInt(r.PathValue("thread_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	thread, err := h.store.GetThreadByID(ctx, threadID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get thread by id", "id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sub, err := h.store.GetSubcategoryByID(ctx, thread.SubcategoryID)
	if err != nil {
		slog.Error("get subcategory by id", "id", thread.SubcategoryID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	cat, err := h.store.GetCategoryByID(ctx, sub.CategoryID)
	if err != nil {
		slog.Error("get category by id", "id", sub.CategoryID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !cat.IsPublic && user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Only increment view count on full page loads, not HTMX partial requests.
	if !render.IsHTMXRequest(r) {
		if err := h.store.IncrementViewCount(ctx, threadID); err != nil {
			slog.Warn("increment view count", "thread_id", threadID, "error", err)
		}
	}

	page := pageFromQuery(r)
	perPage := defaultPostsPerPage
	editWindowMin := 30
	settings, err := h.store.GetSettings(ctx)
	if err == nil {
		if settings.PostsPerPage > 0 {
			perPage = settings.PostsPerPage
		}
		if settings.EditWindowMinutes > 0 {
			editWindowMin = settings.EditWindowMinutes
		}
	}

	posts, err := h.store.ListPostsByThread(ctx, threadID, store.PageRequest{Page: page, PerPage: perPage})
	if err != nil {
		slog.Error("list posts by thread", "thread_id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	postIDs := make([]int64, len(posts.Items))
	for i, p := range posts.Items {
		postIDs[i] = p.Post.ID
	}

	var userReactions map[int64][]model.ReactionType
	if user != nil && len(postIDs) > 0 {
		userReactions, err = h.store.GetUserReactionsForPosts(ctx, user.ID, postIDs)
		if err != nil {
			slog.Warn("get user reactions for posts", "error", err)
			userReactions = nil
		}
	}

	rows := make([]PostDisplayRow, 0, len(posts.Items))
	editWindow := time.Duration(editWindowMin) * time.Minute
	for _, p := range posts.Items {
		counts, err := h.store.GetReactionCounts(ctx, p.Post.ID)
		if err != nil {
			slog.Warn("get reaction counts", "post_id", p.Post.ID, "error", err)
			counts = nil
		}

		reacted := make(map[model.ReactionType]bool)
		if userReactions != nil {
			for _, rt := range userReactions[p.Post.ID] {
				reacted[rt] = true
			}
		}

		depth := 0
		if p.Post.ParentID != nil {
			depth = 1
		}

		isAuthor := user != nil && user.ID == p.Post.AuthorID
		withinWindow := time.Since(p.Post.CreatedAt) < editWindow
		isMod := user != nil && user.Role.CanModerate()

		row := PostDisplayRow{
			Post:           p.Post,
			Author:         p.Author,
			ReactionCounts: counts,
			UserReacted:    reacted,
			Depth:          depth,
			CanEdit:        !p.Post.IsDeleted && ((isAuthor && withinWindow) || isMod),
			CanDelete:      isMod,
			CanReport:      user != nil && !isAuthor && !p.Post.IsDeleted,
			CanReact:       user != nil && !user.Banned && !p.Post.IsDeleted,
		}

		if p.Post.IsDeleted && p.Post.EditedBy != nil {
			if du, err := h.store.GetUserByID(ctx, *p.Post.EditedBy); err == nil {
				row.DeletedByName = du.DisplayNameOrUsername()
			} else {
				row.DeletedByName = "a moderator"
			}
		}

		rows = append(rows, row)
	}

	baseURL := fmt.Sprintf("/t/%d", threadID)
	canReply := user != nil && !user.Banned && !thread.IsLocked && user.Role != model.RoleGuest
	canMod := user != nil && user.Role.CanModerate()

	data := BaseData(r)
	data["Title"] = thread.Title
	data["Thread"] = thread
	data["Category"] = cat
	data["Subcategory"] = sub
	data["Posts"] = posts
	data["PostRows"] = rows
	data["Pagination"] = buildPagination(posts.Page, posts.TotalPages, baseURL)
	data["CanReply"] = canReply
	data["CanMod"] = canMod
	data["FormFormat"] = string(model.BodyFormatMarkdown)

	if err := h.renderer.Render(w, "thread.html", data); err != nil {
		slog.Error("render thread page", "thread_id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// ReplyToThread handles POST /t/{thread_id}/reply: validates, creates post, redirects.
func (h *PostHandlers) ReplyToThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)

	if user == nil || user.Banned {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	threadID, err := strconv.ParseInt(r.PathValue("thread_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	thread, err := h.store.GetThreadByID(ctx, threadID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get thread for reply", "id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if thread.IsLocked && !user.Role.CanModerate() {
		http.Error(w, "Thread is locked", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	body := strings.TrimSpace(r.FormValue("body"))
	format := model.BodyFormat(r.FormValue("body_format"))
	if !format.IsValid() {
		format = model.BodyFormatMarkdown
	}

	var parentID *int64
	if pidStr := r.FormValue("parent_id"); pidStr != "" {
		if pid, err := strconv.ParseInt(pidStr, 10, 64); err == nil && pid > 0 {
			parentID = &pid
		}
	}

	if body == "" {
		http.Redirect(w, r, fmt.Sprintf("/t/%d", threadID), http.StatusSeeOther)
		return
	}

	now := time.Now()
	post := &model.Post{
		ThreadID:   threadID,
		AuthorID:   user.ID,
		ParentID:   parentID,
		Body:       body,
		BodyFormat: format,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := h.store.CreatePost(ctx, post); err != nil {
		slog.Error("create reply post", "thread_id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdateLastPost(ctx, threadID, user.ID, now); err != nil {
		slog.Warn("update last post", "thread_id", threadID, "error", err)
	}

	if err := h.store.IncrementPostCount(ctx, user.ID); err != nil {
		slog.Warn("increment post count", "user_id", user.ID, "error", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/t/%d#post-%d", threadID, post.ID), http.StatusSeeOther)
}
