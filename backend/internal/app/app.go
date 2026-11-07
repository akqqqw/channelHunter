// Package app wires a process together: configuration, logging, database,
// migrations, health checks and graceful shutdown shared by api, bot and worker.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/database"
	"github.com/akqqqw/channelhunter/backend/internal/http/health"
	"github.com/akqqqw/channelhunter/backend/internal/observability"
	"github.com/akqqqw/channelhunter/backend/migrations"
)

// Version is overridden at build time with
// -ldflags "-X .../internal/app.Version=<git describe>".
var Version = "dev"

// Runtime bundles everything a process needs after boot.
type Runtime struct {
	Config  *config.Config
	Logger  *slog.Logger
	DB      *database.DB
	Health  *health.Checker
	Started time.Time
	// Context is cancelled when the process receives SIGINT/SIGTERM.
	Context context.Context
	cancel  context.CancelFunc
}

// Options configure the boot sequence.
type Options struct {
	Process config.Process
	// EnvFile loads defaults from a .env file (development convenience).
	EnvFile string
	// MigrateOnly skips the readiness checks that require an already migrated
	// schema; used by the one-shot `--migrate-only` command.
	MigrateOnly bool
}

// ParseFlags parses the common command line flags (--env-file).
func ParseFlags(defaultEnvFile string) string {
	envFile := flag.String("env-file", defaultEnvFile, "path to a .env file with configuration defaults")
	flag.Parse()
	return *envFile
}

// Boot initialises logging, configuration, the database and migrations.
//
// Every process goes through Boot so that behaviour (log format, migration
// policy, health probes) is identical across api, bot and worker.
func Boot(opts Options) (*Runtime, error) {
	envFile := opts.EnvFile
	if envFile == "" {
		envFile = os.Getenv("ENV_FILE")
	}

	cfg, err := loadConfig(opts.Process, envFile)
	if err != nil {
		return nil, err
	}

	log := observability.NewLogger(cfg.Log, string(cfg.Process), cfg.Environment)
	slog.SetDefault(log)

	started := time.Now().UTC()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	rootCtx := observability.WithLogger(ctx, log)

	db, err := database.Open(rootCtx, cfg.Database, log)
	if err != nil {
		stop()
		return nil, err
	}

	switch {
	case opts.MigrateOnly:
		// The caller (`--migrate-only`) applies migrations itself; doing it here
		// as well would only add a redundant advisory-lock round trip.
	case cfg.Database.ApplyOnStart:
		if err := Migrate(rootCtx, cfg, db, log); err != nil {
			db.Close()
			stop()
			return nil, err
		}
	default:
		if err := checkSchemaVersion(rootCtx, cfg, db, log); err != nil {
			db.Close()
			stop()
			return nil, err
		}
	}

	checker := health.NewChecker(
		"channelhunter", string(cfg.Process), Version, string(cfg.Environment),
		started, log,
		health.Check{
			Name:     "postgres",
			Critical: true,
			Timeout:  3 * time.Second,
			Probe:    func(ctx context.Context) error { return db.Ping(ctx) },
		},
		health.Check{
			Name:     "migrations",
			Critical: true,
			Timeout:  3 * time.Second,
			Probe:    schemaProbe(db),
		},
	)

	rt := &Runtime{
		Config:  cfg,
		Logger:  log,
		DB:      db,
		Health:  checker,
		Started: started,
		Context: rootCtx,
		cancel:  stop,
	}

	log.Info("process started",
		slog.String("version", Version),
		slog.String("env", string(cfg.Environment)),
	)

	return rt, nil
}

// Close releases resources and stops signal handling.
func (r *Runtime) Close() {
	if r.DB != nil {
		r.DB.Close()
	}
	if r.cancel != nil {
		r.cancel()
	}
}

// WaitForShutdown blocks until SIGINT/SIGTERM is received or ctx is cancelled.
func (r *Runtime) WaitForShutdown(ctx context.Context) {
	<-ctx.Done()
	r.Logger.Info("shutdown requested")
}

func loadConfig(process config.Process, envFile string) (*config.Config, error) {
	if envFile == "" {
		return config.Load(process)
	}

	cfg, err := config.LoadFromEnvFile(process, envFile)
	if err == nil {
		return cfg, nil
	}
	// A missing default env file must not stop a container that is configured
	// purely through environment variables.
	if errors.Is(err, fs.ErrNotExist) {
		cfg, cfgErr := config.Load(process)
		if cfgErr != nil {
			return nil, cfgErr
		}
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Warn(
			"env file not found, using environment only", slog.String("env_file", envFile))
		return cfg, nil
	}
	return nil, err
}

// Migrate applies every pending migration and reports the resulting version.
// It is idempotent and safe to run concurrently from several containers.
func Migrate(ctx context.Context, cfg *config.Config, db *database.DB, log *slog.Logger) error {
	migrator, err := database.NewMigrator(db, "", migrations.FS, log)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := migrator.Close(); cerr != nil {
			log.Warn("close migrator", slog.String("error", cerr.Error()))
		}
	}()
	if err := migrator.Up(); err != nil {
		return err
	}
	version, dirty, err := migrator.Version()
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if dirty {
		return fmt.Errorf("database schema is dirty at version %d", version)
	}
	log.Info("schema ready", slog.Uint64("version", uint64(version)))
	return nil
}

// checkSchemaVersion verifies that the schema was migrated (typically by the
// api container or a migration job) without touching it.
func checkSchemaVersion(ctx context.Context, cfg *config.Config, db *database.DB, log *slog.Logger) error {
	var version int64
	err := db.Pool().QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w (run `make migrate-up`)", err)
	}
	if version == 0 {
		return errors.New("database schema is empty: run `make migrate-up` or set DATABASE_MIGRATE_ON_START=true")
	}
	log.Info("schema verified", slog.Int64("version", version))
	return nil
}

func schemaProbe(db *database.DB) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		var version int64
		if err := db.Pool().QueryRow(ctx,
			`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
			return err
		}
		if version == 0 {
			return errors.New("no migrations applied")
		}
		return nil
	}
}

// Pool exposes the pgx pool for processes that need it directly.
func (r *Runtime) Pool() *pgxpool.Pool { return r.DB.Pool() }
