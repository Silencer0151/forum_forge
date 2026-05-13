package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

const defaultReportsPerPage = 25

// ModerationHandlers handles the mod queue, thread lock/pin, and mod-initiated bans.
type ModerationHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewModerationHandler constructs a ModerationHandlers.
func NewModerationHandler(st store.Store, r *render.Renderer) *ModerationHandlers {
	return &ModerationHandlers{store: st, renderer: r}
}

// ReportWithMeta enriches a report with display-ready post and user context.
type ReportWithMeta struct {
	Report         *model.Report
	Post           *model.Post
	PostSnippet    string
	ReporterName   string
	PostAuthorName string
}

// requireMod checks that the request carries an authenticated moderator or admin.
// Writes 403 and returns false if not.
func requireMod(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	user := middleware.UserFromContext(r.Context())
	if user == nil || !user.Role.CanModerate() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	return user, true
}

// ReportsQueue handles GET /mod/reports: paginated list of open reports.
func (h *ModerationHandlers) ReportsQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, ok := requireMod(w, r); !ok {
		return
	}

	page := pageFromQuery(r)
	result, err := h.store.ListOpenReports(ctx, store.PageRequest{Page: page, PerPage: defaultReportsPerPage})
	if err != nil {
		slog.Error("list open reports", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	reports := make([]ReportWithMeta, 0, len(result.Items))
	for _, rep := range result.Items {
		meta := ReportWithMeta{Report: rep}

		if post, err := h.store.GetPostByID(ctx, rep.PostID); err == nil {
			meta.Post = post
			body := []rune(post.Body)
			if len(body) > 200 {
				meta.PostSnippet = string(body[:200]) + "..."
			} else {
				meta.PostSnippet = post.Body
			}
			if author, err := h.store.GetUserByID(ctx, post.AuthorID); err == nil {
				meta.PostAuthorName = author.DisplayNameOrUsername()
			}
		}

		if reporter, err := h.store.GetUserByID(ctx, rep.ReporterID); err == nil {
			meta.ReporterName = reporter.DisplayNameOrUsername()
		} else {
			meta.ReporterName = "[unknown]"
		}

		reports = append(reports, meta)
	}

	data := BaseData(r)
	data["Title"] = "Moderation Queue"
	data["Reports"] = reports
	data["Pagination"] = buildPagination(result.Page, result.TotalPages, "/mod/reports", "#reports-list")

	if render.IsHTMXRequest(r) {
		if err := h.renderer.RenderContent(w, "mod_queue.html", data); err != nil {
			slog.Error("render mod queue htmx", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	if err := h.renderer.Render(w, "mod_queue.html", data); err != nil {
		slog.Error("render mod queue", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// ReviewReport handles POST /mod/reports/{id}/review.
// Form field "action": "dismiss" or "delete_and_dismiss".
func (h *ModerationHandlers) ReviewReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireMod(w, r)
	if !ok {
		return
	}

	reportID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	report, err := h.store.GetReportByID(ctx, reportID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get report for review", "id", reportID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")
	var status model.ReportStatus
	switch action {
	case "delete_and_dismiss":
		if err := h.store.DeletePost(ctx, report.PostID, user.ID); err != nil {
			slog.Error("delete post via report review", "post_id", report.PostID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		slog.Info("mod deleted post via report", "report_id", reportID, "post_id", report.PostID,
			"mod_id", user.ID, "mod_username", user.Username)
		status = model.ReportStatusReviewed
	case "dismiss":
		status = model.ReportStatusDismissed
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	if err := h.store.ReviewReport(ctx, reportID, user.ID, status); err != nil {
		slog.Error("review report", "id", reportID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	slog.Info("mod reviewed report", "report_id", reportID, "action", action,
		"mod_id", user.ID, "mod_username", user.Username)
	http.Redirect(w, r, "/mod/reports", http.StatusSeeOther)
}

// LockThread handles POST /mod/threads/{id}/lock: toggles IsLocked on the thread.
func (h *ModerationHandlers) LockThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireMod(w, r)
	if !ok {
		return
	}

	threadID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
		slog.Error("get thread for lock toggle", "id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	newLocked := !thread.IsLocked
	if err := h.store.LockThread(ctx, threadID, newLocked); err != nil {
		slog.Error("lock thread", "id", threadID, "locked", newLocked, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	action := "locked"
	if !newLocked {
		action = "unlocked"
	}
	slog.Info("mod toggled thread lock", "thread_id", threadID, "action", action,
		"mod_id", user.ID, "mod_username", user.Username)
	http.Redirect(w, r, "/t/"+strconv.FormatInt(threadID, 10), http.StatusSeeOther)
}

// PinThread handles POST /mod/threads/{id}/pin: toggles IsPinned on the thread.
func (h *ModerationHandlers) PinThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireMod(w, r)
	if !ok {
		return
	}

	threadID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
		slog.Error("get thread for pin toggle", "id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	newPinned := !thread.IsPinned
	if err := h.store.PinThread(ctx, threadID, newPinned); err != nil {
		slog.Error("pin thread", "id", threadID, "pinned", newPinned, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	action := "pinned"
	if !newPinned {
		action = "unpinned"
	}
	slog.Info("mod toggled thread pin", "thread_id", threadID, "action", action,
		"mod_id", user.ID, "mod_username", user.Username)
	http.Redirect(w, r, "/t/"+strconv.FormatInt(threadID, 10), http.StatusSeeOther)
}

// DeleteThread handles POST /mod/threads/{id}/delete: hard-deletes the thread
// and all of its posts via the schema's ON DELETE CASCADE. Used by admins
// who need to clear a subcategory before deleting it (deletion is refused
// while threads remain). Restricted to moderators and admins.
func (h *ModerationHandlers) DeleteThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireMod(w, r)
	if !ok {
		return
	}

	threadID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Resolve the parent subcategory before deletion so the redirect can
	// land back on the subcategory listing instead of a now-404 thread URL.
	thread, err := h.store.GetThreadByID(ctx, threadID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get thread for delete", "id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sub, err := h.store.GetSubcategoryByID(ctx, thread.SubcategoryID)
	if err != nil {
		slog.Error("get subcategory for thread delete redirect", "id", thread.SubcategoryID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	cat, err := h.store.GetCategoryByID(ctx, sub.CategoryID)
	if err != nil {
		slog.Error("get category for thread delete redirect", "id", sub.CategoryID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.DeleteThread(ctx, threadID); err != nil {
		slog.Error("delete thread", "id", threadID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	slog.Info("mod deleted thread",
		"thread_id", threadID, "title", thread.Title,
		"mod_id", user.ID, "mod_username", user.Username)
	http.Redirect(w, r, "/c/"+cat.Slug+"/"+sub.Slug, http.StatusSeeOther)
}

// BanUserByID handles POST /mod/users/{id}/ban: bans a user by ID from the mod panel.
func (h *ModerationHandlers) BanUserByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := requireMod(w, r)
	if !ok {
		return
	}

	targetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	target, err := h.store.GetUserByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get user for mod ban", "user_id", targetID, "error", err)
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

	if err := h.store.BanUser(ctx, targetID, reason); err != nil {
		slog.Error("mod ban user", "user_id", targetID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.DeleteUserSessions(ctx, targetID); err != nil {
		slog.Warn("delete user sessions on mod ban", "user_id", targetID, "error", err)
	}

	slog.Info("mod banned user", "target_id", targetID, "target_username", target.Username,
		"mod_id", user.ID, "mod_username", user.Username, "reason", reason)
	http.Redirect(w, r, "/mod/reports", http.StatusSeeOther)
}
