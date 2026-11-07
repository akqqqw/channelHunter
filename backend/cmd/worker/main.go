// Command worker runs ChannelHunter background jobs.
//
// Stage 1 scope: boot, readiness probe and a heartbeat loop that proves the
// process is schedulable. The job queue, provider adapters and the notification
// pipeline land in stage 6; they are inserted where the ticker is declared.
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
	envFile := app.ParseFlags(".env")
	rt, err := app.Boot(app.Options{Process: config.ProcessWorker, EnvFile: envFile})
	if err != nil {
		os.Stderr.WriteString("worker: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer rt.Close()

	log := rt.Logger

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

	// --- Stage 6 integration point -----------------------------------------
	// scheduler := scheduler.New(...); go scheduler.Run(rt.Context)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Info("worker process started", slog.Duration("heartbeat", 30*time.Second))

	for {
		select {
		case <-rt.Context.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), rt.Config.HTTP.ShutdownTimeout)
			defer cancel()
			if err := probeSrv.Shutdown(shutdownCtx); err != nil {
				log.Warn("probe server shutdown", slog.String("error", err.Error()))
			}
			log.Info("worker process stopped")
			return
		case <-ticker.C:
			log.Debug("worker heartbeat",
				slog.String("uptime", time.Since(rt.Started).Round(time.Second).String()))
		}
	}
}
