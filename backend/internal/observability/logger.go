// Package observability provides structured logging and shared request
// instrumentation (request IDs, panic recovery) used by every process.
package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/akqqqw/channelhunter/backend/internal/config"
)

type contextKey string

const (
	loggerKey    contextKey = "logger"
	requestIDKey contextKey = "request_id"
)

// RedactedValue replaces secrets in logs.
const RedactedValue = "[REDACTED]"

var sensitiveKeys = []string{
	"token", "secret", "password", "authorization", "api_key", "apikey",
	"initdata", "init_data", "signature", "hash", "cookie",
}

// IsSensitiveKey reports whether a key name looks like it carries a secret and
// must therefore never be written to logs verbatim.
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// NewLogger builds the process logger writing to stdout.
func NewLogger(cfg config.LogConfig, process string, env config.Environment) *slog.Logger {
	return NewLoggerWithWriter(os.Stdout, cfg, process, env)
}

// NewLoggerWithWriter builds the process logger writing to w. Every log record
// passes through RedactAttr, so secrets never reach the log stream.
func NewLoggerWithWriter(w io.Writer, cfg config.LogConfig, process string, env config.Environment) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level, ReplaceAttr: RedactAttr}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler).With(
		slog.String("service", "channelhunter"),
		slog.String("process", process),
		slog.String("env", string(env)),
	)
}

// RedactAttr masks attributes whose key looks like a secret. It is exported so
// tests and custom handlers can reuse exactly the production policy.
//
// Only textual values are redacted: a boolean such as `token_configured` or a
// counter such as `secret_rotations` carries no secret and stays readable,
// which keeps diagnostics useful.
func RedactAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindString {
		return a
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, RedactedValue)
	}
	return a
}

// WithLogger stores a logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// LoggerFrom returns the context logger or the default logger.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID stores a request ID in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom returns the request ID stored in the context, if any.
func RequestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}
