package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// newPost builds a Post for test use.
func newPost(threadID, authorID int64, body string) *model.Post {
	return &model.Post{
		ThreadID:   threadID,
		AuthorID:   authorID,
		Body:       body,
		BodyFormat: model.BodyFormatMarkdown,
	}
}

// setupThreadForPost creates a user, category, subcategory, and thread.
// Returns the user and thread.
func setupThreadForPost(t *testing.T, s interface {
	store.UserStore
	store.CategoryStore
	store.ThreadStore
}, ctx context.Context, prefix string) (*model.User, *model.Thread) {
	t.Helper()
	u := newUser(prefix+"-author", prefix+"@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, subID := createTestCategoryAndSub(t, s, ctx, prefix+"-cat", prefix+"-sub")
	th := newThread(subID, u.ID, prefix+" Thread")
	if err := s.CreateThread(ctx, th); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	return u, th
}

func TestPost_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "cag")

	p := newPost(th.ID, u.ID, "Hello world")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected non-zero ID after CreatePost")
	}

	got, err := s.GetPostByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPostByID: %v", err)
	}
	if got.Body != p.Body {
		t.Errorf("Body: got %q, want %q", got.Body, p.Body)
	}
	if got.ThreadID != th.ID {
		t.Errorf("ThreadID: got %d, want %d", got.ThreadID, th.ID)
	}
	if got.AuthorID != u.ID {
		t.Errorf("AuthorID: got %d, want %d", got.AuthorID, u.ID)
	}
	if got.IsDeleted {
		t.Error("expected IsDeleted=false on new post")
	}
}

func TestPost_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetPostByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPost_CreateUpdatesThreadCounters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "ctr")

	if err := s.CreatePost(ctx, newPost(th.ID, u.ID, "first")); err != nil {
		t.Fatalf("CreatePost 1: %v", err)
	}
	if err := s.CreatePost(ctx, newPost(th.ID, u.ID, "second")); err != nil {
		t.Fatalf("CreatePost 2: %v", err)
	}

	got, err := s.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID: %v", err)
	}
	if got.ReplyCount != 2 {
		t.Errorf("ReplyCount: got %d, want 2", got.ReplyCount)
	}
	if got.LastPostBy == nil || *got.LastPostBy != u.ID {
		t.Errorf("LastPostBy: got %v, want %d", got.LastPostBy, u.ID)
	}
}

func TestPost_Update(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "upd")

	p := newPost(th.ID, u.ID, "original body")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	editorID := u.ID
	p.Body = "updated body"
	p.EditedBy = &editorID
	if err := s.UpdatePost(ctx, p); err != nil {
		t.Fatalf("UpdatePost: %v", err)
	}

	got, err := s.GetPostByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPostByID: %v", err)
	}
	if got.Body != "updated body" {
		t.Errorf("Body: got %q, want %q", got.Body, "updated body")
	}
	if got.EditCount != 1 {
		t.Errorf("EditCount: got %d, want 1", got.EditCount)
	}
	if got.EditedBy == nil || *got.EditedBy != editorID {
		t.Errorf("EditedBy: got %v, want %d", got.EditedBy, editorID)
	}
	if got.EditedAt == nil {
		t.Error("EditedAt: expected non-nil after update")
	}
}

func TestPost_SoftDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "del")

	p := newPost(th.ID, u.ID, "to be deleted")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	if err := s.DeletePost(ctx, p.ID, u.ID); err != nil {
		t.Fatalf("DeletePost: %v", err)
	}

	got, err := s.GetPostByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPostByID after delete: %v", err)
	}
	if !got.IsDeleted {
		t.Error("expected IsDeleted=true after DeletePost")
	}
	if got.EditedBy == nil || *got.EditedBy != u.ID {
		t.Errorf("EditedBy: got %v, want %d", got.EditedBy, u.ID)
	}
}

