# Исходное техническое задание 1.0

> Документ сохранён без изменений как источник требований. Актуальная
> документация проекта: [README.md](../README.md), [architecture.md](architecture.md),
> [database.md](database.md), [api.md](api.md), [product.md](product.md).

ChannelHunter — техническое задание 1.0
Концепция зафиксирована
Версия 1.0
Go + React + Telegram

Принятые решения:
- Целевая аудитория: владельцы каналов, рекламодатели и рекламные агентства.
- Получение данных: комбинированное — API, публичные источники, ручной импорт.
- Первый интерфейс: Telegram Bot.
- Второй интерфейс: Telegram Mini App.
- География первого запуска: Россия и СНГ.
- Монетизация: подписки, дополнительные услуги и будущая комиссия с рекламных сделок.
Важное архитектурное решение: мы создаём одну backend-систему с единым API. Бот и Mini App будут лишь разными интерфейсами одного продукта. Не нужно разрабатывать два независимых приложения.
1. Цели и границы первой версии
ChannelHunter 1.0 должен позволять:
1. Добавлять и отслеживать Telegram-каналы.
2. Получать и хранить доступные показатели каналов.
3. Просматривать динамику аудитории и публикаций.
4. Сравнивать несколько каналов.
5. Рассчитывать стоимость рекламных размещений.
6. Создавать рекламные кампании и отслеживать их результаты.
7. Формировать отчёты.
8. Управлять подпиской и лимитами.
9. Получать уведомления в Telegram.
Три роли пользователей
Роль	Основные задачи
Владелец канала	Аналитика своего канала, рекламные размещения
Рекламодатель	Поиск и сравнение площадок, контроль бюджета
Агентство	Управление клиентами, каналами и кампаниями

Один пользователь может иметь несколько ролей одновременно. Это важно: не нужно создавать отдельные аккаунты для рекламодателя и владельца канала.
2. Функциональные требования
2.1. Telegram Bot
Бот — основной интерфейс первой версии.

Главное меню:

ChannelHunter
Telegram Analytics Assistant
Online


Аналитика


Мои каналы


Сравнение


Реклама


Калькулятор


Отчёты


Мониторинг


Подписка



Концептуальный макет главного меню бота.
Команды:
Команда	Действие
/start	Регистрация и главное меню
/channels	Мои каналы
/add	Добавить канал
/stats	Статистика
/compare	Сравнение каналов
/campaigns	Рекламные кампании
/report	Создать отчёт
/plan	Тариф и лимиты
/help	Помощь

Кроме команд, все основные действия должны быть доступны через inline-кнопки.
2.2. Добавление каналов
Пользователь может:
- Отправить ссылку t.me/channel_name.
- Добавить публичный канал по username.
- Подключить собственный канал через выдачу боту необходимых прав.
- Загрузить CSV/Excel с показателями.
- Добавить данные вручную.
После добавления создаётся карточка канала.

Поля карточки:
- Название.
- Username.
- Ссылка.
- Описание.
- Категория.
- Количество подписчиков.
- Средний охват.
- ERR.
- Частота публикаций.
- Последнее обновление данных.
- Источник данных.
- Уровень достоверности.
Очень важно: нельзя показывать отсутствующие данные как нули. Если показатель неизвестен, он должен иметь статус unavailable.
2.3. Аналитика
Основные показатели:
Метрика	Значение
Subscribers	Количество подписчиков
Growth	Прирост аудитории
Views	Просмотры публикаций
Average Views	Средний охват
ERR	Охват относительно аудитории
Posts Frequency	Частота публикаций
CPM	Цена 1000 просмотров
CPC	Цена перехода
CPF	Цена подписчика

Формулы:
\[
ERR = \frac{AverageViews}{Subscribers}\times100\%
\]

\[
CPM = \frac{AdCost}{Views}\times1000
\]

\[
CPC = \frac{AdCost}{Clicks}
\]

\[
CPF = \frac{AdCost}{NewSubscribers}
\]

При расчётах необходимо сохранять исходные значения, период и методику. Например, ERR за последние 10 публикаций нельзя сравнивать с ERR за последние 30 публикаций без соответствующей пометки.
2.4. Сравнение каналов
Пользователь выбирает от 2 до 10 каналов.

