# ChannelHunter — HTTP API

Base URL: `/api/v1` · Contract: [`shared/api/openapi.yaml`](../shared/api/openapi.yaml)

## 1. Conventions

* JSON in, JSON out (`Content-Type: application/json`). The API rejects unknown
  fields and other content types.
* Request bodies are capped at 1 MiB (uploads have their own limit,
  `MAX_UPLOAD_BYTES`, default 10 MiB).
* Every response carries `X-Request-ID`; send your own to correlate logs.
* Authentication: `Authorization: Bearer <token>` issued by
  `POST /auth/telegram`. The Mini App sends `X-Telegram-Init-Data` instead — the
  backend verifies the signature itself.
* Collections use `{"data": [...], "meta": {"total": n, "limit": n, "offset": n}}`.
* Timestamps are RFC 3339 in UTC (`2026-11-08T10:15:30Z`).
* Money is returned as a decimal **string** (`"1490.00"`) to avoid float issues.
* Unknown metrics are `null`; they are never reported as `0`.

## 2. Error format

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Проверьте корректность входных данных",
    "request_id": "6f1d1c3b1f0e4b8e9a1c2d3e4f5a6b7c",
    "details": [{"field": "username", "message": "обязательное поле"}]
  }
}
```

| Code | HTTP | When |
| ---- | ---- | ---- |
| `BAD_REQUEST` | 400 | malformed request |
| `VALIDATION_ERROR` | 422 | semantic validation failure (with `details`) |
| `UNAUTHORIZED` | 401 | missing/invalid credentials |
| `FORBIDDEN` | 403 | authenticated but not allowed (role or workspace) |
| `NOT_FOUND` | 404 | unknown route or resource |
| `CONFLICT` | 409 | duplicate/state conflict |
| `PLAN_LIMIT_EXCEEDED` | 402 | plan limit reached (channels, members, history) |
| `RATE_LIMITED` | 429 | throttled (`Retry-After` header) |
| `PAYLOAD_TOO_LARGE` | 413 | body/upload too large |
| `UNSUPPORTED` | 400 | unsupported media type / feature not available for the source |
| `UPSTREAM_ERROR` | 502 | external provider failure |
| `SERVICE_UNAVAILABLE` | 503 | dependency down |
| `INTERNAL_ERROR` | 500 | unexpected failure (details are only in logs) |

## 3. Implemented in stage 1

| Method | Path | Description |
| ------ | ---- | ----------- |
| `GET` | `/health`, `/healthz`, `/livez` | Liveness. No dependency checks. |
| `GET` | `/ready`, `/readyz` | Readiness with per-dependency status; `503` when a critical dependency is down. |
| `GET` | `/api/v1/meta` | Service name, version, environment, API version. |

Example `/ready`:

```json
{
  "status": "ok",
  "service": "channelhunter",
  "process": "api",
  "version": "v0.1.0",
  "environment": "production",
  "uptime": "3h12m4s",
  "time": "2026-11-08T10:15:30Z",
  "dependencies": {
    "postgres":   {"status": "ok", "critical": true,  "latency_ms": 0.84, "checked_at": "2026-11-08T10:15:30Z"},
    "migrations": {"status": "ok", "critical": true,  "latency_ms": 0.62, "checked_at": "2026-11-08T10:15:30Z"}
  }
}
```

## 4. Planned surface (stages 2–7)

The contract is fixed in advance so the Mini App can be built against it without
backend rework. Endpoints are added stage by stage; until then they return the
standard `NOT_FOUND` envelope.

### Authentication & profile

| Method | Path | Stage | Notes |
| ------ | ---- | ----- | ----- |
| `POST` | `/auth/telegram` | 2 | Body: Telegram `initData` (or bot-side identity). Returns session token, user and workspaces. Never trusts a client-supplied Telegram ID. |
| `POST` | `/auth/refresh` | 2 | Renews a session token. |
| `GET`  | `/me` | 2 | Profile, roles, active workspace. |
| `GET`  | `/workspaces` | 2 | Workspaces the caller belongs to. |
| `POST` | `/workspaces` | 2 | Create a workspace (owner). |
| `POST` | `/workspaces/:id/members` | 7 | Invite member (plan-limited). |

### Channels & analytics

| Method | Path | Stage | Notes |
| ------ | ---- | ----- | ----- |
| `GET`  | `/channels` | 3 | Channels tracked by the active workspace. |
| `POST` | `/channels` | 3 | Add by `@username`, `t.me` link or Telegram ID. Enforces the plan channel limit. |
| `GET`  | `/channels/:id` | 3 | Card: title, category, subscribers, average views, ERR, post frequency, source, freshness, confidence. |
| `GET`  | `/channels/:id/metrics` | 4 | Metrics for a period, with `sources` and `confidence` per value. |
| `POST` | `/channels/:id/refresh` | 4 | Enqueues a refresh job (rate-limited per source). |
| `POST` | `/channels/compare` | 4 | 2–10 channels, sortable by any metric. |
| `POST` | `/imports` | 4 | CSV/XLSX upload (`multipart/form-data`). |

`GET /channels/:id/metrics` response sketch (values may be `null`):

```json
{
  "channel_id": "0e8a…",
  "period": {"from": "2026-10-09T00:00:00Z", "to": "2026-11-08T00:00:00Z", "days": 30},
  "metrics": {
    "subscribers": 48250,
    "growth": 1240,
    "average_views": 12840,
    "err": 26.61,
    "post_frequency": 4.2,
    "cpm": null
  },
  "sources": {
    "subscribers": {"code": "telegram_bot_api", "confidence": "verified", "captured_at": "2026-11-08T09:00:00Z"},
    "average_views": {"code": "provider_api", "confidence": "estimated", "captured_at": "2026-11-08T08:30:00Z"},
    "cpm": {"code": null, "confidence": "unavailable", "captured_at": null}
  }
}
```

### Campaigns

| Method | Path | Stage |
| ------ | ---- | ----- |
| `GET`  | `/campaigns` | 5 (filters: status, channel, advertiser, period) |
| `POST` | `/campaigns` | 5 |
| `GET`  | `/campaigns/:id` | 5 |
| `PATCH` | `/campaigns/:id` | 5 |
| `DELETE` | `/campaigns/:id` | 5 (archives, keeps history) |
| `POST` | `/campaigns/:id/metrics` | 5 |

### Billing, reports, notifications

| Method | Path | Stage |
| ------ | ---- | ----- |
| `GET`  | `/subscription` | 7 |
| `POST` | `/payments` | 7 |
| `POST` | `/webhooks/payments/:provider` | 7 (signature + idempotency) |
| `GET`  | `/reports/:id` | 6 |
| `POST` | `/reports` | 6 |
| `GET`  | `/notifications` | 6 |
| `POST` | `/notifications/:id/read` | 6 |

## 5. Authentication details

**Bot users** are identified by Telegram itself: the bot process receives an
update from Telegram and looks up/creates the user server-side. Nothing is taken
from user input.

**Mini App users** send `initData`. The backend:

1. parses the query string,
2. recomputes `HMAC_SHA256(data_check_string, HMAC_SHA256("WebAppData", bot_token))`,
3. compares with `hash` using a constant-time comparison,
4. rejects stale payloads (`auth_date` older than `AUTH_INITDATA_TTL`, default 24 h).

`initDataUnsafe` and any client-provided Telegram ID are ignored.

**Session tokens** are signed with `AUTH_JWT_SECRET` and expire after
`AUTH_TOKEN_TTL` (default 24 h). Each request resolves the workspace and the
caller's role in it before the handler runs.

## 6. Rate limits

* `/api/v1` — `RATE_LIMIT_REQUESTS` per `RATE_LIMIT_WINDOW` per client IP
  (default 60/min); responses include `X-RateLimit-Remaining`.
* `/auth/telegram` and `/telegram/webhook` — tighter dedicated buckets
  (10/min at the reverse proxy, plus a per-user bucket in the API).
* Throttled responses return `429` with `Retry-After`.

## 7. Versioning

`/api/v1` is frozen once Stage 8 ships; breaking changes go to `/api/v2`.
Additive changes (new fields, new endpoints, new enum members clients can
ignore) are allowed within v1 and documented here first.
