package ollama

import "testing"

func TestDetectErrorJSON(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "object error envelope",
			content: `{"error":{"type":"llm_call_failed","message":"Operation not allowed"}}`,
			want:    "Operation not allowed",
		},
		{
			name:    "string error envelope",
			content: `{"error":"upstream timeout"}`,
			want:    "upstream timeout",
		},
		{
			name:    "object error type only",
			content: `{"error":{"type":"rate_limited"}}`,
			want:    "rate_limited",
		},
		{
			name:    "plain content",
			content: "Here is the answer",
			want:    "",
		},
		{
			name:    "json that is not an error",
			content: `{"result":"ok"}`,
			want:    "",
		},
		{
			name:    "empty",
			content: "",
			want:    "",
		},
		{
			name:    "leading whitespace",
			content: "\n  {\"error\":\"x\"}",
			want:    "x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectErrorJSON(tt.content)
			if got != tt.want {
				t.Errorf("detectErrorJSON(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
