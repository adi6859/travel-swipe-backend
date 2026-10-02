// Package dbtest provisions an isolated, migrated PostgreSQL schema per test.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/migrations"
)

// Open returns a connection whose search_path is a fresh schema with all
// migrations applied; the schema is dropped on cleanup. It skips the test when
// TEST_DATABASE_URL (URL form) is unset.
func Open(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database test")
	}
	ctx := context.Background()

	admin, err := sqlx.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	schema := "test_" + hex.EncodeToString(buf)
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`) })

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	db, err := sqlx.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("open test connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrations.RunWithDB(ctx, db.DB, "up"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
