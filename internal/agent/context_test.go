/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/raoptimus/kodrun/internal/llm"
)

func TestEstimateStringTokens_EmptyString_Successfully(t *testing.T) {
	t.Parallel()

	got := estimateStringTokens("")

	assert.Equal(t, 0, got)
}

func TestEstimateStringTokens_ASCIIText_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "4 chars",
			input: "abcd",
			want:  1, // 4 / 4 = 1
		},
		{
			name:  "8 chars",
			input: "abcdefgh",
			want:  2, // 8 / 4 = 2
		},
		{
			name:  "single char",
			input: "a",
			want:  0, // 1 / 4 = 0 (integer division)
		},
		{
			name:  "3 chars",
			input: "abc",
			want:  0, // 3 / 4 = 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := estimateStringTokens(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEstimateStringTokens_NonASCIIHeavyText_Successfully(t *testing.T) {
	t.Parallel()

	// "Привет мир" = 10 runes, 9 non-ASCII (space is ASCII).
	// nonASCII ratio = 9*100/10 = 90% > 30%, so non-ASCII path.
	// totalRunes / charsPerTokenNonASCII = 10 / 2 = 5.
	got := estimateStringTokens("Привет мир")

	assert.Equal(t, 5, got)
}

func TestEstimateStringTokens_MixedTextBelowThreshold_Successfully(t *testing.T) {
	t.Parallel()

	// "abcdefghijklmnopqrstuvwxyя" = 26 runes, 1 non-ASCII.
	// nonASCII ratio = 1*100/26 = 3% < 30%, so ASCII path.
	// len(s) in bytes = 25 + 2 (Cyrillic я = 2 bytes) = 27.
	// 27 / 4 = 6.
	got := estimateStringTokens("abcdefghijklmnopqrstuvwxyя")

	assert.Equal(t, 6, got)
}

