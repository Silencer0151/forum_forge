package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

func TestReport_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "rep")
	p := newPost(th.ID, u.ID, "Reportable post")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	reporter := newUser("reporter", "reporter@example.com")
	if err := s.CreateUser(ctx, reporter); err != nil {
		t.Fatalf("CreateUser reporter: %v", err)
	}

	r := &model.Report{
		ReporterID: reporter.ID,
		PostID:     p.ID,
		Reason:     "spam",
	}
	if err := s.CreateReport(ctx, r); err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("expected non-zero ID after CreateReport")
	}
	if r.Status != model.ReportStatusOpen {
		t.Errorf("Status after create: got %q, want %q", r.Status, model.ReportStatusOpen)
	}

	got, err := s.GetReportByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetReportByID: %v", err)
	}
	if got.Reason != r.Reason {
		t.Errorf("Reason: got %q, want %q", got.Reason, r.Reason)
	}
	if got.Status != model.ReportStatusOpen {
		t.Errorf("Status: got %q, want open", got.Status)
	}
	if got.ReviewedBy != nil {
		t.Errorf("ReviewedBy: expected nil on new report, got %v", got.ReviewedBy)
	}
	if got.ReviewedAt != nil {
		t.Errorf("ReviewedAt: expected nil on new report, got %v", got.ReviewedAt)
	}
}

func TestReport_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetReportByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReport_ListOpen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "replist")
	reporter := newUser("replist-reporter", "replistrt@example.com")
	if err := s.CreateUser(ctx, reporter); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for i := 0; i < 3; i++ {
		p := newPost(th.ID, u.ID, fmt.Sprintf("Post %d", i))
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("CreatePost %d: %v", i, err)
		}
		r := &model.Report{ReporterID: reporter.ID, PostID: p.ID, Reason: "spam"}
		if err := s.CreateReport(ctx, r); err != nil {
			t.Fatalf("CreateReport %d: %v", i, err)
		}
	}

	result, err := s.ListOpenReports(ctx, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListOpenReports: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("Total: got %d, want 3", result.Total)
	}
	if len(result.Items) != 3 {
		t.Errorf("Items len: got %d, want 3", len(result.Items))
	}
}

func TestReport_Review(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "reprev")
	p := newPost(th.ID, u.ID, "Reviewed post")
	if err := s.CreatePost(ctx, p); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	reporter := newUser("reprev-reporter", "reprevrt@example.com")
	if err := s.CreateUser(ctx, reporter); err != nil {
		t.Fatalf("CreateUser reporter: %v", err)
	}

	mod := newUser("reprev-mod", "reprevmod@example.com")
	mod.Role = model.RoleModerator
	if err := s.CreateUser(ctx, mod); err != nil {
		t.Fatalf("CreateUser mod: %v", err)
	}

	r := &model.Report{ReporterID: reporter.ID, PostID: p.ID, Reason: "spam"}
	if err := s.CreateReport(ctx, r); err != nil {
		t.Fatalf("CreateReport: %v", err)
	}

	if err := s.ReviewReport(ctx, r.ID, mod.ID, model.ReportStatusDismissed); err != nil {
		t.Fatalf("ReviewReport: %v", err)
	}

	got, err := s.GetReportByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetReportByID: %v", err)
	}
	if got.Status != model.ReportStatusDismissed {
		t.Errorf("Status: got %q, want dismissed", got.Status)
	}
	if got.ReviewedBy == nil || *got.ReviewedBy != mod.ID {
		t.Errorf("ReviewedBy: got %v, want %d", got.ReviewedBy, mod.ID)
	}
	if got.ReviewedAt == nil {
		t.Error("expected ReviewedAt to be set after review")
	}

	// Reviewed report should not appear in the open queue.
	result, err := s.ListOpenReports(ctx, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListOpenReports: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 open reports after review, got %d", result.Total)
	}
}

func TestReport_ListOpenPagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u, th := setupThreadForPost(t, s, ctx, "reppag")
	reporter := newUser("reppag-reporter", "reppagreporter@example.com")
	if err := s.CreateUser(ctx, reporter); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for i := 0; i < 5; i++ {
		p := newPost(th.ID, u.ID, fmt.Sprintf("Pag Post %d", i))
		if err := s.CreatePost(ctx, p); err != nil {
			t.Fatalf("CreatePost %d: %v", i, err)
		}
		r := &model.Report{ReporterID: reporter.ID, PostID: p.ID, Reason: "test"}
		if err := s.CreateReport(ctx, r); err != nil {
			t.Fatalf("CreateReport %d: %v", i, err)
		}
	}

	page1, err := s.ListOpenReports(ctx, store.PageRequest{Page: 1, PerPage: 3})
	if err != nil {
		t.Fatalf("ListOpenReports page 1: %v", err)
	}
	if page1.Total != 5 {
		t.Errorf("Total: got %d, want 5", page1.Total)
	}
	if len(page1.Items) != 3 {
		t.Errorf("page 1 items: got %d, want 3", len(page1.Items))
	}
	if page1.TotalPages != 2 {
		t.Errorf("TotalPages: got %d, want 2", page1.TotalPages)
	}

	page2, err := s.ListOpenReports(ctx, store.PageRequest{Page: 2, PerPage: 3})
	if err != nil {
		t.Fatalf("ListOpenReports page 2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Errorf("page 2 items: got %d, want 2", len(page2.Items))
	}
}
