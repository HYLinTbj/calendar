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
cp .env.example .env && sed -i "s/^JWT_SECRET=.*/JWT_SECRET=$(openssl rand -hex 32)/" .env   # once; .env is gitignored
docker compose up --build          # api :8080, nginx :80, postgres host :5433, redis :6379, mailhog UI :8025
```

The api refuses to start without a `JWT_SECRET` of at least 32 bytes (there is deliberately no default); running it outside compose needs the variable exported too.

Schema is created/upgraded automatically at api startup (see migrations below) — there is no separate migrate step. The UI is `http://localhost/` (nginx serves `Calendar.html` as index, proxies `/api/` to the api); the api is also reachable directly at `:8080`. Captured emails appear in MailHog at `:8025`. Note Postgres is published on host port **5433** (internal 5432).

### Frontend without Docker

`Calendar.html` is a single self-contained file (React 18 + Babel via CDN, shared `S` style object, an `api()` fetch helper, `API_BASE` hardcoded to `http://localhost:8080`). To iterate on the UI alone, serve a mock of the API on `:8080` (with CORS headers) and open `Calendar.html` directly. `mock_api.py` is the conventional name for that mock — a stdlib-only Python server with seed data — but it is a **local, untracked helper** (gitignored, not part of the repo): write one if it's missing, and keep its response shapes in sync with the Go handlers when you change an endpoint. To verify the mock live here, background it and poll with `curl --retry-connrefused --retry 20 http://localhost:8080/health` (foreground `sleep` is blocked); the `kill` afterward exits nonzero — not a failure.

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
  - **Exception, events in shared calendars:** writes go through `eventWriteAccess` in `repository/event.go`. The calendar's owner may always write. The event's author may write only while they hold an `edit` share. Series can only live in calendars their owner owns.
- **Repository style**: a column-list `const`, a `scan*` helper, CRUD methods; `pgx.ErrNoRows` bubbles up and handlers translate it to 404. Helpers like `itoa` and unique-violation detection live alongside.
- **Auth**: `middleware.Auth(userRepo)` validates a JWT from the `Authorization: Bearer` header and sets `UserIDKey`. It also checks, one query per request, that the user still exists and that the token's `tv` claim matches `users.token_version`. A password change bumps that version, which signs out every older session. Changing email or password needs `current_password`.
  - Token-only routes (invitation accept/decline) sit outside the auth group.
  - `middleware.CORS()` is applied globally (wildcard origin; tokens travel in headers, not cookies).
  - Emails are case-insensitive: they're stored lowercased (`normalizeEmail`) and matched with `lower(email)`.
- **Calendar resolution**: event-creating handlers call `resolveCalendar` to fall back to the user's default calendar when none is given.

### Database & migrations

Schema lives **inline in `internal/db/db.go`'s `Migrate()`** as one idempotent SQL block (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, indexes, an `events` full-text `tsvector` trigger). It runs on every api boot. **Add schema changes by appending idempotent statements here** — there are no migration files or a migration tool.

### Async flows

- **Reminders**: api enqueues reminder jobs to Redis via `internal/queue`; the notification worker polls and emails them. Editing/deleting an event cancels and requeues its reminders, but only *after* the change applied: the queue is keyed by event id alone, so cancelling first would let any caller wipe another user's reminders. Reminders whose time has already passed aren't queued (except one still pending that an edit moved into the past, which goes out at once), and ones already due aren't cancelled (the worker sends or drops them). The worker sends from the event's current row, one message per attendee, and drops a job whose event or reminder no longer exists. Some deletes (an account, a series) don't cancel their reminders.
- **Invitations**: `event_invitations` rows in `pending_send` are mailed by the worker, oldest first. A series' occurrences are mailed only once they're within `repository.WindowDays`, however far ahead a view materialized them.
  - A row whose own send fails is retried with backoff, then set to `failed` (`attempts`, `last_error`). A failure of the mail relay itself (`mailer.Systemic`) ends the poll without counting against anyone. A refusal for the relay's own reasons (`mailer.RelayRefused`, by the reply's enhanced status code: policy, our sender, its own state) leaves the row pending, uncounted. A later change to the event re-queues `failed` rows.
  - Edits invite only newly added attendees.
  - Attendee lists are validated as email addresses (max 100).
  - ICS imports never invite anyone: `recurring_events.send_invitations=false` for imported series.
