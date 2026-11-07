//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/http/health"
	"github.com/akqqqw/channelhunter/backend/internal/http/middleware"
	"github.com/akqqqw/channelhunter/backend/internal/http/routes"
	"github.com/akqqqw/channelhunter/backend/internal/http/server"
	"github.com/akqqqw/channelhunter/backend/tests/testsupport"
)

// freePort asks the kernel for an unused port so parallel tests never collide.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func startServer(t *testing.T) (baseURL string) {
	t.Helper()

	db := testsupport.NewDB(t)
	log := testsupport.TestLogger(t)
	port := freePort(t)

	cfg := &config.Config{
		Process:     config.ProcessAPI,
		Environment: config.EnvDevelopment,
		HTTP: config.HTTPConfig{
			Host:         "127.0.0.1",
			Port:         port,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			IdleTimeout:  time.Minute,
			// Must exceed the 5s window during which net/http counts brand-new
			// connections as busy.
			ShutdownTimeout: 10 * time.Second,
		},
		RateLimit: config.RateLimitConfig{Enabled: false},
		App:       config.AppConfig{Timezone: "UTC"},
	}

	checker := health.NewChecker("channelhunter", "api", "test", "test", time.Now(), log,
		health.Check{Name: "postgres", Critical: true,
			Probe: func(ctx context.Context) error { return db.Ping(ctx) }},
	)

	handler := routes.New(routes.Deps{
		Config:      cfg,
		Logger:      log,
		Health:      checker,
		Version:     "e2e",
		RateLimiter: middleware.NewRateLimiter(1000, time.Minute),
	})

	srv := server.New(cfg.HTTP, handler, log)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, base+"/health")

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err, "server must shut down cleanly")
		case <-time.After(10 * time.Second):
			t.Fatal("server did not shut down")
		}
	})

	return base
}

func waitForServer(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become healthy", url)
}

func getJSON(t *testing.T, url string, out any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, out), "body: %s", string(body))
	return resp
}

func TestLiveServerHealthAndReadiness(t *testing.T) {
	base := startServer(t)

	var live map[string]any
	resp := getJSON(t, base+"/health", &live)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", live["status"])

	var ready health.Report
	resp = getJSON(t, base+"/ready", &ready)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, health.StatusOK, ready.Status)
	assert.Equal(t, health.StatusOK, ready.Dependencies["postgres"].Status)
	assert.NotEmpty(t, resp.Header.Get(middleware.RequestIDHeader))
}

func TestLiveServerEnvelopeAndSecurityHeaders(t *testing.T) {
	base := startServer(t)

	resp, err := http.Get(base + "/api/v1/unknown")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))

	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &envelope))
	assert.Equal(t, "NOT_FOUND", envelope.Error.Code)
	assert.NotEmpty(t, envelope.Error.RequestID)
}

func TestLiveServerUnsupportedMethod(t *testing.T) {
	base := startServer(t)

	req, err := http.NewRequest(http.MethodPost, base+"/health", bytes.NewReader(nil))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServerReturnsErrorOnPortConflict(t *testing.T) {
	db := testsupport.NewDB(t)
	log := testsupport.TestLogger(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Process: config.ProcessAPI,
		HTTP: config.HTTPConfig{
			Host: "127.0.0.1", Port: port,
			ReadTimeout: time.Second, WriteTimeout: time.Second,
			ShutdownTimeout: time.Second,
		},
	}
	checker := health.NewChecker("channelhunter", "api", "test", "test", time.Now(), log,
		health.Check{Name: "postgres", Critical: true, Probe: func(ctx context.Context) error { return db.Ping(ctx) }})
	handler := routes.New(routes.Deps{
		Config: cfg, Logger: log, Health: checker, Version: "e2e",
		RateLimiter: middleware.NewRateLimiter(10, time.Minute),
	})

	srv := server.New(cfg.HTTP, handler, log)
	err = srv.Start(t.Context())
	require.Error(t, err, "a busy port must fail fast instead of reporting healthy")
	assert.Contains(t, err.Error(), "listen")
}
