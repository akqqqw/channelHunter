// Package server runs the HTTP API with production-grade lifecycle handling:
// bounded timeouts, graceful shutdown on SIGTERM/SIGINT and structured errors.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/akqqqw/channelhunter/backend/internal/config"
)

// Server wraps an http.Server plus its configuration.
type Server struct {
	http *http.Server
	cfg  config.HTTPConfig
	log  *slog.Logger
}

// New builds a server. The handler is provided by the caller so tests can inject
// routers without touching process wiring.
//
// Note on shutdown: net/http treats a connection in StateNew (TCP accepted but
// no request byte received yet) as idle only after 5 hardcoded seconds, so a
// slow client can hold up Shutdown for that long. ReadHeaderTimeout therefore
// stays well below any sane shutdown budget (see Validate in config).
func New(cfg config.HTTPConfig, handler http.Handler, log *slog.Logger) *Server {
	return &Server{
		http: &http.Server{
			Addr:    cfg.Addr(),
			Handler: handler,
			// Bounds the window described above and mitigates slowloris.
			ReadHeaderTimeout: 3 * time.Second,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			MaxHeaderBytes:    1 << 20,
			BaseContext: func(net.Listener) context.Context {
				return context.Background()
			},
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		cfg: cfg,
		log: log,
	}
}

// Start listens and serves until ctx is cancelled, then shuts down gracefully.
//
// Binding happens before serving so that a port conflict is reported
// synchronously (a container should crash fast, not report healthy).
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Addr(), err)
	}

	s.log.Info("http server listening",
		slog.String("addr", s.cfg.Addr()),
		slog.Duration("read_timeout", s.cfg.ReadTimeout),
		slog.Duration("write_timeout", s.cfg.WriteTimeout),
	)

	errCh := make(chan error, 1)
	go func() {
		if serveErr := s.http.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.log.Info("shutdown signal received, draining connections",
			slog.Duration("timeout", s.cfg.ShutdownTimeout))
		return s.Shutdown()
	}
}

// Shutdown stops accepting connections and waits for in-flight requests.
func (s *Server) Shutdown() error {
	stopCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.http.Shutdown(stopCtx); err != nil {
		// Give the operator a clear signal and force-close.
		_ = s.http.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	s.log.Info("http server stopped")
	return nil
}

// Addr returns the configured listen address (useful for tests).
func (s *Server) Addr() string { return s.cfg.Addr() }
