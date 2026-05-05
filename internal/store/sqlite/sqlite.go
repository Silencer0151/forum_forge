// Package sqlite provides a SQLite-backed implementation of store.Store.
package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"time"

	"github.com/nitro/forum_forge/internal/database"
)

// Store is the SQLite-backed implementation of store.Store.
// Methods are added across user.go, session.go, settings.go, etc.
type Store struct {
	db *sql.DB
}

// New opens a SQLite database at dsn, applies pragmas, and runs all pending migrations.
func New(dsn string, migrationsFS fs.FS) (*Store, error) {
	db, err := database.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := database.MigrateUp(db, migrationsFS); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// dbTime is a sql.Scanner that handles SQLite's TEXT datetime format.
// modernc.org/sqlite returns DATETIME columns as strings, not time.Time.
type dbTime struct{ T time.Time }

var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	time.RFC3339,
}

func (t *dbTime) Scan(v any) error {
	switch s := v.(type) {
	case time.Time:
		t.T = s.UTC()
	case string:
		return t.parseStr(s)
	case []byte:
		return t.parseStr(string(s))
	default:
		return fmt.Errorf("dbTime: unsupported type %T", v)
	}
	return nil
}

func (t *dbTime) parseStr(s string) error {
	for _, layout := range timeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.T = parsed.UTC()
			return nil
		}
	}
	return fmt.Errorf("dbTime: cannot parse %q", s)
}

// dbNullTime is a sql.Scanner for nullable SQLite datetimes.
type dbNullTime struct{ T *time.Time }

func (t *dbNullTime) Scan(v any) error {
	if v == nil {
		t.T = nil
		return nil
	}
	dt := &dbTime{}
	if err := dt.Scan(v); err != nil {
		return err
	}
	t.T = &dt.T
	return nil
}
