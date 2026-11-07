# Stage 1 — Infrastructure: completion report

Status: **done** · Date: 2026-11-08 · Version: `v0.1.0-stage1`

## 1. What was requested and what exists

| Stage 1 requirement | Status | Where |
| ------------------- | ------ | ----- |
| Create the project structure | ✅ | `backend/`, `frontend/`, `shared/`, `deploy/`, `docs/` |
| Configure the Go module | ✅ | `backend/go.mod` (`go 1.25`, module `github.com/akqqqw/channelhunter/backend`) |
| Configure PostgreSQL | ✅ | `deploy/docker-compose.yml` (postgres:16-alpine, named volume, healthcheck) |
| Docker Compose | ✅ | `deploy/docker-compose.yml` — postgres, migrate, api, bot, worker |
| `.env.example` | ✅ | root `.env.example`, every variable documented |
| Makefile | ✅ | `Makefile` — 30 targets (`make help`) |
| Migrations | ✅ | `backend/migrations/000001_init`, `000002_plans_seed`, embedded in the binary |
| Logging | ✅ | `internal/observability` — structured `slog`, JSON/text, secret redaction |
| HTTP server | ✅ | `internal/http/{server,routes,middleware,health}` |
| `/health` endpoint | ✅ | `/health`, `/healthz`, `/livez` + `/ready`, `/readyz` |
| Tests | ✅ | unit, HTTP, integration (real PostgreSQL), e2e (real socket) |
| README with run commands | ✅ | root `README.md` |

## 2. Verified checks

All commands were executed in this workspace (Go 1.27 toolchain, PostgreSQL 16).

| Command | Result |
| ------- | ------ |
| `gofmt -l .` | clean (no files) |
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `go test -race -count=1 -tags=integration ./...` | OK — 6 packages with tests, 0 failures |
| `go test -tags=integration ./tests/integration` | 10 tests (schema, invariants, reversibility, concurrent migrations) |
| `go test -tags=integration ./tests/e2e` | 4 tests (live server, shutdown, port conflict) |
| `ch-api --migrate-only` | applies 000001+000002, version 2, idempotent on re-run |
| `go run ./cmd/api` + `curl /health` | `{"status":"ok",...}` |
| `curl /ready` | `{"status":"ok","dependencies":{"postgres":"ok","migrations":"ok"}}` |
| `curl /api/v1/meta` | service/version/environment/API version |
| `curl /api/v1/unknown` | `404` + `{"error":{"code":"NOT_FOUND",...,"request_id":...}}` |
| DB invariant probes (psql) | append-only snapshot rejects UPDATE/DELETE, duplicate `(channel, source, captured_at)` rejected, `channels_username_normalized` rejects `UPPER`, `updated_at` trigger works |
| `make help` | lists all targets |
| compose file | parsed and validated (5 services, 1 volume, 1 network) |

Not executed in this sandbox (no Docker daemon / no external network):
`docker compose up`, `docker build`, `golangci-lint`, `sqlc`, `go mod tidy`
against a live proxy. The Go module graph was verified by building and testing
the full dependency set; CI runs lint + image builds.

## 3. Decisions worth knowing

1. **Migrations are embedded, not mounted.** `migrations.FS` is compiled into all
   three binaries, and `api --migrate-only` is the single migration entry point
   used by `make migrate-up`, containers and CI.
2. **Liveness ≠ readiness.** `/health` never touches PostgreSQL; `/ready` probes
   it with a timeout and returns 503 only for *critical* dependencies.
3. **No `pgcrypto`.** `gen_random_uuid()` is core since PostgreSQL 13, so the
   schema applies on managed PostgreSQL services that restrict extensions.
4. **Shutdown budget has a floor of 6 s.** `net/http` keeps a brand-new
   connection out of the idle set for 5 hardcoded seconds; a shorter
   `HTTP_SHUTDOWN_TIMEOUT` failed intermittently under the race detector until
   this was understood and encoded in config validation.
5. **`HTTP_TRUSTED_PROXIES` is empty by default**, so `X-Forwarded-For` is
   ignored unless a proxy is explicitly trusted — rate limits cannot be spoofed.
6. **The bot and worker are bootable but not functional yet.** They validate
   configuration, connect to PostgreSQL and expose probes, and say so at startup
   instead of pretending to serve users. Telegram wiring is stage 2.

## 4. What is intentionally not here yet

* Telegram bot handlers, menus, registration (stage 2).
* Channel CRUD and the provider adapters (stages 3–4).
* Campaigns, reports, notifications (stages 5–6).
* Billing adapters (stage 7); only the schema and plan data exist.
* Mini App UI (stage 8); `frontend/` still holds the placeholder from the
  initial commit and is untouched by this stage.

## 5. How to run it

```bash
cp .env.example .env          # fill TELEGRAM_BOT_TOKEN, AUTH_JWT_SECRET
make setup                    # deps + .env
make up                       # PostgreSQL in Docker
make migrate-up               # schema
make run-api                  # API on :8080
make health                    # curl /health and /ready
```

Tests:

```bash
make test                  # unit
make test-race             # unit + race detector
make test-integration      # needs PostgreSQL (TEST_DATABASE_URL)
make verify                # fmt-check + vet + unit tests
```
