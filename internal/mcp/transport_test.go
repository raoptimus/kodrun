/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingBuffer_Write_SmallData_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		size     int
		writes   []string
		expected string
	}{
		{
			name:     "Empty write",
			size:     8,
			writes:   []string{""},
			expected: "",
		},
		{
			name:     "Single byte",
			size:     8,
			writes:   []string{"a"},
			expected: "a",
		},
		{
			name:     "Exact buffer size",
			size:     4,
			writes:   []string{"abcd"},
			expected: "abcd",
		},
		{
			name:     "Multiple small writes within capacity",
			size:     8,
			writes:   []string{"ab", "cd"},
			expected: "abcd",
		},
		{
			name:     "Fill buffer exactly with multiple writes",
			size:     4,
			writes:   []string{"ab", "cd"},
			expected: "abcd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := newRingBuffer(tt.size)

			for _, w := range tt.writes {
				n, err := rb.Write([]byte(w))
				require.NoError(t, err)
				assert.Equal(t, len(w), n)
			}

			assert.Equal(t, tt.expected, rb.String())
		})
	}
}

func TestRingBuffer_Write_Overflow_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		size     int
		writes   []string
		expected string
	}{
		{
			name:     "Single write exceeds buffer",
			size:     4,
			writes:   []string{"abcdef"},
			expected: "cdef",
		},
		{
			name:     "Multiple writes cause wrap",
			size:     4,
			writes:   []string{"abc", "de"},
			expected: "bcde",
		},
		{
			name:     "Many small writes wrap around",
			size:     4,
			writes:   []string{"ab", "cd", "ef"},
			expected: "cdef",
		},
		{
			name:     "Write exactly double buffer size",
			size:     4,
			writes:   []string{"abcdefgh"},
			expected: "efgh",
		},
		{
			name:     "Write one byte more than buffer",
			size:     4,
			writes:   []string{"abcde"},
			expected: "bcde",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := newRingBuffer(tt.size)

			for _, w := range tt.writes {
				n, err := rb.Write([]byte(w))
				require.NoError(t, err)
				assert.Equal(t, len(w), n)
			}

			assert.Equal(t, tt.expected, rb.String())
		})
	}
}

func TestRingBuffer_String_EmptyBuffer_Successfully(t *testing.T) {
	rb := newRingBuffer(8)

	assert.Equal(t, "", rb.String())
}

func TestValidEnvKey_MatchesValidKeys_Successfully(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{
			name:  "Simple uppercase",
			key:   "HOME",
			valid: true,
		},
		{
			name:  "Lowercase",
			key:   "path",
			valid: true,
		},
		{
			name:  "Underscore prefix",
			key:   "_VAR",
			valid: true,
		},
		{
			name:  "Mixed with digits",
			key:   "MY_VAR_2",
			valid: true,
		},
		{
			name:  "Single letter",
			key:   "A",
			valid: true,
		},
		{
			name:  "Single underscore",
			key:   "_",
			valid: true,
		},
		{
			name:  "Starts with digit",
			key:   "1VAR",
			valid: false,
		},
		{
			name:  "Contains dash",
			key:   "MY-VAR",
			valid: false,
		},
		{
			name:  "Contains dot",
			key:   "my.var",
			valid: false,
		},
		{
			name:  "Contains space",
			key:   "MY VAR",
			valid: false,
		},
		{
			name:  "Empty string",
			key:   "",
			valid: false,
		},
		{
			name:  "Contains equals",
			key:   "KEY=VAL",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, validEnvKey.MatchString(tt.key))
		})
	}
}
