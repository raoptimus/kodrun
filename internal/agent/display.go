package agent

import "strings"

// ToolDisplayName maps internal tool names to user-friendly display names.
func ToolDisplayName(name string) string {
	switch name {
	case "read_file":
		return "Read"
	case "write_file":
		return "Write"
	case "edit_file":
		return "Edit"
	case "delete_file":
		return "Delete"
	case "list_dir":
		return "ListDir"
	case "find_files":
		return "Find"
	case "create_dir":
		return "CreateDir"
	case "move_file":
		return "Move"
	case "grep":
		return "Grep"
	case "go_build":
		return "Build"
	case "go_test":
		return "Test"
	case "go_lint":
		return "Lint"
	case "go_vet":
		return "Vet"
	case "go_doc":
		return "GoDoc"
	case "search_docs":
		return "SearchDocs"
	case "bash":
		return "Bash"
	case "snippets":
		return "Snippets"
	case "git_status":
		return "GitStatus"
	case "git_diff":
		return "GitDiff"
	case "git_log":
		return "GitLog"
	case "git_commit":
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
