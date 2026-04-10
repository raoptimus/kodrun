package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatErrors_MultipleErrors_Successfully(t *testing.T) {
	errors := []ParsedError{
		{Raw: "a.go:1:1: error one"},
		{Raw: "b.go:2:3: error two"},
	}

	got := FormatErrors(context.Background(), errors)

	assert.Equal(t, "a.go:1:1: error one\nb.go:2:3: error two", got)
}

func TestFormatErrors_EmptySlice_Successfully(t *testing.T) {
	got := FormatErrors(context.Background(), nil)

	assert.Equal(t, "", got)
}

func TestFormatErrors_SingleError_Successfully(t *testing.T) {
	errors := []ParsedError{
		{Raw: "main.go:10:5: undefined: foo"},
	}

	got := FormatErrors(context.Background(), errors)

	assert.Equal(t, "main.go:10:5: undefined: foo", got)
}

func TestParseErrors_DeduplicatesSameFileLineMessage_Successfully(t *testing.T) {
	output := `a.go:10:5: undefined: x
a.go:10:5: undefined: x
a.go:10:5: undefined: x`

	errors := ParseErrors(context.Background(), output)

	assert.Len(t, errors, 1)
}

func TestParseErrors_DifferentMessagesSameLocation_Successfully(t *testing.T) {
	output := `a.go:10:5: undefined: x
a.go:10:5: declared and not used: y`

	errors := ParseErrors(context.Background(), output)

	assert.Len(t, errors, 2)
}

func TestParseErrors_WhitespaceOnlyLines_Successfully(t *testing.T) {
	output := "   \n\t\n  \n"

	errors := ParseErrors(context.Background(), output)

	assert.Empty(t, errors)
}

func TestParseErrors_MixedFormats_Successfully(t *testing.T) {
	output := `./internal/agent/loop.go:45:10: undefined: Context
    loop_test.go:32: expected 3 iterations, got 2
internal/ollama/client.go:12:1: exported function Chat should have comment (revive)`

	errors := ParseErrors(context.Background(), output)

	require.Len(t, errors, 3)
	assert.Equal(t, "internal/agent/loop.go", errors[0].File)
	assert.Equal(t, "loop_test.go", errors[1].File)
	assert.Equal(t, "internal/ollama/client.go", errors[2].File)
}

func TestParseErrors_NonMatchingLinesSkipped_Successfully(t *testing.T) {
	output := `ok  	github.com/raoptimus/kodrun/internal/config	0.005s
FAIL	github.com/raoptimus/kodrun/internal/agent	0.123s
./main.go:5:3: missing return`

	errors := ParseErrors(context.Background(), output)

	require.Len(t, errors, 1)
	assert.Equal(t, "main.go", errors[0].File)
}

func TestAffectedFiles_EmptySlice_Successfully(t *testing.T) {
	files := AffectedFiles(context.Background(), nil)

	assert.Empty(t, files)
}

func TestAffectedFiles_SkipsEmptyFilePaths_Successfully(t *testing.T) {
	errors := []ParsedError{
		{File: "a.go", Line: 1},
		{File: "", Line: 5},
		{File: "b.go", Line: 10},
	}

	files := AffectedFiles(context.Background(), errors)

	assert.Equal(t, []string{"a.go", "b.go"}, files)
}

func TestParseErrors_BuildFormatWithLeadingSlash_Successfully(t *testing.T) {
	output := `./cmd/main.go:1:1: missing package clause`

	errors := ParseErrors(context.Background(), output)

	require.Len(t, errors, 1)
	assert.Equal(t, "cmd/main.go", errors[0].File)
	assert.Equal(t, 1, errors[0].Line)
	assert.Equal(t, 1, errors[0].Col)
	assert.Equal(t, "missing package clause", errors[0].Message)
}
