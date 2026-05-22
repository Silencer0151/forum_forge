# Forum Forge

A classic-leaning, text-first discussion forum built with Go, HTMX, and SQLite. Designed to work out of the box as a standalone community platform, with clean integration points for embedding into existing applications.

- **Single binary** — no Node, no Redis, no separate process manager
- **SQLite by default** — zero ops, backs up with a file copy; Postgres supported for scale
- **HTMX-powered** — progressive enhancement, fully functional without JavaScript
- **Dark theme** — purple-black with gold accents, designed to harmonise with Kanoogi's aesthetic
- **Auth adapter** — swap between built-in email/password auth and an external provider without touching handler code

---

## Quickstart

### Build and run locally

```sh
git clone https://github.com/nitro/forum_forge
cd forum_forge

make build
./forum_forge
```

Open [http://localhost:8080](http://localhost:8080). The first user to register becomes the admin.

### Seed demo content (optional)

For local demos or manual smoke-testing, populate the database with realistic content — categories, threads, posts, and demo accounts spanning admin/moderator/member roles:

```sh
go run ./cmd/seed -force
```

Every demo account uses the password `password123`. Log in as `admin@forum.test`, `moderator@forum.test`, or any of `alice/bob/carol/dave/eve/mallory @forum.test`. See [docs/verify-seed.md](docs/verify-seed.md) for the full walkthrough.

### Run with Docker

```sh
docker compose up
```

See [docs/deployment.md](docs/deployment.md) for production deployment options.

---

## Configuration

All configuration is via environment variables with the `FORUM_` prefix.

| Variable | Default | Description |
|---|---|---|
| `FORUM_BASE_URL` | `http://localhost:8080` | Full public URL of the forum (must include scheme) |
| `FORUM_LISTEN_ADDR` | `:8080` | TCP address to listen on |
| `FORUM_DB_DRIVER` | `sqlite` | Database driver: `sqlite` or `postgres` |
| `FORUM_DB_PATH` | `./data/forum.db` | SQLite database file path |
| `FORUM_DB_DSN` | — | Postgres DSN (required when `DB_DRIVER=postgres`) |
| `FORUM_UPLOAD_PATH` | `./uploads` | Directory for user-uploaded files |
| `FORUM_SESSION_SECRET` | — | Secret key for session signing (required in production) |
| `FORUM_AUTH_MODE` | `standalone` | Auth mode: `standalone` or `integrated` |
| `FORUM_TLS_MODE` | `none` | TLS mode: `none`, `autocert`, or `manual` |
| `FORUM_TLS_DOMAINS` | — | Comma-separated domains for autocert (required if `TLS_MODE=autocert`) |
| `FORUM_TLS_CERT_DIR` | `./certs` | Directory for autocert cache |
| `FORUM_TLS_CERT_FILE` | — | Path to TLS certificate (required if `TLS_MODE=manual`) |
| `FORUM_TLS_KEY_FILE` | — | Path to TLS private key (required if `TLS_MODE=manual`) |
| `FORUM_REDIRECT_HTTP` | `true` (when TLS active) | Redirect HTTP → HTTPS |
| `FORUM_TRUST_PROXY` | `false` | Trust `X-Forwarded-*` headers from upstream proxy |
| `FORUM_SMTP_HOST` | — | SMTP server hostname |
| `FORUM_SMTP_PORT` | `587` | SMTP server port |
| `FORUM_SMTP_USER` | — | SMTP username |
| `FORUM_SMTP_PASS` | — | SMTP password |
| `FORUM_SMTP_FROM` | — | From address for outgoing email |
| `FORUM_CAPTCHA_PROVIDER` | `none` | CAPTCHA provider: `none` or `hcaptcha` |
| `FORUM_CAPTCHA_SITEKEY` | — | hCaptcha site key |
| `FORUM_CAPTCHA_SECRET` | — | hCaptcha secret key |
| `FORUM_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `FORUM_LOG_FORMAT` | `text` | Log format: `text` or `json` |

If `SMTP_HOST` is not set, verification and reset links are logged to stdout instead of emailed (useful during development).

---

## Development

### Prerequisites

- Go 1.22+
- [`golangci-lint`](https://golangci-lint.run/usage/install/) (for linting)

### Make targets

```sh
make build        # compile binary to ./forum_forge
make run          # go run (no binary)
make test         # run all tests
make lint         # run golangci-lint
make migrate-up   # apply database migrations
make migrate-down # roll back most recent migration batch
make clean        # remove compiled binary
```

### Running tests

```sh
# All tests (unit + integration)
make test

# Integration tests only
go test ./tests/... -v

# Specific package
go test ./internal/store/sqlite/... -run TestUser
```

Integration tests use an in-memory SQLite database and a real HTTP server (`httptest.Server`), so they require no external setup.

### Project layout

```
cmd/forum/          — entry point: config loading, DB init, server startup
internal/
  auth/             — auth adapter interface, standalone and Kanoogi implementations
  config/           — environment-based configuration with validation
  database/         — migration runner
  handler/          — HTTP handlers, one file per feature area
  middleware/       — auth context, CSRF, rate limiting, security headers, logging
  model/            — data structs (User, Thread, Post, etc.)
  render/           — html/template renderer + goldmark markdown pipeline
  server/           — router setup, TLS startup, graceful shutdown
  store/            — Store interface + SQLite implementation
  upload/           — file upload validation, storage, serving
migrations/         — SQL migration files (up + down)
templates/          — html/template files (layout, pages, partials)
static/             — CSS, vendored JS (HTMX, Alpine.js)
tests/              — integration test suite
docs/               — spec, deployment guide, integration guide
```

---

## Contributing

1. Fork the repo and create a feature branch.
2. `make lint` and `make test` must both pass before opening a PR.
3. Keep commits focused — one logical change per commit.
4. The data model is defined in `docs/spec.md §2`. Adding columns or tables requires a new migration file.
5. New HTTP endpoints must be registered in `internal/server/server.go` and follow the handler pattern in the existing files.

For larger changes (new features, schema changes, auth changes), open an issue first to discuss the approach.
