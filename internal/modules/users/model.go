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
