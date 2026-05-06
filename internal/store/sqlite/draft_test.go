package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

func TestDraft_ByThread(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "draft-thread")

	threadID := th.ID
	d := &model.Draft{
		UserID:   u.ID,
		ThreadID: &threadID,
		Body:     "draft body",
	}
	if err := s.UpsertDraft(ctx, d); err != nil {
		t.Fatalf("UpsertDraft: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("expected non-zero ID after UpsertDraft")
	}

	got, err := s.GetDraftByThread(ctx, u.ID, th.ID)
	if err != nil {
		t.Fatalf("GetDraftByThread: %v", err)
	}
	if got.Body != d.Body {
		t.Errorf("Body: got %q, want %q", got.Body, d.Body)
	}
	if got.ThreadID == nil || *got.ThreadID != th.ID {
		t.Errorf("ThreadID: got %v, want %d", got.ThreadID, th.ID)
	}
}

func TestDraft_BySubcategory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("draftsub-user", "draftsubuser@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, "draftsub-cat", "draftsub-sub")

	subcategoryID := subID
	d := &model.Draft{
		UserID:        u.ID,
		SubcategoryID: &subcategoryID,
		Title:         "Draft Thread Title",
		Body:          "draft for new thread",
	}
	if err := s.UpsertDraft(ctx, d); err != nil {
		t.Fatalf("UpsertDraft: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("expected non-zero ID after UpsertDraft")
	}

	got, err := s.GetDraftBySubcategory(ctx, u.ID, subID)
	if err != nil {
		t.Fatalf("GetDraftBySubcategory: %v", err)
	}
	if got.Title != d.Title {
		t.Errorf("Title: got %q, want %q", got.Title, d.Title)
	}
	if got.Body != d.Body {
		t.Errorf("Body: got %q, want %q", got.Body, d.Body)
	}
	if got.SubcategoryID == nil || *got.SubcategoryID != subID {
		t.Errorf("SubcategoryID: got %v, want %d", got.SubcategoryID, subID)
	}
}

func TestDraft_Upsert(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "upsert")

	threadID := th.ID
	d := &model.Draft{
		UserID:   u.ID,
		ThreadID: &threadID,
		Body:     "initial body",
	}
	if err := s.UpsertDraft(ctx, d); err != nil {
		t.Fatalf("UpsertDraft initial: %v", err)
	}
	firstID := d.ID

	// Update by upserting again with the same user_id + thread_id.
	d.Body = "updated body"
	if err := s.UpsertDraft(ctx, d); err != nil {
		t.Fatalf("UpsertDraft update: %v", err)
	}

	got, err := s.GetDraftByThread(ctx, u.ID, th.ID)
	if err != nil {
		t.Fatalf("GetDraftByThread: %v", err)
	}
	if got.Body != "updated body" {
		t.Errorf("Body after upsert: got %q, want %q", got.Body, "updated body")
	}
	if got.ID != firstID {
		t.Errorf("ID changed on upsert: got %d, want %d", got.ID, firstID)
	}
}

func TestDraft_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("draftnotfound", "draftnotfound@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err := s.GetDraftByThread(ctx, u.ID, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetDraftByThread: expected ErrNotFound, got %v", err)
	}

	_, err = s.GetDraftBySubcategory(ctx, u.ID, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetDraftBySubcategory: expected ErrNotFound, got %v", err)
	}
}

func TestDraft_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "draft-del")

	threadID := th.ID
	d := &model.Draft{
		UserID:   u.ID,
		ThreadID: &threadID,
		Body:     "to be deleted",
	}
	if err := s.UpsertDraft(ctx, d); err != nil {
		t.Fatalf("UpsertDraft: %v", err)
	}

	if err := s.DeleteDraft(ctx, d.ID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}

	_, err := s.GetDraftByThread(ctx, u.ID, th.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDraft_CleanupOldDrafts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "draft-cleanup")

	threadID := th.ID
	d := &model.Draft{
		UserID:   u.ID,
		ThreadID: &threadID,
		Body:     "old draft",
	}
	if err := s.UpsertDraft(ctx, d); err != nil {
		t.Fatalf("UpsertDraft: %v", err)
	}

	// Cutoff in the future — should delete the just-inserted draft.
	cutoff := time.Now().Add(24 * time.Hour)
	if err := s.CleanupOldDrafts(ctx, cutoff); err != nil {
		t.Fatalf("CleanupOldDrafts: %v", err)
	}

	_, err := s.GetDraftByThread(ctx, u.ID, th.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected draft to be cleaned up, got %v", err)
	}
}

func TestDraft_TwoUsersIndependent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u1, th := setupThreadForPost(t, s, ctx, "two-draft")
	u2 := newUser("two-draft-user2", "twodraftuser2@example.com")
	if err := s.CreateUser(ctx, u2); err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}

	threadID := th.ID
	d1 := &model.Draft{UserID: u1.ID, ThreadID: &threadID, Body: "user1 draft"}
	if err := s.UpsertDraft(ctx, d1); err != nil {
		t.Fatalf("UpsertDraft u1: %v", err)
	}

	d2 := &model.Draft{UserID: u2.ID, ThreadID: &threadID, Body: "user2 draft"}
	if err := s.UpsertDraft(ctx, d2); err != nil {
		t.Fatalf("UpsertDraft u2: %v", err)
	}

	got1, err := s.GetDraftByThread(ctx, u1.ID, th.ID)
	if err != nil {
		t.Fatalf("GetDraftByThread u1: %v", err)
	}
	if got1.Body != "user1 draft" {
		t.Errorf("u1 body: got %q, want user1 draft", got1.Body)
	}

	got2, err := s.GetDraftByThread(ctx, u2.ID, th.ID)
	if err != nil {
		t.Fatalf("GetDraftByThread u2: %v", err)
	}
	if got2.Body != "user2 draft" {
		t.Errorf("u2 body: got %q, want user2 draft", got2.Body)
	}
}