- **Recurring events**: the `events` table holds *materialized* instances; the scheduler extends the window. Recurrence edits and deletes take a `scope` of `this` | `this_and_following` | `all` (`PUT` / `DELETE /events/:id/recurrence`), handled in `handler/event.go` + `repository/recurring_event.go`. Scopes `all` and `this_and_following` apply only the fields that differ from the occurrence edited (past occurrences keep details later series edits changed). `Update` and `TruncateAt` run in one transaction holding the series' row. A weekly series every 2+ weeks groups its days by `week_start` (the UI sends Sunday; unset is Monday, as in RFC 5545); moving its days moves the week start along.
  - **Exceptions**: any edit of a single instance (`EventRepository.Update`, which covers scope `this`, drag and the Log view) detaches it into a standalone event. Deleting one (`EventRepository.Delete`) removes it. Both append the instance's start time to `recurring_events.exdates`. An edited instance stays part of its series, as in Google Calendar and Outlook: the API returns its `detached_from`, the UI keeps its ↻ marker, and deleting the series (or this-and-following) deletes it. `DELETE /events/:id/recurrence` accepts it too, counting this-and-following from `original_start`; edits apply to it alone. Generation skips exdates but still counts them toward `max_occurrences`. So a *linked* instance's `start_time` is always its original occurrence time. Series-wide regeneration (`Update` / `SplitAt` delete linked future rows and regenerate; `Update` edits them in place when no occurrence moves) relies on this. Keep it true, and shift or carry exdates whenever you move a series' anchor. Data from before this rule (instances edited in place while still linked, recognisable by `updated_at > created_at`; occurrences deleted without an exdate) is fixed once, at the first api startup, by `RecurringEventRepository.RepairLegacyExceptions` (recorded in `maintenance_runs`).

### Time tracking = categorized events + traces

Tracked time comes from two sources:

- **Sessions:** a categorized, non-all-day event is a session at a real time. Its duration is `end_time − start_time`, its area is its `category_id`, and its sub-activity is its `title`.
- **Traces:** `time_traces` (`internal/{model,repository,handler}/trace.go`, `/traces`) record short stretches with **no clock time**: a `day` (`model.Date`, "YYYY-MM-DD"), `minutes`, an area and a note. They never render on the calendar grid, only as a strip under the day headers.
- **The timer:** a stopped timer under 15 min (`TRACE_CUTOFF_MIN` in `Calendar.html`) becomes a trace. Longer ones become an event.

Areas and codes:

- Categories double as "Areas" (they carry `weekly_target_minutes`).
- Areas can optionally belong to a **category group** (`category_groups`, `/category-groups`; e.g. "Language learning" holding French and Japanese). The group carries the shared colour, and deleting it leaves its areas ungrouped.
- Each area has a short per-owner `code` badge ("FR"), derived from the name when not supplied (`model.DeriveCategoryCode`).

Stats: `GET /events/stats?from=&to=&tz=` (`EventRepository.Stats`) rolls minutes up per area, with its group fields, as `event_minutes` + `trace_minutes` = `total_minutes`.

- Only events have a per-title breakdown. All-day events are excluded.
- A trace counts in the window that holds the local midnight (in `tz`) starting its day.
- Query `to=<now>` to count only elapsed time.

**Tasks** (`internal/{model,repository,handler}/task.go`) are a separate lightweight backlog entity. Keep short, clock-less time in traces, not in fake events.

### Adding a persisted entity (touches several files)

1. `internal/model/<x>.go` — struct + Create/Update request DTOs.
2. `internal/repository/<x>.go` — column-list const, `scan*`, CRUD scoped by `owner_id`.
3. `internal/handler/<x>.go` — Gin handlers.
4. `api/cmd/api/main.go` — construct repo+handler, register a route group under `protected`.
5. `internal/db/db.go` — append the idempotent `CREATE TABLE` to `Migrate()`.
6. Tests — register routes in `internal/handler/testmain_test.go`, add the table to the `truncateAll` lists in both `helpers_test.go` files.

Shared integration-test helpers (e.g. `createCategory`) live in `internal/handler/helpers_test.go`, not in a feature's `_test.go` — feature test files get deleted/renamed but are used across the whole `package handler_test` suite.
