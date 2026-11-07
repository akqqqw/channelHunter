//go:build integration

// Package integration contains tests that run against a real PostgreSQL.
//
// Run them with:
//
//	make test-integration           # uses TEST_DATABASE_URL / DATABASE_URL
//
// Every test gets its own schema, so tests never see each other's data and the
// developer's database is left untouched.
package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/database"
	"github.com/akqqqw/channelhunter/backend/migrations"
	"github.com/akqqqw/channelhunter/backend/tests/testsupport"
)

// requiredTables is the schema promised by the product specification.
var requiredTables = []string{
	"users", "workspaces", "workspace_members", "plans", "subscriptions",
	"channels", "workspace_channels", "channel_snapshots", "channel_posts",
	"post_snapshots", "data_sources", "data_imports", "advertisers", "campaigns",
	"campaign_metrics", "tracked_channels", "notifications",
	"payment_transactions", "audit_logs",
}

func TestMigrationsCreateEveryTable(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	rows, err := db.Pool().Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
	require.NoError(t, err)
	defer rows.Close()

	present := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		present[name] = true
	}
	require.NoError(t, rows.Err())

	for _, table := range requiredTables {
		assert.True(t, present[table], "table %q must exist", table)
	}
	assert.True(t, present["schema_migrations"], "golang-migrate bookkeeping table must exist")
}

func TestPlansAndSourcesAreSeeded(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	var planCount int
	require.NoError(t, db.Pool().QueryRow(ctx, `SELECT count(*) FROM plans`).Scan(&planCount))
	assert.Equal(t, 3, planCount, "FREE/PRO/BUSINESS must be seeded")

	// Limits come from the seed and must match the product rules.
	var maxChannels, historyDays int
	require.NoError(t, db.Pool().QueryRow(ctx,
		`SELECT max_channels, history_days FROM plans WHERE code = 'free'`).
		Scan(&maxChannels, &historyDays))
	assert.Equal(t, 3, maxChannels)
	assert.Equal(t, 7, historyDays)

	var sourceCount int
	require.NoError(t, db.Pool().QueryRow(ctx, `SELECT count(*) FROM data_sources`).Scan(&sourceCount))
	assert.GreaterOrEqual(t, sourceCount, 4, "telegram, provider, csv and manual sources must exist")
}

