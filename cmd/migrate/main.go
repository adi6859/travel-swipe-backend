// Command migrate applies the embedded Goose migrations. The API never migrates
// on startup; run this as a separate deploy step.
//
// Usage: migrate [up|down|status|version|reset]
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/adi6859/travel-swipe-backend/migrations"
)

func main() {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	if command == "reset" && os.Getenv("APP_ENV") == "production" {
		fmt.Fprintln(os.Stderr, "reset is not allowed when APP_ENV=production")
		os.Exit(1)
	}

	if err := migrations.Run(context.Background(), dsn, command); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}
