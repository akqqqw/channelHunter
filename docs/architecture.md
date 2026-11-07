# ChannelHunter — Architecture

Status: **Stage 1 (infrastructure) complete** · Last updated: 2026-11-08

## 1. Shape of the system

ChannelHunter is a **modular monolith** written in Go, deployed as three
processes from one code base:

| Process  | Command            | Responsibility                                                        |
| -------- | ------------------ | --------------------------------------------------------------------- |
| `api`    | `cmd/api`          | REST API under `/api/v1`, Telegram WebApp auth, webhook receiver, health |
| `bot`    | `cmd/bot`          | Telegram bot (long polling locally, webhook in production)            |
| `worker` | `cmd/worker`       | Scheduler, job queue, provider refresh, notifications                 |

All three share the same packages (`internal/services`, `internal/repositories`,
`internal/config`, `internal/database`) and the same PostgreSQL database. There
is no service-to-service RPC, no message broker and no Redis — the queue is
PostgreSQL-backed, which is enough for the expected order of magnitude and keeps
operations simple.

```
        Telegram                       Browser / Mini App
            │                                   │
            ▼                                   ▼
     ┌─────────────┐   HTTPS    ┌──────────────────────┐
     │  bot        │◄──────────►│  api  /api/v1        │
     │  (polling / │            │  /health /ready      │
     │   webhook)  │            └──────────┬───────────┘
     └──────┬──────┘                       │
            │        shared Go packages    │
            ▼                              ▼
     ┌──────────────────────────────────────────────┐
     │ services → repositories (sqlc, pgx)          │
     └───────────────────┬──────────────────────────┘
                         ▼
              ┌──────────────────────┐        ┌───────────────┐
              │ PostgreSQL 16        │◄───────│ worker        │
              │ (single source of    │        │ scheduler /   │
              │  truth, job queue)   │        │ jobs          │
              └──────────────────────┘        └───────┬───────┘
                                                      │
                                              provider adapters
                                        (Telegram Bot API, external API,
                                         CSV/XLSX import, manual entry)
```

### Why a monolith

* One deployable unit and one migration path keeps the first release small.
* The bot and the API need exactly the same business rules (plan limits,
  workspace isolation, analytics). Duplicating them across services would be the
  classic source of divergence.
* The packages are already split by domain, so extracting a service later (for
  example the analytics pipeline) is a mechanical change, not a rewrite.

## 2. Repository layout

```
backend/
  cmd/{api,bot,worker}/        three executables, thin main.go only
  internal/
    app/                       boot sequence shared by all processes
    config/                    env-driven configuration + validation
    database/                  pgx pool, transactions, migrations
    domain/                    domain types (stage 2+)
    providers/                 data-source adapters (stage 4)
    bot/                       Telegram handlers, keyboards, state (stage 2)
    services/                  business rules (stage 2+)
    repositories/              sqlc-generated + hand-written queries
    workers/ scheduler/        background processing (stage 6)
    payments/                  PaymentProvider interface (stage 7)
    http/
      routes/ handlers/ dto/   HTTP surface
      middleware/              request ids, logging, recovery, CORS, limits
      health/                  /health, /ready
      server/                  http.Server lifecycle
    platform/                  apierror, httpx — no domain knowledge
    observability/             slog logger, redaction, context helpers
  migrations/                  golang-migrate SQL files (embedded)
  tests/                       httpapi, integration, e2e, testsupport
frontend/                      Mini App (stage 8+, React/Vite skeleton)
shared/api/openapi.yaml        API contract shared by backend and Mini App
deploy/                        docker-compose, nginx
docs/                          this documentation set
```

Dependency direction is strictly inward:
`cmd → http|bot|workers → services → repositories → database`, and
`platform`/`observability` are leaves that everything may import.

## 3. Configuration

`internal/config` reads environment variables, applies documented defaults and
**fails fast** on invalid combinations. Notable invariants:

* `AUTH_JWT_SECRET` is required for the API and must be ≥ 32 chars in production.
* `TELEGRAM_BOT_TOKEN` is required for `bot` and `api` (Mini App `initData` is
  signed with it).
