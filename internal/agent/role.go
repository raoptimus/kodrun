package agent

import (
	"fmt"
	"strings"
)

// Role identifies a sub-agent's purpose in the orchestrator pipeline.
type Role string

const (
	RolePlanner  Role = "planner"
	RoleExecutor Role = "executor"
	RoleReviewer Role = "reviewer"
)

// systemPromptForRole generates a role-specific system prompt for a sub-agent.
// Optional bool flags: hasSnippets, hasRAG.
func systemPromptForRole(role Role, lang, ruleCatalog string, toolNames []string, flags ...bool) string {
	snippetsEnabled := len(flags) > 0 && flags[0]
	ragEnabled := len(flags) > 1 && flags[1]
	var b strings.Builder

	b.WriteString("You are KodRun, a Go programming assistant.\n")
	ln := langName(lang)
	if ln != "English" {
		fmt.Fprintf(&b, "IMPORTANT: ALL your responses MUST be in %s. This is mandatory.\n", ln)
	}
	b.WriteByte('\n')

	if ruleCatalog != "" {
		b.WriteString(ruleCatalog)
		b.WriteString("\n")
	}

	switch role {
	case RolePlanner:
		b.WriteString("You are the PLANNER agent.\n")
		b.WriteString("Your job is to analyze the task and create a detailed, actionable plan.\n\n")
		b.WriteString("Guidelines:\n")
		b.WriteString("- Read and analyze the relevant code using read-only tools\n")
		b.WriteString("- Identify all files that need changes\n")
		b.WriteString("- Create a numbered step-by-step plan\n")
		b.WriteString("- Each step should reference a specific file:line\n")
		b.WriteString("- Describe concrete issues found, not vague suggestions\n")
		b.WriteString("- Do NOT write code or generate code blocks\n")
		b.WriteString("- Be concise and actionable\n")
		b.WriteString("- Use idiomatic Go, Go best practices and project conventions\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
		if ragEnabled {
			b.WriteString("\nIMPORTANT — Project conventions (from RAG):\n")
			b.WriteString("Project conventions and documentation are automatically included in the task context.\n")
			b.WriteString("You MUST follow ALL provided conventions. Include them as requirements in your plan.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if snippetsEnabled {
			b.WriteString("\nIMPORTANT — Code conventions check:\n")
			b.WriteString("BEFORE creating the plan, you MUST call snippets(paths=[<list of all .go files you read>])\n")
			b.WriteString("to get the project's code conventions. Include violations of these conventions in your plan.\n")
		}

	case RoleExecutor:
		b.WriteString("You are the EXECUTOR agent.\n")
		b.WriteString("Your job is to IMPLEMENT an approved plan by writing/editing code.\n\n")
		b.WriteString("Available tools: " + strings.Join(toolNames, ", ") + "\n\n")
		b.WriteString("CRITICAL RULES:\n")
		b.WriteString("- The source code is PROVIDED in the task. Do NOT call read_file or list_dir.\n")
		b.WriteString("- Start IMMEDIATELY with edit_file/write_file. No analysis, no re-reading.\n")
		b.WriteString("- Do NOT re-analyze or re-review the code. The plan is already approved.\n\n")
		b.WriteString("Guidelines:\n")
		b.WriteString("- Follow the plan exactly, step by step\n")
		b.WriteString("- Use edit_file for small changes, write_file for new files\n")
		b.WriteString("- After each step, confirm what was done\n")
		b.WriteString("- Run go_build after changes to verify compilation\n")
		b.WriteString("- Run go_lint to check style\n")
		b.WriteString("- Run go_test to verify correctness\n")
		b.WriteString("- Fix any errors before proceeding to next step\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
		if ragEnabled {
			b.WriteString("\nIMPORTANT — Project conventions (from RAG):\n")
			b.WriteString("Project conventions and documentation are automatically included in the task context.\n")
			b.WriteString("You MUST follow ALL provided conventions (naming, structure, patterns, error handling).\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if snippetsEnabled {
			b.WriteString("\nIMPORTANT — Code conventions:\n")
			b.WriteString("BEFORE editing a file, call snippets(paths=[<file_path>]) to get conventions for that file.\n")
			b.WriteString("Follow ALL matched conventions (naming, structure, patterns, error handling).\n")
		}

	case RoleReviewer:
		b.WriteString("You are the REVIEWER agent.\n")
		b.WriteString("Your job is to review changes made by the executor.\n\n")
		if ragEnabled {
			b.WriteString("IMPORTANT — Project conventions (from RAG):\n")
			b.WriteString("Project conventions and documentation are automatically included in the task context.\n")
			b.WriteString("You MUST use provided conventions as review criteria. Flag any violations.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n\n")
		} else if snippetsEnabled {
			b.WriteString("IMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("BEFORE reviewing code, call snippets(paths=[<list of changed files>])\n")
			b.WriteString("to get the project's code conventions. Use found conventions as the review criteria.\n\n")
		}
		b.WriteString("Guidelines:\n")
		b.WriteString("- Read the changed files using read-only tools\n")
		b.WriteString("- Check for bugs, logic errors, and edge cases\n")
		b.WriteString("- Check for Go best practices and project conventions\n")
		b.WriteString("- Check for security issues\n")
		b.WriteString("- Verify error handling\n")
		b.WriteString("- If changes look good, say so clearly\n")
		b.WriteString("- If there are issues, list them with specific file:line references\n")
		b.WriteString("- Be concise — only flag real issues, not style preferences\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
	}

	return b.String()
}
