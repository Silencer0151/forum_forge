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
	defaultThreadsPerPage = 25
	maxTitleLen           = 200
)

// ThreadHandlers handles thread listing and creation.
type ThreadHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewThreadHandler constructs a ThreadHandlers.
func NewThreadHandler(st store.Store, r *render.Renderer) *ThreadHandlers {
	return &ThreadHandlers{store: st, renderer: r}
}

// PaginationData holds pre-computed pagination state for templates.
type PaginationData struct {
	CurrentPage int
	TotalPages  int
	Pages       []int
	BaseURL     string
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	// HTMXTarget is the CSS selector for hx-target on pagination links.
	// When non-empty, links include hx-get, hx-target, and hx-push-url attributes.
	HTMXTarget string
}

func buildPagination(page, totalPages int, baseURL, htmxTarget string) PaginationData {
	pages := make([]int, totalPages)
	for i := range pages {
		pages[i] = i + 1
	}
	return PaginationData{
		CurrentPage: page,
		TotalPages:  totalPages,
		Pages:       pages,
		BaseURL:     baseURL,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
		HTMXTarget:  htmxTarget,
	}
}

func pageFromQuery(r *http.Request) int {
	p := r.URL.Query().Get("page")
	if p == "" {
		return 1
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// SubcategoryPage renders /c/{category_slug}/{sub_slug}: paginated thread list.
func (h *ThreadHandlers) SubcategoryPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	catSlug := r.PathValue("category_slug")
	subSlug := r.PathValue("sub_slug")

	cat, err := h.store.GetCategoryBySlug(ctx, catSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get category by slug", "slug", catSlug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !cat.IsPublic && user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	sub, err := h.store.GetSubcategoryBySlug(ctx, catSlug, subSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get subcategory by slug", "cat", catSlug, "sub", subSlug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	page := pageFromQuery(r)
	perPage := defaultThreadsPerPage
	if settings, err := h.store.GetSettings(ctx); err == nil && settings.ThreadsPerPage > 0 {
		perPage = settings.ThreadsPerPage
	}

	result, err := h.store.ListThreads(ctx, store.ThreadListOptions{
		SubcategoryID: sub.ID,
		PinnedFirst:   true,
		Page:          store.PageRequest{Page: page, PerPage: perPage},
	})
	if err != nil {
		slog.Error("list threads", "subcategory_id", sub.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	baseURL := fmt.Sprintf("/c/%s/%s", catSlug, subSlug)
	canPost := user != nil && !user.Banned && user.Role != model.RoleGuest

	data := BaseData(r)
	data["Title"] = sub.Name
	data["Category"] = cat
	data["Subcategory"] = sub
	data["Threads"] = result
	data["Pagination"] = buildPagination(result.Page, result.TotalPages, baseURL, "#subcategory-content")
	data["CanPost"] = canPost

	if render.IsHTMXRequest(r) {
		if err := h.renderer.RenderContent(w, "subcategory.html", data); err != nil {
			slog.Error("render subcategory content (htmx)", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}
	if err := h.renderer.Render(w, "subcategory.html", data); err != nil {
		slog.Error("render subcategory", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// NewThreadPage renders the new-thread form at GET /t/new?cat={cat_slug}&sub={sub_slug}.
func (h *ThreadHandlers) NewThreadPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil || user.Banned {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}

	catSlug := r.URL.Query().Get("cat")
	subSlug := r.URL.Query().Get("sub")
	if catSlug == "" || subSlug == "" {
		http.Error(w, "missing subcategory", http.StatusBadRequest)
		return
	}

	cat, err := h.store.GetCategoryBySlug(ctx, catSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get category by slug", "slug", catSlug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sub, err := h.store.GetSubcategoryBySlug(ctx, catSlug, subSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get subcategory by slug", "cat", catSlug, "sub", subSlug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := BaseData(r)
	data["Title"] = "New Thread"
	data["Category"] = cat
	data["Subcategory"] = sub
	data["FormFormat"] = string(model.BodyFormatMarkdown)
	if err := h.renderer.Render(w, "new_thread.html", data); err != nil {
		slog.Error("render new thread", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// CreateThread handles POST /t/new: validates input, creates thread + OP post, redirects.
func (h *ThreadHandlers) CreateThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	if user == nil || user.Banned {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	catSlug := r.FormValue("cat_slug")
	subSlug := r.FormValue("sub_slug")
	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	format := model.BodyFormat(r.FormValue("body_format"))
	if !format.IsValid() {
		format = model.BodyFormatMarkdown
	}

	cat, err := h.store.GetCategoryBySlug(ctx, catSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get category by slug", "slug", catSlug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sub, err := h.store.GetSubcategoryBySlug(ctx, catSlug, subSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get subcategory by slug", "cat", catSlug, "sub", subSlug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var formErr string
	switch {
	case title == "":
		formErr = "Title is required."
	case len(title) > maxTitleLen:
		formErr = fmt.Sprintf("Title must be %d characters or fewer.", maxTitleLen)
	case body == "":
		formErr = "Post body is required."
	}

	if formErr != "" {
		data := BaseData(r)
		data["Title"] = "New Thread"
		data["Category"] = cat
		data["Subcategory"] = sub
		data["Error"] = formErr
		data["FormTitle"] = title
		data["FormBody"] = body
		data["FormFormat"] = string(format)
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := h.renderer.Render(w, "new_thread.html", data); err != nil {
			slog.Error("render new thread (error)", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	thread := &model.Thread{
		SubcategoryID: sub.ID,
		AuthorID:      user.ID,
		Title:         title,
		LastPostAt:    now,
		LastPostBy:    &user.ID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.store.CreateThread(ctx, thread); err != nil {
		slog.Error("create thread", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	post := &model.Post{
		ThreadID:   thread.ID,
		AuthorID:   user.ID,
		Body:       body,
		BodyFormat: format,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := h.store.CreatePost(ctx, post); err != nil {
		slog.Error("create op post", "thread_id", thread.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/t/%d", thread.ID), http.StatusSeeOther)
}
