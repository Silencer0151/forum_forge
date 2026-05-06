package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

func newCategory(name, slug string) *model.Category {
	return &model.Category{
		Name:         name,
		Slug:         slug,
		Description:  "desc " + name,
		DisplayOrder: 0,
		IsPublic:     true,
	}
}

func newSubcategory(categoryID int64, name, slug string) *model.Subcategory {
	return &model.Subcategory{
		CategoryID:   categoryID,
		Name:         name,
		Slug:         slug,
		Description:  "desc " + name,
		DisplayOrder: 0,
	}
}

func TestCategory_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("General", "general")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if cat.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := s.GetCategoryByID(ctx, cat.ID)
	if err != nil {
		t.Fatalf("GetCategoryByID: %v", err)
	}
	if got.Name != cat.Name {
		t.Errorf("Name: got %q, want %q", got.Name, cat.Name)
	}
	if got.Slug != cat.Slug {
		t.Errorf("Slug: got %q, want %q", got.Slug, cat.Slug)
	}
	if !got.IsPublic {
		t.Error("expected IsPublic=true")
	}

	gotSlug, err := s.GetCategoryBySlug(ctx, cat.Slug)
	if err != nil {
		t.Fatalf("GetCategoryBySlug: %v", err)
	}
	if gotSlug.ID != cat.ID {
		t.Errorf("GetCategoryBySlug ID: got %d, want %d", gotSlug.ID, cat.ID)
	}
}

func TestCategory_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetCategoryByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	_, err = s.GetCategoryBySlug(ctx, "no-such-slug")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCategory_Update(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("Tech", "tech")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	cat.Name = "Technology"
	cat.Description = "All things tech"
	cat.IsPublic = false
	if err := s.UpdateCategory(ctx, cat); err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}

	got, err := s.GetCategoryByID(ctx, cat.ID)
	if err != nil {
		t.Fatalf("GetCategoryByID: %v", err)
	}
	if got.Name != "Technology" {
		t.Errorf("Name: got %q, want %q", got.Name, "Technology")
	}
	if got.Description != "All things tech" {
		t.Errorf("Description: got %q, want %q", got.Description, "All things tech")
	}
	if got.IsPublic {
		t.Error("expected IsPublic=false after update")
	}
}

func TestCategory_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("ToDelete", "to-delete")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if err := s.DeleteCategory(ctx, cat.ID); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}

	_, err := s.GetCategoryByID(ctx, cat.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestCategory_List(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	names := []string{"Alpha", "Beta", "Gamma"}
	for i, name := range names {
		cat := &model.Category{
			Name:         name,
			Slug:         "cat-" + name,
			DisplayOrder: i,
			IsPublic:     true,
		}
		if err := s.CreateCategory(ctx, cat); err != nil {
			t.Fatalf("CreateCategory %s: %v", name, err)
		}
	}

	cats, err := s.ListCategories(ctx)
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(cats) != 3 {
		t.Fatalf("expected 3 categories, got %d", len(cats))
	}
	// Ordered by display_order ASC.
	if cats[0].Name != "Alpha" || cats[1].Name != "Beta" || cats[2].Name != "Gamma" {
		t.Errorf("unexpected order: %v", []string{cats[0].Name, cats[1].Name, cats[2].Name})
	}
}

func TestCategory_Reorder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var ids []int64
	for i, name := range []string{"First", "Second", "Third"} {
		cat := &model.Category{Name: name, Slug: "reord-" + name, DisplayOrder: i, IsPublic: true}
		if err := s.CreateCategory(ctx, cat); err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		ids = append(ids, cat.ID)
	}

	// Reverse order.
	reversed := []int64{ids[2], ids[1], ids[0]}
	if err := s.ReorderCategories(ctx, reversed); err != nil {
		t.Fatalf("ReorderCategories: %v", err)
	}

	cats, err := s.ListCategories(ctx)
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if cats[0].ID != ids[2] {
		t.Errorf("expected first category to be id=%d after reorder, got %d", ids[2], cats[0].ID)
	}
	if cats[2].ID != ids[0] {
		t.Errorf("expected last category to be id=%d after reorder, got %d", ids[0], cats[2].ID)
	}
}

// ── Subcategory tests ─────────────────────────────────────────────────────────