* `TELEGRAM_MODE=polling` is rejected in production; webhook mode requires both
  a secret token and a public URL.
* `HTTP_SHUTDOWN_TIMEOUT` must be ≥ 6 s, because `net/http` counts a freshly
  accepted connection as busy for 5 hardcoded seconds — a shorter budget makes
  graceful shutdown fail under slow clients.

Secrets never live in code; `.env` is git-ignored and `.env.example` documents
every variable.

## 4. Request lifecycle (API)

```
client → Recover → RequestID → AccessLog → SecurityHeaders → [CORS] → RateLimit (/api/v1) → handler
                                                                              ↓
                                                        service → repository → PostgreSQL
```

* **Recover** turns panics into `INTERNAL_ERROR` envelopes and never leaks the
  panic value.
* **RequestID** reuses a sane inbound `X-Request-ID` (≤128 chars) or generates
  a 128-bit one; it is echoed in headers *and* in every error body.
* **AccessLog** writes one structured record per request (method, path, status,
  bytes, duration, client IP).
* **Client IP** is derived from `X-Forwarded-For` only if the direct peer is in
  `HTTP_TRUSTED_PROXIES`; otherwise the header is ignored, so rate limiting
  cannot be bypassed by spoofing.
* **RateLimit** is a token bucket per client; sensitive routes get their own
  limiter in later stages.

### Error contract

Every failure — validation, auth, 404, panic, database error — uses one shape:

```json
{"error": {"code": "VALIDATION_ERROR", "message": "…", "request_id": "…",
           "details": [{"field": "username", "message": "обязательное поле"}]}}
```

`apierror.Error` carries the HTTP status, a stable code and an internal cause.
The cause is logged, never serialised. Unknown errors always surface as
`INTERNAL_ERROR` with a generic message.

## 5. Health model

| Endpoint            | Purpose   | Touches dependencies |
| ------------------- | --------- | -------------------- |
| `/health`, `/healthz`, `/livez` | liveness  | **no** — a slow database must not trigger restarts |
| `/ready`, `/readyz`             | readiness | yes, concurrently, with per-probe timeouts |

Readiness reports each dependency (`postgres`, `migrations`) with status, latency
and error. A failing **critical** probe returns `503`; a failing optional probe
degrades the report but keeps the instance in rotation.

## 6. Database access

* **pgx v5** pool with bounded connections, `statement_timeout` and
  `idle_in_transaction_session_timeout` set at the server level as a second line
  of defence.
* `database.DB.InTx(ctx, fn)` provides transactions; nested calls join the outer
  transaction via context, so a service can call another service safely.
* Errors are classified with `IsUniqueViolation` / `IsForeignKeyViolation` /
  `IsCheckViolation` so handlers can map them to `409`/`422` without string
  matching.
* Queries will be generated by **sqlc** (`backend/sqlc.yaml`); generated code is
  committed so builds stay hermetic. Money stays `NUMERIC`, never `float64`.

## 7. Migrations

* Files live in `backend/migrations`, named `NNNNNN_name.{up,down}.sql`, and are
  **embedded** into the binaries (`migrations.FS`), so containers and `go test`
  need no working-directory tricks.
* Applied by golang-migrate, which takes a PostgreSQL advisory lock — several
  processes booting at once are safe (covered by a test).
* In production the one-shot `migrate` service runs `api --migrate-only`; the app
  processes only *verify* the schema version (`DATABASE_MIGRATE_ON_START=false`).
* Every change is a new file; applied migrations are never edited, and no
  migration deletes user data (archive columns instead).

See [database.md](database.md) for the schema and its invariants.

## 8. Data sources — honest capabilities

This is a product constraint, not an implementation detail.

