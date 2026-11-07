-- ChannelHunter initial schema.
--
-- Design rules enforced here:
--   * UUID primary keys for all business entities, BIGINT for Telegram IDs.
--   * NUMERIC for money and for rates that must not suffer float rounding.
--   * JSONB for provider-specific payloads ("raw" upstream data).
--   * every table carries created_at/updated_at (trigger-maintained).
--   * the Telegram channel catalogue is global; workspaces link to it through
--     workspace_channels, so a channel is stored exactly once.
--   * snapshot tables are append-only: statistics history is never rewritten.
--   * tenancy: every workspace-scoped table carries workspace_id and is
--     indexed on it; repositories always filter by workspace_id.

-- gen_random_uuid() is part of PostgreSQL core since version 13, so no
-- extension (pgcrypto/uuid-ossp) is required. If you target an older server,
-- enable pgcrypto before applying this migration.

-- ---------------------------------------------------------------------------
-- Shared helpers
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION prevent_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'table % is append-only: rows cannot be updated or deleted', TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- Identity
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_id   BIGINT UNIQUE NOT NULL,
    username      TEXT,
    first_name    TEXT,
    last_name     TEXT,
    language_code VARCHAR(10) NOT NULL DEFAULT 'ru',
    is_blocked    BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_telegram_id_positive CHECK (telegram_id > 0)
);

COMMENT ON TABLE users IS 'Telegram users of ChannelHunter; telegram_id is the natural key.';
COMMENT ON COLUMN users.is_blocked IS 'Blocks access without deleting data (soft ban).';

CREATE INDEX users_username_idx ON users (lower(username)) WHERE username IS NOT NULL;

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(150) NOT NULL,
    slug        VARCHAR(80),
    owner_id    UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- archive instead of delete: history, campaigns and audit stay readable
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT workspaces_name_not_blank CHECK (btrim(name) <> '')
);

COMMENT ON TABLE workspaces IS 'Tenant boundary. Every business row is scoped by workspace_id.';

CREATE UNIQUE INDEX workspaces_slug_unique_idx ON workspaces (lower(slug)) WHERE slug IS NOT NULL;
CREATE INDEX workspaces_owner_idx ON workspaces (owner_id);
CREATE INDEX workspaces_active_idx ON workspaces (created_at DESC) WHERE archived_at IS NULL;

CREATE TRIGGER workspaces_set_updated_at BEFORE UPDATE ON workspaces
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE workspace_members (
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         VARCHAR(20) NOT NULL
        CHECK (role IN ('owner', 'admin', 'analyst', 'viewer')),
    invited_by   UUID REFERENCES users (id) ON DELETE SET NULL,
    joined_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, user_id)
);

COMMENT ON TABLE workspace_members IS 'Role of a user inside a workspace: owner, admin, analyst, viewer.';

CREATE INDEX workspace_members_user_idx ON workspace_members (user_id);
CREATE INDEX workspace_members_role_idx ON workspace_members (workspace_id, role);

CREATE TRIGGER workspace_members_set_updated_at BEFORE UPDATE ON workspace_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Billing: plans and subscriptions
-- ---------------------------------------------------------------------------