Сравнение по:
- Размеру аудитории.
- Среднему охвату.
- ERR.
- Стоимости рекламы.
- CPM.
- Частоте публикаций.
- Динамике подписчиков.
Результат — таблица с возможностью сортировки по показателям.
2.5. Рекламные кампании
Пользователь создаёт кампанию:
- Название.
- Рекламодатель.
- Бюджет.
- Канал размещения.
- Дата публикации.
- Стоимость размещения.
- Ссылка на рекламный пост.
- Охват.
- Переходы.
- Новые подписчики.
- Доход от кампании.
Статусы:

draft → planned → running → completed → cancelled

После завершения система формирует отчёт.
2.6. Отчёты
Два типа:
- Отчёт по отдельному каналу.
- Отчёт по рекламной кампании.
Форматы первой версии:
- Сообщение в Telegram.
- CSV.
- PDF — можно добавить после стабилизации основной логики.
2.7. Мониторинг
Пользователь добавляет канал в отслеживание и выбирает период обновления.

Система собирает доступные данные, сохраняет исторические снимки и уведомляет о значимых изменениях.

Например:
ChannelHunter: за последние 24 часа количество подписчиков канала изменилось на +320. Средний охват последних публикаций снизился на 12%.

Уведомления должны содержать дату, источник и период сравнения.
3. Архитектура получения данных
Это самый технически важный раздел ChannelHunter. Если неправильно спроектировать его сейчас, позднее придётся переписывать большую часть аналитики.
3.1. Три источника данных

Источник A — внешние API
Автоматизация
Данные аналитических провайдеров, если доступ разрешён их условиями.




Источник B — Telegram
Собственные каналы
Bot API для доступных данных, а также авторизованное получение расширенной статистики там, где Telegram это разрешает.




Источник C — импорт
Ручные данные
CSV, XLSX и ручной ввод владельцем канала или рекламодателем.



Каждый источник реализуется отдельным адаптером:
type ChannelDataProvider interface {
    GetChannel(ctx context.Context, ref ChannelRef) (*ChannelData, error)
    GetMetrics(ctx context.Context, ref ChannelRef) (*ChannelMetrics, error)
    GetPosts(ctx context.Context, ref ChannelRef, limit int) ([]PostData, error)
}


В результате данные из разных источников приводятся к единому формату.
3.2. Важное ограничение Telegram
Обычный Bot API не предоставляет универсального доступа к полной аналитике любого чужого канала.

Telegram имеет отдельный метод stats.getBroadcastStats, но он предназначен для пользователей, являющихся администраторами каналов с доступной статистикой. 
Telegram Core+1


Поэтому:
- Публичные каналы анализируем через разрешённые внешние источники.
- Собственные каналы подключаем с разрешениями.
- Расширенную статистику получаем через отдельный пользовательский авторизованный коннектор, если это оправдано.
- CSV/XLSX остаётся резервным способом.
- У каждого значения сохраняем источник и время получения.
Не стоит использовать неофициальный парсинг как основу бизнеса: блокировки, изменения форматов и проблемы с достоверностью могут сделать сервис нестабильным.
3.3. Модель достоверности
Каждое полученное значение должно сопровождаться метаданными:
{
  "metric": "average_views",
  "value": 12840,
  "source": "external_provider",
  "collected_at": "2026-11-07T20:00:00Z",
  "period_days": 30,
  "confidence": "verified",
  "status": "available"
}


Предусмотреть три состояния:
- verified — подтверждённый источник.
- estimated — расчётное значение.
- unverified — пользовательские данные без независимой проверки.
Отсутствующее значение — null, а не 0.

4. Схема базы данных PostgreSQL
Я предлагаю использовать PostgreSQL с UUID в качестве первичных ключей, BIGINT для Telegram ID, NUMERIC для денежных значений и JSONB для дополнительных метаданных источников.

Основные сущности:
ER-модель ChannelHunter
users
Пользователи Telegram

workspaces
Рабочие пространства и организации

workspace_members
Роли и доступы
subscriptions
Тарифы и подписки


channels
Единый каталог каналов

