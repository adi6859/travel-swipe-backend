// Package migrations embeds the Goose SQL migrations so binaries never depend on
// migration files being present on disk.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// Files returns the embedded migration filesystem.
func Files() fs.FS {
	return files
}

// Run executes a Goose command ("up", "down", "status", "version", "reset").
func Run(ctx context.Context, dsn, command string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	return RunWithDB(ctx, db, command)
}

func RunWithDB(ctx context.Context, db *sql.DB, command string) error {
	goose.SetBaseFS(files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.RunContext(ctx, command, db, ".")
}