CREATE TABLE plans (
    code            VARCHAR(32) PRIMARY KEY,
    name            VARCHAR(80) NOT NULL,
    description     TEXT,
    -- limits are data, not code: pricing can change without a migration
    max_channels    INTEGER NOT NULL CHECK (max_channels >= 0),
    history_days    INTEGER NOT NULL CHECK (history_days >= 0),
    max_members     INTEGER NOT NULL CHECK (max_members >= 1),
    max_campaigns   INTEGER NOT NULL CHECK (max_campaigns >= 0),
    features        JSONB NOT NULL DEFAULT '{}'::jsonb,
    price_monthly   NUMERIC(12, 2) NOT NULL DEFAULT 0 CHECK (price_monthly >= 0),
    currency        CHAR(3) NOT NULL DEFAULT 'RUB',
    trial_days      INTEGER NOT NULL DEFAULT 0 CHECK (trial_days >= 0),
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE plans IS 'Subscription tiers. Limits are enforced by the backend service layer.';
COMMENT ON COLUMN plans.features IS 'Feature switches, e.g. {"comparison": true, "csv_export": true}.';

CREATE TRIGGER plans_set_updated_at BEFORE UPDATE ON plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE subscriptions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    plan_code          VARCHAR(32) NOT NULL REFERENCES plans (code) ON DELETE RESTRICT,
    status             VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (status IN ('trialing', 'active', 'past_due', 'cancelled', 'expired')),
    provider           VARCHAR(30) NOT NULL DEFAULT 'internal',
    provider_ref       TEXT,
    current_period_start TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    current_period_end   TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
    cancelled_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscriptions_period_valid CHECK (
        current_period_end IS NULL OR current_period_end > current_period_start
    )
);

COMMENT ON TABLE subscriptions IS 'Current and past subscriptions of a workspace.';

-- at most one live subscription per workspace
CREATE UNIQUE INDEX subscriptions_one_active_per_workspace
    ON subscriptions (workspace_id)
    WHERE status IN ('trialing', 'active', 'past_due');
CREATE INDEX subscriptions_workspace_idx ON subscriptions (workspace_id, created_at DESC);
CREATE INDEX subscriptions_plan_idx ON subscriptions (plan_code);

CREATE TRIGGER subscriptions_set_updated_at BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Channel catalogue (global) and per-workspace tracking
-- ---------------------------------------------------------------------------

CREATE TABLE channels (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_id       BIGINT UNIQUE,
    username          VARCHAR(64) NOT NULL,
    title             TEXT NOT NULL,
    description       TEXT,
    category          VARCHAR(64),
    language_code     VARCHAR(10),
    channel_type      VARCHAR(20) NOT NULL DEFAULT 'channel'
        CHECK (channel_type IN ('channel', 'group', 'supergroup')),
    is_verified       BOOLEAN NOT NULL DEFAULT FALSE,
    -- only known when a workspace owns the channel and granted access
    has_owner_access  BOOLEAN NOT NULL DEFAULT FALSE,
    subscriber_count  BIGINT,
    avatar_url        TEXT,
    provider_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_refreshed_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channels_username_normalized CHECK (username = lower(username)),
    CONSTRAINT channels_username_not_blank CHECK (btrim(username) <> ''),
    CONSTRAINT channels_subscribers_non_negative CHECK (subscriber_count IS NULL OR subscriber_count >= 0)
);

COMMENT ON TABLE channels IS 'Global Telegram channel catalogue. A channel is stored once for all workspaces.';
COMMENT ON COLUMN channels.username IS 'Normalised (lower-cased, @ stripped) public username.';
COMMENT ON COLUMN channels.subscriber_count IS 'Last known subscriber count; NULL means unknown, not zero.';

CREATE INDEX channels_category_idx ON channels (category);
CREATE INDEX channels_created_idx ON channels (created_at DESC);
CREATE INDEX channels_title_trgm_idx ON channels USING gin (to_tsvector('simple', title));

CREATE TRIGGER channels_set_updated_at BEFORE UPDATE ON channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE workspace_channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    channel_id       UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    added_by         UUID REFERENCES users (id) ON DELETE SET NULL,
    source           VARCHAR(30) NOT NULL DEFAULT 'manual'
        CHECK (source IN ('telegram', 'provider', 'import', 'manual')),
    -- owner_access means the workspace controls the channel and can read
    -- privileged statistics through the Telegram Bot API
    access_level     VARCHAR(20) NOT NULL DEFAULT 'public'
        CHECK (access_level IN ('public', 'owner_access')),
    monitoring_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    monitor_interval_minutes INTEGER NOT NULL DEFAULT 360
        CHECK (monitor_interval_minutes BETWEEN 15 AND 10080),
    notes            TEXT,
    archived_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT workspace_channels_unique UNIQUE (workspace_id, channel_id)
);

