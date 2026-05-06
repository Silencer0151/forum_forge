// Command forum starts the ForumForge HTTP server. It loads configuration from
// the environment, opens the database, runs pending migrations, and serves
// HTTP(S) until it receives SIGINT or SIGTERM.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	forum "github.com/nitro/forum_forge"
	"github.com/nitro/forum_forge/internal/config"
	"github.com/nitro/forum_forge/internal/database"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/server"
	sqlitestore "github.com/nitro/forum_forge/internal/store/sqlite"
)

func main() {
	migrateCmd := flag.String("migrate", "", "run migrations and exit: up | down")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(2)
	}

	configureLogging(cfg)

	if *migrateCmd != "" {
		if err := runMigrate(cfg, *migrateCmd); err != nil {
			slog.Error("migration failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := run(cfg); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(cfg *config.Config) error {
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	migrationsFS, err := embeddedMigrations()
	if err != nil {
		return err
	}

	st, err := sqlitestore.New(cfg.DBPath, migrationsFS)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	templatesFS, err := embeddedTemplates()
	if err != nil {
		return err
	}
	r, err := render.New(templatesFS)
	if err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	staticFS, err := embeddedStatic()
	if err != nil {
		return err
	}

	srv := server.New(cfg, st, r, staticFS)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("forum starting",
		"db_driver", cfg.DBDriver,
		"db_path", cfg.DBPath,
		"listen_addr", cfg.ListenAddr,
		"base_url", cfg.BaseURL,
		"auth_mode", cfg.AuthMode,
		"tls_mode", cfg.TLSMode)

	return srv.Run(ctx)
}

func runMigrate(cfg *config.Config, cmd string) error {
	if cfg.DBDriver != "sqlite" {
		return fmt.Errorf("only the sqlite driver is supported for now")
	}
	if err := ensureDirs(cfg); err != nil {
		return err
	}

	migrationsFS, err := embeddedMigrations()
	if err != nil {
		return err
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	switch cmd {
	case "up":
		if err := database.MigrateUp(db, migrationsFS); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		slog.Info("migrations applied", "direction", "up")
	case "down":
		if err := database.MigrateDown(db, migrationsFS); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		slog.Info("migrations rolled back", "direction", "down")
	default:
		return fmt.Errorf("unknown migrate command %q (expected up or down)", cmd)
	}
	return nil
}

func ensureDirs(cfg *config.Config) error {
	dirs := []string{
		filepath.Dir(cfg.DBPath),
		cfg.UploadPath,
	}
	if cfg.TLSMode == "autocert" {
		dirs = append(dirs, cfg.TLSCertDir)
	}
	for _, d := range dirs {
		if d == "" || d == "." {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

func configureLogging(cfg *config.Config) {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func embeddedMigrations() (fs.FS, error) {
	sub, err := fs.Sub(forum.MigrationsFS(), "migrations")
	if err != nil {
		return nil, fmt.Errorf("sub migrations: %w", err)
	}
	return sub, nil
}

func embeddedTemplates() (fs.FS, error) {
	sub, err := fs.Sub(forum.TemplatesFS(), "templates")
	if err != nil {
		return nil, fmt.Errorf("sub templates: %w", err)
	}
	return sub, nil
}

func embeddedStatic() (fs.FS, error) {
	sub, err := fs.Sub(forum.StaticFS(), "static")
	if err != nil {
		return nil, fmt.Errorf("sub static: %w", err)
	}
	return sub, nil
}
