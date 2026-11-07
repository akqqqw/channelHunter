// Package httpapi_test exercises the HTTP surface end to end (routing,
// middleware chain and handlers) without opening a network socket.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/http/health"
	"github.com/akqqqw/channelhunter/backend/internal/http/middleware"
	"github.com/akqqqw/channelhunter/backend/internal/http/routes"
)

func testConfig() *config.Config {
	return &config.Config{
		Process:     config.ProcessAPI,
		Environment: config.EnvDevelopment,
		HTTP: config.HTTPConfig{
			Host:            "127.0.0.1",
			Port:            0,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
			IdleTimeout:     time.Minute,
			ShutdownTimeout: 10 * time.Second,
		},
		RateLimit: config.RateLimitConfig{Enabled: true, Requests: 1000, Window: time.Minute},
		App:       config.AppConfig{Timezone: "UTC", MaxUploadBytes: 1 << 20},
	}
}

func newRouter(t *testing.T, checks ...health.Check) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	checker := health.NewChecker("channelhunter", "api", "test", "test", time.Now(), log, checks...)

	return routes.New(routes.Deps{
		Config:      testConfig(),
		Logger:      log,
		Health:      checker,
		Version:     "test",
		RateLimiter: middleware.NewRateLimiter(1000, time.Minute),
	})
}

func TestHealthEndpointDoesNotTouchDependencies(t *testing.T) {
	called := false
	router := newRouter(t, health.Check{
		Name:     "postgres",
		Critical: true,
		Probe: func(context.Context) error {
			called = true
			return errors.New("database is down")
		},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, called, "liveness must not depend on the database")

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "test", body["version"])
	assert.NotEmpty(t, rec.Header().Get(middleware.RequestIDHeader))
}

func TestReadinessReportsDependencies(t *testing.T) {
	router := newRouter(t,
		health.Check{Name: "postgres", Critical: true, Probe: func(context.Context) error { return nil }},
		health.Check{Name: "provider", Critical: false, Probe: func(context.Context) error {
			return errors.New("upstream timeout")
		}},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	// A non-critical dependency degrades readiness but keeps the pod in rotation.
	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	assert.Equal(t, health.StatusDegraded, report.Status)
	assert.Equal(t, health.StatusOK, report.Dependencies["postgres"].Status)
	assert.Equal(t, health.StatusDown, report.Dependencies["provider"].Status)
	assert.Equal(t, "upstream timeout", report.Dependencies["provider"].Error)
}

func TestReadinessFailsWhenCriticalDependencyIsDown(t *testing.T) {
	router := newRouter(t, health.Check{
		Name:     "postgres",
		Critical: true,
		Probe:    func(context.Context) error { return errors.New("connection refused") },
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "connection refused")
}

func TestReadinessTimesOutSlowProbes(t *testing.T) {
	router := newRouter(t, health.Check{
		Name:     "postgres",
		Critical: true,
		Timeout:  10 * time.Millisecond,
		Probe: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				return nil
			}
		},
	})

	start := time.Now()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	assert.Less(t, time.Since(start), time.Second, "probe must respect its timeout")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "timeout")
}

func TestAPIMetaEndpoint(t *testing.T) {
	router := newRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"service": "channelhunter",
		"version": "test",
		"environment": "development",
		"api_version": "v1",
		"time": "`+jsonTime(t, rec)+`"
	}`, rec.Body.String())
}

// jsonTime extracts the "time" value so the assertion stays exact.
func jsonTime(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	value, ok := body["time"].(string)
	require.True(t, ok)
	return value
}

func TestUnknownRouteReturnsErrorEnvelope(t *testing.T) {
	router := newRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "NOT_FOUND", envelope.Error.Code)
	assert.NotEmpty(t, envelope.Error.Message)
	assert.Equal(t, rec.Header().Get(middleware.RequestIDHeader), envelope.Error.RequestID)
}

func TestRateLimitAppliesToAPIPrefixOnly(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cfg := testConfig()
	cfg.RateLimit.Requests = 2

	router := routes.New(routes.Deps{
		Config:      cfg,
		Logger:      log,
		Health:      health.NewChecker("channelhunter", "api", "test", "test", time.Now(), log),
		Version:     "test",
		RateLimiter: middleware.NewRateLimiter(2, time.Minute),
	})

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)

	// Probes stay reachable for the orchestrator even while the API is throttled.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestProbeAliases(t *testing.T) {
	router := newRouter(t)
	for _, path := range []string{"/healthz", "/livez"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}
	for _, path := range []string{"/readyz"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}
}
