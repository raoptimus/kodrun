/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
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
