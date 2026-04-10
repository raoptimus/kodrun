package ollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanToolCallText_RemovesXMLToolCalls_Successfully(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "xml function block removed",
			input: "Let me read that. <function=read_file><parameter=path>main.go</parameter></function> Done.",
			want:  "Let me read that.  Done.",
		},
		{
			name:  "tool_call tags removed",
			input: "<tool_call>{\"name\":\"read_file\"}</tool_call>",
			want:  "{\"name\":\"read_file\"}",
		},
		{
			name:  "plain text unchanged",
			input: "This is just a text response.",
			want:  "This is just a text response.",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanToolCallText(tt.input)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseToolCalls_JSONWithWhitespace_Successfully(t *testing.T) {
	input := `  {"name": "edit_file", "arguments": {"path": "a.go", "old_str": "x", "new_str": "y"}}  `

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "edit_file", calls[0].Function.Name)
	assert.Equal(t, "a.go", calls[0].Function.Arguments["path"])
}

func TestParseToolCalls_ArrayWithEmptyNames_Successfully(t *testing.T) {
	input := `[{"name": "read_file", "arguments": {"path": "a.go"}}, {"name": "", "arguments": {}}]`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "read_file", calls[0].Function.Name)
}

func TestParseToolCalls_EmptyArray_Failure(t *testing.T) {
	input := `[]`

	_, ok := ParseToolCalls(input)

	assert.False(t, ok)
}

func TestParseToolCalls_XMLWithMultipleParams_Successfully(t *testing.T) {
	input := `<function=edit_file><parameter=path>main.go</parameter><parameter=old_str>foo</parameter><parameter=new_str>bar</parameter></function>`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "edit_file", calls[0].Function.Name)
	assert.Equal(t, "main.go", calls[0].Function.Arguments["path"])
	assert.Equal(t, "foo", calls[0].Function.Arguments["old_str"])
	assert.Equal(t, "bar", calls[0].Function.Arguments["new_str"])
}

func TestParseToolCalls_FallbackIDsAreSequential_Successfully(t *testing.T) {
	input := `[{"name": "a", "arguments": {}}, {"name": "b", "arguments": {}}]`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 2)
	assert.Equal(t, "fallback-0", calls[0].ID)
	assert.Equal(t, "fallback-1", calls[1].ID)
}
