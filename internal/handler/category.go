package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

// CategoryHandlers handles the forum index and per-category pages.
type CategoryHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewCategory constructs a CategoryHandlers.
func NewCategory(st store.Store, r *render.Renderer) *CategoryHandlers {
	return &CategoryHandlers{store: st, renderer: r}
}

// SubcategoryRow pairs a subcategory with its aggregate stats for template rendering.
type SubcategoryRow struct {
	Sub   *model.Subcategory
	Stats *store.SubcategoryStats
}

// CategorySection groups a category with its subcategories for the home page.
type CategorySection struct {
	Cat           *model.Category
	Subcategories []SubcategoryRow
}

// Home renders the forum index: all visible categories with their subcategories
// and stats (thread count, post count, last post info).
func (h *CategoryHandlers) Home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)

	cats, err := h.store.ListCategories(ctx)
	if err != nil {
		slog.Error("list categories", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	type catWithSubs struct {
		cat  *model.Category
		subs []*model.Subcategory
	}

	var catList []catWithSubs
	var allSubIDs []int64

	for _, cat := range cats {
		if !cat.IsPublic && user == nil {
			continue
		}
		subs, err := h.store.ListSubcategoriesByCategoryID(ctx, cat.ID)
		if err != nil {
			slog.Error("list subcategories", "category_id", cat.ID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		catList = append(catList, catWithSubs{cat, subs})
		for _, sub := range subs {
			allSubIDs = append(allSubIDs, sub.ID)
		}
	}

	statsMap, err := h.store.GetSubcategoryStatsBatch(ctx, allSubIDs)
	if err != nil {
		slog.Error("subcategory stats batch", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sections := make([]CategorySection, 0, len(catList))
	for _, cd := range catList {
		rows := make([]SubcategoryRow, len(cd.subs))
		for i, sub := range cd.subs {
			rows[i] = SubcategoryRow{Sub: sub, Stats: statsMap[sub.ID]}
		}
		sections = append(sections, CategorySection{Cat: cd.cat, Subcategories: rows})
	}

	data := BaseData(r)
	data["Title"] = "Forum Index"
	data["Sections"] = sections
	if err := h.renderer.Render(w, "home.html", data); err != nil {
		slog.Error("render home", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// Category renders /c/{category_slug}: the category header and its subcategories with stats.
func (h *CategoryHandlers) Category(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.UserFromContext(ctx)
	slug := r.PathValue("category_slug")

	cat, err := h.store.GetCategoryBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get category by slug", "slug", slug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !cat.IsPublic && user == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	subs, err := h.store.ListSubcategoriesByCategoryID(ctx, cat.ID)
	if err != nil {
		slog.Error("list subcategories", "category_id", cat.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	subIDs := make([]int64, len(subs))
	for i, sub := range subs {
		subIDs[i] = sub.ID
	}

	statsMap, err := h.store.GetSubcategoryStatsBatch(ctx, subIDs)
	if err != nil {
		slog.Error("subcategory stats batch", "category_id", cat.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rows := make([]SubcategoryRow, len(subs))
	for i, sub := range subs {
		rows[i] = SubcategoryRow{Sub: sub, Stats: statsMap[sub.ID]}
	}

	data := BaseData(r)
	data["Title"] = cat.Name
	data["Category"] = cat
	data["Subcategories"] = rows
	if err := h.renderer.Render(w, "category.html", data); err != nil {
		slog.Error("render category", "slug", slug, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