COMMENT ON TABLE workspace_channels IS 'Links a workspace to a catalogue channel; carries per-workspace settings.';

CREATE INDEX workspace_channels_workspace_idx
    ON workspace_channels (workspace_id, created_at DESC) WHERE archived_at IS NULL;
CREATE INDEX workspace_channels_channel_idx ON workspace_channels (channel_id);
CREATE INDEX workspace_channels_monitoring_idx
    ON workspace_channels (monitoring_enabled, monitor_interval_minutes) WHERE archived_at IS NULL;

CREATE TRIGGER workspace_channels_set_updated_at BEFORE UPDATE ON workspace_channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Data provenance
-- ---------------------------------------------------------------------------

CREATE TABLE data_sources (
    code             VARCHAR(32) PRIMARY KEY,
    name             VARCHAR(100) NOT NULL,
    kind             VARCHAR(20) NOT NULL
        CHECK (kind IN ('api', 'telegram', 'import', 'manual')),
    reliability      VARCHAR(20) NOT NULL DEFAULT 'unverified'
        CHECK (reliability IN ('verified', 'estimated', 'unverified')),
    description      TEXT,
    is_enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    -- minimum minutes between two refreshes of the same channel
    rate_limit_minutes INTEGER NOT NULL DEFAULT 60 CHECK (rate_limit_minutes >= 0),
    config           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE data_sources IS 'Catalogue of providers that can supply channel statistics.';

CREATE TRIGGER data_sources_set_updated_at BEFORE UPDATE ON data_sources
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE data_imports (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    channel_id     UUID REFERENCES channels (id) ON DELETE SET NULL,
    uploaded_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    source_code    VARCHAR(32) NOT NULL DEFAULT 'csv_import' REFERENCES data_sources (code) ON DELETE RESTRICT,
    file_name      TEXT NOT NULL,
    file_size      BIGINT NOT NULL CHECK (file_size >= 0 AND file_size <= 104857600),
    content_type   VARCHAR(100),
    file_checksum  CHAR(64),
    status         VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    rows_total     INTEGER NOT NULL DEFAULT 0,
    rows_imported  INTEGER NOT NULL DEFAULT 0,
    rows_failed    INTEGER NOT NULL DEFAULT 0,
    error_message  TEXT,
    report         JSONB NOT NULL DEFAULT '{}'::jsonb,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE data_imports IS 'CSV/XLSX uploads and their processing outcome.';
COMMENT ON COLUMN data_imports.file_checksum IS 'SHA-256 of the upload, used for replay protection.';

CREATE INDEX data_imports_workspace_idx ON data_imports (workspace_id, created_at DESC);
CREATE UNIQUE INDEX data_imports_dedupe_idx
    ON data_imports (workspace_id, file_checksum) WHERE file_checksum IS NOT NULL;

CREATE TRIGGER data_imports_set_updated_at BEFORE UPDATE ON data_imports
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Channel statistics history (append-only)
-- ---------------------------------------------------------------------------

CREATE TABLE channel_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    workspace_id    UUID REFERENCES workspaces (id) ON DELETE CASCADE,
    source_code     VARCHAR(32) NOT NULL REFERENCES data_sources (code) ON DELETE RESTRICT,
    captured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period_start    TIMESTAMPTZ,
    period_end      TIMESTAMPTZ,
    subscriber_count BIGINT,
    growth           BIGINT,
    views            BIGINT,
    average_views    BIGINT,
    err              NUMERIC(8, 4),
    post_frequency   NUMERIC(8, 4),
    reach            BIGINT,
    confidence       VARCHAR(20) NOT NULL DEFAULT 'unverified'
        CHECK (confidence IN ('verified', 'estimated', 'unverified', 'unavailable')),
    raw_payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channel_snapshots_period_valid CHECK (
        period_end IS NULL OR period_start IS NULL OR period_end >= period_start
    ),
    CONSTRAINT channel_snapshots_non_negative CHECK (
        (subscriber_count IS NULL OR subscriber_count >= 0) AND
        (views IS NULL OR views >= 0) AND
        (average_views IS NULL OR average_views >= 0) AND
        (err IS NULL OR err >= 0) AND
        (post_frequency IS NULL OR post_frequency >= 0)
    )
);

COMMENT ON TABLE channel_snapshots IS
    'Append-only metric history. A new row per measurement; nothing is ever overwritten.';
COMMENT ON COLUMN channel_snapshots.confidence IS
    'verified = from an authoritative source, estimated = derived, unverified = user supplied, unavailable = not obtainable.';
COMMENT ON COLUMN channel_snapshots.workspace_id IS
    'Set when the measurement was collected for a specific workspace (owner access); NULL for shared/public data.';

CREATE INDEX channel_snapshots_channel_time_idx
    ON channel_snapshots (channel_id, captured_at DESC);
CREATE INDEX channel_snapshots_workspace_idx
    ON channel_snapshots (workspace_id, captured_at DESC) WHERE workspace_id IS NOT NULL;
CREATE INDEX channel_snapshots_source_idx ON channel_snapshots (source_code, captured_at DESC);
CREATE UNIQUE INDEX channel_snapshots_dedupe_idx
    ON channel_snapshots (channel_id, source_code, captured_at);

CREATE TRIGGER channel_snapshots_append_only BEFORE UPDATE OR DELETE ON channel_snapshots
    FOR EACH ROW EXECUTE FUNCTION prevent_mutation();

CREATE TABLE channel_posts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id     UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    telegram_msg_id BIGINT NOT NULL,
    published_at   TIMESTAMPTZ NOT NULL,
    text_preview   TEXT,
    has_media      BOOLEAN NOT NULL DEFAULT FALSE,
    permalink      TEXT,
    raw_payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channel_posts_unique_per_channel UNIQUE (channel_id, telegram_msg_id)
);

