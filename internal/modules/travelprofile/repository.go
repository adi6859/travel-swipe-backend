package travelprofile

import (
	"context"
	"database/sql"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
)

const (
	pgForeignKeyViolation   = "23503"
	fkTravelProfileUser     = "travel_profiles_user_id_fkey"
	fkTravelProfileInterest = "travel_profile_interests_interest_slug_fkey"
)

// Repository is the PostgreSQL store for travel profiles. Methods join the
// caller's transaction when one is present in ctx.
type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) ActiveInterests(ctx context.Context) ([]Interest, error) {
	out := []Interest{}
	err := database.Conn(ctx, r.db).SelectContext(ctx, &out,
		`SELECT slug, label, category FROM interests WHERE active ORDER BY sort_order, slug`)
	return out, err
}

// profileRow carries arrays as JSON so they scan through database/sql without
// driver-specific array types.
type profileRow struct {
	UserID       uuid.UUID `db:"user_id"`
	TravelStyles []byte    `db:"travel_styles"`
	Interests    []byte    `db:"interests"`
	Languages    []byte    `db:"languages"`
	BudgetBand   *string   `db:"budget_band"`
	GroupSize    *string   `db:"group_size"`
	Pace         *string   `db:"pace"`
	Smoking      *string   `db:"smoking"`
	Drinking     *string   `db:"drinking"`
	Diet         *string   `db:"diet"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// Get returns the user's profile, or an empty profile when none is saved.
func (r *Repository) Get(ctx context.Context, userID uuid.UUID) (Profile, error) {
	var row profileRow
	err := database.Conn(ctx, r.db).GetContext(ctx, &row, `
		SELECT tp.user_id,
		       array_to_json(tp.travel_styles) AS travel_styles,
		       array_to_json(tp.languages)     AS languages,
		       COALESCE((
		           SELECT json_agg(tpi.interest_slug ORDER BY i.sort_order, i.slug)
		           FROM travel_profile_interests tpi
		           JOIN interests i ON i.slug = tpi.interest_slug
		           WHERE tpi.user_id = tp.user_id
		       ), '[]'::json) AS interests,
		       tp.budget_band, tp.group_size, tp.pace, tp.smoking, tp.drinking, tp.diet, tp.updated_at
		FROM travel_profiles tp
		WHERE tp.user_id = $1`, userID)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return emptyProfile(userID), nil
	}
	if err != nil {
		return Profile{}, err
	}

	p := Profile{
		UserID:     row.UserID,
		BudgetBand: row.BudgetBand,
		GroupSize:  row.GroupSize,
		Pace:       row.Pace,
		Smoking:    row.Smoking,
		Drinking:   row.Drinking,
		Diet:       row.Diet,
		UpdatedAt:  &row.UpdatedAt,
	}
	for _, f := range []struct {
		raw []byte
		dst *[]string
	}{{row.TravelStyles, &p.TravelStyles}, {row.Interests, &p.Interests}, {row.Languages, &p.Languages}} {
		*f.dst = []string{}
		if err := json.Unmarshal(f.raw, f.dst); err != nil {
			return Profile{}, fmt.Errorf("decode travel profile arrays: %w", err)
		}
	}
	return p, nil
}

// Save upserts the profile and replaces its interests. Callers must run it in a
// transaction so the profile and its interests change together.
func (r *Repository) Save(ctx context.Context, p Profile, at time.Time) error {
	conn := database.Conn(ctx, r.db)
	_, err := conn.ExecContext(ctx, `
		INSERT INTO travel_profiles
		    (user_id, travel_styles, languages, budget_band, group_size, pace, smoking, drinking, diet, created_at, updated_at)
		VALUES ($1, $2::text[], $3::text[], $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (user_id) DO UPDATE SET
		    travel_styles = EXCLUDED.travel_styles,
		    languages     = EXCLUDED.languages,
		    budget_band   = EXCLUDED.budget_band,
		    group_size    = EXCLUDED.group_size,
		    pace          = EXCLUDED.pace,
		    smoking       = EXCLUDED.smoking,
		    drinking      = EXCLUDED.drinking,
		    diet          = EXCLUDED.diet,
		    updated_at    = EXCLUDED.updated_at`,
		p.UserID, nonNil(p.TravelStyles), nonNil(p.Languages),
		p.BudgetBand, p.GroupSize, p.Pace, p.Smoking, p.Drinking, p.Diet, at)
	if err != nil {
		return mapFKErr(err)
	}

	if _, err := conn.ExecContext(ctx, `DELETE FROM travel_profile_interests WHERE user_id = $1`, p.UserID); err != nil {
		return err
	}
	if len(p.Interests) == 0 {
		return nil
	}
	_, err = conn.ExecContext(ctx,
		`INSERT INTO travel_profile_interests (user_id, interest_slug) SELECT $1, unnest($2::text[])`,
		p.UserID, p.Interests)
	return mapFKErr(err)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func mapFKErr(err error) error {
	var pgErr *pgconn.PgError
	if stdErrors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		switch pgErr.ConstraintName {
		case fkTravelProfileUser:
			return ErrUserNotFound
		case fkTravelProfileInterest:
			return ErrUnknownInterest
		}
	}
	return err
}