channel_snapshots
История метрик
channel_posts
Публикации
campaigns
Рекламные кампании
advertisers
Рекламодатели и клиенты


campaign_metrics
Результаты рекламы
data_imports
Импорт данных


4.1. Основные таблицы
Таблица	Назначение
users	Пользователи
workspaces	Организации и личные кабинеты
workspace_members	Связь пользователей с организациями
subscriptions	Тарифы и сроки подписки
channels	Каталог Telegram-каналов
workspace_channels	Отслеживаемые каналы
channel_snapshots	Исторические показатели
channel_posts	Публикации
post_snapshots	История просмотров публикаций
data_sources	Источники информации
data_imports	Импорт CSV/XLSX
advertisers	Рекламодатели
campaigns	Рекламные кампании
campaign_metrics	Результаты кампаний
tracked_channels	Настройки мониторинга
notifications	Уведомления
payment_transactions	Платежи
audit_logs	История важных действий

Отдельно важно разделить глобальный каталог каналов и отслеживание каналов конкретным рабочим пространством. Иначе несколько клиентов будут создавать дубли одной и той же записи.
4.2. SQL-схема первой версии
Ниже основа миграции 001_init.sql. Это рабочая отправная схема, которую можно расширять по мере разработки.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Пользователи
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_id BIGINT UNIQUE NOT NULL,
    username TEXT,
    first_name TEXT,
    last_name TEXT,
    language_code VARCHAR(10) DEFAULT 'ru',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Рабочие пространства
CREATE TABLE workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(150) NOT NULL,
    owner_id UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Участники и роли
CREATE TABLE workspace_members (
    workspace_id UUID REFERENCES workspaces(id),
    user_id UUID REFERENCES users(id),
    role VARCHAR(30) NOT NULL
        CHECK (role IN ('owner', 'admin', 'analyst', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, user_id)
);

-- Тарифы
CREATE TABLE plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(40) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    price NUMERIC(12,2) NOT NULL DEFAULT 0,
    currency VARCHAR(3) NOT NULL DEFAULT 'RUB',
    limits JSONB NOT NULL DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

-- Подписки
CREATE TABLE subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    plan_id UUID NOT NULL REFERENCES plans(id),
    status VARCHAR(30) NOT NULL,
    provider VARCHAR(50),
    external_id TEXT,
    started_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Каталог каналов
CREATE TABLE channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_chat_id BIGINT UNIQUE,
    username VARCHAR(100),
    title VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(100),
    is_public BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX channels_username_idx
ON channels (LOWER(username))
WHERE username IS NOT NULL;

-- Каналы рабочего пространства
CREATE TABLE workspace_channels (
    workspace_id UUID REFERENCES workspaces(id),
    channel_id UUID REFERENCES channels(id),
    label VARCHAR(100),
    is_favorite BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, channel_id)
);

-- Источники данных
CREATE TABLE data_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    source_type VARCHAR(30) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

-- Исторические показатели
CREATE TABLE channel_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES channels(id),
    source_id UUID REFERENCES data_sources(id),
    collected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period_days INTEGER,
    subscribers BIGINT,
    average_views BIGINT,
    average_reactions NUMERIC(14,2),
    average_forwards NUMERIC(14,2),
    err NUMERIC(10,4),
    confidence VARCHAR(20) NOT NULL,
    raw_data JSONB DEFAULT '{}'
);

CREATE INDEX channel_snapshots_history_idx
ON channel_snapshots(channel_id, collected_at DESC);

-- Публикации
CREATE TABLE channel_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES channels(id),
    telegram_message_id BIGINT,
    published_at TIMESTAMPTZ,
    text_preview TEXT,
    post_url TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(channel_id, telegram_message_id)
);

-- История показателей публикаций
CREATE TABLE post_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES channel_posts(id),
    source_id UUID REFERENCES data_sources(id),
    collected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    views BIGINT,
    forwards BIGINT,
    reactions BIGINT
);

-- Рекламодатели
CREATE TABLE advertisers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    name VARCHAR(200) NOT NULL,
    contact TEXT,
    notes TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Рекламные кампании
CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    advertiser_id UUID REFERENCES advertisers(id),
    channel_id UUID REFERENCES channels(id),
    name VARCHAR(200) NOT NULL,
    status VARCHAR(30) NOT NULL DEFAULT 'draft',
    budget NUMERIC(14,2),
    currency VARCHAR(3) DEFAULT 'RUB',
    placement_cost NUMERIC(14,2),
    planned_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    post_url TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Результаты кампаний
CREATE TABLE campaign_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    views BIGINT,
    clicks BIGINT,
    new_subscribers BIGINT,
    revenue NUMERIC(14,2),
    source VARCHAR(50),
    raw_data JSONB DEFAULT '{}'
);

-- Мониторинг
CREATE TABLE tracked_channels (
    workspace_id UUID REFERENCES workspaces(id),
    channel_id UUID REFERENCES channels(id),
    interval_minutes INTEGER NOT NULL DEFAULT 1440,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_checked_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, channel_id)
);

-- Импорт файлов
CREATE TABLE data_imports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    file_name TEXT NOT NULL,
    file_type VARCHAR(20),
    status VARCHAR(30) NOT NULL DEFAULT 'pending',
    records_total INTEGER DEFAULT 0,
    records_processed INTEGER DEFAULT 0,
    errors JSONB DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Уведомления
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    user_id UUID REFERENCES users(id),
    type VARCHAR(50) NOT NULL,
    title VARCHAR(200) NOT NULL,
    message TEXT NOT NULL,
    payload JSONB DEFAULT '{}',
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Платежи
CREATE TABLE payment_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    provider VARCHAR(50) NOT NULL,
    external_id TEXT,
    amount NUMERIC(14,2) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    status VARCHAR(30) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(provider, external_id)
);



Что ещё обязательно добавить перед production:
- audit_logs для истории действий.
- Проверку принадлежности всех сущностей workspace_id.
- Идемпотентную обработку webhook платежей.
- Ограничения и индексы для больших исторических таблиц.
- Политику удаления и хранения персональных данных.
- Отдельные миграции, а не изменение уже применённых миграций.
Я бы не стал сразу вводить сложную микросервисную архитектуру или TimescaleDB. На первых тысячах каналов обычного PostgreSQL будет достаточно при нормальной индексации и разумной частоте сбора.
5. Структура Go + React проекта
Предлагаю монорепозиторий. Так проще поддерживать backend, Telegram-бота, будущий Mini App и общие контракты API.
channelhunter/
│
├── backend/
│   ├── cmd/
│   │   ├── api/
│   │   │   └── main.go
│   │   ├── bot/
│   │   │   └── main.go
│   │   └── worker/
│   │       └── main.go
│   │
│   ├── internal/
│   │   ├── config/
│   │   ├── database/
│   │   ├── domain/
│   │   │   ├── user/
│   │   │   ├── workspace/
│   │   │   ├── channel/
│   │   │   ├── analytics/
│   │   │   ├── campaign/
│   │   │   ├── subscription/
│   │   │   └── notification/
│   │   │
│   │   ├── providers/
│   │   │   ├── telegram/
│   │   │   ├── external/
│   │   │   └── imports/
│   │   │
│   │   ├── bot/
│   │   │   ├── handlers/
│   │   │   ├── keyboards/
│   │   │   ├── middleware/
│   │   │   └── states/
│   │   │
│   │   ├── services/
│   │   ├── repositories/
│   │   ├── workers/
│   │   ├── scheduler/
│   │   ├── payments/
│   │   ├── middleware/
│   │   └── http/
│   │       ├── handlers/
│   │       ├── routes/
│   │       └── dto/
│   │
│   ├── migrations/
│   ├── tests/
│   ├── go.mod
│   └── Dockerfile
│
├── frontend/
│   ├── src/
│   │   ├── app/
│   │   ├── pages/
│   │   ├── components/
│   │   ├── features/
│   │   │   ├── channels/
│   │   │   ├── analytics/
│   │   │   ├── campaigns/
│   │   │   ├── compare/
│   │   │   ├── ai-analyst/
│   │   │   └── billing/
│   │   ├── hooks/
│   │   ├── services/
│   │   ├── stores/
│   │   ├── types/
│   │   └── styles/
│   ├── public/
│   ├── package.json
│   └── vite.config.ts
│
├── shared/
│   └── api/
│       └── openapi.yaml
│
├── deploy/
│   ├── docker-compose.yml
│   └── nginx/
│
├── docs/
│   ├── architecture.md
│   ├── database.md
│   ├── api.md
│   └── product.md
│
├── .env.example
├── Makefile
└── README.md


