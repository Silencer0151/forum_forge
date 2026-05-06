package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

const categoryCols = `id, name, slug, description, display_order, is_public, created_at`

func scanCategory(row rowScanner) (*model.Category, error) {
	var c model.Category
	var isPublic int
	var createdAt dbTime
	err := row.Scan(&c.ID, &c.Name, &c.Slug, &c.Description, &c.DisplayOrder, &isPublic, &createdAt)
	if err != nil {
		return nil, err
	}
	c.IsPublic = isPublic != 0
	c.CreatedAt = createdAt.T
	return &c, nil
}

func (s *Store) CreateCategory(ctx context.Context, c *model.Category) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO categories (name, slug, description, display_order, is_public)
		VALUES (?, ?, ?, ?, ?)`,
		c.Name, c.Slug, c.Description, c.DisplayOrder, boolInt(c.IsPublic),
	)
	if err != nil {
		return fmt.Errorf("create category: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create category last id: %w", err)
	}
	c.ID = id
	return nil
}

func (s *Store) GetCategoryByID(ctx context.Context, id int64) (*model.Category, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+categoryCols+" FROM categories WHERE id = ?", id)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

func (s *Store) GetCategoryBySlug(ctx context.Context, slug string) (*model.Category, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+categoryCols+" FROM categories WHERE slug = ?", slug)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

func (s *Store) UpdateCategory(ctx context.Context, c *model.Category) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE categories SET name = ?, slug = ?, description = ?, display_order = ?, is_public = ?
		WHERE id = ?`,
		c.Name, c.Slug, c.Description, c.DisplayOrder, boolInt(c.IsPublic), c.ID,
	)
	if err != nil {
		return fmt.Errorf("update category: %w", err)
	}
	return nil
}

func (s *Store) DeleteCategory(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM categories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	return nil
}

func (s *Store) ListCategories(ctx context.Context) ([]*model.Category, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+categoryCols+" FROM categories ORDER BY display_order ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var cats []*model.Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

func (s *Store) ReorderCategories(ctx context.Context, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			"UPDATE categories SET display_order = ? WHERE id = ?", i, id); err != nil {
			return fmt.Errorf("reorder category %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// ── Subcategory ───────────────────────────────────────────────────────────────

const subcategoryCols = `id, category_id, name, slug, description, display_order, created_at`

func scanSubcategory(row rowScanner) (*model.Subcategory, error) {
	var sub model.Subcategory
	var createdAt dbTime
	err := row.Scan(&sub.ID, &sub.CategoryID, &sub.Name, &sub.Slug, &sub.Description, &sub.DisplayOrder, &createdAt)
	if err != nil {
		return nil, err
	}
	sub.CreatedAt = createdAt.T
	return &sub, nil
}

func (s *Store) CreateSubcategory(ctx context.Context, sub *model.Subcategory) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO subcategories (category_id, name, slug, description, display_order)
		VALUES (?, ?, ?, ?, ?)`,
		sub.CategoryID, sub.Name, sub.Slug, sub.Description, sub.DisplayOrder,
	)
	if err != nil {
		return fmt.Errorf("create subcategory: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create subcategory last id: %w", err)
	}
	sub.ID = id
	return nil
}

func (s *Store) GetSubcategoryByID(ctx context.Context, id int64) (*model.Subcategory, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+subcategoryCols+" FROM subcategories WHERE id = ?", id)
	sub, err := scanSubcategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return sub, err
}

func (s *Store) GetSubcategoryBySlug(ctx context.Context, categorySlug, subcategorySlug string) (*model.Subcategory, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.category_id, s.name, s.slug, s.description, s.display_order, s.created_at
		FROM subcategories s
		JOIN categories c ON c.id = s.category_id
		WHERE c.slug = ? AND s.slug = ?`,
		categorySlug, subcategorySlug,
	)
	sub, err := scanSubcategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return sub, err
}

func (s *Store) UpdateSubcategory(ctx context.Context, sub *model.Subcategory) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subcategories SET category_id = ?, name = ?, slug = ?, description = ?, display_order = ?
		WHERE id = ?`,
		sub.CategoryID, sub.Name, sub.Slug, sub.Description, sub.DisplayOrder, sub.ID,
	)
	if err != nil {
		return fmt.Errorf("update subcategory: %w", err)
	}
	return nil
}

func (s *Store) DeleteSubcategory(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM subcategories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete subcategory: %w", err)
	}
	return nil
}

func (s *Store) ListSubcategoriesByCategoryID(ctx context.Context, categoryID int64) ([]*model.Subcategory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+subcategoryCols+`
		FROM subcategories
		WHERE category_id = ?
		ORDER BY display_order ASC, id ASC`,
		categoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("list subcategories: %w", err)
	}
	defer rows.Close()

	var subs []*model.Subcategory
	for rows.Next() {
		sub, err := scanSubcategory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subcategory: %w", err)
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *Store) ReorderSubcategories(ctx context.Context, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			"UPDATE subcategories SET display_order = ? WHERE id = ?", i, id); err != nil {
			return fmt.Errorf("reorder subcategory %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// GetSubcategoryStatsBatch returns thread/post counts and last-post info for
// the given subcategory IDs in a single query. Missing IDs (no threads) are
// returned with zero counts and an empty LastThreadTitle.
func (s *Store) GetSubcategoryStatsBatch(ctx context.Context, subcategoryIDs []int64) (map[int64]*store.SubcategoryStats, error) {
	if len(subcategoryIDs) == 0 {
		return make(map[int64]*store.SubcategoryStats), nil
	}

	ph := strings.Repeat("?,", len(subcategoryIDs))
	ph = ph[:len(ph)-1] // strip trailing comma

	args := make([]any, len(subcategoryIDs))
	for i, id := range subcategoryIDs {
		args[i] = id
	}

	q := fmt.Sprintf(`
		SELECT
			s.id,
			COUNT(t.id)                                       AS thread_count,
			COALESCE(SUM(t.reply_count + 1), 0)              AS post_count,
			(SELECT ts.last_post_at
			   FROM threads ts
			  WHERE ts.subcategory_id = s.id
			  ORDER BY ts.last_post_at DESC LIMIT 1)         AS last_post_at,
			COALESCE(
				(SELECT u.username
				   FROM threads ts
				   LEFT JOIN users u ON u.id = ts.last_post_by
				  WHERE ts.subcategory_id = s.id
				  ORDER BY ts.last_post_at DESC LIMIT 1),
			'')                                              AS last_post_username,
			COALESCE(
				(SELECT ts.title
				   FROM threads ts
				  WHERE ts.subcategory_id = s.id
				  ORDER BY ts.last_post_at DESC LIMIT 1),
			'')                                              AS last_thread_title
		FROM subcategories s
		LEFT JOIN threads t ON t.subcategory_id = s.id
		WHERE s.id IN (%s)
		GROUP BY s.id`, ph)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("subcategory stats batch: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]*store.SubcategoryStats, len(subcategoryIDs))
	for rows.Next() {
		var stat store.SubcategoryStats
		var lastPostAt dbNullTime
		if err := rows.Scan(
			&stat.SubcategoryID,
			&stat.ThreadCount,
			&stat.PostCount,
			&lastPostAt,
			&stat.LastPostUsername,
			&stat.LastThreadTitle,
		); err != nil {
			return nil, fmt.Errorf("scan subcategory stats: %w", err)
		}
		if lastPostAt.T != nil {
			stat.LastPostAt = *lastPostAt.T
		}
		result[stat.SubcategoryID] = &stat
	}
	return result, rows.Err()
}
