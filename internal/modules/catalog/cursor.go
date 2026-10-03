package catalog

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// Cursors are opaque keyset positions bound to the filter set that produced
// them (adapted from Krozd's scope-bound social graph cursors), so a cursor
// cannot be replayed against different filters.
const cursorVersion = 1

type cursorPayload struct {
	Version int       `json:"v"`
	Filter  string    `json:"f"`
	Key     float64   `json:"k"`
	ID      uuid.UUID `json:"id"`
}

type position struct {
	Key float64
	ID  uuid.UUID
}

func filterFingerprint(f Filter) string {
	var month string
	if f.Month != nil {
		month = f.Month.Format("2006-01")
	}
	canonical := fmt.Sprintf("%q|%q|%q|%q|%q|%v|%v|%v|%q|%q",
		f.Query, f.Category, f.Interest, f.Difficulty, f.DepartureCity,
		deref(f.MinDays), deref(f.MaxDays), deref64(f.MaxPricePaise), month, f.Sort)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8])
}

func encodeCursor(f Filter, last TripCard) string {
	data, _ := json.Marshal(cursorPayload{Version: cursorVersion, Filter: filterFingerprint(f), Key: last.SortKey, ID: last.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(f Filter) (*position, error) {
	if f.Cursor == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(f.Cursor)
	if err != nil {
		return nil, err
	}
	var p cursorPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Version != cursorVersion || p.Filter != filterFingerprint(f) || p.ID == uuid.Nil || math.IsNaN(p.Key) || math.IsInf(p.Key, 0) {
		return nil, fmt.Errorf("cursor does not match")
	}
	return &position{Key: p.Key, ID: p.ID}, nil
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func deref64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
