package ingest

import (
	"context"
	stdErrors "errors"
	"log/slog"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/require"
)

var sourceKey = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

type Service struct {
	repo  *Repository
	tx    database.TxRunner
	clock clock.Clock
	log   *slog.Logger
}

func NewService(repo *Repository, tx database.TxRunner, clk clock.Clock, log *slog.Logger) *Service {
	require.AllNotNil("ingest service", map[string]any{"repo": repo, "tx": tx, "clock": clk, "log": log})
	return &Service{repo: repo, tx: tx, clock: clk, log: log}
}

func (s *Service) ListSources(ctx context.Context) ([]Source, error) {
	out, err := s.repo.ListSources(ctx)
	if err != nil {
		return nil, apperrors.Internal("list sources", err)
	}
	return out, nil
}

// PutSource creates or replaces a source's configuration and rights.
func (s *Service) PutSource(ctx context.Context, key string, in SourceInput) (Source, error) {
	if err := validateSource(key, &in); err != nil {
		return Source{}, err
	}
	var out Source
	err := s.tx.RunInTx(ctx, func(ctx context.Context) error {
		prev, err := s.repo.LockSource(ctx, key)
		var prevPtr *Source
		switch {
		case err == nil:
			prevPtr = &prev
		case !stdErrors.Is(err, ErrSourceNotFound):
			return err
		}
		out, err = s.repo.UpsertSource(ctx, key, in, prevPtr, s.clock.Now())
		return err
	})
	if err != nil {
		return Source{}, apperrors.Internal("save source", err)
	}
	return out, nil
}

func (s *Service) ListRuns(ctx context.Context, key string, limit int) ([]Run, error) {
	src, err := s.repo.GetSource(ctx, key)
	if err != nil {
		return nil, mapSourceErr(err)
	}
	runs, err := s.repo.ListRuns(ctx, src.ID, limit)
	if err != nil {
		return nil, apperrors.Internal("list runs", err)
	}
	return runs, nil
}

// Import applies one batch from a source. Invalid listings are rejected
// individually and reported on the run; the rest are applied atomically.
func (s *Service) Import(ctx context.Context, key string, b Batch) (Run, error) {
	switch {
	case b.Trigger != TriggerScraper && b.Trigger != TriggerManual && b.Trigger != TriggerSeed:
		return Run{}, apperrors.InvalidFields("request validation failed", map[string]string{"trigger": "must be scraper, manual or seed"})
	case len(b.Listings) == 0 || len(b.Listings) > maxListingsPerBatch:
		return Run{}, apperrors.InvalidFields("request validation failed",
			map[string]string{"listings": "must contain between 1 and " + strconv.Itoa(maxListingsPerBatch) + " listings"})
	}
	src, err := s.repo.GetSource(ctx, key)
	if err != nil {
		return Run{}, mapSourceErr(err)
	}
	if !src.Enabled {
		return Run{}, apperrors.Conflict("source is disabled")
	}

	run := Run{
		ID: uuid.Must(uuid.NewV7()), SourceID: src.ID, Trigger: b.Trigger, Complete: b.Complete,
		Status: RunRunning, StartedAt: s.clock.Now(), ReceivedCount: len(b.Listings), Rejections: []Rejection{},
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return Run{}, apperrors.Internal("create run", err)
	}

	applied := run
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		applied = run
		return s.apply(ctx, key, b, &applied)
	})

	finished := s.clock.Now()
	if err != nil {
		run.Status, run.FinishedAt = RunFailed, &finished
		summary := "import failed; see server logs for run " + run.ID.String()
		if finishErr := s.repo.FinishRun(ctx, run, &summary); finishErr != nil {
			s.log.ErrorContext(ctx, "record failed ingest run", "run_id", run.ID, "error", finishErr)
		}
		return Run{}, apperrors.Internal("import batch", err)
	}

	applied.Status, applied.FinishedAt = RunSucceeded, &finished
	if applied.RejectedCount > 0 {
		applied.Status = RunPartial
	}
	if err := s.repo.FinishRun(ctx, applied, nil); err != nil {
		return Run{}, apperrors.Internal("finish run", err)
	}
	s.log.InfoContext(ctx, "ingest run finished", "source", key, "run_id", applied.ID, "status", applied.Status,
		"inserted", applied.InsertedCount, "updated", applied.UpdatedCount, "unchanged", applied.UnchangedCount,
		"rejected", applied.RejectedCount, "expired", applied.ExpiredCount)
	return applied, nil
}

