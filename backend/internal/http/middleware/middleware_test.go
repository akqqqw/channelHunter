package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akqqqw/channelhunter/backend/internal/http/middleware"
	"github.com/akqqqw/channelhunter/backend/internal/observability"
	"github.com/akqqqw/channelhunter/backend/internal/platform/apierror"
	"github.com/akqqqw/channelhunter/backend/internal/platform/httpx"
)

func echoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, r, http.StatusOK, map[string]string{
			"request_id": observability.RequestIDFrom(r.Context()),
		})
	})
}

func TestRequestIDGeneratesAndPropagates(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	h := middleware.RequestID(log)(echoHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body["request_id"])
	assert.Equal(t, body["request_id"], rec.Header().Get(middleware.RequestIDHeader))

	// A provided ID is kept (useful for cross-service tracing).
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.RequestIDHeader, "existing-id")
	h.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), "existing-id")
}

func TestRequestIDRejectsOverlongHeader(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	h := middleware.RequestID(log)(echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.RequestIDHeader, string(bytes.Repeat([]byte("a"), 200)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.NotEqual(t, 200, len(rec.Header().Get(middleware.RequestIDHeader)))
}

func TestRecoverConvertsPanicTo500Envelope(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	panicky := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("unexpected nil map")
	})
	h := middleware.RequestID(log)(middleware.Recover()(panicky))

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")
	assert.NotContains(t, rec.Body.String(), "unexpected nil map")
}

func TestAccessLogRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := middleware.RequestID(log)(middleware.AccessLog(nil)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpx.Fail(w, r, apierror.NotFound("канал"))
		})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/channels/x", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, buf.String(), `"status":404`)
	assert.Contains(t, buf.String(), `"path":"/api/v1/channels/x"`)
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	middleware.NotFound().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "NOT_FOUND")

	rec = httptest.NewRecorder()
	middleware.MethodNotAllowed().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/health", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSecurityHeaders(t *testing.T) {
	h := middleware.SecurityHeaders()(echoHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
}

func TestCORSPolicy(t *testing.T) {
	h := middleware.CORS([]string{"https://app.example.com"})(echoHandler())

	// Allowed origin gets headers.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, "https://app.example.com", rec.Header().Get("Access-Control-Allow-Origin"))

	// Preflight is answered without reaching the handler.
	req = httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Foreign origins never receive CORS headers (no reflection).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestClientIPIgnoresSpoofedForwardedHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:4567"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	assert.Equal(t, "203.0.113.9", middleware.ClientIP(req, nil))
}

func TestClientIPUsesForwardedHeaderBehindTrustedProxy(t *testing.T) {
	trusted, err := middleware.ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")

	assert.Equal(t, "203.0.113.9", middleware.ClientIP(req, trusted))
}

func TestParseTrustedProxies(t *testing.T) {
	nets, err := middleware.ParseTrustedProxies([]string{"10.0.0.0/8", "192.168.1.1", " ", "::1"})
	require.NoError(t, err)
	assert.Len(t, nets, 3)

	_, err = middleware.ParseTrustedProxies([]string{"not-a-cidr/99"})
	require.Error(t, err)
}

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	limiter := middleware.NewRateLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		allow, remaining, _ := limiter.Allow("client-a")
		require.True(t, allow, "request %d should pass", i+1)
		assert.Equal(t, 2-i, remaining)
	}

	allow, _, retryAfter := limiter.Allow("client-a")
	assert.False(t, allow)
	assert.Greater(t, retryAfter, time.Duration(0))

	// Other clients are unaffected.
	allow, _, _ = limiter.Allow("client-b")
	assert.True(t, allow)
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	limiter := middleware.NewRateLimiter(60, time.Minute) // 1 token per second
	now := time.Now()
	limiter.SetClock(func() time.Time { return now })

	allow, _, _ := limiter.Allow("c")
	require.True(t, allow)
	for i := 0; i < 59; i++ {
		limiter.Allow("c")
	}
	allow, _, _ = limiter.Allow("c")
	require.False(t, allow)

	now = now.Add(2 * time.Second)
	allow, _, _ = limiter.Allow("c")
	assert.True(t, allow, "bucket should have refilled")
}

func TestRateLimiterCleanup(t *testing.T) {
	limiter := middleware.NewRateLimiter(10, time.Minute)
	now := time.Now()
	limiter.SetClock(func() time.Time { return now })

	limiter.Allow("old")
	assert.Equal(t, 1, limiter.Size())

	now = now.Add(time.Hour)
	limiter.Cleanup(10 * time.Minute)
	assert.Equal(t, 0, limiter.Size())
}

func TestRateLimitMiddleware(t *testing.T) {
	limiter := middleware.NewRateLimiter(1, time.Minute)
	h := middleware.RateLimit(limiter, nil, nil)(echoHandler())

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
	// 1 request per minute => roughly 60 seconds until a token is available.
	assert.NotEmpty(t, second.Header().Get("Retry-After"))
	assert.Equal(t, "0", second.Header().Get("X-RateLimit-Remaining"))
	assert.Contains(t, second.Body.String(), "RATE_LIMITED")
}
