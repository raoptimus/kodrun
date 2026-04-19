/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import "strings"

// ToolDisplayName maps internal tool names to user-friendly display names.
func ToolDisplayName(name string) string {
	switch name {
	case toolNameReadFile:
		return "Read"
	case toolNameWriteFile:
		return "Write"
	case toolNameEditFile:
		return "Edit"
	case "delete_file":
		return actionDelete
	case "list_dir":
		return "ListDir"
	case "find_files":
		return "Find"
	case "create_dir":
		return "CreateDir"
	case toolNameMoveFile:
		return "Move"
	case "grep":
		return "Grep"
	case "go_build":
		return "Build"
	case toolNameGoTest:
		return "Test"
	case "go_lint":
		return "Lint"
	case toolNameGoVet:
		return "Vet"
	case "go_doc":
		return "GoDoc"
	case "go_structure":
		return "Structure"
	case "search_docs":
		return "SearchDocs"
	case toolNameBash:
		return "Bash"
	case "snippets":
		return "Snippets"
	case "git_status":
		return "GitStatus"
	case "git_diff":
		return "GitDiff"
	case "git_log":
		return "GitLog"
	case toolNameGitCommit:
		return "GitCommit"
	case "get_rule":
		return "GetRule"
	default:
		return snakeToPascal(name)
	}
}

func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
