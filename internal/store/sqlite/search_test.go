package sqlite_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nitro/forum_forge/internal/store"
)

func TestSearch_Basic(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "srch")

	// Create a post with a distinctive keyword.
	p := newPost(th.ID, u.ID, "The quick brown fox jumps over the lazy dog")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	// Create a post that should not match.
	if err := s.CreatePost(ctx, newPost(th.ID, u.ID, "Something completely different")); err != nil {
		t.Fatalf("CreatePost noise: %v", err)
	}

	result, err := s.Search(ctx, "fox", store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total: got %d, want 1", result.Total)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Items: got %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.PostID != p.ID {
		t.Errorf("PostID: got %d, want %d", item.PostID, p.ID)
	}
	if item.ThreadID != th.ID {
		t.Errorf("ThreadID: got %d, want %d", item.ThreadID, th.ID)
	}
	if item.AuthorUsername != u.Username {
		t.Errorf("AuthorUsername: got %q, want %q", item.AuthorUsername, u.Username)
	}
	if item.ThreadTitle == "" {
		t.Error("ThreadTitle should not be empty")
	}
	// Snippet should contain highlight markers.
	if !strings.Contains(item.Snippet, "<mark>") {
		t.Errorf("Snippet does not contain highlight markers: %q", item.Snippet)
	}
}

func TestSearch_DeletedPostsExcluded(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "srch-del")

	p := newPost(th.ID, u.ID, "uniquekeyword12345")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if err := s.DeletePost(ctx, p.ID, u.ID); err != nil {
		t.Fatalf("DeletePost: %v", err)
	}

	result, err := s.Search(ctx, "uniquekeyword12345", store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 results for deleted post, got %d", result.Total)
	}
}

func TestSearch_Pagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "srch-pag")

	// Create 10 posts all containing the same keyword.
	for i := 0; i < 10; i++ {
		p := newPost(th.ID, u.ID, fmt.Sprintf("searchterm post number %d content", i))
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("CreatePost %d: %v", i, err)
		}
	}

	page1, err := s.Search(ctx, "searchterm", store.PageRequest{Page: 1, PerPage: 5})
	if err != nil {
		t.Fatalf("Search page1: %v", err)
	}
	if page1.Total != 10 {
		t.Errorf("Total: got %d, want 10", page1.Total)
	}
	if len(page1.Items) != 5 {
		t.Errorf("page1 items: got %d, want 5", len(page1.Items))
	}
	if page1.TotalPages != 2 {
		t.Errorf("TotalPages: got %d, want 2", page1.TotalPages)
	}

	page2, err := s.Search(ctx, "searchterm", store.PageRequest{Page: 2, PerPage: 5})
	if err != nil {
		t.Fatalf("Search page2: %v", err)
	}
	if len(page2.Items) != 5 {
		t.Errorf("page2 items: got %d, want 5", len(page2.Items))
	}
}

func TestSearch_NoResults(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	result, err := s.Search(ctx, "nonexistentterm99999", store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("Total: got %d, want 0", result.Total)
	}
	if len(result.Items) != 0 {
		t.Errorf("Items: got %d, want 0", len(result.Items))
	}
}

func TestSearch_UpdatedPostReflected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "srch-upd")

	p := newPost(th.ID, u.ID, "original content without keyword")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	// Should not match before update.
	r1, err := s.Search(ctx, "specialword", store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("Search before update: %v", err)
	}
	if r1.Total != 0 {
		t.Errorf("expected 0 before update, got %d", r1.Total)
	}

	// Update the post body to include the keyword.
	p.Body = "now contains specialword in the content"
	if err := s.UpdatePost(ctx, p); err != nil {
		t.Fatalf("UpdatePost: %v", err)
	}

	r2, err := s.Search(ctx, "specialword", store.PageRequest{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("Search after update: %v", err)
	}
	if r2.Total != 1 {
		t.Errorf("expected 1 after update, got %d", r2.Total)
	}
}
