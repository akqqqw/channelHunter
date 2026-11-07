// Package database owns the PostgreSQL connection pool, health checks,
// transactions and schema migrations for every ChannelHunter process.
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/akqqqw/channelhunter/backend/internal/config"
)

// DB wraps a pgx pool and exposes the small surface the rest of the code needs.
type DB struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// Querier is implemented by both *pgxpool.Pool and pgx.Tx, which lets
// repositories take part in a transaction without knowing it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TxFunc is the unit of work executed inside a transaction.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// Open connects to PostgreSQL and verifies the connection.
func Open(ctx context.Context, cfg config.DatabaseConfig, log *slog.Logger) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	// Apply defaults for unset values: pgxpool panics on a zero
	// HealthCheckPeriod, and a caller (test, tool, future service) should not
	// have to repeat production tuning just to open a pool.
	cfg = withDefaults(cfg)
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	// Server-side timeouts are a second line of defence: a query can never
	// block a pool connection forever, even if the caller forgets a ctx.
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "channelhunter"
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = "30000"
	poolCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "60000"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	db := &DB{pool: pool, log: log}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Info("database connected",
		slog.Int("max_conns", int(cfg.MaxConns)),
		slog.String("host", poolCfg.ConnConfig.Host),
		slog.String("database", poolCfg.ConnConfig.Database),
	)
	return db, nil
}

// withDefaults fills zero values with safe defaults.
func withDefaults(cfg config.DatabaseConfig) config.DatabaseConfig {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 10
	}
	if cfg.MinConns < 0 {
		cfg.MinConns = 0
	}
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = time.Hour
	}
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 30 * time.Minute
	}
	if cfg.HealthCheckPeriod <= 0 {
		cfg.HealthCheckPeriod = time.Minute
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	return cfg
}

// Close releases all pooled connections.
func (db *DB) Close() {
	if db == nil || db.pool == nil {
		return
	}
	db.pool.Close()
}

// Pool exposes the underlying pool for repositories.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Ping checks database availability with a bounded timeout.
func (db *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return db.pool.Ping(ctx)
}

// InTx runs fn inside a transaction, committing on success and rolling back on
// error or panic. Nested calls join the outer transaction.
func (db *DB) InTx(ctx context.Context, fn TxFunc) error {
	if tx, ok := txFromContext(ctx); ok {
		return fn(ctx, tx)
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			db.log.Error("transaction rollback failed", slog.String("error", rbErr.Error()))
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type txContextKey struct{}

// WithTx makes an existing transaction available to nested service calls.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txContextKey{}).(pgx.Tx)
	return tx, ok
}

// IsUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation (SQLSTATE 23505) — used for idempotent upserts.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsForeignKeyViolation reports whether err violates a foreign key (23503).
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// IsCheckViolation reports whether err violates a CHECK constraint (23514).
func IsCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
