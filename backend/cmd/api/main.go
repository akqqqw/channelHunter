// Command api serves the ChannelHunter REST API.
//
// It is one of three processes (api, bot, worker) that share the same business
// services and PostgreSQL database. The binary also implements
// `--migrate-only`, which deploy/migrate uses so that schema handling lives in
// exactly one place.
package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/akqqqw/channelhunter/backend/internal/app"
	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/http/middleware"
	"github.com/akqqqw/channelhunter/backend/internal/http/routes"
	"github.com/akqqqw/channelhunter/backend/internal/http/server"
)

func main() {
	envFile := flag.String("env-file", ".env", "path to a .env file with configuration defaults")
	migrateOnly := flag.Bool("migrate-only", false, "apply pending migrations and exit")
	flag.Parse()

	if *migrateOnly {
		os.Exit(runMigrateOnly(*envFile))
	}
	run(*envFile)
}

func runMigrateOnly(envFile string) int {
	rt, err := app.Boot(app.Options{Process: config.ProcessAPI, EnvFile: envFile, MigrateOnly: true})
	if err != nil {
		os.Stderr.WriteString("migrate: " + err.Error() + "\n")
		return 1
	}
	defer rt.Close()

	if err := app.Migrate(rt.Context, rt.Config, rt.DB, rt.Logger); err != nil {
		rt.Logger.Error("migration failed", slog.String("error", err.Error()))
		return 1
	}
	return 0
}

func run(envFile string) {
	rt, err := app.Boot(app.Options{Process: config.ProcessAPI, EnvFile: envFile})
	if err != nil {
		// Boot failed before logging was configured: write to stderr and exit.
		os.Stderr.WriteString("api: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer rt.Close()

	log := rt.Logger
	cfg := rt.Config

	trusted, err := middleware.ParseTrustedProxies(cfg.HTTP.TrustedProxies)
	if err != nil {
		log.Error("invalid trusted proxy configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	limiter := middleware.NewRateLimiter(cfg.RateLimit.Requests, cfg.RateLimit.Window)

	handler := routes.New(routes.Deps{
		Config:         cfg,
		Logger:         log,
		Health:         rt.Health,
		Version:        app.Version,
		TrustedProxies: trusted,
		RateLimiter:    limiter,
	})

	srv := server.New(cfg.HTTP, handler, log)

	// Periodic limiter bookkeeping so the bucket map stays bounded.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-rt.Context.Done():
				return
			case <-ticker.C:
				limiter.Cleanup(10 * time.Minute)
			}
		}
	}()

	if err := srv.Start(rt.Context); err != nil {
		log.Error("http server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
