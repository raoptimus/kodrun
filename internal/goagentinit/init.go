package goagentinit

import (
	"fmt"
	"os"
	"path/filepath"
)

// Run creates the .goagent/ starter structure.
func Run(workDir string) error {
	dirs := []string{
		".goagent/rules",
		".goagent/docs",
		".goagent/commands",
	}

	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(workDir, d), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}

	files := map[string]string{
		".goagent/rules/style.md": `---
priority: high
scope: coding
---

# Go Style Rules

- Use errors.Is/errors.As instead of direct comparison
- All public functions must have godoc comments
- Prefer table-driven tests
- Handle all errors explicitly
`,
		".goagent/rules/errors.md": `---
priority: high
scope: all
---

# Error Handling

- Always wrap errors with context: fmt.Errorf("operation: %w", err)
- Define sentinel errors as package-level vars
- Use errors.Is/errors.As for error checking
`,
		".goagent/docs/README.md": `# Project Documentation

Add your project-specific documentation here.
GoAgent will include these files in its context.
`,
		".goagent/commands/review.md": `---
command: /review
description: "Review a Go file for issues"
---

Review the file {{file}} for:
- Bugs and logic errors
- Error handling issues
- Code style violations
- Missing tests
- Performance concerns

Provide specific, actionable feedback.
`,
		".goagent/commands/refactor.md": `---
command: /refactor
description: "Refactor code based on description"
---

Refactor the code as described: {{description}}

Requirements:
- Maintain existing behavior
- Run go_build and go_test after changes
- Keep changes minimal
`,
		".goagent/.gitignore": `# GoAgent local state
*.log
`,
	}

	for path, content := range files {
		fullPath := filepath.Join(workDir, path)
		if _, err := os.Stat(fullPath); err == nil {
			continue // Don't overwrite existing files
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	return nil
}
