/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

// Canonical tool names. Each Tool implementation in this package returns one
// of these constants from Name(); other packages reference the same constants
// instead of duplicating the string literals.
const (
	NameReadFile         = "read_file"
	NameReadChangedFiles = "read_changed_files"
	NameWriteFile        = "write_file"
	NameEditFile         = "edit_file"
	NameDeleteFile       = "delete_file"
	NameMoveFile         = "move_file"
	NameCreateDir        = "create_dir"
	NameListDir          = "list_dir"
	NameFindFiles        = "find_files"
	NameGrep             = "grep"
	NameFileStat         = "file_stat"
	NameBash             = "bash"

	NameGoBuild     = "go_build"
	NameGoTest      = "go_test"
	NameGoVet       = "go_vet"
	NameGoFmt       = "go_fmt"
	NameGoLint      = "go_lint"
	NameGoModTidy   = "go_mod_tidy"
	NameGoGet       = "go_get"
	NameGoDoc       = "go_doc"
	NameGoStructure = "go_structure"

	NamePythonRun  = "python_run"
	NamePytest     = "pytest"
	NamePipInstall = "pip_install"
	NameRuff       = "ruff"
	NameBlack      = "black"

	NameNpmInstall = "npm_install"
	NameNpmRun     = "npm_run"
	NameNpmTest    = "npm_test"
	NameTSC        = "tsc"
	NameESLint     = "eslint"

	NameSearchDocs = "search_docs"
	NameSnippets   = "snippets"
	NameGetRule    = "get_rule"
	NameWebFetch   = "web_fetch"

	NameGitStatus = "git_status"
	NameGitDiff   = "git_diff"
	NameGitLog    = "git_log"
	NameGitCommit = "git_commit"
)
