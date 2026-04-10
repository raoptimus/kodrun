package agent

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainOutput_Handle_EventAgent_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventAgent, Message: "thinking about it"})

	assert.Equal(t, "thinking about it\n", buf.String())
}

func TestPlainOutput_Handle_EventToolSuccess_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event *Event
		want  string
	}{
		{
			name:  "tool with message",
			event: &Event{Type: EventTool, Tool: "read_file", Message: "main.go", Success: true},
			want:  "✓ Read(main.go)\n",
		},
		{
			name:  "tool without message",
			event: &Event{Type: EventTool, Tool: "go_build", Message: "", Success: true},
			want:  "✓ Build\n",
		},
		{
			name:  "tool with executing message",
			event: &Event{Type: EventTool, Tool: "go_build", Message: "executing...", Success: true},
			want:  "✓ Build\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			out := NewPlainOutput(&buf)

			out.Handle(tt.event)

			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestPlainOutput_Handle_EventToolFailure_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventTool, Tool: "edit_file", Message: "access denied", Success: false})

	assert.Equal(t, "✗ Edit(access denied)\n", buf.String())
}

func TestPlainOutput_Handle_EventToolEmptyName_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventTool, Tool: "", Message: "something"})

	assert.Equal(t, "", buf.String())
}

func TestPlainOutput_Handle_EventFix_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventFix, Message: "applied fix"})

	assert.Equal(t, "[fix]   applied fix\n", buf.String())
}

func TestPlainOutput_Handle_EventError_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventError, Tool: "go_build", Message: "compile error"})

	assert.Equal(t, "[error] go_build: compile error\n", buf.String())
}

func TestPlainOutput_Handle_EventTokens_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventTokens, PromptTokens: 1500, EvalTokens: 200})

	assert.Equal(t, "[tokens] prompt: 1500, eval: 200\n", buf.String())
}

func TestPlainOutput_Handle_EventCompact_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventCompact, Message: "context trimmed"})

	assert.Equal(t, "[compact] context trimmed\n", buf.String())
}

func TestPlainOutput_Handle_EventGroupTitleUpdate_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventGroupTitleUpdate, Message: "Phase: reviewing"})

	assert.Equal(t, "Phase: reviewing\n", buf.String())
}

func TestPlainOutput_Handle_EventDone_Successfully(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	out := NewPlainOutput(&buf)

	out.Handle(&Event{Type: EventDone, Message: "Done"})

	assert.Equal(t, "", buf.String())
}
