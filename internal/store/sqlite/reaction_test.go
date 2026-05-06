package sqlite_test

import (
	"context"
	"testing"

	"github.com/nitro/forum_forge/internal/model"
)

func TestReaction_Toggle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "react-toggle")

	p := newPost(th.ID, u.ID, "react target")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	// First toggle: should add the reaction.
	added, err := s.ToggleReaction(ctx, p.ID, u.ID, model.ReactionTypeLike)
	if err != nil {
		t.Fatalf("ToggleReaction (add): %v", err)
	}
	if !added {
		t.Error("expected added=true on first toggle")
	}

	// Verify it exists via counts.
	counts, err := s.GetReactionCounts(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetReactionCounts: %v", err)
	}
	if len(counts) != 1 || counts[0].Count != 1 || counts[0].Type != model.ReactionTypeLike {
		t.Errorf("unexpected counts after add: %v", counts)
	}

	// Second toggle: should remove the reaction.
	added, err = s.ToggleReaction(ctx, p.ID, u.ID, model.ReactionTypeLike)
	if err != nil {
		t.Fatalf("ToggleReaction (remove): %v", err)
	}
	if added {
		t.Error("expected added=false on second toggle (removal)")
	}

	// Verify it is gone.
	counts, err = s.GetReactionCounts(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetReactionCounts after remove: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected 0 counts after removal, got %d", len(counts))
	}
}

func TestReaction_GetCounts_MultipleUsers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u1, th := setupThreadForPost(t, s, ctx, "rc-multi")
	u2 := newUser("rc-multi-u2", "rc-multi-u2@example.com")
	if err := s.CreateUser(ctx, u2); err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}

	p := newPost(th.ID, u1.ID, "popular post")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	if _, err := s.ToggleReaction(ctx, p.ID, u1.ID, model.ReactionTypeLike); err != nil {
		t.Fatalf("ToggleReaction u1: %v", err)
	}
	if _, err := s.ToggleReaction(ctx, p.ID, u2.ID, model.ReactionTypeLike); err != nil {
		t.Fatalf("ToggleReaction u2: %v", err)
	}

	counts, err := s.GetReactionCounts(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetReactionCounts: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("expected 1 reaction type, got %d", len(counts))
	}
	if counts[0].Count != 2 {
		t.Errorf("Count: got %d, want 2", counts[0].Count)
	}
}

func TestReaction_GetUserReactionsForPosts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "batch-react")
	other := newUser("batch-react-other", "batch-react-other@example.com")
	if err := s.CreateUser(ctx, other); err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}

	p1 := newPost(th.ID, u.ID, "post 1")
	p2 := newPost(th.ID, u.ID, "post 2")
	p3 := newPost(th.ID, u.ID, "post 3")
	for _, p := range []*model.Post{p1, p2, p3} {
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("CreatePost: %v", err)
		}
	}

	// u reacts to p1 and p3; other reacts to p2.
	if _, err := s.ToggleReaction(ctx, p1.ID, u.ID, model.ReactionTypeLike); err != nil {
		t.Fatalf("ToggleReaction p1 u: %v", err)
	}
	if _, err := s.ToggleReaction(ctx, p3.ID, u.ID, model.ReactionTypeLike); err != nil {
		t.Fatalf("ToggleReaction p3 u: %v", err)
	}
	if _, err := s.ToggleReaction(ctx, p2.ID, other.ID, model.ReactionTypeLike); err != nil {
		t.Fatalf("ToggleReaction p2 other: %v", err)
	}

	result, err := s.GetUserReactionsForPosts(ctx, u.ID, []int64{p1.ID, p2.ID, p3.ID})
	if err != nil {
		t.Fatalf("GetUserReactionsForPosts: %v", err)
	}

	if len(result[p1.ID]) != 1 || result[p1.ID][0] != model.ReactionTypeLike {
		t.Errorf("p1 reactions for u: got %v, want [like]", result[p1.ID])
	}
	if len(result[p2.ID]) != 0 {
		t.Errorf("p2 reactions for u: got %v, want []", result[p2.ID])
	}
	if len(result[p3.ID]) != 1 || result[p3.ID][0] != model.ReactionTypeLike {
		t.Errorf("p3 reactions for u: got %v, want [like]", result[p3.ID])
	}
}

func TestReaction_GetUserReactionsForPosts_Empty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	result, err := s.GetUserReactionsForPosts(ctx, 1, []int64{})
	if err != nil {
		t.Fatalf("GetUserReactionsForPosts empty: %v", err)
	}
	if result == nil || len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}
