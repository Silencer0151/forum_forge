// Package database provides SQLite connection setup and migration running.
// It uses golang-migrate for migration tracking with a custom database.Driver
// backed by modernc.org/sqlite (pure Go, no CGO required).
package database

import (
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

// Open opens a SQLite database at dsn and applies the pragmas required for
// correct web-server operation (WAL mode, FK enforcement, etc.).
// The caller is responsible for calling db.Close().
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := setPragmas(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// setPragmas applies the SQLite pragmas from spec.md §5.4.
func setPragmas(db *sql.DB) error {
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA cache_size = -64000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("set %s: %w", pragma, err)
		}
	}
	return nil
}

// MigrateUp applies all pending up-migrations from migrationsFS.
// migrationsFS must be rooted at the directory containing the *.sql files
// (e.g. pass fs.Sub(embeddedFS, "migrations") from the caller).
func MigrateUp(db *sql.DB, migrationsFS fs.FS) error {
	return runMigrate(db, migrationsFS, func(m *migrate.Migrate) error {
		return m.Up()
	})
}

// MigrateDown rolls back all applied migrations using the down-migration files.
func MigrateDown(db *sql.DB, migrationsFS fs.FS) error {
	return runMigrate(db, migrationsFS, func(m *migrate.Migrate) error {
		return m.Down()
	})
}

func runMigrate(db *sql.DB, migrationsFS fs.FS, fn func(*migrate.Migrate) error) error {
	src, err := iofs.New(migrationsFS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	drv := &sqliteDriver{db: db}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	if err := fn(m); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

// ── golang-migrate database.Driver implementation ─────────────────────────
//
// sqliteDriver implements migrate/database.Driver backed by modernc.org/sqlite.
// golang-migrate's built-in sqlite3 driver requires mattn/go-sqlite3 (CGO);
// this driver provides the same interface without CGO.

type sqliteDriver struct {
	db *sql.DB
}

// Open is required by the Driver interface but unused when the driver is
// created via migrate.NewWithInstance. It exists for interface compliance.
func (d *sqliteDriver) Open(url string) (migratedb.Driver, error) {
	db, err := Open(url)
	if err != nil {
		return nil, err
	}
	return &sqliteDriver{db: db}, nil
}

// Close is a no-op: the DB lifecycle is managed by the caller of Open/MigrateUp.
func (d *sqliteDriver) Close() error { return nil }

// Lock and Unlock are no-ops: SQLite handles its own file-level locking.
func (d *sqliteDriver) Lock() error   { return nil }
func (d *sqliteDriver) Unlock() error { return nil }

// Run executes the SQL in the migration reader as individual statements.
// It splits on semicolons while correctly handling trigger BEGIN/END bodies.
func (d *sqliteDriver) Run(migration io.Reader) error {
	raw, err := io.ReadAll(migration)
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	for _, stmt := range splitSQL(string(raw)) {
		if _, err := d.db.Exec(stmt); err != nil {
			preview := stmt
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			return fmt.Errorf("exec %q: %w", preview, err)
		}
	}
	return nil
}

func (d *sqliteDriver) SetVersion(version int, dirty bool) error {
	if err := d.ensureVersionTable(); err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM schema_migrations"); err != nil {
		return err
	}
	if version != migratedb.NilVersion {
		dirtyBit := 0
		if dirty {
			dirtyBit = 1
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, dirty) VALUES (?, ?)",
			version, dirtyBit,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *sqliteDriver) Version() (int, bool, error) {
	if err := d.ensureVersionTable(); err != nil {
		return 0, false, err
	}
	var version, dirty int
	err := d.db.QueryRow(
		"SELECT version, dirty FROM schema_migrations LIMIT 1",
	).Scan(&version, &dirty)
	if err == sql.ErrNoRows {
		return migratedb.NilVersion, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return version, dirty != 0, nil
}

// Drop removes all user-created tables and triggers from the database.
// It is called by golang-migrate when a migration needs to be re-applied
// from scratch. Foreign key checks are disabled for the duration.
func (d *sqliteDriver) Drop() error {
	if _, err := d.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer d.db.Exec("PRAGMA foreign_keys = ON")

	for _, objType := range []string{"trigger", "table"} {
		rows, err := d.db.Query(
			"SELECT name FROM sqlite_master WHERE type = ? AND name NOT LIKE 'sqlite_%'",
			objType,
		)
		if err != nil {
			return fmt.Errorf("list %ss: %w", objType, err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			names = append(names, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		dropKW := strings.ToUpper(objType)
		for _, name := range names {
			if _, err := d.db.Exec(
				"DROP " + dropKW + " IF EXISTS " + name,
			); err != nil {
				return fmt.Errorf("drop %s %q: %w", objType, name, err)
			}
		}
	}
	return nil
}

func (d *sqliteDriver) ensureVersionTable() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER NOT NULL,
			dirty   INTEGER NOT NULL DEFAULT 0
		)
	`)
	return err
}

// ── SQL statement splitter ────────────────────────────────────────────────

// splitSQL splits a SQL string into individual statements. It handles trigger
// bodies that contain semicolons inside BEGIN/END blocks, which a naive
// split-on-semicolon would break.
func splitSQL(sql string) []string {
	var stmts []string
	var buf strings.Builder
	inTrigger := false

	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}

		buf.WriteString(line)
		buf.WriteByte('\n')

		upper := strings.ToUpper(trimmed)

		switch {
		case !inTrigger && (strings.HasPrefix(upper, "CREATE TRIGGER") ||
			strings.HasPrefix(upper, "CREATE OR REPLACE TRIGGER")):
			inTrigger = true

		case inTrigger && upper == "END;":
			inTrigger = false
			if s := strings.TrimSpace(buf.String()); s != "" {
				stmts = append(stmts, s)
			}
			buf.Reset()

		case !inTrigger && strings.HasSuffix(trimmed, ";"):
			if s := strings.TrimSpace(buf.String()); s != "" {
				stmts = append(stmts, s)
			}
			buf.Reset()
		}
	}

	return stmts
}
