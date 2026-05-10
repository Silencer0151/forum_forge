package handler

import (
	"context"
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
	CanQuote       bool
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
			CanQuote:       user != nil && !p.Post.IsDeleted,
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

	quoteText := ""
	if quoteIDStr := r.URL.Query().Get("quote"); quoteIDStr != "" {
		if quoteID, err2 := strconv.ParseInt(quoteIDStr, 10, 64); err2 == nil {
			if qPost, err2 := h.store.GetPostByID(ctx, quoteID); err2 == nil && !qPost.IsDeleted {
				qUsername := "unknown"
				if qAuthor, err2 := h.store.GetUserByID(ctx, qPost.AuthorID); err2 == nil {
					qUsername = qAuthor.DisplayNameOrUsername()
				}
				quoteText = buildQuoteText(qUsername, qPost.Body)
			}
		}
	}

	var draftBody string
	var draftID int64
	if user != nil {
		if d, dErr := h.store.GetDraftByThread(ctx, user.ID, threadID); dErr == nil {
			draftBody = d.Body
			draftID = d.ID
		}
	}

	data := BaseData(r)
	data["Title"] = thread.Title
	data["Thread"] = thread
	data["Category"] = cat
	data["Subcategory"] = sub
	data["Posts"] = posts
	data["PostRows"] = rows
	data["Pagination"] = buildPagination(posts.Page, posts.TotalPages, baseURL, "#thread-wrapper")
	data["CanReply"] = canReply
	data["CanMod"] = canMod
	data["FormFormat"] = string(model.BodyFormatMarkdown)
	data["QuoteText"] = quoteText
	data["DraftBody"] = draftBody
	data["DraftID"] = draftID

	if render.IsHTMXRequest(r) {
		if err := h.renderer.RenderContent(w, "thread.html", data); err != nil {
			slog.Error("render thread content (htmx)", "thread_id", threadID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}
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

	// Best-effort: delete any saved draft for this thread now that the reply is posted.
	if d, dErr := h.store.GetDraftByThread(ctx, user.ID, threadID); dErr == nil {
		if dErr := h.store.DeleteDraft(ctx, d.ID); dErr != nil {
			slog.Warn("delete draft after reply", "thread_id", threadID, "error", dErr)
		}
	}

	if render.IsHTMXRequest(r) {
		editWindowMin := 30
		if settings, err := h.store.GetSettings(ctx); err == nil && settings.EditWindowMinutes > 0 {
			editWindowMin = settings.EditWindowMinutes
		}
		row, err := h.buildSinglePostRow(ctx, post, user, editWindowMin)
		if err != nil {
			slog.Error("build reply row", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if err := h.renderer.RenderPartial(w, "post.html", row); err != nil {
			slog.Error("render reply partial", "error", err)
		}
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/t/%d#post-%d", threadID, post.ID), http.StatusSeeOther)
}

// ReactionData holds data for the reaction_button.html partial.
type ReactionData struct {
	PostID         int64
	ReactionCounts []store.ReactionCount
	UserReacted    map[model.ReactionType]bool
	CanReact       bool
}

// buildSinglePostRow fetches author + reaction data for a single post and builds
// a PostDisplayRow ready for template rendering.
func (h *PostHandlers) buildSinglePostRow(ctx context.Context, post *model.Post, user *model.User, editWindowMin int) (PostDisplayRow, error) {
	author, err := h.store.GetUserByID(ctx, post.AuthorID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return PostDisplayRow{}, fmt.Errorf("get author: %w", err)
	}

	counts, _ := h.store.GetReactionCounts(ctx, post.ID)
	reacted := make(map[model.ReactionType]bool)
	if user != nil {
		userReactions, _ := h.store.GetUserReactionsForPosts(ctx, user.ID, []int64{post.ID})
		for _, rt := range userReactions[post.ID] {
			reacted[rt] = true
		}
	}

	depth := 0
	if post.ParentID != nil {
		depth = 1
	}

	editWindow := time.Duration(editWindowMin) * time.Minute
	isAuthor := user != nil && user.ID == post.AuthorID
	isMod := user != nil && user.Role.CanModerate()

	row := PostDisplayRow{
		Post:           post,
		Author:         author,
		ReactionCounts: counts,
		UserReacted:    reacted,
		Depth:          depth,
		CanEdit:        !post.IsDeleted && ((isAuthor && time.Since(post.CreatedAt) < editWindow) || isMod),
		CanDelete:      isMod,
		CanReport:      user != nil && !isAuthor && !post.IsDeleted,
		CanReact:       user != nil && !user.Banned && !post.IsDeleted,
		CanQuote:       user != nil && !post.IsDeleted,
	}

	if post.IsDeleted && post.EditedBy != nil {
		if du, err := h.store.GetUserByID(ctx, *post.EditedBy); err == nil {
			row.DeletedByName = du.DisplayNameOrUsername()
		} else {
			row.DeletedByName = "a moderator"
		}
	}

	return row, nil
}

// buildQuoteText formats a post body into a markdown block-quote attributed to username.
// The body is truncated to 500 characters if longer.
func buildQuoteText(username, body string) string {
	if len([]rune(body)) > 500 {
		runes := []rune(body)
		body = string(runes[:500]) + "..."
	}
	lines := strings.Split(body, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "> **%s wrote:**\n", username)
	for _, line := range lines {
		fmt.Fprintf(&b, "> %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

// QuotePost handles POST /p/{post_id}/quote.
// HTMX: returns the formatted quote text as plain text for the client to inject into the reply textarea.
// Non-HTMX: redirects to the thread page with ?quote={post_id} so ThreadPage pre-fills the textarea.
func (h *PostHandlers) QuotePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	postID, err := strconv.ParseInt(r.PathValue("post_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.store.GetPostByID(ctx, postID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get post for quote", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if post.IsDeleted {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	username := "unknown"
	if author, err := h.store.GetUserByID(ctx, post.AuthorID); err == nil {
		username = author.DisplayNameOrUsername()
	}

	quoteText := buildQuoteText(username, post.Body)

	if render.IsHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, quoteText)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/t/%d?quote=%d", post.ThreadID, post.ID), http.StatusSeeOther)
}

// getEditWindowMin loads the edit window setting, falling back to 30 minutes.
func (h *PostHandlers) getEditWindowMin(ctx context.Context) int {
	if settings, err := h.store.GetSettings(ctx); err == nil && settings.EditWindowMinutes > 0 {
		return settings.EditWindowMinutes
	}
	return 30
}

// GetPostView handles GET /p/{post_id}: returns the post partial (used by HTMX cancel-edit).
func (h *PostHandlers) GetPostView(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)

	postID, err := strconv.ParseInt(r.PathValue("post_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.store.GetPostByID(ctx, postID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get post", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	row, err := h.buildSinglePostRow(ctx, post, user, h.getEditWindowMin(ctx))
	if err != nil {
		slog.Error("build post row", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.renderer.RenderPartial(w, "post.html", row); err != nil {
		slog.Error("render post partial", "error", err)
	}
}

// GetEditForm handles GET /p/{post_id}/edit.
// HTMX: returns the inline edit form partial replacing the post.
// Non-HTMX: redirects to the thread page (inline editing requires JS).
func (h *PostHandlers) GetEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	postID, err := strconv.ParseInt(r.PathValue("post_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.store.GetPostByID(ctx, postID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get post for edit form", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if post.IsDeleted {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	editWindow := time.Duration(h.getEditWindowMin(ctx)) * time.Minute
	isAuthor := user.ID == post.AuthorID
	isMod := user.Role.CanModerate()
	if !((isAuthor && time.Since(post.CreatedAt) < editWindow) || isMod) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	editData := map[string]any{
		"Post":      post,
		"CSRFToken": middleware.CSRFTokenFromContext(ctx),
	}

	if render.IsHTMXRequest(r) {
		if err := h.renderer.RenderPartial(w, "post_edit_form.html", editData); err != nil {
			slog.Error("render edit form partial", "error", err)
		}
		return
	}

	// Non-HTMX fallback: redirect to thread page; inline editing requires HTMX.
	http.Redirect(w, r, fmt.Sprintf("/t/%d#post-%d", post.ThreadID, post.ID), http.StatusSeeOther)
}

// UpdatePost handles PUT /p/{post_id} (and POST with _method=PUT override).
func (h *PostHandlers) UpdatePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	postID, err := strconv.ParseInt(r.PathValue("post_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.store.GetPostByID(ctx, postID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get post for update", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if post.IsDeleted {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	editWindow := time.Duration(h.getEditWindowMin(ctx)) * time.Minute
	isAuthor := user.ID == post.AuthorID
	isMod := user.Role.CanModerate()
	if !((isAuthor && time.Since(post.CreatedAt) < editWindow) || isMod) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Error(w, "body required", http.StatusUnprocessableEntity)
		return
	}

	format := model.BodyFormat(r.FormValue("body_format"))
	if !format.IsValid() {
		format = model.BodyFormatMarkdown
	}

	now := time.Now()
	post.Body = body
	post.BodyFormat = format
	post.EditCount++
	post.EditedAt = &now
	post.EditedBy = &user.ID
	post.UpdatedAt = now

	if err := h.store.UpdatePost(ctx, post); err != nil {
		slog.Error("update post", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if render.IsHTMXRequest(r) {
		row, err := h.buildSinglePostRow(ctx, post, user, h.getEditWindowMin(ctx))
		if err != nil {
			slog.Error("build updated post row", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if err := h.renderer.RenderPartial(w, "post.html", row); err != nil {
			slog.Error("render updated post partial", "error", err)
		}
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/t/%d#post-%d", post.ThreadID, post.ID), http.StatusSeeOther)
}

// DeletePost handles DELETE /p/{post_id} (and POST with _method=DELETE override).
func (h *PostHandlers) DeletePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil || !user.Role.CanModerate() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	postID, err := strconv.ParseInt(r.PathValue("post_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.store.GetPostByID(ctx, postID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get post for delete", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.DeletePost(ctx, postID, user.ID); err != nil {
		slog.Error("delete post", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Reload the post to get IsDeleted=true and EditedBy set.
	post, err = h.store.GetPostByID(ctx, postID)
	if err != nil {
		// Fallback: mark deleted inline.
		post.IsDeleted = true
		post.EditedBy = &user.ID
	}

	if render.IsHTMXRequest(r) {
		row, err := h.buildSinglePostRow(ctx, post, user, h.getEditWindowMin(ctx))
		if err != nil {
			slog.Error("build deleted post row", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if err := h.renderer.RenderPartial(w, "post.html", row); err != nil {
			slog.Error("render deleted post partial", "error", err)
		}
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/t/%d", post.ThreadID), http.StatusSeeOther)
}

// ReactToPost handles POST /p/{post_id}/react: toggles a reaction and returns the
// reaction_button partial for HTMX swap.
func (h *PostHandlers) ReactToPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil || user.Banned {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	postID, err := strconv.ParseInt(r.PathValue("post_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.store.GetPostByID(ctx, postID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get post for react", "id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if post.IsDeleted {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	reactionType := model.ReactionType(r.URL.Query().Get("type"))
	if !reactionType.IsValid() {
		reactionType = model.ReactionTypeLike
	}

	if _, err := h.store.ToggleReaction(ctx, postID, user.ID, reactionType); err != nil {
		slog.Error("toggle reaction", "post_id", postID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	counts, _ := h.store.GetReactionCounts(ctx, postID)
	userReactions, _ := h.store.GetUserReactionsForPosts(ctx, user.ID, []int64{postID})
	reacted := make(map[model.ReactionType]bool)
	for _, rt := range userReactions[postID] {
		reacted[rt] = true
	}

	data := ReactionData{
		PostID:         postID,
		ReactionCounts: counts,
		UserReacted:    reacted,
		CanReact:       true,
	}

	if err := h.renderer.RenderPartial(w, "reaction_button.html", data); err != nil {
		slog.Error("render reaction button partial", "error", err)
		return
	}
}

// PostMethodOverride handles POST /p/{post_id} with _method form field for non-HTMX
// browsers that can only send GET/POST from HTML forms.
func (h *PostHandlers) PostMethodOverride(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch strings.ToUpper(r.FormValue("_method")) {
	case "PUT":
		h.UpdatePost(w, r)
	case "DELETE":
		h.DeletePost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
