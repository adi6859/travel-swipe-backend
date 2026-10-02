// Package patch provides tri-state JSON fields for partial (PATCH) updates:
// absent (leave unchanged), null (clear), or a value.
package patch

import (
	"bytes"
	"encoding/json"
)

type Field[T any] struct {
	Set   bool // present in the payload
	Null  bool // present and explicitly null
	Value T
}

func (f *Field[T]) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Null = true
		return nil
	}
	return json.Unmarshal(data, &f.Value)
}

// Of returns a field set to v.
func Of[T any](v T) Field[T] {
	return Field[T]{Set: true, Value: v}
}

// Null returns a field explicitly set to null.
func Null[T any]() Field[T] {
	return Field[T]{Set: true, Null: true}
}
