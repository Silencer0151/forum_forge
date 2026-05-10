package handler

import (
	"encoding/json"
	"errors"
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

// DraftHandlers serves the draft auto-save API.
type DraftHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewDraftHandler constructs a DraftHandlers.
func NewDraftHandler(st store.Store, r *render.Renderer) *DraftHandlers {
	return &DraftHandlers{store: st, renderer: r}
}

// GetDraft handles GET /drafts?thread_id=X or ?subcategory_id=X.
// Returns 200 JSON draft or 404 if none exists.
func (h *DraftHandlers) GetDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var draft *model.Draft
	var err error

	if threadIDStr := r.URL.Query().Get("thread_id"); threadIDStr != "" {
		threadID, parseErr := strconv.ParseInt(threadIDStr, 10, 64)
		if parseErr != nil {
			http.Error(w, "invalid thread_id", http.StatusBadRequest)
			return
		}
		draft, err = h.store.GetDraftByThread(ctx, user.ID, threadID)
	} else if subIDStr := r.URL.Query().Get("subcategory_id"); subIDStr != "" {
		subID, parseErr := strconv.ParseInt(subIDStr, 10, 64)
		if parseErr != nil {
			http.Error(w, "invalid subcategory_id", http.StatusBadRequest)
			return
		}
		draft, err = h.store.GetDraftBySubcategory(ctx, user.ID, subID)
	} else {
		http.Error(w, "thread_id or subcategory_id required", http.StatusBadRequest)
		return
	}

	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		slog.Error("get draft", "user_id", user.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(draft); err != nil {
		slog.Error("encode draft", "error", err)
	}
}

// UpsertDraft handles POST /drafts: silently saves or updates a draft.
// Returns 204 No Content — intended for hx-swap="none" auto-save.
func (h *DraftHandlers) UpsertDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	draft := &model.Draft{
		UserID:    user.ID,
		Title:     strings.TrimSpace(r.FormValue("title")),
		Body:      body,
		UpdatedAt: time.Now(),
	}

	if threadIDStr := r.FormValue("thread_id"); threadIDStr != "" {
		if threadID, err := strconv.ParseInt(threadIDStr, 10, 64); err == nil && threadID > 0 {
			draft.ThreadID = &threadID
		}
	} else if subIDStr := r.FormValue("subcategory_id"); subIDStr != "" {
		if subID, err := strconv.ParseInt(subIDStr, 10, 64); err == nil && subID > 0 {
			draft.SubcategoryID = &subID
		}
	}

	if draft.ThreadID == nil && draft.SubcategoryID == nil {
		http.Error(w, "thread_id or subcategory_id required", http.StatusBadRequest)
		return
	}

	if err := h.store.UpsertDraft(ctx, draft); err != nil {
		slog.Error("upsert draft", "user_id", user.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteDraft handles DELETE /drafts/{id}.
func (h *DraftHandlers) DeleteDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	draftID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := h.store.DeleteDraft(ctx, draftID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("delete draft", "id", draftID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