func TestPost_ListByThread_Pagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "pag")

	for i := 0; i < 30; i++ {
		p := newPost(th.ID, u.ID, fmt.Sprintf("post body %d", i))
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("CreatePost %d: %v", i, err)
		}
	}

	page1, err := s.ListPostsByThread(ctx, th.ID, store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPostsByThread page1: %v", err)
	}
	if len(page1.Items) != 25 {
		t.Errorf("page1 items: got %d, want 25", len(page1.Items))
	}
	if page1.Total != 30 {
		t.Errorf("Total: got %d, want 30", page1.Total)
	}
	if page1.TotalPages != 2 {
		t.Errorf("TotalPages: got %d, want 2", page1.TotalPages)
	}

	page2, err := s.ListPostsByThread(ctx, th.ID, store.PageRequest{Page: 2, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPostsByThread page2: %v", err)
	}
	if len(page2.Items) != 5 {
		t.Errorf("page2 items: got %d, want 5", len(page2.Items))
	}
}

func TestPost_ListByThread_IncludesAuthor(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "auth")

	if err := s.CreatePost(ctx, newPost(th.ID, u.ID, "some body")); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	result, err := s.ListPostsByThread(ctx, th.ID, store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPostsByThread: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	item := result.Items[0]
	if item.Author == nil {
		t.Fatal("Author is nil")
	}
	if item.Author.Username != u.Username {
		t.Errorf("Author.Username: got %q, want %q", item.Author.Username, u.Username)
	}
}

func TestPost_ListByAuthor(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "byauthor")
	other := newUser("other-byauthor", "other-byauthor@example.com")
	if err := s.CreateUser(ctx, other); err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}

	// Create 3 posts by u, 1 by other, 1 deleted by u.
	for i := 0; i < 3; i++ {
		if err := s.CreatePost(ctx, newPost(th.ID, u.ID, fmt.Sprintf("u post %d", i))); err != nil {
			t.Fatalf("CreatePost u: %v", err)
		}
	}
	otherPost := newPost(th.ID, other.ID, "other post")
	if err := s.CreatePost(ctx, otherPost); err != nil {
		t.Fatalf("CreatePost other: %v", err)
	}
	delPost := newPost(th.ID, u.ID, "deleted post")
	if err := s.CreatePost(ctx, delPost); err != nil {
		t.Fatalf("CreatePost del: %v", err)
	}
	if err := s.DeletePost(ctx, delPost.ID, u.ID); err != nil {
		t.Fatalf("DeletePost: %v", err)
	}

	result, err := s.ListPostsByAuthor(ctx, u.ID, store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPostsByAuthor: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("Total: got %d, want 3 (deleted excluded)", result.Total)
	}
	for _, item := range result.Items {
		if item.Post.IsDeleted {
			t.Error("ListPostsByAuthor should not return deleted posts")
		}
		if item.Post.AuthorID != u.ID {
			t.Errorf("AuthorID: got %d, want %d", item.Post.AuthorID, u.ID)
		}
	}
}

func TestPost_NestedReplyDepth(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "nest")

	// Level 0: top-level post.
	level0 := newPost(th.ID, u.ID, "level 0")
	if err := s.CreatePost(ctx, level0); err != nil {
		t.Fatalf("CreatePost level0: %v", err)
	}

	// Level 1: reply to level 0.
	level1 := newPost(th.ID, u.ID, "level 1")
	level1.ParentID = &level0.ID
	if err := s.CreatePost(ctx, level1); err != nil {
		t.Fatalf("CreatePost level1: %v", err)
	}

	// Level 2: reply to level 1 — should succeed (max depth).
	level2 := newPost(th.ID, u.ID, "level 2")
	level2.ParentID = &level1.ID
	if err := s.CreatePost(ctx, level2); err != nil {
		t.Fatalf("CreatePost level2: %v", err)
	}

	// Level 3: reply to level 2 — should fail.
	level3 := newPost(th.ID, u.ID, "level 3 (should fail)")
	level3.ParentID = &level2.ID
	if err := s.CreatePost(ctx, level3); err == nil {
		t.Error("expected error when creating post at depth 3, got nil")
	}
}
