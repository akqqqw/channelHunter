package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/config"
)

// setEnv applies environment variables for the duration of a test and clears
// the ones a test does not set, so the developer's shell cannot influence it.
func setEnv(t *testing.T, values map[string]string) {
	t.Helper()
	all := []string{
		"APP_ENV", "HTTP_HOST", "HTTP_PORT", "HTTP_READ_TIMEOUT", "HTTP_TRUSTED_PROXIES",
		"HTTP_CORS_ALLOWED_ORIGINS", "DATABASE_URL", "DATABASE_MAX_CONNS", "DATABASE_MIN_CONNS",
		"DATABASE_MIGRATE_ON_START", "LOG_LEVEL", "LOG_FORMAT", "TELEGRAM_BOT_TOKEN",
		"TELEGRAM_MODE", "TELEGRAM_WEBHOOK_SECRET_TOKEN", "TELEGRAM_WEBHOOK_URL",
		"AUTH_JWT_SECRET", "AUTH_TOKEN_TTL", "RATE_LIMIT_ENABLED", "RATE_LIMIT_REQUESTS",
		"RATE_LIMIT_WINDOW", "APP_TIMEZONE", "MAX_UPLOAD_BYTES", "PROVIDER_TIMEOUT",
		"TELEGRAM_ADMIN_IDS", "PUBLIC_BASE_URL", "BILLING_CURRENCY",
	}
	for _, key := range all {
		require.NoError(t, os.Unsetenv(key))
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func validAPIEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":       "postgres://user:pass@localhost:5432/db?sslmode=disable",
		"AUTH_JWT_SECRET":    "0123456789abcdef0123456789abcdef",
		"TELEGRAM_BOT_TOKEN": "123:abc",
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	setEnv(t, validAPIEnv())

	cfg, err := config.Load(config.ProcessAPI)
	require.NoError(t, err)

	assert.Equal(t, config.ProcessAPI, cfg.Process)
	assert.Equal(t, config.EnvDevelopment, cfg.Environment)
	assert.Equal(t, "0.0.0.0", cfg.HTTP.Host)
	assert.Equal(t, 8080, cfg.HTTP.Port)
	assert.Equal(t, "0.0.0.0:8080", cfg.HTTP.Addr())
	assert.Equal(t, 15*time.Second, cfg.HTTP.ReadTimeout)
	assert.Equal(t, 20*time.Second, cfg.HTTP.ShutdownTimeout)
	assert.Equal(t, slog.LevelInfo, cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, int32(10), cfg.Database.MaxConns)
	assert.Equal(t, "RUB", cfg.App.BillingCurrency)
	assert.Equal(t, "Europe/Moscow", cfg.App.Timezone)
	assert.Equal(t, int64(10<<20), cfg.App.MaxUploadBytes)
	assert.True(t, cfg.RateLimit.Enabled)
	assert.Equal(t, 60, cfg.RateLimit.Requests)
	assert.Equal(t, 24*time.Hour, cfg.Auth.TokenTTL)
}

func TestLoadParsesOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":         "postgres://u:p@db:5432/x",
		"AUTH_JWT_SECRET":      "0123456789abcdef0123456789abcdef",
		"TELEGRAM_BOT_TOKEN":   "123:abc",
		"HTTP_PORT":            "9090",
		"LOG_LEVEL":            "debug",
		"LOG_FORMAT":           "text",
		"HTTP_TRUSTED_PROXIES": "10.0.0.0/8, 192.168.1.1",
		"DATABASE_MAX_CONNS":   "25",
		"TELEGRAM_ADMIN_IDS":   "42, 43",
		"RATE_LIMIT_ENABLED":   "false",
		"MAX_UPLOAD_BYTES":     "2048",
		"PROVIDER_TIMEOUT":     "5s",
	})

	cfg, err := config.Load(config.ProcessAPI)
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.HTTP.Port)
	assert.Equal(t, slog.LevelDebug, cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.1"}, cfg.HTTP.TrustedProxies)
	assert.Equal(t, int32(25), cfg.Database.MaxConns)
	assert.Equal(t, []int64{42, 43}, cfg.Telegram.AdminIDs)
	assert.False(t, cfg.RateLimit.Enabled)
	assert.Equal(t, int64(2048), cfg.App.MaxUploadBytes)
	assert.Equal(t, 5*time.Second, cfg.App.ProviderTimeout)
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	// Invalid integers/durations fall back to defaults instead of failing the
	// boot, which avoids a fatal typo taking a whole deployment down.
	setEnv(t, validAPIEnv())
	t.Setenv("HTTP_PORT", "not-a-port")
	t.Setenv("LOG_LEVEL", "shout")

	cfg, err := config.Load(config.ProcessAPI)
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.HTTP.Port)
	assert.Equal(t, slog.LevelInfo, cfg.Log.Level)
}