COMMENT ON TABLE channel_posts IS 'Known publications of a channel.';

CREATE INDEX channel_posts_channel_time_idx ON channel_posts (channel_id, published_at DESC);

CREATE TRIGGER channel_posts_set_updated_at BEFORE UPDATE ON channel_posts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE post_snapshots (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id       UUID NOT NULL REFERENCES channel_posts (id) ON DELETE CASCADE,
    source_code   VARCHAR(32) NOT NULL REFERENCES data_sources (code) ON DELETE RESTRICT,
    captured_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    views         BIGINT CHECK (views IS NULL OR views >= 0),
    forwards      BIGINT CHECK (forwards IS NULL OR forwards >= 0),
    reactions     BIGINT CHECK (reactions IS NULL OR reactions >= 0),
    confidence    VARCHAR(20) NOT NULL DEFAULT 'unverified'
        CHECK (confidence IN ('verified', 'estimated', 'unverified', 'unavailable')),
    raw_payload   JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE post_snapshots IS 'Append-only view/reaction history of individual posts.';

CREATE INDEX post_snapshots_post_time_idx ON post_snapshots (post_id, captured_at DESC);
CREATE UNIQUE INDEX post_snapshots_dedupe_idx ON post_snapshots (post_id, source_code, captured_at);

CREATE TRIGGER post_snapshots_append_only BEFORE UPDATE OR DELETE ON post_snapshots
    FOR EACH ROW EXECUTE FUNCTION prevent_mutation();

-- ---------------------------------------------------------------------------
-- Advertising
-- ---------------------------------------------------------------------------

CREATE TABLE advertisers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          VARCHAR(150) NOT NULL,
    contact_name  TEXT,
    contact_telegram VARCHAR(64),
    contact_email TEXT,
    notes         TEXT,
    archived_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT advertisers_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT advertisers_unique_name UNIQUE (workspace_id, name)
);

COMMENT ON TABLE advertisers IS 'Advertising clients/companies, scoped to one workspace.';

CREATE INDEX advertisers_workspace_idx
    ON advertisers (workspace_id, created_at DESC) WHERE archived_at IS NULL;

