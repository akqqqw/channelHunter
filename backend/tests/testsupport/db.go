// Package testsupport provides helpers shared by unit and integration tests.
package testsupport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/database"
	"github.com/akqqqw/channelhunter/backend/migrations"
)

// TestDatabaseURLEnv names the environment variable holding a PostgreSQL URL
// that integration tests may create schema-isolated databases in.
const TestDatabaseURLEnv = "TEST_DATABASE_URL"

// DatabaseURL resolves the admin connection URL for tests.
//
// Order: TEST_DATABASE_URL, then DATABASE_URL. If neither is set the test is
// skipped instead of failing, so `go test ./...` works on a laptop without a
// database while CI always exercises the real thing.
func DatabaseURL(t testing.TB) string {
	t.Helper()
	for _, key := range []string{TestDatabaseURLEnv, "DATABASE_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	t.Skipf("integration test skipped: set %s (or DATABASE_URL) to a PostgreSQL URL", TestDatabaseURLEnv)
	return ""
}

// NewDatabaseURL derives a unique database URL from base, so parallel test
// binaries never share state. The database itself must already exist.
func NewDatabaseURL(t testing.TB, base, schema string) string {
	t.Helper()
	u, err := url.Parse(base)
	require.NoError(t, err)

	q := u.Query()
	q.Set("search_path", schema)
	// Keep the connection usable across the short test lifetime.
	q.Set("sslmode", defaultSSLMode(q.Get("sslmode")))
	u.RawQuery = q.Encode()
	return u.String()
}

func defaultSSLMode(current string) string {
	if current == "" {
		return "disable"
	}
	return current
}

// RandomSchema returns a schema name unlikely to collide.
func RandomSchema(t testing.TB) string {
	t.Helper()
	var b [6]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return "test_" + hex.EncodeToString(b[:])
}

// TestLogger returns a logger that writes through t.Log.
func TestLogger(t testing.TB) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testWriter struct{ t testing.TB }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// DB is a migrated test database bound to a throwaway schema.
type DB struct {
	*database.DB
	Schema string
}

// NewDB creates an isolated schema, applies all embedded migrations and returns
// a pool connected to it. Everything is dropped when the test ends.
func NewDB(t testing.TB) *DB {
	t.Helper()

	adminURL := DatabaseURL(t)
	schema := RandomSchema(t)
	log := TestLogger(t)

	// Connect to the admin URL to create the schema.
	adminCfg := config.DatabaseConfig{
		URL:            adminURL,
		MaxConns:       2,
		MinConns:       1,
		ConnectTimeout: 5 * time.Second,
	}
	admin, err := database.Open(context.Background(), adminCfg, log)
	require.NoError(t, err, "connect to the test database")

	ctx := context.Background()
	_, err = admin.Pool().Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema))
	require.NoError(t, err, "create test schema")

	t.Cleanup(func() {
		cleanCtx := context.Background()
		_, _ = admin.Pool().Exec(cleanCtx, "DROP SCHEMA IF EXISTS "+quoteIdent(schema)+" CASCADE")
		admin.Close()
	})

	testCfg := adminCfg
	testCfg.URL = NewDatabaseURL(t, adminURL, schema)
	testCfg.MaxConns = 5

	db, err := database.Open(ctx, testCfg, log)
	require.NoError(t, err, "connect to the test schema")

	migrator, err := database.NewMigrator(db, "", migrations.FS, log)
	require.NoError(t, err)
	require.NoError(t, migrator.Up())
	require.NoError(t, migrator.Close())

	return &DB{DB: db, Schema: schema}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Truncate empties the given tables, ignoring FK order by using CASCADE.
// Snapshot tables are append-only; TRUNCATE bypasses the row triggers, which is
// intentional for test isolation.
func (db *DB) Truncate(t testing.TB, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	for i, table := range tables {
		tables[i] = quoteIdent(table)
	}
	stmt := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
	_, err := db.Pool().Exec(context.Background(), stmt)
	require.NoError(t, err)
}
