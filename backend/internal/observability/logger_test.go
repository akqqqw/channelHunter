package observability_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/observability"
)

func TestLoggerIncludesProcessMetadata(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLoggerWithWriter(&buf,
		config.LogConfig{Level: slog.LevelDebug, Format: "json"}, "worker", config.EnvStaging)

	logger.Info("hello")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "channelhunter", line["service"])
	assert.Equal(t, "worker", line["process"])
	assert.Equal(t, "staging", line["env"])
	assert.Equal(t, "hello", line["msg"])
}

func TestLoggerRedactsSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLoggerWithWriter(&buf,
		config.LogConfig{Level: slog.LevelDebug, Format: "json"}, "api", config.EnvProduction)

	logger.Info("login",
		slog.String("telegram_init_data", "query_id=AAA&hash=deadbeef"),
		slog.String("api_key", "sk-live-123"),
		slog.String("password", "hunter2"),
		slog.String("authorization", "Bearer secret-token"),
		slog.String("username", "visible"),
	)

	for _, secret := range []string{"deadbeef", "sk-live-123", "hunter2", "secret-token"} {
		assert.NotContains(t, buf.String(), secret)
	}
	assert.Contains(t, buf.String(), observability.RedactedValue)
	assert.Contains(t, buf.String(), "visible", "non-sensitive fields must stay readable")
}

func TestLoggerRedactsGroupedAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLoggerWithWriter(&buf,
		config.LogConfig{Level: slog.LevelDebug, Format: "json"}, "api", config.EnvProduction)

	logger.Info("payment", slog.Group("provider",
		slog.String("name", "telegram_stars"),
		slog.String("webhook_secret", "super-secret"),
	))

	assert.NotContains(t, buf.String(), "super-secret")
	assert.Contains(t, buf.String(), "telegram_stars")
}

func TestLoggerKeepsNonSecretValuesOfSensitiveLookingKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLoggerWithWriter(&buf,
		config.LogConfig{Level: slog.LevelDebug, Format: "json"}, "bot", config.EnvDevelopment)

	// Diagnostics must stay readable: these are booleans/integers, not secrets.
	logger.Info("startup",
		slog.Bool("token_configured", true),
		slog.Int("secret_rotation_count", 3),
	)

	assert.Contains(t, buf.String(), `"token_configured":true`)
	assert.Contains(t, buf.String(), `"secret_rotation_count":3`)
	assert.NotContains(t, buf.String(), observability.RedactedValue)
}

func TestIsSensitiveKey(t *testing.T) {
	for _, key := range []string{"token", "BOT_TOKEN", "webhook_secret", "initData", "Authorization", "signature", "password", "api_key"} {
		assert.True(t, observability.IsSensitiveKey(key), key)
	}
	for _, key := range []string{"username", "channel_id", "status", "count", "msg", "level", "time"} {
		assert.False(t, observability.IsSensitiveKey(key), key)
	}
}

func TestLoggerHonoursLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLoggerWithWriter(&buf,
		config.LogConfig{Level: slog.LevelWarn, Format: "text"}, "api", config.EnvProduction)

	require.NotNil(t, logger)
	assert.True(t, logger.Enabled(t.Context(), slog.LevelWarn))
	assert.False(t, logger.Enabled(t.Context(), slog.LevelInfo))

	logger.Info("must not appear")
	logger.Warn("must appear")
	assert.NotContains(t, buf.String(), "must not appear")
	assert.Contains(t, buf.String(), "must appear")
}

func TestContextLoggerAndRequestID(t *testing.T) {
	ctx := t.Context()
	assert.Empty(t, observability.RequestIDFrom(ctx))
	assert.NotNil(t, observability.LoggerFrom(ctx))

	custom := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	ctx = observability.WithLogger(ctx, custom)
	ctx = observability.WithRequestID(ctx, "abc")

	assert.Same(t, custom, observability.LoggerFrom(ctx))
	assert.Equal(t, "abc", observability.RequestIDFrom(ctx))
}
