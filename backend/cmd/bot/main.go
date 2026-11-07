// Command bot runs the ChannelHunter Telegram bot.
//
// Stage 1 scope: the process boots with validated configuration, connects to
// PostgreSQL and exposes liveness/readiness probes (useful in containers).
// Telegram polling/webhook, handlers and the menu are implemented in stage 2;
// the wiring points are marked below so the process never runs half
// configured.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/akqqqw/channelhunter/backend/internal/app"
	"github.com/akqqqw/channelhunter/backend/internal/config"
)

func main() {
	envFile := app.ParseFlags("../backend/.env")
	rt, err := app.Boot(app.Options{Process: config.ProcessBot, EnvFile: envFile})
	if err != nil {
		os.Stderr.WriteString("bot: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer rt.Close()

	log := rt.Logger

	log.Info("bot process started",
		slog.String("mode", rt.Config.Telegram.Mode),
		slog.Bool("token_configured", rt.Config.Telegram.BotToken != ""),
	)

	// Operational probe server. It listens on HTTP_PORT so orchestrators can
	// distinguish "bot crashed" from "bot alive but Telegram is slow".
	mux := http.NewServeMux()
	mux.HandleFunc("/health", rt.Health.LivenessHandler().ServeHTTP)
	mux.HandleFunc("/ready", rt.Health.ReadinessHandler().ServeHTTP)

	probeSrv := &http.Server{
		Addr:              rt.Config.HTTP.Addr(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	go func() {
		if serveErr := probeSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("probe server unavailable: health endpoints are down", slog.String("error", serveErr.Error()))
		}
	}()

	// --- Stage 2 integration point -----------------------------------------
	// The Telegram client, middleware chain, handler registry and the
	// polling/webhook loop are added here; see docs/architecture.md.
	// Until then the process reports readiness instead of pretending to be a
	// working bot.
	if rt.Config.Telegram.BotToken == "" {
		log.Warn("TELEGRAM_BOT_TOKEN is empty: bot interface is not available")
	}

	<-rt.Context.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), rt.Config.HTTP.ShutdownTimeout)
	defer cancel()
	if err := probeSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("probe server shutdown", slog.String("error", err.Error()))
	}
	log.Info("bot process stopped")
}
