# glue

## Objective

glue (`maragu.dev/glue`) is an opinionated, personal Go framework providing glue code for web applications. It exists because every web app needs the same boring infrastructure — HTTP servers, database connections, background jobs, email, object storage, auth, observability — and wiring that up from scratch each time is tedious and error-prone.

glue is **not** a general-purpose framework. It is built for one user (maragu) and all consuming applications are well-known and controlled. This means:

- Breaking changes are acceptable.
- Configuration is intentionally minimal. Sensible defaults are chosen once, not exposed as options.
- The framework favors convention and composition over flexibility.

## Tech Stack

| Concern | Choice |
|---|---|
| Language | Go (latest stable, currently 1.25) |
| HTTP router | chi/v5 |
| HTML rendering | gomponents (server-side, pure Go) |
| Session management | alexedwards/scs/v2 |
| Database (relational) | SQLite (mattn/go-sqlite3) and/or PostgreSQL (jackc/pgx/v5) via jmoiron/sqlx |
| Migrations | maragu.dev/migrate |
| Background jobs | maragu.dev/goqite (database-backed queue) |
| Object storage | AWS SDK v2 (S3-compatible) |
| Transactional email | Postmark |
| Observability | OpenTelemetry (traces via Honeycomb, otel-config-go) |
| Logging | stdlib slog |
| Error wrapping | maragu.dev/errors |
| Environment config | maragu.dev/env (.env files, /run/secrets/env) |
| Linter | golangci-lint |
| CI | GitHub Actions |

## Project Structure

```
glue/
  app/            Application lifecycle (signal handling, env loading, OTel init)
  aws/            AWS SDK config wrapper
  email/          Email sender interface and template loading
    postmark/     Postmark implementation of email.Sender
    postmarktest/ Test helper for Postmark sender
  html/           HTML page props, layout helpers, pagination (gomponents)
  http/           HTTP server, router, middleware, auth, tracing
  jobs/           Background job queue with OTel trace propagation
  log/            Structured logging (slog wrapper)
  model/          Core domain types (IDs, roles, permissions, email, time, errors)
  oteltest/       OTel test helpers (span recorder, attribute checks)
  postgresstore/  SCS session store backed by PostgreSQL
  postgrestest/   PostgreSQL test helper (isolated DB per test)
  s3/             S3 object storage client with OTel tracing
  s3test/         S3 test helper (uses VersityGW locally)
  sql/            Database abstraction (connect, transact, query, migrate, job queues)
  sqlite/         SQLite-specific code and embedded migrations
  sqlitestore/    SCS session store backed by SQLite
  sqlitetest/     SQLite test helper (in-memory DB per test, fixtures)
  .github/        CI workflows and Dependabot config
  docs/           Project documentation (this spec)
```

## Commands

```shell
# Build
go build ./...

# Test (all, with shuffled order)
go test -shuffle on ./...

# Test with coverage
go test -coverprofile cover.out -shuffle on ./...
go tool cover -html cover.out

# Benchmark
go test -bench . ./...

# Lint
golangci-lint run

# Format
goimports -w -local maragu.dev/glue .

# Start test dependencies (PostgreSQL 17, VersityGW for S3)
docker compose up -d

# Stop test dependencies
docker compose down
```

## Code Style

- Standard Go conventions. `goimports` for formatting with local import grouping (`maragu.dev/glue`).
- gomponents dot-imports are allowed: `maragu.dev/gomponents`, `maragu.dev/gomponents/components`, `maragu.dev/gomponents/html`, `maragu.dev/gomponents/http`.
- String-based types for domain concepts (`model.ID`, `model.UserID`, `model.Role`, `model.Permission`, `model.EmailAddress`).
- Errors wrapped with `maragu.dev/errors` for context.
- OTel spans added to all I/O boundaries (HTTP, SQL, S3, email, jobs).
- Middleware typed as `func(http.Handler) http.Handler`.
- Pages are functions: `func(PageProps) (Node, error)`.

## Testing

- Standard `go test` with `-shuffle on`.
- Test helper packages (`sqlitetest`, `postgrestest`, `s3test`, `postmarktest`, `oteltest`) create isolated resources per test with automatic cleanup via `t.Cleanup()`.
- SQLite tests use in-memory databases. PostgreSQL tests use advisory locks to coordinate parallel template1 migration, then create ephemeral databases per test.
- S3 tests run against VersityGW (S3-compatible, replaces MinIO). Tests skip under `testing.Short()`.
- Integration test dependencies run via `docker compose up -d`: PostgreSQL 17 on port 5433, VersityGW on port 7072.
- CI runs the same test matrix in GitHub Actions with service containers.
- Fixtures for SQL tests live in `sqlite/testdata/fixtures/`.

## Git Workflow

- Single long-lived branch: `main`.
- Feature branches with pull requests. PRs run CI (test + lint) before merge.
- Commit messages are short and descriptive, imperative mood. Dependency bumps are automated.
- Dependabot configured with 7-day cooldown for Go modules.

## Boundaries

- **No general-purpose abstractions.** Do not add configuration knobs, strategy patterns, or plugin systems. If something needs to change, change it directly.
- **No frontend JavaScript framework.** HTML is rendered server-side with gomponents. Interactivity comes from Datastar/HTMX in consuming apps, not in glue itself.
- **No ORM.** SQL is written directly. sqlx is used for scanning, not query building.
- **Do not add new database backends.** SQLite and PostgreSQL are the only supported databases.
- **Do not add new email providers.** Postmark is the only supported provider.
- **Do not make breaking changes backward-compatible.** No shims, re-exports, or deprecation periods. Just change it.
- **logo.jpg** is the project logo. Do not modify or remove it.
