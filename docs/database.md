# ChannelHunter — Database

PostgreSQL 16 · migrations in `backend/migrations` · applied by golang-migrate

## 1. Conventions

| Concern            | Convention |
| ------------------ | ---------- |
| Primary keys       | `UUID DEFAULT gen_random_uuid()` (core since PostgreSQL 13 — no `pgcrypto` needed) |
| Telegram IDs       | `BIGINT` (`users.telegram_id`, `channels.telegram_id`, `channel_posts.telegram_msg_id`) |
| Money              | `NUMERIC(14,2)` / `NUMERIC(12,2)`, never floating point |
| Derived rates      | `NUMERIC(8,4)` … `NUMERIC(14,4)` |
| Provider payloads  | `JSONB NOT NULL DEFAULT '{}'` |
| Timestamps         | `TIMESTAMPTZ`, `created_at`/`updated_at` maintained by `set_updated_at()` trigger |
| Deletion           | `archived_at TIMESTAMPTZ` — archive first, delete only when legally required |
| Unknown values     | `NULL`, never `0` |
| Naming             | snake_case, plural table names, `idx` suffix for indexes |

## 2. Entity map

```
users ──┬── workspace_members ──── workspaces ──┬── workspace_channels ──── channels
        │        (role)                        │        (settings)            │
        │                                       │                              ├── channel_snapshots (append-only)
        │                                       ├── subscriptions ── plans     ├── channel_posts
        │                                       ├── advertisers                │      └── post_snapshots (append-only)
        │                                       ├── campaigns ──────────────────┘
        │                                       │      └── campaign_metrics (append-only)
        │                                       ├── tracked_channels
        │                                       ├── notifications
        │                                       ├── payment_transactions
        │                                       ├── data_imports ── data_sources
        │                                       └── audit_logs
```

| Table | Purpose |
| ----- | ------- |
| `users` | Telegram users; `telegram_id` is the natural key |
| `workspaces` | Tenant boundary; `owner_id` → users |
| `workspace_members` | Role of a user in a workspace (`owner`/`admin`/`analyst`/`viewer`) |
| `plans` | Tier catalogue: limits and features as **data**, prices as `NUMERIC` |
| `subscriptions` | Current/historical subscription per workspace (one live each) |
| `channels` | **Global** channel catalogue — one row per channel for the whole product |
| `workspace_channels` | Which workspace tracks which channel, plus access level and monitoring settings |
| `channel_snapshots` | Append-only metric history |
| `channel_posts` | Known publications |
| `post_snapshots` | Append-only per-post view history |
| `data_sources` | Provider catalogue (kind, reliability, rate limit, config) |
| `data_imports` | CSV/XLSX uploads, status and row-level report |
| `advertisers` | Advertising clients of a workspace |
| `campaigns` | Ad placements with status, money and funnel numbers |
| `campaign_metrics` | Append-only campaign measurements |
| `tracked_channels` | Monitoring schedule (`next_run_at` drives the worker) |
| `notifications` | Notification outbox with de-duplication |
| `payment_transactions` | Payment history with idempotency keys |
| `audit_logs` | Immutable audit trail |

## 3. Invariants enforced by the database

These are guarantees the application can rely on; each is covered by an
integration test in `backend/tests/integration`.

1. **A channel is stored once.**
   `channels.username` is unique and `channels` has no `workspace_id`. Tenants
   link through `workspace_channels (workspace_id, channel_id)` with
   `UNIQUE (workspace_id, channel_id)`.

2. **History is never rewritten.**
   `channel_snapshots`, `post_snapshots`, `campaign_metrics` and `audit_logs`
   have a `prevent_mutation()` trigger that raises on `UPDATE`/`DELETE`. A new
   measurement is a new row; `UNIQUE (channel_id, source_code, captured_at)`
   makes repeated refreshes idempotent.

3. **Unknown is not zero.**
   Metric columns are nullable; the UI and analytics treat `NULL` as
   "unavailable". `confidence = 'unavailable'` marks a metric that the source
   cannot provide at all.

4. **Money is exact.**
   All monetary columns are `NUMERIC` with `CHECK (>= 0)`.

5. **Tenant isolation is indexable.**
   Every workspace-scoped table has `workspace_id` with an index leading on it,
   so repositories can always filter by tenant cheaply.

6. **One live subscription per workspace.**
   Partial unique index on `subscriptions (workspace_id) WHERE status IN
   ('trialing','active','past_due')`.

7. **Webhook replays cannot double-charge.**
   `payment_transactions.idempotency_key` is `UNIQUE`; the provider reference is
   unique per provider.

8. **Notifications are de-duplicated.**
   Partial unique index on `notifications (workspace_id, type, dedupe_key)`.

9. **Usernames are normalised.**
   `CHECK (username = lower(username))` prevents `@Channel`/`@channel` duplicates.

10. **`updated_at` is trustworthy.**
    Triggers maintain it; application code never sets it by hand.

## 4. Migrations

| Version | File | Contents |
| ------- | ---- | -------- |
| 000001 | `000001_init.up.sql` / `.down.sql` | Extension-free UUID defaults, helper functions, all 19 tables, indexes, constraints, triggers |
| 000002 | `000002_plans_seed.up.sql` / `.down.sql` | Seed FREE/PRO/BUSINESS plans and the four data sources |

Rules:

* A new change is a **new** file (`make migrate-create NAME=…`); applied files are
  never edited.
* `down` must be reversible for the previous `up` (verified by a test that rolls
  back and re-applies).
* No migration drops or rewrites user data. Archiving uses `archived_at`.
* Files are embedded in the binary, so `schema_migrations` is the only runtime
  state.

### Common commands

```bash
make migrate-up                 # apply everything (via the API binary)
make migrate-up-cli             # same, using golang-migrate CLI
make migrate-version            # current version
make migrate-down               # roll back one step (development only)
make migrate-create NAME=add_reports
```

## 5. Plan limits

Limits live in `plans` and are enforced by the backend, not by the UI:

| Plan | Channels | History | Members | Campaigns | Features |
| ---- | -------- | ------- | ------- | --------- | -------- |
| `free` | 3 | 7 days | 1 | 3 | basic analytics, notifications |
| `pro` | 30 | 90 days | 3 | 50 | comparison, campaigns, CSV export |
| `business` | 150 | 365 days | 5 | 500 | advanced analytics, API access, 60-min monitoring |

`plans.features` (`JSONB`) carries switches and the minimum monitoring interval;
prices are ordinary rows so they can be changed without a deploy.

## 6. Performance notes

* Hot paths are covered by composite indexes leading with the tenant key and
  `captured_at DESC`, which matches the "latest metric + history" queries.
* Append-only tables are indexed for `(entity_id, captured_at DESC)` scans.
* `tracked_channels (next_run_at) WHERE enabled` is the scheduler's claim index.
* `notifications (scheduled_at) WHERE sent_at IS NULL` is the outbox scan.
* `channels` has a GIN index on `to_tsvector('simple', title)` for search.

## 7. Backup and restore

```bash
# logical backup (schema + data)
docker compose exec -T postgres pg_dump -U channelhunter -Fc channelhunter > channelhunter.dump

# restore into a fresh database
docker compose exec -T postgres pg_restore -U channelhunter -d channelhunter --clean --if-exists < channelhunter.dump
```

Recommended production practice: daily `pg_dump` to object storage **plus**
continuous WAL archiving (PITR) for the payments/audit tables. Test a restore
monthly — an untested backup is not a backup.
