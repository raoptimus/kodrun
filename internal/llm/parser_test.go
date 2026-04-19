package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ParseToolCalls: JSON format ---

func TestParseToolCalls_SingleJSONObject_Successfully(t *testing.T) {
	input := `{"name":"read_file","arguments":{"path":"/tmp"}}`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "fallback-0", calls[0].ID)
	assert.Equal(t, "read_file", calls[0].Function.Name)
	assert.Equal(t, "/tmp", calls[0].Function.Arguments["path"])
}

func TestParseToolCalls_JSONArray_Successfully(t *testing.T) {
	input := `[{"name":"read_file","arguments":{"path":"a.go"}},{"name":"write_file","arguments":{"path":"b.go","content":"test"}}]`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 2)
	assert.Equal(t, "fallback-0", calls[0].ID)
	assert.Equal(t, "read_file", calls[0].Function.Name)
	assert.Equal(t, "a.go", calls[0].Function.Arguments["path"])
	assert.Equal(t, "fallback-1", calls[1].ID)
	assert.Equal(t, "write_file", calls[1].Function.Name)
	assert.Equal(t, "b.go", calls[1].Function.Arguments["path"])
	assert.Equal(t, "test", calls[1].Function.Arguments["content"])
}

func TestParseToolCalls_JSONWithWhitespace_Successfully(t *testing.T) {
	input := `  {"name": "edit_file", "arguments": {"path": "a.go", "old_str": "x", "new_str": "y"}}  `

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "edit_file", calls[0].Function.Name)
	assert.Equal(t, "a.go", calls[0].Function.Arguments["path"])
}

func TestParseToolCalls_ArrayWithEmptyNamesFiltered_Successfully(t *testing.T) {
	input := `[{"name":"read_file","arguments":{"path":"a.go"}},{"name":"","arguments":{}}]`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "read_file", calls[0].Function.Name)
}

func TestParseToolCalls_FallbackIDsAreSequential_Successfully(t *testing.T) {
	input := `[{"name":"a","arguments":{}},{"name":"b","arguments":{}}]`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 2)
	assert.Equal(t, "fallback-0", calls[0].ID)
	assert.Equal(t, "fallback-1", calls[1].ID)
}

// --- ParseToolCalls: XML format ---

func TestParseToolCalls_XMLSingleFunction_Successfully(t *testing.T) {
	input := `<function=read_file><parameter=path>/tmp</parameter></function>`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "fallback-0", calls[0].ID)
	assert.Equal(t, "read_file", calls[0].Function.Name)
	assert.Equal(t, "/tmp", calls[0].Function.Arguments["path"])
}

func TestParseToolCalls_XMLMultipleFunctions_Successfully(t *testing.T) {
	input := `<function=read_file>
<parameter=path>a.go</parameter>
</function>
<function=read_file>
<parameter=path>b.go</parameter>
</function>`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 2)
	assert.Equal(t, "fallback-0", calls[0].ID)
	assert.Equal(t, "a.go", calls[0].Function.Arguments["path"])
	assert.Equal(t, "fallback-1", calls[1].ID)
	assert.Equal(t, "b.go", calls[1].Function.Arguments["path"])
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

func TestParseToolCalls_XMLWithMultilineParamValues_Successfully(t *testing.T) {
	input := `<function=write_file>
<parameter=path>
hello.txt
</parameter>
<parameter=content>
Hello from KodRun!
</parameter>
</function>`

	calls, ok := ParseToolCalls(input)

	require.True(t, ok)
	require.Len(t, calls, 1)
	assert.Equal(t, "write_file", calls[0].Function.Name)
	assert.Equal(t, "hello.txt", calls[0].Function.Arguments["path"])
	assert.Equal(t, "Hello from KodRun!", calls[0].Function.Arguments["content"])
}

// --- ParseToolCalls: invalid inputs ---

func TestParseToolCalls_InvalidInput_Failure(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "plain text",
			input: "This is just a text response",
		},
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "json object without name",
			input: `{"arguments":{"path":"a.go"}}`,
		},
		{
			name:  "json object with empty name",
			input: `{"name":"","arguments":{}}`,
		},
		{
			name:  "empty json array",
			input: `[]`,
		},
		{
			name:  "whitespace only",
			input: "   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ParseToolCalls(tt.input)

			assert.False(t, ok)
		})
	}
}

// --- CleanToolCallText ---

func TestCleanToolCallText_RemovesMarkup_Successfully(t *testing.T) {
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
			name:  "whitespace only trimmed",
			input: "   ",
			want:  "",
		},
		{
			name:  "multiple function blocks removed",
			input: "<function=a><parameter=x>1</parameter></function> text <function=b><parameter=y>2</parameter></function>",
			want:  "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanToolCallText(tt.input)

			assert.Equal(t, tt.want, got)
		})
	}
}

// --- parseXMLToolCalls ---

func TestParseXMLToolCalls_NoMatches_Successfully(t *testing.T) {
	result := parseXMLToolCalls("no xml here")

	assert.Nil(t, result)
}

func TestParseXMLToolCalls_EmptyString_Successfully(t *testing.T) {
	result := parseXMLToolCalls("")

	assert.Nil(t, result)
}