func TestValidateRequiresDatabaseURL(t *testing.T) {
	env := validAPIEnv()
	delete(env, "DATABASE_URL")
	setEnv(t, env)

	_, err := config.Load(config.ProcessAPI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL is required")
}

func TestValidateRequiresJWTSecretForAPI(t *testing.T) {
	env := validAPIEnv()
	delete(env, "AUTH_JWT_SECRET")
	setEnv(t, env)

	_, err := config.Load(config.ProcessAPI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_JWT_SECRET is required")
}

func TestProductionRequiresStrongJWTSecret(t *testing.T) {
	setEnv(t, validAPIEnv())
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_JWT_SECRET", "short")

	_, err := config.Load(config.ProcessAPI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 32 characters")
}

func TestValidateRejectsInvalidTimezone(t *testing.T) {
	setEnv(t, validAPIEnv())
	t.Setenv("APP_TIMEZONE", "Mars/Olympus")

	_, err := config.Load(config.ProcessAPI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid IANA timezone")
}

func TestValidateRejectsInconsistentPoolSizes(t *testing.T) {
	setEnv(t, validAPIEnv())
	t.Setenv("DATABASE_MAX_CONNS", "2")
	t.Setenv("DATABASE_MIN_CONNS", "5")

	_, err := config.Load(config.ProcessAPI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_MAX_CONNS must be >= DATABASE_MIN_CONNS")
}

func TestBotRequiresToken(t *testing.T) {
	env := validAPIEnv()
	delete(env, "TELEGRAM_BOT_TOKEN")
	setEnv(t, env)
	t.Setenv("TELEGRAM_MODE", "polling")

	_, err := config.Load(config.ProcessBot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_BOT_TOKEN is required for the bot process")
}

func TestBotPollingIsRejectedInProduction(t *testing.T) {
	setEnv(t, validAPIEnv())
	t.Setenv("APP_ENV", "production")
	t.Setenv("TELEGRAM_MODE", "polling")

	_, err := config.Load(config.ProcessBot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "polling is not allowed in production")
}

func TestBotWebhookRequiresSecretAndURL(t *testing.T) {
	setEnv(t, validAPIEnv())
	t.Setenv("TELEGRAM_MODE", "webhook")

	_, err := config.Load(config.ProcessBot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_WEBHOOK_SECRET_TOKEN is required")
	assert.Contains(t, err.Error(), "TELEGRAM_WEBHOOK_URL is required")
}

func TestBotWebhookValid(t *testing.T) {
	setEnv(t, validAPIEnv())
	t.Setenv("TELEGRAM_MODE", "webhook")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET_TOKEN", "secret-token")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://example.com/telegram/webhook")

	cfg, err := config.Load(config.ProcessBot)
	require.NoError(t, err)
	assert.Equal(t, "webhook", cfg.Telegram.Mode)
}

func TestWorkerDoesNotRequireBotToken(t *testing.T) {
	env := validAPIEnv()
	delete(env, "TELEGRAM_BOT_TOKEN")
	delete(env, "AUTH_JWT_SECRET")
	setEnv(t, env)

	cfg, err := config.Load(config.ProcessWorker)
	require.NoError(t, err)
	assert.Equal(t, config.ProcessWorker, cfg.Process)
}

func TestLoadFromEnvFile(t *testing.T) {
	setEnv(t, nil)

	dir := t.TempDir()
	path := filepath.Join(dir, ".env.test")
	content := strings.Join([]string{
		"DATABASE_URL=postgres://file:file@localhost:5432/filedb",
		"AUTH_JWT_SECRET=0123456789abcdef0123456789abcdef",
		"TELEGRAM_BOT_TOKEN=999:file",
		"HTTP_PORT=7070",
		"APP_TIMEZONE=UTC",
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	cfg, err := config.LoadFromEnvFile(config.ProcessAPI, path)
	require.NoError(t, err)
	assert.Equal(t, "postgres://file:file@localhost:5432/filedb", cfg.Database.URL)
	assert.Equal(t, 7070, cfg.HTTP.Port)
	assert.Equal(t, "UTC", cfg.App.Timezone)

	// Real environment variables win over the file.
	t.Setenv("HTTP_PORT", "6060")
	cfg, err = config.LoadFromEnvFile(config.ProcessAPI, path)
	require.NoError(t, err)
	assert.Equal(t, 6060, cfg.HTTP.Port)
}

func TestLoadFromMissingEnvFile(t *testing.T) {
	setEnv(t, validAPIEnv())

	_, err := config.LoadFromEnvFile(config.ProcessAPI, filepath.Join(t.TempDir(), "nope.env"))
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestValidateGuardsShutdownBudget(t *testing.T) {
	// net/http treats a brand-new connection as busy for 5 seconds, so a
	// shorter shutdown budget can be exceeded by a silent client.
	setEnv(t, validAPIEnv())
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "2s")

	_, err := config.Load(config.ProcessAPI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP_SHUTDOWN_TIMEOUT must be at least 6s")

	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "0s")
	_, err = config.Load(config.ProcessAPI)
	require.Error(t, err)
}
