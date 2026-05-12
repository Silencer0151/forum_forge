package sqlite

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/nitro/forum_forge/internal/store"
)

// Search performs a full-text search over posts using the FTS5 index.
// Results are ordered by FTS5 rank (relevance). Snippet highlighting is
// applied in Go because modernc.org/sqlite does not support FTS5 auxiliary
// functions (highlight/snippet) with external content tables.
func (s *Store) Search(ctx context.Context, query string, page store.PageRequest) (*store.PageResult[store.SearchResult], error) {
	perPage := page.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	pg := page.Page
	if pg <= 0 {
		pg = 1
	}
	offset := (pg - 1) * perPage

	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM posts_fts
		JOIN posts p ON p.id = posts_fts.rowid
		WHERE posts_fts MATCH ? AND p.is_deleted = 0`,
		query,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("search count: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.thread_id,
			t.title,
			p.body,
			u.username,
			p.created_at
		FROM posts_fts
		JOIN posts p ON p.id = posts_fts.rowid
		JOIN threads t ON t.id = p.thread_id
		JOIN users u ON u.id = p.author_id
		WHERE posts_fts MATCH ? AND p.is_deleted = 0
		ORDER BY posts_fts.rank
		LIMIT ? OFFSET ?`,
		query, perPage, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var items []store.SearchResult
	for rows.Next() {
		var r store.SearchResult
		var body string
		var createdAt dbTime
		if err := rows.Scan(
			&r.PostID, &r.ThreadID, &r.ThreadTitle, &body, &r.AuthorUsername, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.CreatedAt = createdAt.T
		r.Snippet = buildSnippet(body, query)
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}
	return &store.PageResult[store.SearchResult]{
		Items:      items,
		Total:      total,
		Page:       pg,
		TotalPages: totalPages,
	}, nil
}

// buildSnippet extracts a short excerpt of body text around the first query
// term match and wraps matches in <mark>...</mark> tags. The body is HTML-
// escaped before <mark> insertion so user-supplied content cannot inject
// scripts into the rendered search results page.
func buildSnippet(body, query string) string {
	words := strings.Fields(query)
	lower := strings.ToLower(body)

	// Find the earliest match to centre the snippet.
	start := 0
	for _, w := range words {
		w = strings.ToLower(w)
		if w == "" {
			continue
		}
		if idx := strings.Index(lower, w); idx >= 0 {
			s := idx - 50
			if s < 0 {
				s = 0
			}
			start = s
			break
		}
	}
	end := start + 200
	if end > len(body) {
		end = len(body)
	}
	snippet := html.EscapeString(body[start:end])

	// Wrap each query word in <mark> tags (case-insensitive). Operate on the
	// already-escaped snippet so the match text remains escaped inside the
	// <mark> wrapper.
	for _, w := range words {
		if w == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(html.EscapeString(w)))
		if err != nil {
			continue
		}
		snippet = re.ReplaceAllStringFunc(snippet, func(match string) string {
			return "<mark>" + match + "</mark>"
		})
	}
	return snippet
}
