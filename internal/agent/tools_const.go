/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import "github.com/raoptimus/kodrun/internal/tools"

// Tool name aliases used by the agent package. All values come from the
// canonical constants in internal/tools/names.go to avoid duplication.
const (
	toolNameReadFile   = tools.NameReadFile
	toolNameWriteFile  = tools.NameWriteFile
	toolNameEditFile   = tools.NameEditFile
	toolNameDeleteFile = tools.NameDeleteFile
	toolNameMoveFile   = tools.NameMoveFile
	toolNameFindFiles  = tools.NameFindFiles
	toolNameListDir    = tools.NameListDir
	toolNameCreateDir  = tools.NameCreateDir
	toolNameGrep       = tools.NameGrep
	toolNameBash       = tools.NameBash
	toolNameGoBuild    = tools.NameGoBuild
	toolNameGoTest     = tools.NameGoTest
	toolNameGoLint     = tools.NameGoLint
	toolNameGoVet      = tools.NameGoVet
	toolNameGoDoc      = tools.NameGoDoc
	toolNameSearchDocs = tools.NameSearchDocs
	toolNameSnippets   = tools.NameSnippets
	toolNameGitStatus  = tools.NameGitStatus
	toolNameGitDiff    = tools.NameGitDiff
	toolNameGitLog     = tools.NameGitLog
	toolNameGitCommit  = tools.NameGitCommit
	toolNameGetRule    = tools.NameGetRule
	toolNameWebFetch   = tools.NameWebFetch
)