CREATE TRIGGER advertisers_set_updated_at BEFORE UPDATE ON advertisers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE campaigns (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    advertiser_id      UUID REFERENCES advertisers (id) ON DELETE SET NULL,
    channel_id         UUID REFERENCES channels (id) ON DELETE SET NULL,
    name               VARCHAR(200) NOT NULL,
    status             VARCHAR(20) NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'planned', 'running', 'completed', 'cancelled')),
    budget             NUMERIC(14, 2) CHECK (budget IS NULL OR budget >= 0),
    cost               NUMERIC(14, 2) CHECK (cost IS NULL OR cost >= 0),
    revenue            NUMERIC(14, 2) CHECK (revenue IS NULL OR revenue >= 0),
    currency           CHAR(3) NOT NULL DEFAULT 'RUB',
    scheduled_at       TIMESTAMPTZ,
    started_at         TIMESTAMPTZ,
    finished_at        TIMESTAMPTZ,
    -- NULL means "not measured yet", never zero
    views              BIGINT CHECK (views IS NULL OR views >= 0),
    clicks             BIGINT CHECK (clicks IS NULL OR clicks >= 0),
    new_subscribers    BIGINT CHECK (new_subscribers IS NULL OR new_subscribers >= 0),
    post_url           TEXT,
    comment            TEXT,
    created_by         UUID REFERENCES users (id) ON DELETE SET NULL,
    archived_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT campaigns_name_not_blank CHECK (btrim(name) <> '')
);

COMMENT ON TABLE campaigns IS 'Advertising placements bought by a workspace in a Telegram channel.';