Почему три Go-приложения?
Приложение	Ответственность
api	HTTP API, авторизация, бизнес-логика
bot	Telegram updates, команды, уведомления
worker	Импорт, обновление статистики, отчёты

Это три процесса, но один Go-монолит с общей бизнес-логикой и базой. В будущем можно масштабировать их независимо.
Основные библиотеки
Задача	Инструмент
HTTP	chi
PostgreSQL	pgx
SQL	sqlc
Telegram	go-telegram/bot
Конфигурация	envconfig или koanf
Логи	slog
Миграции	golang-migrate
Тесты	testify
Документация API	OpenAPI 3.1

Frontend:
- React 19 + TypeScript.
- Vite.
- Tailwind CSS.
- shadcn/ui.
- TanStack Query.
- Zustand.
- Recharts.
- React Router.
В первой версии React-приложение можно не запускать для пользователей: сначала создаём backend API и бота, но проектируем API сразу с учётом будущего Mini App.
6. Backend API
Все интерфейсы будут обращаться к единому REST API.

Базовый адрес:
/api/v1


6.1. Авторизация
Для первой версии:
- Пользователь нажимает /start.
- Backend получает Telegram ID.
- Создаёт или находит пользователя.
- Создаёт личное рабочее пространство.
- Возвращает информацию о пользователе.
Для Mini App позднее добавляем Telegram initData validation на сервере. Нельзя доверять initDataUnsafe или просто принимать Telegram ID из запроса: Telegram прямо требует проверять подпись исходных данных на backend. 
Telegram Core+1

6.2. Список API endpoints
Метод	Endpoint	Назначение
POST	/auth/telegram	Авторизация
GET	/me	Профиль
GET	/workspaces	Рабочие пространства
GET	/channels	Список каналов
POST	/channels	Добавить канал
GET	/channels/:id	Карточка канала
GET	/channels/:id/metrics	Метрики
POST	/channels/:id/refresh	Обновить данные
POST	/channels/compare	Сравнение
GET	/campaigns	Список кампаний
POST	/campaigns	Создать кампанию
GET	/campaigns/:id	Кампания
POST	/campaigns/:id/metrics	Добавить результаты
GET	/reports/:id	Получить отчёт
POST	/imports	Загрузить файл
GET	/subscription	Текущий тариф
POST	/payments	Создать платёж

6.3. Пример ответа API
GET /api/v1/channels/:id/metrics
{
  "channel_id": "uuid",
  "period": "30d",
  "metrics": {
    "subscribers": 48250,
    "average_views": 12840,
    "average_reactions": 320,
    "err": 26.6
  },
  "sources": {
    "subscribers": "external_provider",
    "average_views": "external_provider"
  },
  "collected_at": "2026-11-07T20:00:00Z"
}


Это пример контракта с демонстрационными значениями.
6.4. Ошибки API
Используем единый формат:
{
  "error": {
    "code": "CHANNEL_NOT_FOUND",
    "message": "Канал не найден",
    "request_id": "uuid"
  }
}


Предусмотреть коды:
- UNAUTHORIZED
- FORBIDDEN
- PLAN_LIMIT_REACHED
- CHANNEL_NOT_FOUND
- DATA_SOURCE_UNAVAILABLE
- INVALID_IMPORT_FILE
- PAYMENT_FAILED
- VALIDATION_ERROR

7. Фоновые процессы
Сбор аналитики не должен выполняться непосредственно при нажатии пользователем кнопки.

Для этого создаём worker.
Поток обновления статистики
Scheduler
Выбирает каналы, которые пора обновить

Job Queue
Создаёт задачи с учётом приоритетов и лимитов

Provider Adapter
Получает информацию из источника

Data Normalizer
Приводит показатели к общему формату

PostgreSQL
Сохраняет исторические показатели

Notification Service
Отправляет уведомления пользователям

