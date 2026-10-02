// Package database owns the PostgreSQL connection pool and transaction plumbing.
package database

import (
	"context"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/internal/config"
)

// Open creates the pool and verifies connectivity so startup fails fast when
// PostgreSQL is unreachable.
func Open(ctx context.Context, cfg config.PostgresConfig) (*sqlx.DB, error) {
	db, err := sqlx.Open("pgx", cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres startup ping failed: %w", err)
	}
	return db, nil
}

// Executor is the subset of sql operations used by repositories.
// Both *sqlx.DB and *sqlx.Tx satisfy it.
type Executor interface {
	sqlx.ExtContext
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
}

var (
	_ Executor = (*sqlx.DB)(nil)
	_ Executor = (*sqlx.Tx)(nil)
)

// Conn returns the transaction stored in ctx when present, otherwise db, so the
// same repository code works inside and outside a unit of work.
func Conn(ctx context.Context, db *sqlx.DB) Executor {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return db
}
