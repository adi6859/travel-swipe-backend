// Command seed loads sample treks into a development database through the
// regular ingest pipeline, so the Explore API has data to show. Providers and
// prices are fictional and booking links point to example.com.
//
// Requires DATABASE_URL; refuses to run when APP_ENV=production.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/adi6859/travel-swipe-backend/internal/config"
	"github.com/adi6859/travel-swipe-backend/internal/modules/ingest"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/logger"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
)

const sourceKey = "travelswipe_samples"

//go:embed treks.json
var treksJSON []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	env := os.Getenv("APP_ENV")
	if env == "production" {
		return errors.New("seeding is not allowed when APP_ENV=production")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}

	var listings []ingest.Listing
	dec := json.NewDecoder(bytes.NewReader(treksJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&listings); err != nil {
		return fmt.Errorf("decode treks.json: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, config.PostgresConfig{
		URL: dsn, MaxOpenConns: 2, MaxIdleConns: 1, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		return err
	}
	defer db.Close()

	if env == "" {
		env = "development"
	}
	svc := ingest.NewService(ingest.NewRepository(db), database.NewTxManager(db), clock.RealClock{}, logger.New("travel-swipe-seed", env, "info"))

	attribution := "Sample data for development"
	if _, err := svc.PutSource(ctx, sourceKey, ingest.SourceInput{
		DisplayName:        "TravelSwipe sample treks",
		BaseURL:            "https://example.com",
		Enabled:            true,
		AutoPublish:        true,
		RightsBasis:        ingest.BasisFirstParty,
		DescriptionAllowed: true,
		AttributionText:    &attribution,
		EvidenceRef:        "cmd/seed/treks.json (original sample content)",
		MissingGraceRuns:   1,
	}); err != nil {
		return fmt.Errorf("put source: %w", err)
	}

	result, err := svc.Import(ctx, sourceKey, ingest.Batch{Trigger: ingest.TriggerSeed, Complete: true, Listings: listings})
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	fmt.Printf("seeded %s: run %s %s, received=%d inserted=%d updated=%d unchanged=%d rejected=%d expired=%d\n",
		sourceKey, result.ID, result.Status, result.ReceivedCount, result.InsertedCount, result.UpdatedCount,
		result.UnchangedCount, result.RejectedCount, result.ExpiredCount)
	if result.RejectedCount > 0 {
		out, _ := json.MarshalIndent(result.Rejections, "", "  ")
		return fmt.Errorf("%d listings rejected:\n%s", result.RejectedCount, out)
	}
	return nil
}