func TestContextManager_EstimateTokens_Successfully(t *testing.T) {
	t.Parallel()

	cm := NewContextManager(1000, nil, "test-model")

	tests := []struct {
		name     string
		messages []llm.Message
		want     int
	}{
		{
			name:     "empty messages",
			messages: nil,
			want:     0,
		},
		{
			name: "single message 8 chars",
			messages: []llm.Message{
				{Role: "user", Content: "abcdefgh"},
			},
			want: 6, // 8/4=2 tokens + 4 overhead = 6
		},
		{
			name: "two messages",
			messages: []llm.Message{
				{Role: "system", Content: "abcdefgh"},
				{Role: "user", Content: "abcdefghijklmnop"},
			},
			want: 14, // (8/4 + 4) + (16/4 + 4) = 6 + 8 = 14
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cm.estimateTokens(tt.messages)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContextManager_SetLanguage_Successfully(t *testing.T) {
	t.Parallel()

	cm := NewContextManager(1000, nil, "test-model")
	cm.SetLanguage("ru")

	assert.Equal(t, "ru", cm.language)
}

func TestContextManager_BuildTrimmed_Successfully(t *testing.T) {
	t.Parallel()

	cm := NewContextManager(1000, nil, "test-model")

	head := []llm.Message{
		{Role: "system", Content: "you are an assistant"},
		{Role: "user", Content: "hello"},
	}
	tail := []llm.Message{
		{Role: "user", Content: "latest question"},
		{Role: "assistant", Content: "latest answer"},
	}
	summaryMsg := "[Summary] earlier conversation"

	result := cm.buildTrimmed(head, tail, summaryMsg)

	assert.Len(t, result, 5) // 2 head + 1 summary + 2 tail
	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "user", result[1].Role)
	assert.Equal(t, "user", result[2].Role)
	assert.Equal(t, summaryMsg, result[2].Content)
	assert.Equal(t, "user", result[3].Role)
	assert.Equal(t, "assistant", result[4].Role)
}

func TestContextManager_Trim_UnderBudget_Successfully(t *testing.T) {
	t.Parallel()

	// maxTokens is very large, so no trimming should happen.
	cm := NewContextManager(100000, nil, "test-model")

	messages := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	result, err := cm.Trim(t.Context(), messages)

	assert.NoError(t, err)
	assert.Equal(t, messages, result)
}

func TestContextManager_Trim_TooFewMessages_Successfully(t *testing.T) {
	t.Parallel()

	// maxTokens is 1 (force trimming), but messages count <= keepFirst + keepLast.
	cm := NewContextManager(1, nil, "test-model")

	messages := make([]llm.Message, keepFirstMessages+keepLastMessages)
	for i := range messages {
		messages[i] = llm.Message{Role: "user", Content: "msg"}
	}

	result, err := cm.Trim(t.Context(), messages)

	assert.NoError(t, err)
	assert.Equal(t, messages, result)
}

func TestSmartTruncate_ShortString_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "empty string",
			input:  "",
			maxLen: 100,
			want:   "",
		},
		{
			name:   "string shorter than maxLen",
			input:  "hello world",
			maxLen: 100,
			want:   "hello world",
		},
		{
			name:   "string exactly at maxLen",
			input:  "hello world",
			maxLen: 11,
			want:   "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := smartTruncate(tt.input, tt.maxLen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSmartTruncate_LongString_Successfully(t *testing.T) {
	t.Parallel()

	// Build a 1000-char string: 'a' * 600 + 'b' * 400.
	// After truncation with maxLen=600: first 300 'a's + sep + last 200 'b's.
	input := strings.Repeat("a", 600) + strings.Repeat("b", 400)
	want := strings.Repeat("a", 300) + smartTruncateSep + strings.Repeat("b", 200)

	got := smartTruncate(input, 600)

	assert.Equal(t, want, got)
}

func TestSmartTruncate_SmallMaxLen_Successfully(t *testing.T) {
	t.Parallel()

	// maxLen=50 < head(300)+tail(200), so proportional split: head=50*3/5=30, tail=50-30=20.
	// Input: 100 chars ('a'*60 + 'b'*40).
	input := strings.Repeat("a", 60) + strings.Repeat("b", 40)
	want := strings.Repeat("a", 30) + smartTruncateSep + strings.Repeat("b", 20)

	got := smartTruncate(input, 50)

	assert.Equal(t, want, got)
}

func TestSmartTruncate_StackTrace_Successfully(t *testing.T) {
	t.Parallel()

	// The tail (last 200 runes) must be preserved so error lines at the end are kept.
	tail := "goroutine 1 [running]:\nmain.main()\n\t/app/main.go:42 +0x68\npanic: runtime error: index out of range"
	// Pad head to push total length well beyond maxLen=500.
	head := strings.Repeat("x", 700)
	input := head + tail

	got := smartTruncate(input, 500)

	// Last 200 runes of input must appear at the end of the result.
	runes := []rune(input)
	expectedTail := string(runes[len(runes)-200:])
	assert.True(t, strings.HasSuffix(got, expectedTail), "tail of stack trace must be preserved")
	assert.Contains(t, got, smartTruncateSep)
}

func TestContextManager_EffectiveBudget_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		maxTokens int
		want      int
	}{
		{
			// reserve = max(8192*0.15=1228, 1024) = 1228; budget = 8192-1228 = 6964
			name:      "large maxTokens",
			maxTokens: 8192,
			want:      6964,
		},
		{
			// reserve = max(2048*0.15=307, 1024) = 1024; budget = 2048-1024 = 1024
			name:      "medium maxTokens",
			maxTokens: 2048,
			want:      1024,
		},
		{
			// reserve = max(500*0.15=75, 1024) = 1024; budget = 500-1024 = -524 → 0
			name:      "small maxTokens below reserve",
			maxTokens: 500,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cm := NewContextManager(tt.maxTokens, nil, "test-model")
			got := cm.effectiveBudget()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolCallIndex_NoToolCalls_Successfully(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		{Role: "system", Content: "you are an assistant"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}

	got := toolCallIndex(messages)

	assert.Empty(t, got)
}

func TestToolCallIndex_AssistantWithToolCalls_Successfully(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		{Role: "user", Content: "do something"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-001", Function: llm.ToolCallFunc{Name: "read_file"}},
				{ID: "call-002", Function: llm.ToolCallFunc{Name: "write_file"}},
			},
		},
		{Role: "tool", Content: "file content", ToolCallID: "call-001"},
		{Role: "tool", Content: "written", ToolCallID: "call-002"},
	}

	got := toolCallIndex(messages)

	assert.Len(t, got, 2)
	assert.Equal(t, 1, got["call-001"]) // assistant is at index 1
	assert.Equal(t, 1, got["call-002"]) // same assistant message
}

