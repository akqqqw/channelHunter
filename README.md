# ChannelHunter

**Аналитика. Реклама. Рост.**

SaaS-платформа для аналитики Telegram-каналов, поиска рекламных площадок,
управления рекламными кампаниями и контроля эффективности рекламы.

Один backend — два интерфейса: Telegram-бот (первый этап) и Telegram Mini App
(React, следующий этап). Всё общается с единым REST API `/api/v1`.

```
Go 1.25 · chi · PostgreSQL 16 · pgx v5 · golang-migrate · slog · Docker
```

**Статус: этап 1 завершён** — инфраструктура, БД, миграции, HTTP-сервер,
health-проверки, тесты, документация. Отчёт: [docs/stage-1-report.md](docs/stage-1-report.md).

---

## Содержание

- [Быстрый старт](#быстрый-старт)
- [Структура проекта](#структура-проекта)
- [Команды](#команды)
- [Конфигурация](#конфигурация)
- [HTTP API](#http-api)
- [База данных](#база-данных)
- [Тесты](#тесты)
- [Что реализовано и что дальше](#что-реализовано-и-что-дальше)
- [Документация](#документация)

---

## Быстрый старт

### Вариант A — Docker (всё сразу)

```bash
cp .env.example .env
# обязательно заполните: TELEGRAM_BOT_TOKEN, AUTH_JWT_SECRET
#   AUTH_JWT_SECRET: openssl rand -hex 32
#   TELEGRAM_BOT_TOKEN: получите у @BotFather

cd deploy
docker compose up -d --build
```

Сервисы: `postgres` (только localhost), `migrate` (однократно применяет схему),
`api` на `:8080`, `bot`, `worker`.

```bash
curl localhost:8080/health
curl localhost:8080/ready
```

### Вариант B — локально (Go + PostgreSQL)

Нужны Go 1.25+ и PostgreSQL 13+ (для `gen_random_uuid()`).

```bash
cp .env.example .env          # отредактируйте DATABASE_URL под свой инстанс
make setup                    # зависимости + .env
make up                       # PostgreSQL в Docker (или поднимите свой)
make migrate-up               # применить миграции
make run-api                  # API на http://localhost:8080
```

Проверка:

```bash
make health
# → {"status":"ok",...}
# → {"status":"ok","dependencies":{"migrations":{"status":"ok",...},"postgres":{"status":"ok",...}}}
```

### Остальные процессы

```bash
make run-bot       # Telegram-бот (этап 2, сейчас поднимается и отдаёт probes)
make run-worker    # фоновые задачи (этап 6)
make build         # три бинарника в ./bin
```

---

## Структура проекта

```
channelhunter/
├── backend/
│   ├── cmd/
│   │   ├── api/                 REST API + --migrate-only
│   │   ├── bot/                 Telegram-бот
│   │   └── worker/              фоновые задачи
│   ├── internal/
│   │   ├── app/                 общий boot-процесс (config → db → миграции → health)
│   │   ├── config/              конфигурация из ENV + валидация
│   │   ├── database/            пул pgx, транзакции, миграции
│   │   ├── domain/              доменные типы          (этап 2+)
│   │   ├── providers/           адаптеры источников   (этап 4)
│   │   ├── bot/                 handlers/keyboards/states (этап 2)
│   │   ├── services/            бизнес-логика         (этап 2+)
│   │   ├── repositories/        SQL-доступ (sqlc)
│   │   ├── workers/ scheduler/  очередь и расписание   (этап 6)
│   │   ├── payments/            PaymentProvider        (этап 7)
│   │   ├── http/
│   │   │   ├── routes/          маршруты /api/v1
│   │   │   ├── handlers/ dto/   обработчики и схемы
│   │   │   ├── middleware/      request-id, логи, recovery, CORS, rate limit
│   │   │   ├── health/          /health и /ready
│   │   │   └── server/          lifecycle, graceful shutdown
│   │   ├── platform/            apierror, httpx (без доменных знаний)
│   │   └── observability/       slog, редакция секретов
│   ├── migrations/              000001_init, 000002_plans_seed (embedded)
│   ├── tests/                   httpapi, integration, e2e, testsupport
│   ├── sqlc.yaml
│   └── Dockerfile
├── frontend/                    Mini App (React/Vite — этап 8)
├── shared/api/openapi.yaml      контракт API для bot и Mini App
├── deploy/
│   ├── docker-compose.yml
│   └── nginx/                   reverse proxy + TLS
├── docs/
│   ├── architecture.md          архитектура и решения
│   ├── database.md              схема, инварианты, бэкапы
│   ├── api.md                   API, формат ошибок, аутентификация
│   ├── product.md               продукт, тарифы, честность данных
│   ├── stage-1-report.md        отчёт по этапу 1
│   └── tech-spec.md             исходное ТЗ
├── .env.example
├── .golangci.yml
├── Makefile
└── README.md
```

---

## Команды

`make help` — полный список. Основное:

| Команда | Что делает |
| ------- | ---------- |
| `make setup` | создать `.env`, скачать зависимости |
| `make up` / `make down` | поднять / остановить PostgreSQL |
| `make stack` | собрать и запустить все сервисы в Docker |
| `make destroy` | остановить и **удалить** том с данными (с подтверждением) |
| `make migrate-up` | применить миграции (через `api --migrate-only`) |
| `make migrate-create NAME=…` | создать пару миграций up/down |
| `make migrate-version` / `migrate-down` | версия схемы / откат на шаг |
| `make run-api` / `run-bot` / `run-worker` | запуск процесса |
| `make build` / `make images` | бинарники / Docker-образы |
| `make health` | проверить `/health` и `/ready` |
| `make fmt` / `fmt-check` / `vet` / `lint` | форматирование и статический анализ |
| `make test` / `test-race` / `test-integration` / `test-all` | тесты |
| `make cover` / `cover-html` | покрытие |
| `make verify` | fmt-check + vet + тесты (как в CI) |
| `make db-shell` | `psql` к локальной БД |

---

## Конфигурация

Все параметры — переменные окружения, значения по умолчанию описаны в
[`.env.example`](.env.example) и `backend/internal/config/config.go`.
Реальные ENV всегда перекрывают `.env`.

Ключевое:

| Переменная | Назначение |
| ---------- | ---------- |
| `APP_ENV` | `development` / `staging` / `production` |
| `DATABASE_URL` | строка подключения PostgreSQL |
| `DATABASE_MIGRATE_ON_START` | применять миграции при старте (локально; в проде — сервис `migrate`) |
| `TELEGRAM_BOT_TOKEN` | токен бота; нужен и API (проверка `initData`) |
| `TELEGRAM_MODE` | `polling` (локально) / `webhook` (обязательно в проде) |
| `TELEGRAM_WEBHOOK_SECRET_TOKEN` | проверка `X-Telegram-Bot-Api-Secret-Token` |
| `AUTH_JWT_SECRET` | подпись токенов, ≥ 32 символов в проде |
| `HTTP_TRUSTED_PROXIES` | CIDR прокси, которым доверяем `X-Forwarded-For` (по умолчанию пусто) |
| `RATE_LIMIT_*` | лимиты запросов |
| `MAX_UPLOAD_BYTES` | лимит импорта CSV/XLSX |

Опасные для прода конфигурации **отклоняются при старте**: polling вместо
webhook, короткий `AUTH_JWT_SECRET`, `HTTP_SHUTDOWN_TIMEOUT < 6s`.

Секреты в логи не попадают: `slog`-хендлер вырезает ключи вида `token`,
`secret`, `password`, `initData`, `hash`.

---

## HTTP API

Префикс `/api/v1`, контракт — [`shared/api/openapi.yaml`](shared/api/openapi.yaml),
описание — [docs/api.md](docs/api.md).

Реализовано сейчас:

| Метод | Путь | Назначение |
| ----- | ---- | ---------- |
| `GET` | `/health`, `/healthz`, `/livez` | liveness (без обращения к БД) |
| `GET` | `/ready`, `/readyz` | readiness: состояние `postgres` и `migrations`, `503` при падении критичной зависимости |
| `GET` | `/api/v1/meta` | сервис, версия, окружение |

Единый формат ошибок:

```json
{"error": {"code": "VALIDATION_ERROR", "message": "…", "request_id": "…",
           "details": [{"field": "username", "message": "обязательное поле"}]}}
```

Каждый ответ содержит `X-Request-ID`.

---

## База данных

PostgreSQL 16. Миграции — `backend/migrations`, применяются golang-migrate,
встроены в бинарник. Схема и инварианты — [docs/database.md](docs/database.md).

Главные правила:

- **Каталог каналов общий**: `channels` без `workspace_id`, связь — через
  `workspace_channels`. Один канал не дублируется для каждого клиента.
- **История не перезаписывается**: `channel_snapshots`, `post_snapshots`,
  `campaign_metrics`, `audit_logs` — append-only (триггер), новая точка = новая
  строка, дубли отсекает `UNIQUE (channel_id, source_code, captured_at)`.
- **Неизвестно ≠ ноль**: метрики nullable, есть `confidence`
  (`verified` / `estimated` / `unverified` / `unavailable`).
- **Деньги — `NUMERIC`**, никогда не float.
- **Изоляция workspace** на уровне индексов и запросов репозиториев.
- **Идемпотентность платежей** — уникальный `idempotency_key`.

---

## Тесты

```bash
make test             # unit
make test-race        # + race detector
make test-integration # требует PostgreSQL: TEST_DATABASE_URL=postgres://…
make test-all
```

Покрыто на этапе 1:

| Область | Тесты |
| ------- | ----- |
| Конфигурация | значения по умолчанию, переопределения, `.env`, все правила валидации |
| Формат ошибок | коды/статусы, `details`, сокрытие внутренних причин |
| HTTP-утилиты | строгий JSON (неизвестные поля, типы, размер, Content-Type), query-параметры |
| Middleware | request-id, логи, recovery, security headers, CORS, client IP, rate limiter |
| Логирование | метаданные процесса, редакция секретов (в т.ч. групп) |
| Health | liveness без зависимостей, деградация, 503, таймауты |
| HTTP API | маршруты, конверт ошибок, rate limit вне `/health` |
| Миграции (integration) | все 19 таблиц, сиды, `updated_at`, append-only, глобальный каталог, лимиты, идемпотентность платежей, откат/накат, конкурентные миграции |
| E2E | реальный сокет: `/health`, `/ready`, конверт 404, конфликт порта, graceful shutdown |

Интеграционные тесты создают собственную схему на каждый тест и удаляют её
после, поэтому не мешают друг другу и не засоряют базу. Без переменной
`TEST_DATABASE_URL` они корректно пропускаются.

---

## Что реализовано и что дальше

**Этап 1 (готово):**

- структура монорепозитория, Go-модуль, Docker Compose (postgres, migrate, api, bot, worker);
- конфигурация с валидацией, структурированное логирование с редакцией секретов;
- подключение к PostgreSQL (pgx-пул, транзакции, классификация ошибок);
- 19 таблиц + сиды тарифов и источников; append-only история; инварианты в БД;
- HTTP-сервер: middleware-цепочка, единый формат ошибок, `/health`, `/ready`, `/api/v1/meta`;
- graceful shutdown, rate limiting, защита от спуфинга `X-Forwarded-For`;
- unit + integration + e2e тесты, CI-workflow, Dockerfile, nginx, документация.

**Дальше по плану:** этап 2 — Telegram-бот: `/start`, идемпотентная регистрация,
workspace, роли, middleware, главное меню.

---

## Документация

| Документ | Содержание |
| -------- | ---------- |
| [docs/architecture.md](docs/architecture.md) | архитектура, потоки запросов, источники данных, безопасность |
| [docs/database.md](docs/database.md) | схема, инварианты, миграции, бэкапы |
| [docs/api.md](docs/api.md) | контракт API, ошибки, аутентификация, лимиты |
| [docs/product.md](docs/product.md) | продукт, тарифы, честность данных |
| [docs/stage-1-report.md](docs/stage-1-report.md) | что проверено и какие решения приняты |
| [docs/tech-spec.md](docs/tech-spec.md) | исходное техническое задание |

## Лицензия

Proprietary. Все права защищены.
