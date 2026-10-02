// Package users owns user accounts and profiles.
package users

import (
	stdErrors "errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

var (
	ErrNotFound   = stdErrors.New("users: not found")
	ErrPhoneTaken = stdErrors.New("users: phone already registered")
)

// User is the account record. Profile fields live in Profile, never here.
type User struct {
	ID              uuid.UUID  `db:"id"`
	PhoneE164       string     `db:"phone_e164"`
	Status          Status     `db:"status"`
	PhoneVerifiedAt time.Time  `db:"phone_verified_at"`
	LastLoginAt     *time.Time `db:"last_login_at"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
}

func (u User) IsActive() bool {
	return u.Status == StatusActive
}

// Avatar is metadata for an already-uploaded profile photo. The backend stores
// the reference only; uploading is a separate concern.
type Avatar struct {
	URL       string `json:"url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
}

// Profile holds user-editable public details, kept apart from credentials.
type Profile struct {
	UserID      uuid.UUID  `db:"user_id"`
	DisplayName string     `db:"display_name"`
	Bio         string     `db:"bio"`
	AvatarURL   *string    `db:"avatar_url"`
	AvatarMeta  []byte     `db:"avatar_meta"`
	DateOfBirth *time.Time `db:"date_of_birth"`
	Gender      *string    `db:"gender"`
	HomeCity    *string    `db:"home_city"`
	CountryCode *string    `db:"country_code"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

// Me is the authenticated user's own account view.
type Me struct {
	User    User
	Profile Profile
}

// ProfileChanges is a validated set of column assignments. A nil value clears
// the column.
type ProfileChanges struct {
	columns []string
	values  []any
}

func (c *ProfileChanges) set(column string, value any) {
	c.columns = append(c.columns, column)
	c.values = append(c.values, value)
}

func (c *ProfileChanges) Empty() bool {
	return len(c.columns) == 0
}
