// Package middleware contains the HTTP middleware chain used by the API:
// request IDs, structured access logging, panic recovery, client IP
// resolution, CORS policy, rate limiting and authentication.
package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/akqqqw/channelhunter/backend/internal/observability"
	"github.com/akqqqw/channelhunter/backend/internal/platform/apierror"
	"github.com/akqqqw/channelhunter/backend/internal/platform/httpx"
)

// RequestIDHeader is echoed back to clients and included in error payloads.
const RequestIDHeader = "X-Request-ID"

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap allows http.ResponseController to reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// RequestID assigns (or propagates) a request ID and puts it together with a
// request-scoped logger into the context.
func RequestID(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get(RequestIDHeader))
			if id == "" || len(id) > 128 {
				id = newRequestID()
			}
			ctx := observability.WithRequestID(r.Context(), id)

			ctx = observability.WithLogger(ctx, base.With(slog.String("request_id", id)))

			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AccessLog writes one structured log line per request. The client IP is
// resolved through the trusted-proxy list so X-Forwarded-For cannot be spoofed.
func AccessLog(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}

			observability.LoggerFrom(r.Context()).Log(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_addr", ClientIP(r, trusted)),
			)
		})
	}
}

// Recover converts panics into 500 responses and logs the stack-free summary.
func Recover() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					observability.LoggerFrom(r.Context()).Error("panic recovered",
						slog.Any("panic", rec),
						slog.String("path", r.URL.Path),
					)
					// http.ErrAbortHandler must not be turned into a response.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					httpx.Fail(w, r, apierror.New(apierror.CodeInternal, "Внутренняя ошибка сервера"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// NotFound answers unmatched routes with the uniform error envelope.
func NotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, apierror.Newf(apierror.CodeNotFound, "Маршрут %s %s не найден", r.Method, r.URL.Path))
	})
}

// MethodNotAllowed answers known paths with unsupported methods.
func MethodNotAllowed() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, apierror.Newf(apierror.CodeNotFound, "Метод %s не поддерживается для %s", r.Method, r.URL.Path))
	})
}
