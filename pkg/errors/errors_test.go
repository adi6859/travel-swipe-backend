package errors

import (
	stdErrors "errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeMessageHidesInternalMessages(t *testing.T) {
	t.Parallel()

	err := Internal("db exploded at 10.0.0.1", stdErrors.New("conn refused"))
	require.Equal(t, CodeInternal, CodeOf(err))
	require.Equal(t, "internal error", SafeMessage(err))
	require.ErrorContains(t, err, "conn refused")
}

func TestSafeMessageExposesSafeMessages(t *testing.T) {
	t.Parallel()

	err := Invalid("phone must be in E.164 format")
	require.Equal(t, "phone must be in E.164 format", SafeMessage(err))
}

func TestCodeOfSurvivesWrapping(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("outer: %w", Conflict("duplicate"))
	require.Equal(t, CodeConflict, CodeOf(err))
	require.True(t, IsCode(err, CodeConflict))
}

func TestCodeOfNonAppErrorIsInternal(t *testing.T) {
	t.Parallel()

	require.Equal(t, CodeInternal, CodeOf(stdErrors.New("boom")))
	require.Equal(t, "internal error", SafeMessage(stdErrors.New("boom")))
}

func TestDetailsOnlyForSafeErrors(t *testing.T) {
	t.Parallel()

	safe := InvalidFields("bad", map[string]string{"phone": "is required"})
	require.Equal(t, map[string]string{"phone": "is required"}, Details(safe))

	unsafe := New(CodeInternal, "x", WithDetails(map[string]string{"secret": "leak"}))
	require.Nil(t, Details(unsafe))
}

func TestStackTraceCaptured(t *testing.T) {
	t.Parallel()

	err := Internal("x", nil)
	require.Contains(t, StackTrace(err), "TestStackTraceCaptured")
}