func TestSubcategory_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("Parent", "parent")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	sub := newSubcategory(cat.ID, "Sub One", "sub-one")
	if err := s.CreateSubcategory(ctx, sub); err != nil {
		t.Fatalf("CreateSubcategory: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := s.GetSubcategoryByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubcategoryByID: %v", err)
	}
	if got.Name != sub.Name {
		t.Errorf("Name: got %q, want %q", got.Name, sub.Name)
	}
	if got.CategoryID != cat.ID {
		t.Errorf("CategoryID: got %d, want %d", got.CategoryID, cat.ID)
	}

	gotSlug, err := s.GetSubcategoryBySlug(ctx, cat.Slug, sub.Slug)
	if err != nil {
		t.Fatalf("GetSubcategoryBySlug: %v", err)
	}
	if gotSlug.ID != sub.ID {
		t.Errorf("GetSubcategoryBySlug ID: got %d, want %d", gotSlug.ID, sub.ID)
	}
}

func TestSubcategory_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetSubcategoryByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	cat := newCategory("P", "p-slug")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	_, err = s.GetSubcategoryBySlug(ctx, cat.Slug, "no-such-sub")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing sub slug, got %v", err)
	}
}

func TestSubcategory_Update(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("Cat", "cat-slug")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	sub := newSubcategory(cat.ID, "Old Name", "old-name")
	if err := s.CreateSubcategory(ctx, sub); err != nil {
		t.Fatalf("CreateSubcategory: %v", err)
	}

	sub.Name = "New Name"
	sub.Slug = "new-name"
	if err := s.UpdateSubcategory(ctx, sub); err != nil {
		t.Fatalf("UpdateSubcategory: %v", err)
	}

	got, err := s.GetSubcategoryByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubcategoryByID: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("Name: got %q, want %q", got.Name, "New Name")
	}
}

func TestSubcategory_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("DelCat", "del-cat")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	sub := newSubcategory(cat.ID, "ToDelete", "to-delete-sub")
	if err := s.CreateSubcategory(ctx, sub); err != nil {
		t.Fatalf("CreateSubcategory: %v", err)
	}

	if err := s.DeleteSubcategory(ctx, sub.ID); err != nil {
		t.Fatalf("DeleteSubcategory: %v", err)
	}

	_, err := s.GetSubcategoryByID(ctx, sub.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSubcategory_ListByCategoryID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("ListCat", "list-cat")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	for i, name := range []string{"SubA", "SubB", "SubC"} {
		sub := &model.Subcategory{
			CategoryID:   cat.ID,
			Name:         name,
			Slug:         "list-sub-" + name,
			DisplayOrder: i,
		}
		if err := s.CreateSubcategory(ctx, sub); err != nil {
			t.Fatalf("CreateSubcategory: %v", err)
		}
	}

	subs, err := s.ListSubcategoriesByCategoryID(ctx, cat.ID)
	if err != nil {
		t.Fatalf("ListSubcategoriesByCategoryID: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("expected 3 subcategories, got %d", len(subs))
	}
	if subs[0].Name != "SubA" || subs[1].Name != "SubB" || subs[2].Name != "SubC" {
		t.Errorf("unexpected order: %v", []string{subs[0].Name, subs[1].Name, subs[2].Name})
	}
}

func TestSubcategory_Reorder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cat := newCategory("ReordCat", "reord-cat")
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	var ids []int64
	for i, name := range []string{"X", "Y", "Z"} {
		sub := &model.Subcategory{
			CategoryID:   cat.ID,
			Name:         name,
			Slug:         "reord-sub-" + name,
			DisplayOrder: i,
		}
		if err := s.CreateSubcategory(ctx, sub); err != nil {
			t.Fatalf("CreateSubcategory: %v", err)
		}
		ids = append(ids, sub.ID)
	}

	reversed := []int64{ids[2], ids[1], ids[0]}
	if err := s.ReorderSubcategories(ctx, reversed); err != nil {
		t.Fatalf("ReorderSubcategories: %v", err)
	}

	subs, err := s.ListSubcategoriesByCategoryID(ctx, cat.ID)
	if err != nil {
		t.Fatalf("ListSubcategoriesByCategoryID: %v", err)
	}
	if subs[0].ID != ids[2] {
		t.Errorf("expected first sub id=%d after reorder, got %d", ids[2], subs[0].ID)
	}
}
