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

const (
	pmPerPage      = 20
	pmRateLimit    = 10
	pmRateWindow   = time.Hour
	pmSubjectMax   = 200
	pmBodyMax      = 10000
)

// PMHandlers handles private message inbox, read, send, and delete.
type PMHandlers struct {
	store    store.Store
	renderer *render.Renderer
	limiter  *middleware.Limiter
}

// NewPMHandler constructs a PMHandlers with its own per-user rate limiter.
func NewPMHandler(st store.Store, r *render.Renderer) *PMHandlers {
	return &PMHandlers{
		store:    st,
		renderer: r,
		limiter:  middleware.NewLimiter(pmRateLimit, pmRateWindow),
	}
}

// PMInboxRow bundles a PM with its sender for display in the inbox list.
type PMInboxRow struct {
	PM     *model.PrivateMessage
	Sender *model.User
}

// InboxPage serves GET /pm: paginated inbox for the authenticated user.
func (h *PMHandlers) InboxPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	page := pageFromQuery(r)
	result, err := h.store.ListInbox(ctx, user.ID, store.PageRequest{Page: page, PerPage: pmPerPage})
	if err != nil {
		slog.Error("list pm inbox", "user_id", user.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rows := make([]PMInboxRow, 0, len(result.Items))
	for _, pm := range result.Items {
		sender, err := h.store.GetUserByID(ctx, pm.SenderID)
		if err != nil {
			slog.Warn("get pm sender", "sender_id", pm.SenderID, "error", err)
			sender = &model.User{Username: "[deleted]"}
		}
		rows = append(rows, PMInboxRow{PM: pm, Sender: sender})
	}

	data := BaseData(r)
	data["Title"] = "Inbox"
	data["Messages"] = rows
	data["Pagination"] = buildPagination(result.Page, result.TotalPages, "/pm", "")

	if err := h.renderer.Render(w, "pm_inbox.html", data); err != nil {
		slog.Error("render pm inbox", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// ViewMessage serves GET /pm/{pm_id}: display a single PM and mark it read.
func (h *PMHandlers) ViewMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	pmID, err := strconv.ParseInt(r.PathValue("pm_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	pm, err := h.store.GetPMByID(ctx, pmID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get pm by id", "pm_id", pmID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Only sender and recipient may view the message.
	if pm.SenderID != user.ID && pm.RecipientID != user.ID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Sender can't view a message they deleted for themselves.
	if pm.SenderID == user.ID && pm.SenderDeleted {
		http.NotFound(w, r)
		return
	}
	// Recipient can't view a message they deleted for themselves.
	if pm.RecipientID == user.ID && pm.RecipientDeleted {
		http.NotFound(w, r)
		return
	}

	// Mark as read when the recipient opens the message.
	if pm.RecipientID == user.ID && !pm.IsRead {
		if err := h.store.MarkPMRead(ctx, pmID); err != nil {
			slog.Warn("mark pm read", "pm_id", pmID, "error", err)
		}
		pm.IsRead = true
	}

	sender, err := h.store.GetUserByID(ctx, pm.SenderID)
	if err != nil {
		slog.Warn("get pm sender for view", "sender_id", pm.SenderID, "error", err)
		sender = &model.User{Username: "[deleted]"}
	}
	recipient, err := h.store.GetUserByID(ctx, pm.RecipientID)
	if err != nil {
		slog.Warn("get pm recipient for view", "recipient_id", pm.RecipientID, "error", err)
		recipient = &model.User{Username: "[deleted]"}
	}

	replySubject := pm.Subject
	if !strings.HasPrefix(strings.ToLower(replySubject), "re:") {
		replySubject = "Re: " + replySubject
	}

	data := BaseData(r)
	data["Title"] = pm.Subject
	data["PM"] = pm
	data["Sender"] = sender
	data["Recipient"] = recipient
	data["ReplySubject"] = replySubject
	data["ReplyTo"] = sender.Username
	data["IsOwner"] = pm.SenderID == user.ID
	data["IsRecipient"] = pm.RecipientID == user.ID

	if err := h.renderer.Render(w, "pm_thread.html", data); err != nil {
		slog.Error("render pm view", "pm_id", pmID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// NewMessagePage serves GET /pm/new: show the compose form.
func (h *PMHandlers) NewMessagePage(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	data := BaseData(r)
	data["Title"] = "New Message"
	// Pre-fill recipient from ?to= query param (e.g. from profile page "Send PM" button).
	data["ToUsername"] = r.URL.Query().Get("to")
	data["Subject"] = r.URL.Query().Get("subject")
	data["Error"] = ""

	if err := h.renderer.Render(w, "pm_new.html", data); err != nil {
		slog.Error("render pm new page", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// SendMessage handles POST /pm/new: validate and create a PM.
func (h *PMHandlers) SendMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Per-user rate limit: 10 PMs / hour.
	ok, retry := h.limiter.Allow(fmt.Sprintf("pm:u:%d", user.ID), time.Now())
	if !ok {
		secs := int(retry.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		http.Error(w, "You are sending messages too quickly. Please wait before sending another.", http.StatusTooManyRequests)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	toUsername := strings.TrimSpace(r.FormValue("to"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := strings.TrimSpace(r.FormValue("body"))

	renderError := func(msg string) {
		data := BaseData(r)
		data["Title"] = "New Message"
		data["ToUsername"] = toUsername
		data["Subject"] = subject
		data["Body"] = body
		data["Error"] = msg
		if err := h.renderer.Render(w, "pm_new.html", data); err != nil {
			slog.Error("render pm new error page", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}

	if toUsername == "" {
		renderError("Recipient is required.")
		return
	}
	if subject == "" {
		renderError("Subject is required.")
		return
	}
	if len(subject) > pmSubjectMax {
		renderError(fmt.Sprintf("Subject must be at most %d characters.", pmSubjectMax))
		return
	}
	if body == "" {
		renderError("Message body is required.")
		return
	}
	if len(body) > pmBodyMax {
		renderError(fmt.Sprintf("Message body must be at most %d characters.", pmBodyMax))
		return
	}

	recipient, err := h.store.GetUserByUsername(ctx, toUsername)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			renderError(fmt.Sprintf("User %q not found.", toUsername))
			return
		}
		slog.Error("get pm recipient by username", "username", toUsername, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if recipient.ID == user.ID {
		renderError("You cannot send a message to yourself.")
		return
	}
	if recipient.Banned {
		renderError("You cannot send a message to a banned user.")
		return
	}

	pm := &model.PrivateMessage{
		SenderID:    user.ID,
		RecipientID: recipient.ID,
		Subject:     subject,
		Body:        body,
	}
	if err := h.store.CreatePM(ctx, pm); err != nil {
		slog.Error("create pm", "sender_id", user.ID, "recipient_id", recipient.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/pm", http.StatusSeeOther)
}

// DeleteMessage handles POST /pm/{pm_id}/delete: soft-delete for the current user's side.
func (h *PMHandlers) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	pmID, err := strconv.ParseInt(r.PathValue("pm_id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	pm, err := h.store.GetPMByID(ctx, pmID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get pm for delete", "pm_id", pmID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	switch user.ID {
	case pm.SenderID:
		if err := h.store.DeletePMForSender(ctx, pmID); err != nil {
			slog.Error("delete pm for sender", "pm_id", pmID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	case pm.RecipientID:
		if err := h.store.DeletePMForRecipient(ctx, pmID); err != nil {
			slog.Error("delete pm for recipient", "pm_id", pmID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	http.Redirect(w, r, "/pm", http.StatusSeeOther)
}