func (s *Service) apply(ctx context.Context, key string, b Batch, run *Run) error {
	src, err := s.repo.LockSource(ctx, key)
	if err != nil {
		return err
	}
	interests, err := s.repo.ActiveInterests(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now()

	seenInBatch := map[string]bool{}
	var touched []string
	for i, raw := range b.Listings {
		n, errs := normalize(raw, interests)
		externalID := clean(raw.ExternalID)
		if errs == nil && seenInBatch[n.ExternalID] {
			errs = map[string]string{"external_id": "appears more than once in this batch"}
		}
		if errs != nil {
			run.RejectedCount++
			run.Rejections = append(run.Rejections, Rejection{Index: i, ExternalID: externalID, Errors: errs})
			// Keep a previously good version alive rather than expiring it
			// because of a validation regression in the producer.
			if externalID != "" && utf8.RuneCountInString(externalID) <= 200 && !seenInBatch[externalID] {
				seenInBatch[externalID] = true
				touched = append(touched, externalID)
			}
			continue
		}
		seenInBatch[n.ExternalID] = true

		prev, err := s.repo.findListing(ctx, src.ID, n.ExternalID)
		if err != nil {
			return err
		}
		if prev != nil && prev.TripID != nil && prev.ContentHash == n.ContentHash {
			run.UnchangedCount++
			touched = append(touched, n.ExternalID)
			continue
		}
		inserted, err := s.repo.saveListing(ctx, src, run.ID, n, prev, now)
		if err != nil {
			return err
		}
		if inserted {
			run.InsertedCount++
		} else {
			run.UpdatedCount++
		}
	}

	if err := s.repo.touchListings(ctx, src.ID, run.ID, touched, src.rightsAt(now).publishable, now); err != nil {
		return err
	}
	if b.Complete {
		expired, err := s.repo.markMissing(ctx, src, run.ID, now)
		if err != nil {
			return err
		}
		run.ExpiredCount = expired
	}
	return nil
}

func validateSource(key string, in *SourceInput) error {
	fields := map[string]string{}
	if !sourceKey.MatchString(key) {
		fields["source_key"] = "must be 2-40 lowercase letters, digits or underscores, starting with a letter"
	}
	in.DisplayName = clean(in.DisplayName)
	if n := utf8.RuneCountInString(in.DisplayName); n == 0 || n > 100 {
		fields["display_name"] = "is required and must be at most 100 characters"
	}
	if !isHTTPURL(in.BaseURL, false) {
		fields["base_url"] = "must be an absolute http(s) URL"
	}
	if !rightsBases[in.RightsBasis] {
		fields["rights_basis"] = "must be one of: first_party, contract, affiliate, api_terms, official_publication, research_only"
	}
	if in.RightsBasis == BasisResearchOnly && in.AutoPublish {
		fields["auto_publish"] = "research_only sources can never publish"
	}
	in.EvidenceRef = clean(in.EvidenceRef)
	if n := utf8.RuneCountInString(in.EvidenceRef); n == 0 || n > 500 {
		fields["evidence_ref"] = "is required (link or note proving the rights) and must be at most 500 characters"
	}
	if in.AttributionText != nil {
		v := clean(*in.AttributionText)
		if utf8.RuneCountInString(v) > 200 {
			fields["attribution_text"] = "must be at most 200 characters"
		}
		if v == "" {
			in.AttributionText = nil
		} else {
			in.AttributionText = &v
		}
	}
	if in.MissingGraceRuns == 0 {
		in.MissingGraceRuns = 2
	}
	if in.MissingGraceRuns < 1 || in.MissingGraceRuns > 30 {
		fields["missing_grace_runs"] = "must be between 1 and 30"
	}
	if in.RightsExpiresAt != nil {
		t := in.RightsExpiresAt.UTC()
		in.RightsExpiresAt = &t
	}
	if len(fields) > 0 {
		return apperrors.InvalidFields("request validation failed", fields)
	}
	return nil
}

func mapSourceErr(err error) error {
	if stdErrors.Is(err, ErrSourceNotFound) {
		return apperrors.NotFound("source not found")
	}
	return apperrors.Internal("load source", err)
}