func TestToolCallIndex_AssistantWithEmptyID_Successfully(t *testing.T) {
	t.Parallel()

	// ToolCall with empty ID must not be indexed.
	messages := []llm.Message{
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "", Function: llm.ToolCallFunc{Name: "noop"}},
			},
		},
	}

	got := toolCallIndex(messages)

	assert.Empty(t, got)
}

func TestAdjustBoundaries_NoToolCalls_Successfully(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "q4"},
		{Role: "assistant", Content: "a4"},
	}

	kf, kl := adjustBoundaries(messages, 2, 4)

	assert.Equal(t, 2, kf)
	assert.Equal(t, 4, kl)
}

func TestAdjustBoundaries_ToolCallOnHeadBorder_Successfully(t *testing.T) {
	t.Parallel()

	// messages[1] is the last head message (keepFirst=2) and it is an assistant
	// with a tool call whose result sits in the middle at index 2.
	// adjustBoundaries must expand keepFirst to 3.
	messages := []llm.Message{
		{Role: "user", Content: "q1"},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "tc-1", Function: llm.ToolCallFunc{Name: "run"}},
			},
		},
		{Role: "tool", Content: "result", ToolCallID: "tc-1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "q4"},
		{Role: "assistant", Content: "a4"},
	}

	kf, kl := adjustBoundaries(messages, 2, 4)

	// The tool result at index 2 is pulled into head, so keepFirst becomes 3.
	assert.Equal(t, 3, kf)
	assert.Equal(t, 4, kl)
}

func TestAdjustBoundaries_ToolResultOnTailBorder_Successfully(t *testing.T) {
	t.Parallel()

	// The first tail message (index n-keepLast = 9-4 = 5) is a tool result.
	// Its producing assistant sits in the middle at index 4.
	// adjustBoundaries must expand keepLast to include index 4.
	messages := []llm.Message{
		{Role: "user", Content: "q1"},      // 0
		{Role: "assistant", Content: "a1"}, // 1
		{Role: "user", Content: "q2"},      // 2
		{Role: "assistant", Content: "a2"}, // 3
		{ // 4 — assistant with tool call in middle
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "tc-2", Function: llm.ToolCallFunc{Name: "search"}},
			},
		},
		{Role: "tool", Content: "results", ToolCallID: "tc-2"}, // 5 — tail border
		{Role: "user", Content: "q3"},                          // 6
		{Role: "assistant", Content: "a3"},                     // 7
		{Role: "user", Content: "q4"},                          // 8
	}

	kf, kl := adjustBoundaries(messages, 2, 4)

	// Assistant at index 4 is included → keepLast = 9 - 4 = 5.
	assert.Equal(t, 2, kf)
	assert.Equal(t, 5, kl)
}

func TestContextManager_Calibrate_FirstSample_Successfully(t *testing.T) {
	t.Parallel()

	// Single message "abcdefgh": estimateStringTokens = 8/4 = 2, overhead = 4, raw = 6.
	// sample = 12 / 6 = 2.0. First call sets calibrationRatio = sample.
	cm := NewContextManager(1000, nil, "test-model")
	messages := []llm.Message{
		{Role: "user", Content: "abcdefgh"},
	}

	cm.Calibrate(messages, 12)

	assert.InDelta(t, 2.0, cm.calibrationRatio, 1e-9)
}