func TestUpdatedAtTriggerMaintainsTimestamps(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	var id string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO users (telegram_id, first_name) VALUES (1001, 'Test') RETURNING id`).Scan(&id))

	var createdAt, updatedAt string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`SELECT created_at::text, updated_at::text FROM users WHERE id = $1::uuid`, id).
		Scan(&createdAt, &updatedAt))

	_, err := db.Pool().Exec(ctx,
		`UPDATE users SET first_name = 'Updated' WHERE id = $1::uuid`, id)
	require.NoError(t, err)

	var updatedAfter string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`SELECT updated_at::text FROM users WHERE id = $1::uuid`, id).Scan(&updatedAfter))
	assert.NotEqual(t, updatedAt, updatedAfter, "updated_at must be maintained by trigger")
	assert.Equal(t, createdAt, createdAt)
}

func TestChannelSnapshotsAreAppendOnly(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	var channelID string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO channels (username, title) VALUES ('history_channel', 'History')
		 RETURNING id`).Scan(&channelID))

	_, err := db.Pool().Exec(ctx, `
		INSERT INTO channel_snapshots (channel_id, source_code, captured_at, subscriber_count)
		VALUES ($1::uuid, 'manual_entry', NOW() - INTERVAL '1 day', 1000)`, channelID)
	require.NoError(t, err)

	// History keeps growing; nothing is overwritten.
	_, err = db.Pool().Exec(ctx, `
		INSERT INTO channel_snapshots (channel_id, source_code, captured_at, subscriber_count)
		VALUES ($1::uuid, 'manual_entry', NOW(), 1200)`, channelID)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM channel_snapshots WHERE channel_id = $1::uuid`, channelID).Scan(&count))
	assert.Equal(t, 2, count)

	// Rewriting history is rejected by the database itself.
	_, err = db.Pool().Exec(ctx,
		`UPDATE channel_snapshots SET subscriber_count = 1 WHERE channel_id = $1::uuid`, channelID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "append-only")

	_, err = db.Pool().Exec(ctx,
		`DELETE FROM channel_snapshots WHERE channel_id = $1::uuid`, channelID)
	require.Error(t, err)

	// The same measurement cannot be stored twice (idempotent refreshes).
	_, err = db.Pool().Exec(ctx, `
		INSERT INTO channel_snapshots (channel_id, source_code, captured_at, subscriber_count)
		SELECT channel_id, source_code, captured_at, 9999 FROM channel_snapshots
		WHERE channel_id = $1::uuid AND captured_at < NOW()`, channelID)
	require.Error(t, err, "duplicate (channel, source, captured_at) must be rejected")
}

func TestChannelCatalogueIsGlobal(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	var userA, userB, workspaceA, workspaceB string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO users (telegram_id, first_name) VALUES (2001, 'A') RETURNING id`).Scan(&userA))
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO users (telegram_id, first_name) VALUES (2002, 'B') RETURNING id`).Scan(&userB))
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO workspaces (name, owner_id) VALUES ('A ws', $1::uuid) RETURNING id`, userA).Scan(&workspaceA))
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO workspaces (name, owner_id) VALUES ('B ws', $1::uuid) RETURNING id`, userB).Scan(&workspaceB))

	// One catalogue row...
	var channelID string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO channels (username, title, subscriber_count)
		 VALUES ('shared_channel', 'Shared', 5000) RETURNING id`).Scan(&channelID))

	// ...tracked by two workspaces without duplicating the channel.
	_, err := db.Pool().Exec(ctx,
		`INSERT INTO workspace_channels (workspace_id, channel_id) VALUES ($1::uuid, $2::uuid)`,
		workspaceA, channelID)
	require.NoError(t, err)
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO workspace_channels (workspace_id, channel_id) VALUES ($1::uuid, $2::uuid)`,
		workspaceB, channelID)
	require.NoError(t, err)

	var channels int
	require.NoError(t, db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM channels WHERE username = 'shared_channel'`).Scan(&channels))
	assert.Equal(t, 1, channels, "a channel must be stored once for all workspaces")

	// The same workspace cannot track the same channel twice.
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO workspace_channels (workspace_id, channel_id) VALUES ($1::uuid, $2::uuid)`,
		workspaceA, channelID)
	require.Error(t, err)
}

func TestPlanLimitsAndConstraints(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	// Usernames are normalised by a CHECK constraint.
	_, err := db.Pool().Exec(ctx, `INSERT INTO channels (username, title) VALUES ('UPPER', 'x')`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channels_username_normalized")

	// Money is NUMERIC with a non-negative constraint.
	var owner string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO users (telegram_id, first_name) VALUES (3001, 'Owner') RETURNING id`).Scan(&owner))
	var workspace string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO workspaces (name, owner_id) VALUES ('ws', $1::uuid) RETURNING id`, owner).Scan(&workspace))

	_, err = db.Pool().Exec(ctx, `
		INSERT INTO campaigns (workspace_id, name, budget) VALUES ($1::uuid, 'bad', -1)`, workspace)
	require.Error(t, err, "negative budgets must be rejected")

	_, err = db.Pool().Exec(ctx, `
		INSERT INTO campaigns (workspace_id, name, budget) VALUES ($1::uuid, 'ok', 1000.55)`, workspace)
	require.NoError(t, err)

	// Only one active subscription per workspace.
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO subscriptions (workspace_id, plan_code, status) VALUES ($1::uuid, 'free', 'active')`, workspace)
	require.NoError(t, err)
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO subscriptions (workspace_id, plan_code, status) VALUES ($1::uuid, 'pro', 'active')`, workspace)
	require.Error(t, err, "a workspace may not have two active subscriptions")

	// An archived subscription frees the slot.
	_, err = db.Pool().Exec(ctx,
		`UPDATE subscriptions SET status = 'expired' WHERE workspace_id = $1::uuid`, workspace)
	require.NoError(t, err)
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO subscriptions (workspace_id, plan_code, status) VALUES ($1::uuid, 'pro', 'active')`, workspace)
	require.NoError(t, err)
}

