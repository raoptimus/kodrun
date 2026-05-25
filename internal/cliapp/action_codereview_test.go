/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCodeReviewArgs_ReturnsEmptyResults_WhenPartsIsEmpty(t *testing.T) {
	tests := []struct {
		name             string
		parts            []string
		expectedDiffArgs []string
		expectedScope    string
	}{
		{
			name:             "nil parts",
			parts:            nil,
			expectedDiffArgs: nil,
			expectedScope:    "",
		},
		{
			name:             "parts с одним элементом (только команда)",
			parts:            []string{"/code-review"},
			expectedDiffArgs: nil,
			expectedScope:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffArgs, scope := parseCodeReviewArgs(tt.parts)

			assert.Equal(t, tt.expectedDiffArgs, diffArgs)
			assert.Equal(t, tt.expectedScope, scope)
		})
	}
}

func TestParseCodeReviewArgs_ReturnsOnlyDiffArgs_WhenNoPackageFlag(t *testing.T) {
	tests := []struct {
		name             string
		parts            []string
		expectedDiffArgs []string
		expectedScope    string
	}{
		{
			name:             "пустая строка аргументов",
			parts:            []string{"/code-review", ""},
			expectedDiffArgs: nil,
			expectedScope:    "",
		},
		{
			name:             "один git-аргумент",
			parts:            []string{"/code-review", "HEAD~1"},
			expectedDiffArgs: []string{"HEAD~1"},
			expectedScope:    "",
		},
		{
			name:             "несколько git-аргументов",
			parts:            []string{"/code-review", "HEAD~3 HEAD"},
			expectedDiffArgs: []string{"HEAD~3", "HEAD"},
			expectedScope:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffArgs, scope := parseCodeReviewArgs(tt.parts)

			assert.Equal(t, tt.expectedDiffArgs, diffArgs)
			assert.Equal(t, tt.expectedScope, scope)
		})
	}
}

func TestParseCodeReviewArgs_ExtractsPackageScope_Successfully(t *testing.T) {
	tests := []struct {
		name             string
		parts            []string
		expectedDiffArgs []string
		expectedScope    string
	}{
		{
			name:             "--package <path> без git-аргументов",
			parts:            []string{"/code-review", "--package internal/foo"},
			expectedDiffArgs: nil,
			expectedScope:    "internal/foo",
		},
		{
			name:             "--package=<path> без git-аргументов",
			parts:            []string{"/code-review", "--package=internal/foo"},
			expectedDiffArgs: nil,
			expectedScope:    "internal/foo",
		},
		{
			name:             "--package <path> с trailing slash обрезается",
			parts:            []string{"/code-review", "--package internal/foo/"},
			expectedDiffArgs: nil,
			expectedScope:    "internal/foo",
		},
		{
			name:             "--package=<path> с trailing slash обрезается",
			parts:            []string{"/code-review", "--package=internal/foo/"},
			expectedDiffArgs: nil,
			expectedScope:    "internal/foo",
		},
		{
			name:             "--package <path> вместе с git-аргументами",
			parts:            []string{"/code-review", "--package internal/bar HEAD~1"},
			expectedDiffArgs: []string{"HEAD~1"},
			expectedScope:    "internal/bar",
		},
		{
			name:             "--package=<path> вместе с git-аргументами",
			parts:            []string{"/code-review", "HEAD~2 --package=internal/bar HEAD~1"},
			expectedDiffArgs: []string{"HEAD~2", "HEAD~1"},
			expectedScope:    "internal/bar",
		},
		{
			name:             "--package без значения (последний токен) — попадает в diffArgs",
			parts:            []string{"/code-review", "--package"},
			expectedDiffArgs: []string{"--package"},
			expectedScope:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffArgs, scope := parseCodeReviewArgs(tt.parts)

			assert.Equal(t, tt.expectedDiffArgs, diffArgs)
			assert.Equal(t, tt.expectedScope, scope)
		})
	}
}
