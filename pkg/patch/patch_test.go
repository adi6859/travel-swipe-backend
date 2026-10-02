package patch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type payload struct {
	Name Field[string] `json:"name"`
	City Field[string] `json:"city"`
	Age  Field[int]    `json:"age"`
}

func TestFieldTriState(t *testing.T) {
	t.Parallel()
	var p payload
	require.NoError(t, json.Unmarshal([]byte(`{"name":"Asha","city":null}`), &p))

	require.Equal(t, Of("Asha"), p.Name)
	require.Equal(t, Null[string](), p.City)
	require.False(t, p.Age.Set, "absent field stays unset")
}

func TestFieldTypeMismatch(t *testing.T) {
	t.Parallel()
	var p payload
	require.Error(t, json.Unmarshal([]byte(`{"age":"old"}`), &p))
}