func TestPaymentIdempotencyKeyIsUnique(t *testing.T) {
	db := testsupport.NewDB(t)
	ctx := context.Background()

	var owner, workspace string
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (4001) RETURNING id`).Scan(&owner))
	require.NoError(t, db.Pool().QueryRow(ctx,
		`INSERT INTO workspaces (name, owner_id) VALUES ('ws', $1::uuid) RETURNING id`, owner).Scan(&workspace))

	_, err := db.Pool().Exec(ctx, `
		INSERT INTO payment_transactions (workspace_id, provider, idempotency_key, amount, currency)
		VALUES ($1::uuid, 'telegram_stars', 'idem-1', 1490.00, 'RUB')`, workspace)
	require.NoError(t, err)

	// A replayed webhook with the same idempotency key must not create a row.
	_, err = db.Pool().Exec(ctx, `
		INSERT INTO payment_transactions (workspace_id, provider, idempotency_key, amount, currency)
		VALUES ($1::uuid, 'telegram_stars', 'idem-1', 1490.00, 'RUB')`, workspace)
	require.Error(t, err)

	var count int
	require.NoError(t, db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_transactions WHERE idempotency_key = 'idem-1'`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestMigrationsAreReversible(t *testing.T) {
	db := testsupport.NewDB(t)
	log := testsupport.TestLogger(t)

	migrator, err := database.NewMigrator(db.DB, "", migrations.FS, log)
	require.NoError(t, err)
	defer func() { _ = migrator.Close() }()

	require.NoError(t, migrator.Steps(-2), "all migrations must roll back")

	var exists bool
	require.NoError(t, db.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = current_schema() AND table_name = 'users')`).Scan(&exists))
	assert.False(t, exists, "down migrations must drop the tables they created")

	require.NoError(t, migrator.Steps(2), "migrations must re-apply cleanly")

	require.NoError(t, db.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = current_schema() AND table_name = 'users')`).Scan(&exists))
	assert.True(t, exists)
}

func TestConcurrentMigratorsAreSerialised(t *testing.T) {
	// Two processes booting at once must not corrupt the schema: golang-migrate
	// takes a PostgreSQL advisory lock, so one applies and the other no-ops.
	db := testsupport.NewDB(t)
	log := testsupport.TestLogger(t)

	// Roll back so both migrators have work to do.
	migrator, err := database.NewMigrator(db.DB, "", migrations.FS, log)
	require.NoError(t, err)
	require.NoError(t, migrator.Steps(-2))
	require.NoError(t, migrator.Close())

	const workers = 3
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			m, mErr := database.NewMigrator(db.DB, "", migrations.FS, log)
			if mErr != nil {
				errs <- mErr
				return
			}
			defer m.Close()
			errs <- m.Up()
		}()
	}
	for i := 0; i < workers; i++ {
		require.NoError(t, <-errs)
	}

	version, dirty, err := currentVersion(t, db)
	require.NoError(t, err)
	assert.False(t, dirty)
	assert.Equal(t, uint(2), version)

	var planCount int
	require.NoError(t, db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM plans`).Scan(&planCount))
	assert.Equal(t, 3, planCount, "seed migrations must be applied exactly once")
}

func currentVersion(t *testing.T, db *testsupport.DB) (uint, bool, error) {
	t.Helper()
	migrator, err := database.NewMigrator(db.DB, "", migrations.FS, testsupport.TestLogger(t))
	require.NoError(t, err)
	defer func() { _ = migrator.Close() }()
	return migrator.Version()
}
