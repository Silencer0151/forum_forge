package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

// AdminHandlers serves the admin settings panel and category management pages.
type AdminHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewAdminHandler constructs an AdminHandlers.
func NewAdminHandler(st store.Store, r *render.Renderer) *AdminHandlers {
	return &AdminHandlers{store: st, renderer: r}
}

// requireAdmin checks that the request carries an authenticated admin user.
// Writes 403 and returns false if not.
func requireAdmin(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	user := middleware.UserFromContext(r.Context())
	if user == nil || !user.Role.CanAdmin() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	return user, true
}

// CategoryWithSubs groups a category with its subcategories for the admin UI.
type CategoryWithSubs struct {
	Category      *model.Category
	Subcategories []*model.Subcategory
	// HasThreads is true when at least one subcategory contains threads;
	// the UI disables the delete button in that case.
	HasThreads bool
}

// ── Settings ──────────────────────────────────────────────────────────────────

// AdminSettingsPage handles GET /admin/settings.
func (h *AdminHandlers) AdminSettingsPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	ctx := r.Context()

	settings, err := h.store.GetSettings(ctx)
	if err != nil {
		slog.Error("admin: get settings", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := BaseData(r)
	data["Title"] = "Admin — Settings"
	data["Settings"] = settings
	data["MaxAttachmentSizeMB"] = fmt.Sprintf("%.2f", float64(settings.MaxAttachmentSizeBytes)/(1024*1024))
	data["AllowedTypesText"] = strings.Join(settings.AllowedAttachmentTypes, "\n")
	data["KeywordBlocklistText"] = strings.Join(settings.KeywordBlocklist, "\n")
	data["Notice"] = r.URL.Query().Get("notice")
	data["Error"] = r.URL.Query().Get("error")

	if err := h.renderer.Render(w, "admin_settings.html", data); err != nil {
		slog.Error("admin: render settings", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// UpdateAdminSettings handles POST /admin/settings.
func (h *AdminHandlers) UpdateAdminSettings(w http.ResponseWriter, r *http.Request) {
	admin, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	current, err := h.store.GetSettings(ctx)
	if err != nil {
		slog.Error("admin: get settings for update", "error", err)
		http.Redirect(w, r, "/admin/settings?error="+url.QueryEscape("Could not load settings."), http.StatusSeeOther)
		return
	}

	var validationErr string

	if v := r.FormValue("edit_window_minutes"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			validationErr = "Edit window must be a non-negative integer."
		} else {
			current.EditWindowMinutes = n
		}
	}
	if validationErr == "" {
		if v := r.FormValue("posts_per_page"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 1 {
				validationErr = "Posts per page must be a positive integer."
			} else {
				current.PostsPerPage = n
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("threads_per_page"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 1 {
				validationErr = "Threads per page must be a positive integer."
			} else {
				current.ThreadsPerPage = n
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("max_attachment_size_mb"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f <= 0 {
				validationErr = "Max attachment size must be a positive number."
			} else {
				current.MaxAttachmentSizeBytes = int64(f * 1024 * 1024)
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("allowed_attachment_types"); v != "" {
			var types []string
			for _, line := range strings.Split(v, "\n") {
				if t := strings.TrimSpace(line); t != "" {
					types = append(types, t)
				}
			}
			if len(types) > 0 {
				current.AllowedAttachmentTypes = types
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("rate_limit_posts_per_minute"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				validationErr = "Rate limit (posts/min) must be a non-negative integer."
			} else {
				current.RateLimitPostsPerMinute = n
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("rate_limit_threads_per_hour"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				validationErr = "Rate limit (threads/hour) must be a non-negative integer."
			} else {
				current.RateLimitThreadsPerHour = n
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("require_captcha_until_post_count"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				validationErr = "CAPTCHA threshold must be a non-negative integer."
			} else {
				current.RequireCaptchaUntilPostCount = n
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("new_user_link_post_count"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				validationErr = "New-user post-count threshold must be a non-negative integer."
			} else {
				current.NewUserLinkPostCount = n
			}
		}
	}
	if validationErr == "" {
		if v := r.FormValue("max_links_for_new_users"); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < -1 {
				validationErr = "Max links for new users must be -1 (disabled), 0 (forbidden), or a positive integer."
			} else {
				current.MaxLinksForNewUsers = n
			}
		}
	}
	if validationErr == "" {
		// Keyword blocklist accepts an empty submission as "clear all"; that
		// is why this block always reassigns the slice rather than gating on
		// v != "" like the numeric fields above.
		raw := r.FormValue("keyword_blocklist")
		var keywords []string
		for _, line := range strings.Split(raw, "\n") {
			if kw := strings.TrimSpace(line); kw != "" {
				keywords = append(keywords, kw)
			}
		}
		current.KeywordBlocklist = keywords
	}

	if validationErr != "" {
		http.Redirect(w, r, "/admin/settings?error="+url.QueryEscape(validationErr), http.StatusSeeOther)
		return
	}

	if err := h.store.SaveSettings(ctx, current); err != nil {
		slog.Error("admin: save settings", "error", err)
		http.Redirect(w, r, "/admin/settings?error="+url.QueryEscape("Could not save settings."), http.StatusSeeOther)
		return
	}

	slog.Info("admin updated settings", "admin_id", admin.ID, "admin_username", admin.Username)
	http.Redirect(w, r, "/admin/settings?notice="+url.QueryEscape("Settings saved successfully."), http.StatusSeeOther)
}

// ── Categories ────────────────────────────────────────────────────────────────

// AdminCategoriesPage handles GET /admin/categories.
func (h *AdminHandlers) AdminCategoriesPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}

	cwsList, err := h.loadCategoriesWithSubs(r.Context())
	if err != nil {
		slog.Error("admin: load categories", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := BaseData(r)
	data["Title"] = "Admin — Categories"
	data["CategoriesWithSubs"] = cwsList
	data["Notice"] = r.URL.Query().Get("notice")
	data["Error"] = r.URL.Query().Get("error")

	if err := h.renderer.Render(w, "admin_categories.html", data); err != nil {
		slog.Error("admin: render categories", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// UpdateAdminCategories handles POST /admin/categories. The hidden field "action"
// routes to the correct sub-handler.
func (h *AdminHandlers) UpdateAdminCategories(w http.ResponseWriter, r *http.Request) {
	admin, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	redirect := func(notice, errMsg string) {
		q := url.Values{}
		if notice != "" {
			q.Set("notice", notice)
		}
		if errMsg != "" {
			q.Set("error", errMsg)
		}
		target := "/admin/categories"
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}

	action := r.FormValue("action")
	switch action {

	case "create_category":
		h.handleCreateCategory(ctx, w, r, admin, redirect)

	case "edit_category":
		h.handleEditCategory(ctx, w, r, admin, redirect)

	case "delete_category":
		h.handleDeleteCategory(ctx, w, r, admin, redirect)

	case "move_category_up":
		h.handleMoveCategoryUp(ctx, w, r, admin, redirect)

	case "move_category_down":
		h.handleMoveCategoryDown(ctx, w, r, admin, redirect)

	case "create_subcategory":
		h.handleCreateSubcategory(ctx, w, r, admin, redirect)

	case "edit_subcategory":
		h.handleEditSubcategory(ctx, w, r, admin, redirect)

	case "delete_subcategory":
		h.handleDeleteSubcategory(ctx, w, r, admin, redirect)

	case "move_subcategory_up":
		h.handleMoveSubcategoryUp(ctx, w, r, admin, redirect)

	case "move_subcategory_down":
		h.handleMoveSubcategoryDown(ctx, w, r, admin, redirect)

	default:
		redirect("", "Unknown action.")
	}
}

// ── Category action handlers ──────────────────────────────────────────────────

func (h *AdminHandlers) handleCreateCategory(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	description := strings.TrimSpace(r.FormValue("description"))
	isPublic := r.FormValue("is_public") == "1"

	if name == "" {
		redirect("", "Category name is required.")
		return
	}
	if slug == "" {
		slug = toSlug(name)
	}

	cats, err := h.store.ListCategories(ctx)
	if err != nil {
		redirect("", "Could not load categories.")
		return
	}

	c := &model.Category{
		Name:         name,
		Slug:         slug,
		Description:  description,
		IsPublic:     isPublic,
		DisplayOrder: len(cats) + 1,
	}
	if err := h.store.CreateCategory(ctx, c); err != nil {
		slog.Error("admin: create category", "error", err)
		redirect("", "Could not create category: "+err.Error())
		return
	}

	slog.Info("admin created category", "category_id", c.ID, "name", name, "admin_id", admin.ID)
	redirect("Category created.", "")
}

func (h *AdminHandlers) handleEditCategory(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	c, err := h.store.GetCategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			redirect("", "Category not found.")
		} else {
			redirect("", "Could not load category.")
		}
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	if name == "" {
		redirect("", "Category name is required.")
		return
	}
	if slug == "" {
		slug = toSlug(name)
	}

	c.Name = name
	c.Slug = slug
	c.Description = strings.TrimSpace(r.FormValue("description"))
	c.IsPublic = r.FormValue("is_public") == "1"

	if err := h.store.UpdateCategory(ctx, c); err != nil {
		slog.Error("admin: update category", "id", id, "error", err)
		redirect("", "Could not update category.")
		return
	}

	slog.Info("admin updated category", "category_id", id, "admin_id", admin.ID)
	redirect("Category updated.", "")
}

func (h *AdminHandlers) handleDeleteCategory(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	// Refuse deletion when any subcategory has threads.
	subs, err := h.store.ListSubcategoriesByCategoryID(ctx, id)
	if err != nil {
		redirect("", "Could not check subcategories.")
		return
	}
	if len(subs) > 0 {
		ids := make([]int64, len(subs))
		for i, s := range subs {
			ids[i] = s.ID
		}
		stats, err := h.store.GetSubcategoryStatsBatch(ctx, ids)
		if err != nil {
			redirect("", "Could not check subcategory threads.")
			return
		}
		for _, st := range stats {
			if st.ThreadCount > 0 {
				redirect("", "Cannot delete a category that contains threads.")
				return
			}
		}
	}

	if err := h.store.DeleteCategory(ctx, id); err != nil {
		slog.Error("admin: delete category", "id", id, "error", err)
		redirect("", "Could not delete category.")
		return
	}

	slog.Info("admin deleted category", "category_id", id, "admin_id", admin.ID)
	redirect("Category deleted.", "")
}

func (h *AdminHandlers) handleMoveCategoryUp(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	cats, err := h.store.ListCategories(ctx)
	if err != nil {
		redirect("", "Could not load categories.")
		return
	}

	idx := indexOfCategory(cats, id)
	if idx <= 0 {
		redirect("", "")
		return
	}
	cats[idx], cats[idx-1] = cats[idx-1], cats[idx]

	if err := h.store.ReorderCategories(ctx, categoryIDs(cats)); err != nil {
		slog.Error("admin: reorder categories (up)", "error", err)
		redirect("", "Could not reorder categories.")
		return
	}
	redirect("", "")
}

func (h *AdminHandlers) handleMoveCategoryDown(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	cats, err := h.store.ListCategories(ctx)
	if err != nil {
		redirect("", "Could not load categories.")
		return
	}

	idx := indexOfCategory(cats, id)
	if idx < 0 || idx >= len(cats)-1 {
		redirect("", "")
		return
	}
	cats[idx], cats[idx+1] = cats[idx+1], cats[idx]

	if err := h.store.ReorderCategories(ctx, categoryIDs(cats)); err != nil {
		slog.Error("admin: reorder categories (down)", "error", err)
		redirect("", "Could not reorder categories.")
		return
	}
	redirect("", "")
}

// ── Subcategory action handlers ───────────────────────────────────────────────

func (h *AdminHandlers) handleCreateSubcategory(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	catID, err := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	description := strings.TrimSpace(r.FormValue("description"))

	if name == "" {
		redirect("", "Subcategory name is required.")
		return
	}
	if slug == "" {
		slug = toSlug(name)
	}

	existing, err := h.store.ListSubcategoriesByCategoryID(ctx, catID)
	if err != nil {
		redirect("", "Could not load subcategories.")
		return
	}

	s := &model.Subcategory{
		CategoryID:   catID,
		Name:         name,
		Slug:         slug,
		Description:  description,
		DisplayOrder: len(existing) + 1,
	}
	if err := h.store.CreateSubcategory(ctx, s); err != nil {
		slog.Error("admin: create subcategory", "error", err)
		redirect("", "Could not create subcategory: "+err.Error())
		return
	}

	slog.Info("admin created subcategory", "subcategory_id", s.ID, "name", name, "category_id", catID, "admin_id", admin.ID)
	redirect("Subcategory created.", "")
}

func (h *AdminHandlers) handleEditSubcategory(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid subcategory ID.")
		return
	}

	s, err := h.store.GetSubcategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			redirect("", "Subcategory not found.")
		} else {
			redirect("", "Could not load subcategory.")
		}
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	if name == "" {
		redirect("", "Subcategory name is required.")
		return
	}
	if slug == "" {
		slug = toSlug(name)
	}

	s.Name = name
	s.Slug = slug
	s.Description = strings.TrimSpace(r.FormValue("description"))

	if err := h.store.UpdateSubcategory(ctx, s); err != nil {
		slog.Error("admin: update subcategory", "id", id, "error", err)
		redirect("", "Could not update subcategory.")
		return
	}

	slog.Info("admin updated subcategory", "subcategory_id", id, "admin_id", admin.ID)
	redirect("Subcategory updated.", "")
}

func (h *AdminHandlers) handleDeleteSubcategory(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid subcategory ID.")
		return
	}

	stats, err := h.store.GetSubcategoryStatsBatch(ctx, []int64{id})
	if err != nil {
		redirect("", "Could not check subcategory threads.")
		return
	}
	if st, ok := stats[id]; ok && st.ThreadCount > 0 {
		redirect("", "Cannot delete a subcategory that contains threads.")
		return
	}

	if err := h.store.DeleteSubcategory(ctx, id); err != nil {
		slog.Error("admin: delete subcategory", "id", id, "error", err)
		redirect("", "Could not delete subcategory.")
		return
	}

	slog.Info("admin deleted subcategory", "subcategory_id", id, "admin_id", admin.ID)
	redirect("Subcategory deleted.", "")
}

func (h *AdminHandlers) handleMoveSubcategoryUp(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid subcategory ID.")
		return
	}
	catID, err := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	subs, err := h.store.ListSubcategoriesByCategoryID(ctx, catID)
	if err != nil {
		redirect("", "Could not load subcategories.")
		return
	}

	idx := indexOfSubcategory(subs, id)
	if idx <= 0 {
		redirect("", "")
		return
	}
	subs[idx], subs[idx-1] = subs[idx-1], subs[idx]

	if err := h.store.ReorderSubcategories(ctx, subcategoryIDs(subs)); err != nil {
		slog.Error("admin: reorder subcategories (up)", "error", err)
		redirect("", "Could not reorder subcategories.")
		return
	}
	redirect("", "")
}

func (h *AdminHandlers) handleMoveSubcategoryDown(ctx context.Context, w http.ResponseWriter, r *http.Request, admin *model.User, redirect func(string, string)) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		redirect("", "Invalid subcategory ID.")
		return
	}
	catID, err := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	if err != nil {
		redirect("", "Invalid category ID.")
		return
	}

	subs, err := h.store.ListSubcategoriesByCategoryID(ctx, catID)
	if err != nil {
		redirect("", "Could not load subcategories.")
		return
	}

	idx := indexOfSubcategory(subs, id)
	if idx < 0 || idx >= len(subs)-1 {
		redirect("", "")
		return
	}
	subs[idx], subs[idx+1] = subs[idx+1], subs[idx]

	if err := h.store.ReorderSubcategories(ctx, subcategoryIDs(subs)); err != nil {
		slog.Error("admin: reorder subcategories (down)", "error", err)
		redirect("", "Could not reorder subcategories.")
		return
	}
	redirect("", "")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// loadCategoriesWithSubs fetches all categories, their subcategories, and
// checks whether any subcategory has threads (to gate the delete button).
func (h *AdminHandlers) loadCategoriesWithSubs(ctx context.Context) ([]CategoryWithSubs, error) {
	cats, err := h.store.ListCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}

	result := make([]CategoryWithSubs, 0, len(cats))
	for _, c := range cats {
		subs, err := h.store.ListSubcategoriesByCategoryID(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("list subcategories for %d: %w", c.ID, err)
		}

		hasThreads := false
		if len(subs) > 0 {
			ids := make([]int64, len(subs))
			for i, s := range subs {
				ids[i] = s.ID
			}
			statsMap, err := h.store.GetSubcategoryStatsBatch(ctx, ids)
			if err != nil {
				return nil, fmt.Errorf("stats for category %d: %w", c.ID, err)
			}
			for _, st := range statsMap {
				if st.ThreadCount > 0 {
					hasThreads = true
					break
				}
			}
		}

		result = append(result, CategoryWithSubs{
			Category:      c,
			Subcategories: subs,
			HasThreads:    hasThreads,
		})
	}
	return result, nil
}

// toSlug converts a display name to a URL-safe slug.
func toSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '-':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func indexOfCategory(cats []*model.Category, id int64) int {
	for i, c := range cats {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func categoryIDs(cats []*model.Category) []int64 {
	ids := make([]int64, len(cats))
	for i, c := range cats {
		ids[i] = c.ID
	}
	return ids
}

func indexOfSubcategory(subs []*model.Subcategory, id int64) int {
	for i, s := range subs {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func subcategoryIDs(subs []*model.Subcategory) []int64 {
	ids := make([]int64, len(subs))
	for i, s := range subs {
		ids[i] = s.ID
	}
	return ids
}