CREATE INDEX campaigns_workspace_idx ON campaigns (workspace_id, created_at DESC);
CREATE INDEX campaigns_workspace_status_idx ON campaigns (workspace_id, status) WHERE archived_at IS NULL;
CREATE INDEX campaigns_channel_idx ON campaigns (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX campaigns_scheduled_idx ON campaigns (scheduled_at) WHERE status IN ('planned', 'running');

CREATE TRIGGER campaigns_set_updated_at BEFORE UPDATE ON campaigns
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE campaign_metrics (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id   UUID NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    captured_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period_start  TIMESTAMPTZ,
    period_end    TIMESTAMPTZ,
    views         BIGINT CHECK (views IS NULL OR views >= 0),
    clicks        BIGINT CHECK (clicks IS NULL OR clicks >= 0),
    new_subscribers BIGINT CHECK (new_subscribers IS NULL OR new_subscribers >= 0),
    spend         NUMERIC(14, 2) CHECK (spend IS NULL OR spend >= 0),
    revenue       NUMERIC(14, 2) CHECK (revenue IS NULL OR revenue >= 0),
    -- derived KPI snapshots; recomputed by the analytics service
    cpm           NUMERIC(14, 4),
    cpc           NUMERIC(14, 4),
    cpf           NUMERIC(14, 4),
    source_code   VARCHAR(32) REFERENCES data_sources (code) ON DELETE SET NULL,
    confidence    VARCHAR(20) NOT NULL DEFAULT 'unverified'
        CHECK (confidence IN ('verified', 'estimated', 'unverified', 'unavailable')),
    raw_payload   JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE campaign_metrics IS 'Append-only measurements of a campaign over time.';

CREATE INDEX campaign_metrics_campaign_idx ON campaign_metrics (campaign_id, captured_at DESC);
CREATE UNIQUE INDEX campaign_metrics_dedupe_idx ON campaign_metrics (campaign_id, captured_at);

CREATE TRIGGER campaign_metrics_append_only BEFORE UPDATE OR DELETE ON campaign_metrics
    FOR EACH ROW EXECUTE FUNCTION prevent_mutation();

-- ---------------------------------------------------------------------------
-- Monitoring, notifications, audit
-- ---------------------------------------------------------------------------

CREATE TABLE tracked_channels (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    channel_id         UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    interval_minutes   INTEGER NOT NULL DEFAULT 360 CHECK (interval_minutes BETWEEN 15 AND 10080),
    last_run_at        TIMESTAMPTZ,
    next_run_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    last_error         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tracked_channels_unique UNIQUE (workspace_id, channel_id)
);

COMMENT ON TABLE tracked_channels IS 'Monitoring schedule for a (workspace, channel) pair.';
COMMENT ON COLUMN tracked_channels.next_run_at IS 'Drives the worker scheduler; indexed for claim queries.';

CREATE INDEX tracked_channels_due_idx ON tracked_channels (next_run_at) WHERE enabled;
CREATE INDEX tracked_channels_workspace_idx ON tracked_channels (workspace_id);

CREATE TRIGGER tracked_channels_set_updated_at BEFORE UPDATE ON tracked_channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id       UUID REFERENCES users (id) ON DELETE CASCADE,
    channel_id    UUID REFERENCES channels (id) ON DELETE SET NULL,
    campaign_id   UUID REFERENCES campaigns (id) ON DELETE SET NULL,
    type          VARCHAR(40) NOT NULL,
    severity      VARCHAR(20) NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info', 'warning', 'critical')),
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- de-duplication key: identical (workspace, type, dedupe_key) is stored once
    dedupe_key    TEXT,
    scheduled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at       TIMESTAMPTZ,
    read_at       TIMESTAMPTZ,
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE notifications IS 'Outbox of notifications; dedupe_key prevents duplicate alerts.';

CREATE UNIQUE INDEX notifications_dedupe_idx
    ON notifications (workspace_id, type, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX notifications_pending_idx
    ON notifications (scheduled_at) WHERE sent_at IS NULL;
CREATE INDEX notifications_workspace_idx ON notifications (workspace_id, created_at DESC);

CREATE TRIGGER notifications_set_updated_at BEFORE UPDATE ON notifications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE payment_transactions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    subscription_id   UUID REFERENCES subscriptions (id) ON DELETE SET NULL,
    user_id           UUID REFERENCES users (id) ON DELETE SET NULL,
    provider          VARCHAR(30) NOT NULL,
    provider_payment_id TEXT,
    -- idempotency key supplied by us; a repeated webhook can never double-charge
    idempotency_key   TEXT NOT NULL,
    amount            NUMERIC(14, 2) NOT NULL CHECK (amount >= 0),
    currency          CHAR(3) NOT NULL,
    status            VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'succeeded', 'failed', 'refunded', 'cancelled')),
    plan_code         VARCHAR(32) REFERENCES plans (code) ON DELETE SET NULL,
    description       TEXT,
    payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    paid_at           TIMESTAMPTZ,
    refunded_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT payment_transactions_idempotency_unique UNIQUE (idempotency_key)
);

COMMENT ON TABLE payment_transactions IS 'Payment history, including webhook replays guarded by idempotency_key.';

CREATE INDEX payment_transactions_workspace_idx ON payment_transactions (workspace_id, created_at DESC);
CREATE UNIQUE INDEX payment_transactions_provider_ref_idx
    ON payment_transactions (provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL;

CREATE TRIGGER payment_transactions_set_updated_at BEFORE UPDATE ON payment_transactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE audit_logs (
    id            BIGSERIAL PRIMARY KEY,
    workspace_id  UUID REFERENCES workspaces (id) ON DELETE SET NULL,
    actor_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    action        VARCHAR(80) NOT NULL,
    entity_type   VARCHAR(40),
    entity_id     UUID,
    changes       JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_address    INET,
    user_agent    TEXT,
    request_id    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE audit_logs IS 'Immutable trail of security- and billing-relevant actions.';

CREATE INDEX audit_logs_workspace_time_idx ON audit_logs (workspace_id, created_at DESC);
CREATE INDEX audit_logs_entity_idx ON audit_logs (entity_type, entity_id);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_user_id, created_at DESC);

CREATE TRIGGER audit_logs_append_only BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION prevent_mutation();