| Source                | What it can supply                                  | Confidence  |
| --------------------- | --------------------------------------------------- | ----------- |
| Telegram Bot API      | Subscriber count and post views **only for channels where our bot is an administrator** | `verified` |
| External provider API | Subscribers, views, reach, ERR for public channels (per contract and licence of that provider) | `estimated` |
| CSV/XLSX import       | Whatever the user uploads (e.g. a channel owner's export) | `unverified` |
| Manual entry          | Numbers typed by the user                           | `unverified` |

The Telegram Bot API has **no** endpoint that returns statistics for arbitrary
foreign channels. ChannelHunter therefore never fabricates those numbers: the
value is `null` with status `unavailable`, and the UI says so explicitly.

Every measurement is stored with `source_code`, `captured_at`, `period_start`,
`period_end`, `confidence`, plus the raw provider payload in `JSONB`, so a value
can always be traced back to its origin. Adapters implement one interface:

```go
type ChannelDataProvider interface {
    GetChannel(ctx context.Context, ref ChannelRef) (*ChannelData, error)
    GetMetrics(ctx context.Context, ref ChannelRef) (*ChannelMetrics, error)
    GetPosts(ctx context.Context, ref ChannelRef, limit int) ([]PostData, error)
}
```

Adding a provider means adding an adapter and a `data_sources` row — no change
to business logic.

## 9. Analytics formulas

| Metric        | Formula                              | Zero/missing handling |
| ------------- | ------------------------------------ | --------------------- |
| Average Views | `Σ views / post count`               | `null` when there are no posts |
| ERR           | `average_views / subscribers * 100`   | `null` when subscribers = 0 or either value is unknown |
| CPM           | `ad_cost / views * 1000`              | `null` when views = 0 |
| CPC           | `ad_cost / clicks`                    | `null` when clicks = 0 |
| CPF           | `ad_cost / new_subscribers`           | `null` when new subscribers = 0 |

Periods are compared only when their length and boundaries match; incompatible
periods are never silently compared.

## 10. Background processing (planned, stage 6)

```
Scheduler → Job Queue (PostgreSQL) → Provider Adapter → Normalizer → PostgreSQL → Notifications
```

* Claims use `SELECT … FOR UPDATE SKIP LOCKED`, so several workers can run
  without duplicate processing.
* Retries with exponential backoff and jitter; `consecutive_failures` on
  `tracked_channels` drives alerting and back-off.
* Refreshes respect per-source `rate_limit_minutes` and the plan's monitoring
  interval (`plans.features.monitoring_interval_minutes`).
* Restart safety comes from the queue being durable state, not process memory.

## 11. Security posture

* Workspace isolation is enforced in the repository layer (every scoped query
  filters by `workspace_id`) and re-checked by the permission middleware.
* `POST /auth/telegram` and the Mini App accept **only** signed `initData`
  (HMAC-SHA256 with the bot token, `auth_date` freshness check); a Telegram ID
  sent by a client is never trusted.
* Telegram webhook requests are verified against
  `X-Telegram-Bot-Api-Secret-Token` before any handler runs.
* Payment webhooks are idempotent by `idempotency_key` (unique index).
* Uploads are size-limited (`MAX_UPLOAD_BYTES`, `client_max_body_size`), type-
  checked and parsed defensively.
* SQL is always parameterised; no string concatenation builds queries.
* Sensitive log keys (`token`, `secret`, `password`, `initData`, `hash`, …) are
  redacted by a `slog` handler attribute filter.
* Security headers are added to every response; CORS is an explicit allowlist.
* `audit_logs` records security- and billing-relevant actions (append-only).

## 12. Observability

* Structured `slog` logs, JSON in production, with `service`, `process`, `env`
  and `request_id` on every record.
* One access-log line per request; failures ≥ 500 log the internal cause.
* `/ready` doubles as a dependency dashboard for operators.
* Build version is injected via `-ldflags` and reported by `/health` and
  `/api/v1/meta`.

## 13. Stage plan

| Stage | Scope                                            | State |
| ----- | ------------------------------------------------ | ----- |
| 1     | Infrastructure: layout, config, DB, migrations, HTTP, health, tests, docs | **done** |
| 2     | Telegram bot, users, workspaces, roles           | next |
| 3     | Channels: add/найти/normalise, card, sources      | planned |
| 4     | Analytics: provider interface, import, snapshots, metrics | planned |
| 5     | Campaigns: advertisers, statuses, metrics, history | planned |
| 6     | Monitoring: worker, scheduler, queue, notifications | planned |
| 7     | Subscriptions: plans, limits, payment adapter     | planned |
| 8     | Launch hardening + Mini App                       | planned |