func TestContextManager_Calibrate_MultipleSamples_Successfully(t *testing.T) {
	t.Parallel()

	// raw = 6 (same message as above).
	// First call: actual=12, sample=2.0, ratio=2.0.
	// Second call: actual=9, sample=9/6=1.5, ratio = 2.0*0.7 + 1.5*0.3 = 1.4 + 0.45 = 1.85.
	cm := NewContextManager(1000, nil, "test-model")
	messages := []llm.Message{
		{Role: "user", Content: "abcdefgh"},
	}

	cm.Calibrate(messages, 12)
	cm.Calibrate(messages, 9)

	assert.InDelta(t, 1.85, cm.calibrationRatio, 1e-9)
}

func TestContextManager_Calibrate_ZeroActual_Successfully(t *testing.T) {
	t.Parallel()

	// actualTokens=0 must be a no-op: calibrationRatio stays 0.
	cm := NewContextManager(1000, nil, "test-model")
	messages := []llm.Message{
		{Role: "user", Content: "abcdefgh"},
	}

	cm.Calibrate(messages, 0)

	assert.Equal(t, 0.0, cm.calibrationRatio)
}

func TestContextManager_Calibrate_EmptyMessages_Successfully(t *testing.T) {
	t.Parallel()

	// Empty messages → estimateTokensRaw = 0 → no-op: calibrationRatio stays 0.
	cm := NewContextManager(1000, nil, "test-model")

	cm.Calibrate(nil, 42)

	assert.Equal(t, 0.0, cm.calibrationRatio)
}

func TestContextManager_EstimateTokens_WithCalibration_Successfully(t *testing.T) {
	t.Parallel()

	// raw = 6 (one message "abcdefgh": 2 tokens + 4 overhead).
	// calibrationRatio set to 2.0 via first Calibrate call (actual=12).
	// estimateTokens must return int(6 * 2.0) = 12.
	cm := NewContextManager(1000, nil, "test-model")
	messages := []llm.Message{
		{Role: "user", Content: "abcdefgh"},
	}
	cm.Calibrate(messages, 12)

	got := cm.estimateTokens(messages)

	assert.Equal(t, 12, got)
}

func TestContextManager_EstimateTokens_WithoutCalibration_Successfully(t *testing.T) {
	t.Parallel()

	// No Calibrate call → calibrationRatio == 0 → estimateTokens returns raw value.
	// raw = 6 (one message "abcdefgh": 2 tokens + 4 overhead).
	cm := NewContextManager(1000, nil, "test-model")
	messages := []llm.Message{
		{Role: "user", Content: "abcdefgh"},
	}

	got := cm.estimateTokens(messages)

	assert.Equal(t, 6, got)
}

func TestAdjustBoundaries_ExpansionAbsorbsAll_Successfully(t *testing.T) {
	t.Parallel()

	// keepFirst+keepLast >= n from the start → the initial guard triggers immediately.
	messages := []llm.Message{
		{Role: "user", Content: "q1"}, // 0
		{ // 1 — assistant with tool call on head border (keepFirst=2)
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "tc-3", Function: llm.ToolCallFunc{Name: "fetch"}},
			},
		},
		{Role: "tool", Content: "data", ToolCallID: "tc-3"}, // 2
		{Role: "user", Content: "q2"},                       // 3
		{Role: "assistant", Content: "a2"},                  // 4
	}

	// keepFirst=2, keepLast=3: sum=5 == n=5, so the initial guard triggers.
	kf, kl := adjustBoundaries(messages, 2, 3)

	assert.Equal(t, 2, kf)
	assert.Equal(t, 3, kl)
}
