# Forum Forge

A classic-leaning, text-first discussion forum built with Go, HTMX, and SQLite.

## Quickstart

```sh
make build
./forum_forge
```

## Development

```sh
make run       # run without building binary
make test      # run all tests
make lint      # run golangci-lint
make clean     # remove built binary
```

## Migrations

```sh
make migrate-up    # apply migrations
make migrate-down  # roll back migrations
```

## Configuration

Configure via environment variables. See `docs/spec.md §14` for the full reference.
