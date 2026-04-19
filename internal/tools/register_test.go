/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"testing"

	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterCoreTools_RegistersExpectedTools_Successfully(t *testing.T) {
	reg := NewRegistry()

	RegisterCoreTools(reg, t.TempDir(), nil, 500, nil)

	names := reg.Names()
	expectedTools := []string{
		"bash",
		"create_dir",
		"delete_file",
		"edit_file",
		"file_stat",
		"find_files",
		"git_commit",
		"git_diff",
		"git_log",
		"git_status",
		"grep",
		"list_dir",
		"move_file",
		"read_changed_files",
		"read_file",
		"write_file",
	}
	for _, expected := range expectedTools {
		assert.Contains(t, names, expected, "missing tool: %s", expected)
	}
}

func TestRegisterLanguageTools_Go_Successfully(t *testing.T) {
	reg := NewRegistry()

	RegisterLanguageTools(reg, projectlang.LangGo, t.TempDir(), nil)

	names := reg.Names()
	assert.Contains(t, names, "go_build")
	assert.Contains(t, names, "go_test")
	assert.Contains(t, names, "go_vet")
	assert.Contains(t, names, "go_fmt")
	assert.Contains(t, names, "go_lint")
	assert.Contains(t, names, "go_mod_tidy")
	assert.Contains(t, names, "go_get")
	assert.Contains(t, names, "go_doc")
	assert.Contains(t, names, "go_structure")
}

func TestRegisterLanguageTools_Python_Successfully(t *testing.T) {
	reg := NewRegistry()

	RegisterLanguageTools(reg, projectlang.LangPython, t.TempDir(), nil)

	names := reg.Names()
	assert.Contains(t, names, "python_run")
	assert.Contains(t, names, "pytest")
	assert.Contains(t, names, "pip_install")
	assert.Contains(t, names, "ruff")
	assert.Contains(t, names, "black")
}

func TestRegisterLanguageTools_JSTS_Successfully(t *testing.T) {
	reg := NewRegistry()

	RegisterLanguageTools(reg, projectlang.LangJSTS, t.TempDir(), nil)

	names := reg.Names()
	assert.Contains(t, names, "npm_install")
	assert.Contains(t, names, "npm_run")
	assert.Contains(t, names, "npm_test")
	assert.Contains(t, names, "tsc")
	assert.Contains(t, names, "eslint")
}

func TestRegisterLanguageTools_UnknownLanguage_Successfully(t *testing.T) {
	reg := NewRegistry()

	RegisterLanguageTools(reg, projectlang.Language("unknown"), t.TempDir(), nil)

	names := reg.Names()
	require.Empty(t, names)
}
