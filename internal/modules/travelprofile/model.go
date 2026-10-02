// Package travelprofile owns how a user likes to travel: styles, interests,
// languages, budget and habits. These answers are inputs to matching.
package travelprofile

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	maxTravelStyles = 3
	maxInterests    = 10
	maxLanguages    = 8
	// minInterestsForComplete is how many interests make a profile useful for matching.
	minInterestsForComplete = 3
)

var (
	ErrUserNotFound    = errors.New("travelprofile: user not found")
	ErrUnknownInterest = errors.New("travelprofile: unknown interest")
)

type Interest struct {
	Slug     string `db:"slug" json:"slug"`
	Label    string `db:"label" json:"label"`
	Category string `db:"category" json:"category"`
}

// Profile is a user's travel profile. Slices are never nil. UpdatedAt is nil
// when the user has not saved a profile yet.
type Profile struct {
	UserID       uuid.UUID
	TravelStyles []string
	Interests    []string
	Languages    []string
	BudgetBand   *string
	GroupSize    *string
	Pace         *string
	Smoking      *string
	Drinking     *string
	Diet         *string
	UpdatedAt    *time.Time
}

func emptyProfile(userID uuid.UUID) Profile {
	return Profile{UserID: userID, TravelStyles: []string{}, Interests: []string{}, Languages: []string{}}
}

// Complete reports whether the profile has enough answers for matching.
func (p Profile) Complete() bool {
	return len(p.TravelStyles) > 0 &&
		len(p.Interests) >= minInterestsForComplete &&
		len(p.Languages) > 0 &&
		p.BudgetBand != nil
}

// Input is a full replacement of the travel profile. Nil lists mean empty and
// nil scalars mean unset.
type Input struct {
	TravelStyles []string
	Interests    []string
	Languages    []string
	BudgetBand   *string
	GroupSize    *string
	Pace         *string
	Smoking      *string
	Drinking     *string
	Diet         *string
}

// Options lists every allowed value, for clients to render pickers.
type Options struct {
	TravelStyles []Option   `json:"travel_styles"`
	Interests    []Interest `json:"interests"`
	Languages    []Option   `json:"languages"`
	BudgetBands  []Option   `json:"budget_bands"`
	GroupSizes   []Option   `json:"group_sizes"`
	Paces        []Option   `json:"paces"`
	Smoking      []Option   `json:"smoking"`
	Drinking     []Option   `json:"drinking"`
	Diets        []Option   `json:"diets"`
}