Для MVP достаточно PostgreSQL-backed очереди задач или простого планировщика в Go. Redis и отдельную очередь можно подключить позже.
Периодичность
Задача	Частота
Обновление собственных каналов	По доступности источника
Публичные показатели	Раз в 24 часа
Проверка кампаний	Раз в 6 часов
Уведомления	По событию
Недельные отчёты	Раз в неделю
Проверка подписок	По webhook и периодически

Частота обновления должна зависеть от тарифа, возможностей провайдера и его ограничений. Нельзя обещать обновление каждые 5 минут, если источник не позволяет его выполнять.

8. Монетизация и биллинг
Я предлагаю реализовать тарифы через конфигурацию в базе, а не зашивать ограничения в код.
Предварительная тарифная сетка
Возможность	Free	Pro	Business
Каналы	3	30	150
История	7 дней	90 дней	365 дней
Сравнения	3/мес.	Безлимит*	Безлимит*
Кампании	3	30	200
Экспорт CSV	Нет	Да	Да
AI Analyst	Нет	Лимит	Расширенный лимит
Команда	Нет	Нет	5 участников
Уведомления	Базовые	Расширенные	Расширенные

С учётом разумных ограничений на частоту запросов.

Стартовые цены:
- Free — 0 ₽.
- Pro — 399 ₽/месяц.
- Business — 990 ₽/месяц.
Дополнительные продажи
1. Пакет дополнительных сравнений.
2. Расширенные отчёты.
3. Дополнительные места для команды.
4. Экспорт рекламной аналитики.
5. В перспективе — комиссия за сделки в Ad Marketplace.
Платёжная архитектура
Создаём общий интерфейс:
type PaymentProvider interface {
    CreatePayment(ctx context.Context, req PaymentRequest) (*Payment, error)
    VerifyWebhook(ctx context.Context, payload []byte) (*PaymentEvent, error)
    Refund(ctx context.Context, paymentID string) error
}


Под него подключаются необходимые платёжные системы.

Важная оговорка: способы оплаты и юридические условия нужно выбирать после определения страны регистрации бизнеса и страны клиентов. Для цифровых функций, продаваемых непосредственно внутри Telegram, нужно отдельно соблюдать правила Telegram Stars. Официальная документация Telegram Stars.

9. Безопасность и надёжность
Обязательные требования:
- Все Telegram webhook-запросы проверяются.
- Telegram Bot Token хранится только в переменных окружения или секрет-хранилище.
- Доступ к данным ограничивается рабочим пространством.
- Все запросы к базе используют параметризованные запросы.
- Файлы импорта проверяются по размеру, формату и содержимому.
- Запрещено исполнять содержимое загруженных файлов.
- Webhook платежей проверяются по подписи и идентификатору операции.
- Повторные webhook не должны повторно начислять подписку.
- Действия с рекламными кампаниями записываются в аудит.
- Настраиваются резервные копии PostgreSQL.
- В логах не сохраняются токены и приватные данные.
Особое внимание — мультиарендности: пользователь не должен иметь возможность получить кампанию другого рабочего пространства, просто подставив чужой UUID.
10. Тестирование и критерии готовности
Версия 1.0 считается готовой, если:
Проверка	Критерий
Регистрация	Новый пользователь создаётся автоматически
Авторизация	Нельзя подделать Telegram ID
Каналы	Добавление и удаление работают
Источники	Ошибки провайдера не ломают систему
Метрики	Формулы проверяются тестами
История	Снимки сохраняются
Сравнение	Работает для 2–10 каналов
Кампании	Создание, изменение и завершение
Импорт	Некорректные файлы отклоняются
Подписки	Лимиты применяются на backend
Платежи	Повторный webhook безопасен
Безопасность	Изоляция рабочих пространств проверена

Дополнительно:
- Unit-тесты расчётов.
- Integration-тесты PostgreSQL.
- Тесты Telegram handlers.
- Тесты API.
- Тесты импорта CSV/XLSX.
- Проверка конкурентных запросов.
- Проверка восстановления после сбоя worker.
Целевые показатели первой версии: 99% успешных ответов API при штатной работе инфраструктуры и время ответа API p95 до 500 мс для обычных запросов, без учёта внешних провайдеров и тяжёлого импорта.