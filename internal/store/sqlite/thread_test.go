package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// createTestCategory creates a category and subcategory returning the subcategory ID.
func createTestCategoryAndSub(t *testing.T, s interface {
	store.CategoryStore
}, ctx context.Context, catSlug, subSlug string) (catID, subID int64) {
	t.Helper()
	cat := &model.Category{Name: catSlug, Slug: catSlug, IsPublic: true}
	if err := s.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	sub := &model.Subcategory{CategoryID: cat.ID, Name: subSlug, Slug: subSlug}
	if err := s.CreateSubcategory(ctx, sub); err != nil {
		t.Fatalf("CreateSubcategory: %v", err)
	}
	return cat.ID, sub.ID
}

func newThread(subID, authorID int64, title string) *model.Thread {
	return &model.Thread{
		SubcategoryID: subID,
		AuthorID:      authorID,
		Title:         title,
	}
}

func TestThread_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("thread-user", "threaduser@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "tcat", "tsub")

	th := newThread(subID, u.ID, "My First Thread")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if th.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := s.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID: %v", err)
	}
	if got.Title != th.Title {
		t.Errorf("Title: got %q, want %q", got.Title, th.Title)
	}
	if got.SubcategoryID != subID {
		t.Errorf("SubcategoryID: got %d, want %d", got.SubcategoryID, subID)
	}
	if got.AuthorID != u.ID {
		t.Errorf("AuthorID: got %d, want %d", got.AuthorID, u.ID)
	}
}

func TestThread_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetThreadByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestThread_Update(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("upd-user", "upd@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "upd-cat", "upd-sub")

	th := newThread(subID, u.ID, "Original Title")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	th.Title = "Updated Title"
	if err := s.UpdateThread(ctx, th); err != nil {
		t.Fatalf("UpdateThread: %v", err)
	}

	got, err := s.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID: %v", err)
	}
	if got.Title != "Updated Title" {
		t.Errorf("Title: got %q, want %q", got.Title, "Updated Title")
	}
}

