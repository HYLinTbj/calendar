# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & test commands

This machine's shell profile sets `GOPATH` equal to `GOROOT` (`~/go`), which leaves the module cache empty and breaks `go build` with "missing go.sum entry". **Always build/test with an explicit GOPATH and module-write enabled:**

```bash
export PATH="$HOME/go/bin:$PATH" GOPATH=/tmp/gopath GOFLAGS=-mod=mod
go build ./...                       # build all three services
go vet ./...                         # vet
go vet -tags integration ./...       # also type-checks integration tests without running them
gofmt -l <files>                     # format check (CI-relevant; gofmt -w to fix)
```

Piping `go build`/`test`/`vet` to `head`/`tail` can kill `go` with SIGPIPE (false exit 141) — redirect to a file instead (`go vet -tags integration ./... > /tmp/vet.out 2>&1`).

Tests are **integration tests gated behind the `integration` build tag**. They spin up a real Postgres via testcontainers (needs Docker) and an in-process Redis via miniredis:

```bash
go test -tags integration ./...                                   # all
go test -tags integration ./internal/handler/ -run TestEventStats_Endpoint   # single test
```

`go test` without `-tags integration` finds no tests. If Docker is unavailable, use `go vet -tags integration ./...` to compile-check test code.

## Run the stack

```bash
docker compose up --build          # api :8080, nginx :80, postgres host :5433, redis :6379, mailhog UI :8025
```

Schema is created/upgraded automatically at api startup (see migrations below) — there is no separate migrate step. The UI is `http://localhost/` (nginx serves `Calendar.html` as index, proxies `/api/` to the api); the api is also reachable directly at `:8080`. Captured emails appear in MailHog at `:8025`. Note Postgres is published on host port **5433** (internal 5432).

### Frontend without Docker

`Calendar.html` is a single self-contained file (React 18 + Babel via CDN, shared `S` style object, an `api()` fetch helper, `API_BASE` hardcoded to `http://localhost:8080`). `mock_api.py` is a stdlib-only mock of the API on `:8080` with CORS + seed data. To iterate on the UI alone: run `python3 mock_api.py` and open `Calendar.html` directly. Keep `mock_api.py`'s response shapes in sync with the Go handlers when you change an endpoint. To verify the mock live here, background it and poll with `curl --retry-connrefused --retry 20 http://localhost:8080/health` (foreground `sleep` is blocked); the `kill` afterward exits nonzero — not a failure.

## Architecture

**One Go module** (`github.com/hylin/calendar`, Go 1.23) containing **three services**, each with its own entrypoint: `api/cmd/api`, `notification/cmd/notification`, `scheduler/cmd/scheduler`.

- **api** — the Gin REST API; all user-facing endpoints.
- **scheduler** — periodically materializes recurring-event instances ~60 days out.
- **notification** — consumes reminder jobs from Redis and sends reminder/invitation emails over SMTP.

Code is shared via Go's `internal/` visibility rule, in two tiers:
- **Top-level `internal/`** (`db`, `model`, `repository`, `handler`, `middleware`, `queue`, `ics`) — importable by all three services.
- **Per-service `internal/`** (`notification/internal/{worker,mailer}`, `scheduler/internal/worker`) — private to that service.

This is why services import `github.com/hylin/calendar/internal/...` for shared logic but `.../<service>/internal/...` for their own. Moving shared code out of the parent of an `internal/` dir breaks the build — keep cross-service code in the top-level `internal/`.

### Layered request flow (api)

`model` (structs + request DTOs with `binding` tags) → `repository` (SQL via pgx/v5 pool) → `handler` (Gin) → routes wired in `api/cmd/api/main.go`. Cross-cutting conventions that repeat across entities — match them when adding code:

- **Multi-tenant by `owner_id`**: every repository query filters/scopes by the owner. Handlers read the caller via `c.MustGet(middleware.UserIDKey).(uuid.UUID)`.
- **Repository style**: a column-list `const`, a `scan*` helper, CRUD methods; `pgx.ErrNoRows` bubbles up and handlers translate it to 404. Helpers like `itoa` and unique-violation detection live alongside.
- **Auth**: `middleware.Auth()` validates a JWT from the `Authorization: Bearer` header and sets `UserIDKey`. Token-only routes (invitation accept/decline) sit outside the auth group. `middleware.CORS()` is applied globally (wildcard origin; tokens travel in headers, not cookies).
- **Calendar resolution**: event-creating handlers call `resolveCalendar` to fall back to the user's default calendar when none is given.

### Database & migrations

Schema lives **inline in `internal/db/db.go`'s `Migrate()`** as one idempotent SQL block (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, indexes, an `events` full-text `tsvector` trigger). It runs on every api boot. **Add schema changes by appending idempotent statements here** — there are no migration files or a migration tool.

### Async flows

- **Reminders**: api enqueues reminder jobs to Redis via `internal/queue`; the notification worker polls and emails them. Editing/deleting an event cancels its pending reminder first.
- **Recurring events**: the `events` table holds *materialized* instances; the scheduler extends the window. Recurrence edits take a `scope` of `this` | `this_and_following` | `all` (`PUT /events/:id/recurrence`), handled in `handler/event.go` + `repository/recurring_event.go`.

### Time tracking = categorized events (no separate table)

Time tracking is **unified into calendar events** — there is intentionally no `time_logs` table. A categorized, non-all-day event *is* a logged session: duration = `end_time − start_time`, area = its `category_id`, sub-activity = its `title`. Categories double as "Areas" (they carry `weekly_target_minutes`). `GET /events/stats?from=&to=` (`EventRepository.Stats`) rolls minutes up per area and per title, excluding all-day events; query `to=<now>` to count only elapsed time. **Tasks** (`internal/{model,repository,handler}/task.go`) are a separate lightweight backlog entity. Don't reintroduce a parallel time-log entity.

### Adding a persisted entity (touches several files)

1. `internal/model/<x>.go` — struct + Create/Update request DTOs.
2. `internal/repository/<x>.go` — column-list const, `scan*`, CRUD scoped by `owner_id`.
3. `internal/handler/<x>.go` — Gin handlers.
4. `api/cmd/api/main.go` — construct repo+handler, register a route group under `protected`.
5. `internal/db/db.go` — append the idempotent `CREATE TABLE` to `Migrate()`.
6. Tests — register routes in `internal/handler/testmain_test.go`, add the table to the `truncateAll` lists in both `helpers_test.go` files.

Shared integration-test helpers (e.g. `createCategory`) live in `internal/handler/helpers_test.go`, not in a feature's `_test.go` — feature test files get deleted/renamed but are used across the whole `package handler_test` suite.
