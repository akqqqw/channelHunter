package database

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/stdlib"
)

// MigrationsTable is the bookkeeping table maintained by golang-migrate.
const MigrationsTable = "schema_migrations"

// Migrator applies the versioned schema migrations that ship with the binary.
type Migrator struct {
	m   *migrate.Migrate
	sql *sql.DB
	log *slog.Logger
}

// NewMigrator creates a migrator for the configured database.
//
// The source is the migration set embedded in the binary, so containers never
// need the .sql files on disk. A non-empty migrationsPath overrides it with the
// directory contents, which is convenient while editing migrations.
func NewMigrator(db *DB, migrationsPath string, embedded fs.FS, log *slog.Logger) (*Migrator, error) {
	// golang-migrate's pgx driver keeps a dedicated connection open for its
	// advisory lock, so it gets its own database/sql DB on top of pgx stdlib.
	sqlDB := stdlib.OpenDBFromPool(db.pool)

	driver, err := migratepgx.WithInstance(sqlDB, &migratepgx.Config{
		MigrationsTable: MigrationsTable,
		DatabaseName:    db.pool.Config().ConnConfig.Database,
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("create migration driver: %w", err)
	}

	src, err := newSource(migrationsPath, embedded, log)
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	return &Migrator{m: m, sql: sqlDB, log: log}, nil
}

// newSource prefers the on-disk directory when it exists and falls back to the
// embedded filesystem.
func newSource(migrationsPath string, embedded fs.FS, log *slog.Logger) (source.Driver, error) {
	if migrationsPath != "" {
		if drv, err := iofs.New(os.DirFS(migrationsPath), "."); err == nil {
			log.Info("using migrations from directory", slog.String("path", migrationsPath))
			return drv, nil
		} else if embedded == nil {
			return nil, fmt.Errorf("open migrations directory %q: %w", migrationsPath, err)
		} else {
			log.Warn("migrations directory unavailable, using embedded migrations",
				slog.String("path", migrationsPath), slog.String("error", err.Error()))
		}
	}
	if embedded == nil {
		return nil, errors.New("no migration source configured")
	}
	drv, err := iofs.New(embedded, ".")
	if err != nil {
		return nil, fmt.Errorf("create embedded migration source: %w", err)
	}
	return drv, nil
}

// Up applies all pending migrations. Safe to call from several processes at
// once: golang-migrate serialises them with a PostgreSQL advisory lock.
func (m *Migrator) Up() error {
	err := m.m.Up()
	switch {
	case errors.Is(err, migrate.ErrNoChange):
		m.log.Info("database schema is up to date")
		return nil
	case err != nil:
		return fmt.Errorf("apply migrations: %w", err)
	default:
		m.log.Info("database migrations applied")
		return nil
	}
}

// Version reports the currently applied migration version and whether the last
// migration failed halfway.
func (m *Migrator) Version() (version uint, dirty bool, err error) {
	v, dirty, err := m.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return v, dirty, err
}

// Steps moves the schema by n migrations (negative = down). Development only.
func (m *Migrator) Steps(n int) error {
	if err := m.m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate %d steps: %w", n, err)
	}
	return nil
}

// Force marks the schema as being at version v (recovery from a dirty state).
func (m *Migrator) Force(v int) error {
	if err := m.m.Force(v); err != nil {
		return fmt.Errorf("force version %d: %w", v, err)
	}
	return nil
}

// Close releases migration resources.
func (m *Migrator) Close() error {
	srcErr, dbErr := m.m.Close()
	if m.sql != nil {
		if err := m.sql.Close(); err != nil {
			return errors.Join(srcErr, dbErr, err)
		}
	}
	return errors.Join(srcErr, dbErr)
}
