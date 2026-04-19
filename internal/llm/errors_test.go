/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package llm

import (
	"net"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

// --- DetectErrorJSON ---

func TestDetectErrorJSON_ReturnsMessage_Successfully(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "object error with message",
			content: `{"error":{"type":"llm_call_failed","message":"Operation not allowed"}}`,
			want:    "Operation not allowed",
		},
		{
			name:    "string error",
			content: `{"error":"upstream timeout"}`,
			want:    "upstream timeout",
		},
		{
			name:    "object error type only",
			content: `{"error":{"type":"rate_limited"}}`,
			want:    "rate_limited",
		},
		{
			name:    "leading whitespace",
			content: "\n  {\"error\":\"x\"}",
			want:    "x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectErrorJSON(tt.content)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectErrorJSON_ReturnsEmpty_Successfully(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "plain text",
			content: "Here is the answer",
		},
		{
			name:    "json without error field",
			content: `{"result":"ok"}`,
		},
		{
			name:    "empty string",
			content: "",
		},
		{
			name:    "single character",
			content: "x",
		},
		{
			name:    "empty error string",
			content: `{"error":""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectErrorJSON(tt.content)

			assert.Empty(t, got)
		})
	}
}

// --- IsDialError ---

func TestIsDialError_ReturnsTrueForDialErrors_Successfully(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "net.OpError with dial op",
			err:  &net.OpError{Op: "dial", Err: errors.New("connection refused")},
		},
		{
			name: "net.DNSError",
			err:  &net.DNSError{Name: "localhost", Err: "no such host"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, IsDialError(tt.err))
		})
	}
}

func TestIsDialError_ReturnsFalseForNonDialErrors_Successfully(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "regular error",
			err:  errors.New("some error"),
		},
		{
			name: "net.OpError with read op",
			err:  &net.OpError{Op: "read", Err: errors.New("connection reset")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, IsDialError(tt.err))
		})
	}
}
