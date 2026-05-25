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

// --- FilterDiffByPackage ---

func TestFilterDiffByPackage_ReturnsInputAsIs_WhenScopeOrDiffIsEmpty(t *testing.T) {
	const sampleDiff = "diff --git a/foo/bar.go b/foo/bar.go\n--- a/foo/bar.go\n+++ b/foo/bar.go\n@@ -1 +1 @@\n-old\n+new"

	tests := []struct {
		name     string
		diff     string
		scope    string
		expected string
	}{
		{
			name:     "пустой scope — возвращает diff без изменений",
			diff:     sampleDiff,
			scope:    "",
			expected: sampleDiff,
		},
		{
			name:     "пустой diff — возвращает пустую строку",
			diff:     "",
			scope:    "foo",
			expected: "",
		},
		{
			name:     "оба пустые — возвращает пустую строку",
			diff:     "",
			scope:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterDiffByPackage(tt.diff, tt.scope)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterDiffByPackage_FiltersFilesByScope_Successfully(t *testing.T) {
	const diffInsideScope = "diff --git a/internal/foo/bar.go b/internal/foo/bar.go\n" +
		"--- a/internal/foo/bar.go\n" +
		"+++ b/internal/foo/bar.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new"

	const diffOutsideScope = "diff --git a/internal/other/baz.go b/internal/other/baz.go\n" +
		"--- a/internal/other/baz.go\n" +
		"+++ b/internal/other/baz.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new"

	mixedDiff := diffInsideScope + "\n" + diffOutsideScope

	tests := []struct {
		name     string
		diff     string
		scope    string
		expected string
	}{
		{
			name:     "единственный файл внутри scope — возвращается",
			diff:     diffInsideScope,
			scope:    "internal/foo",
			expected: diffInsideScope,
		},
		{
			name:     "единственный файл вне scope — отфильтровывается",
			diff:     diffOutsideScope,
			scope:    "internal/foo",
			expected: "",
		},
		{
			name:     "несколько файлов — остаётся только тот, что внутри scope",
			diff:     mixedDiff,
			scope:    "internal/foo",
			expected: diffInsideScope,
		},
		{
			name:     "scope с trailing slash — trailing slash обрезается, работает корректно",
			diff:     diffInsideScope,
			scope:    "internal/foo/",
			expected: diffInsideScope,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterDiffByPackage(tt.diff, tt.scope)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterDiffByPackage_ExactScopeMatch_Successfully(t *testing.T) {
	// Файл, путь которого совпадает со scope в точности (не поддиректория)
	const diff = "diff --git a/internal/foo/bar.go b/internal/foo/bar.go\n" +
		"--- a/internal/foo/bar.go\n" +
		"+++ b/internal/foo/bar.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new"

	// scope равен полному пути файла
	result := FilterDiffByPackage(diff, "internal/foo/bar.go")

	assert.Equal(t, diff, result)
}

func TestFilterDiffByPackage_DoesNotMatchPartialPrefixWithoutSlash(t *testing.T) {
	// scope "internal/fo" не должен захватывать "internal/foo/bar.go"
	const diff = "diff --git a/internal/foo/bar.go b/internal/foo/bar.go\n" +
		"--- a/internal/foo/bar.go\n" +
		"+++ b/internal/foo/bar.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new"

	result := FilterDiffByPackage(diff, "internal/fo")

	assert.Equal(t, "", result)
}

// --- isSourceCodePath ---

func TestIsSourceCodePath_ReturnsFalse_WhenPathIsDenied(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "пустая строка", path: ""},
		{name: "PathDevNull (/dev/null)", path: "/dev/null"},
		{name: "префикс .kodrun/", path: ".kodrun/rules/foo.go"},
		{name: "префикс .claude/", path: ".claude/docs/example.go"},
		{name: "префикс .github/", path: ".github/workflows/ci.yml"},
		{name: "префикс .git/", path: ".git/config"},
		{name: "префикс vendor/", path: "vendor/github.com/foo/bar.go"},
		{name: "префикс node_modules/", path: "node_modules/lodash/index.js"},
		{name: "префикс testdata/", path: "testdata/fixtures/sample.go"},
		{name: "префикс docs/", path: "docs/guide.md"},
		{name: "префикс doc/", path: "doc/api.go"},
		{name: "имя go.sum", path: "go.sum"},
		{name: "имя go.sum в подпапке", path: "sub/go.sum"},
		{name: "имя package-lock.json", path: "package-lock.json"},
		{name: "имя yarn.lock", path: "yarn.lock"},
		{name: "имя pnpm-lock.yaml", path: "pnpm-lock.yaml"},
		{name: "имя Cargo.lock", path: "Cargo.lock"},
		{name: "имя poetry.lock", path: "poetry.lock"},
		{name: "имя Pipfile.lock", path: "Pipfile.lock"},
		{name: "имя AGENTS.md", path: "AGENTS.md"},
		{name: "имя README.md", path: "README.md"},
		{name: "имя CLAUDE.md", path: "CLAUDE.md"},
		{name: "расширение не в списке (.yaml)", path: "config.yaml"},
		{name: "расширение не в списке (.json)", path: "config.json"},
		{name: "расширение не в списке (.md)", path: "CHANGES.md"},
		{name: "расширение не в списке (.txt)", path: "notes.txt"},
		{name: "расширение не в списке (.toml)", path: "Cargo.toml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSourceCodePath(tt.path)

			assert.False(t, result)
		})
	}
}

func TestIsSourceCodePath_ReturnsTrue_WhenPathIsAllowed(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: ".go файл", path: "internal/cliapp/codereview.go"},
		{name: ".py файл", path: "scripts/deploy.py"},
		{name: ".ts файл", path: "frontend/app.ts"},
		{name: ".tsx файл", path: "frontend/App.tsx"},
		{name: ".js файл", path: "frontend/index.js"},
		{name: ".jsx файл", path: "frontend/App.jsx"},
		{name: ".mjs файл", path: "frontend/module.mjs"},
		{name: ".cjs файл", path: "frontend/common.cjs"},
		{name: ".rs файл", path: "src/main.rs"},
		{name: ".java файл", path: "src/Main.java"},
		{name: ".kt файл", path: "src/Main.kt"},
		{name: ".kts файл", path: "build.kts"},
		{name: ".scala файл", path: "src/App.scala"},
		{name: ".rb файл", path: "lib/app.rb"},
		{name: ".php файл", path: "src/index.php"},
		{name: ".c файл", path: "src/main.c"},
		{name: ".h файл", path: "src/header.h"},
		{name: ".cc файл", path: "src/app.cc"},
		{name: ".cpp файл", path: "src/app.cpp"},
		{name: ".hpp файл", path: "src/app.hpp"},
		{name: ".m файл", path: "src/AppDelegate.m"},
		{name: ".mm файл", path: "src/AppDelegate.mm"},
		{name: ".swift файл", path: "src/App.swift"},
		{name: ".cs файл", path: "src/App.cs"},
		{name: ".fs файл", path: "src/App.fs"},
		{name: ".vb файл", path: "src/App.vb"},
		{name: ".ex файл", path: "lib/app.ex"},
		{name: ".exs файл", path: "lib/app.exs"},
		{name: ".erl файл", path: "src/app.erl"},
		{name: ".hrl файл", path: "src/app.hrl"},
		{name: ".lua файл", path: "scripts/app.lua"},
		{name: ".pl файл", path: "scripts/app.pl"},
		{name: ".pm файл", path: "lib/App.pm"},
		{name: ".sh файл", path: "scripts/deploy.sh"},
		{name: ".bash файл", path: "scripts/setup.bash"},
		{name: ".zsh файл", path: "scripts/setup.zsh"},
		{name: ".sql файл", path: "migrations/001_init.sql"},
		{name: ".proto файл", path: "api/service.proto"},
		{name: ".go файл в корне", path: "main.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSourceCodePath(tt.path)

			assert.True(t, result)
		})
	}
}