func TestThread_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("del-user", "del@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "del-cat", "del-sub")

	th := newThread(subID, u.ID, "To Be Deleted")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	if err := s.DeleteThread(ctx, th.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	_, err := s.GetThreadByID(ctx, th.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestThread_IncrementViewCount(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("view-user", "view@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "view-cat", "view-sub")

	th := newThread(subID, u.ID, "View Count Test")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := s.IncrementViewCount(ctx, th.ID); err != nil {
			t.Fatalf("IncrementViewCount: %v", err)
		}
	}

	got, err := s.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID: %v", err)
	}
	if got.ViewCount != 5 {
		t.Errorf("ViewCount: got %d, want 5", got.ViewCount)
	}
}

func TestThread_UpdateLastPost(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	author := newUser("lp-author", "lp-author@example.com")
	if err := s.CreateUser(ctx, author); err != nil {
		t.Fatalf("CreateUser author: %v", err)
	}
	replier := newUser("lp-replier", "lp-replier@example.com")
	if err := s.CreateUser(ctx, replier); err != nil {
		t.Fatalf("CreateUser replier: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "lp-cat", "lp-sub")

	th := newThread(subID, author.ID, "Last Post Test")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	replyTime := time.Now().UTC().Add(time.Minute)
	if err := s.UpdateLastPost(ctx, th.ID, replier.ID, replyTime); err != nil {
		t.Fatalf("UpdateLastPost: %v", err)
	}

	got, err := s.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID: %v", err)
	}
	if got.ReplyCount != 1 {
		t.Errorf("ReplyCount: got %d, want 1", got.ReplyCount)
	}
	if got.LastPostBy == nil || *got.LastPostBy != replier.ID {
		t.Errorf("LastPostBy: got %v, want %d", got.LastPostBy, replier.ID)
	}
}

func TestThread_PinAndLock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("pl-user", "pl@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "pl-cat", "pl-sub")

	th := newThread(subID, u.ID, "Pin Lock Test")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	if err := s.PinThread(ctx, th.ID, true); err != nil {
		t.Fatalf("PinThread: %v", err)
	}
	got, err := s.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID: %v", err)
	}
	if !got.IsPinned {
		t.Error("expected IsPinned=true after pin")
	}

	if err := s.PinThread(ctx, th.ID, false); err != nil {
		t.Fatalf("PinThread false: %v", err)
	}
	got, _ = s.GetThreadByID(ctx, th.ID)
	if got.IsPinned {
		t.Error("expected IsPinned=false after unpin")
	}

	if err := s.LockThread(ctx, th.ID, true); err != nil {
		t.Fatalf("LockThread: %v", err)
	}
	got, _ = s.GetThreadByID(ctx, th.ID)
	if !got.IsLocked {
		t.Error("expected IsLocked=true after lock")
	}

	if err := s.LockThread(ctx, th.ID, false); err != nil {
		t.Fatalf("LockThread false: %v", err)
	}
	got, _ = s.GetThreadByID(ctx, th.ID)
	if got.IsLocked {
		t.Error("expected IsLocked=false after unlock")
	}
}

func TestThread_ListPagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("pag-user", "pag@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "pag-cat", "pag-sub")

	// Create 30 threads.
	for i := 0; i < 30; i++ {
		th := newThread(subID, u.ID, fmt.Sprintf("Thread %02d", i))
		if err := s.CreateThread(ctx, th); err != nil {
			t.Fatalf("CreateThread %d: %v", i, err)
		}
	}

	opts := store.ThreadListOptions{
		SubcategoryID: subID,
		PinnedFirst:   true,
		Page:          store.PageRequest{Page: 1, PerPage: 25},
	}
	page1, err := s.ListThreads(ctx, opts)
	if err != nil {
		t.Fatalf("ListThreads page 1: %v", err)
	}
	if len(page1.Items) != 25 {
		t.Errorf("page 1: expected 25 items, got %d", len(page1.Items))
	}
	if page1.Total != 30 {
		t.Errorf("Total: got %d, want 30", page1.Total)
	}
	if page1.TotalPages != 2 {
		t.Errorf("TotalPages: got %d, want 2", page1.TotalPages)
	}

	opts.Page.Page = 2
	page2, err := s.ListThreads(ctx, opts)
	if err != nil {
		t.Fatalf("ListThreads page 2: %v", err)
	}
	if len(page2.Items) != 5 {
		t.Errorf("page 2: expected 5 items, got %d", len(page2.Items))
	}
}

func TestThread_ListPinnedFirst(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("pin-ord-user", "pinord@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "pin-ord-cat", "pin-ord-sub")

	normal1 := newThread(subID, u.ID, "Normal A")
	if err := s.CreateThread(ctx, normal1); err != nil {
		t.Fatalf("CreateThread normal1: %v", err)
	}
	pinned := newThread(subID, u.ID, "Pinned")
	if err := s.CreateThread(ctx, pinned); err != nil {
		t.Fatalf("CreateThread pinned: %v", err)
	}
	normal2 := newThread(subID, u.ID, "Normal B")
	if err := s.CreateThread(ctx, normal2); err != nil {
		t.Fatalf("CreateThread normal2: %v", err)
	}

	if err := s.PinThread(ctx, pinned.ID, true); err != nil {
		t.Fatalf("PinThread: %v", err)
	}

	opts := store.ThreadListOptions{
		SubcategoryID: subID,
		PinnedFirst:   true,
		Page:          store.PageRequest{Page: 1, PerPage: 25},
	}
	result, err := s.ListThreads(ctx, opts)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("expected 3 threads, got %d", len(result.Items))
	}
	if result.Items[0].Thread.ID != pinned.ID {
		t.Errorf("first thread should be pinned (id=%d), got id=%d",
			pinned.ID, result.Items[0].Thread.ID)
	}
}

func TestThread_ListWithAuthorInfo(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("meta-user", "meta@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "meta-cat", "meta-sub")

	th := newThread(subID, u.ID, "Meta Thread")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	opts := store.ThreadListOptions{
		SubcategoryID: subID,
		PinnedFirst:   true,
		Page:          store.PageRequest{Page: 1, PerPage: 25},
	}
	result, err := s.ListThreads(ctx, opts)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(result.Items))
	}
	meta := result.Items[0]
	if meta.AuthorUsername != u.Username {
		t.Errorf("AuthorUsername: got %q, want %q", meta.AuthorUsername, u.Username)
	}
	// No last post yet; LastPostUsername should be nil.
	if meta.LastPostUsername != nil {
		t.Errorf("LastPostUsername: expected nil, got %q", *meta.LastPostUsername)
	}
}
