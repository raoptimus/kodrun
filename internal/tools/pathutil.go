package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafePath resolves a path relative to workDir and ensures it doesn't escape.
func SafePath(workDir, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute paths not allowed: %s", path)
	}

	resolved := filepath.Join(workDir, path)
	resolved = filepath.Clean(resolved)

	// Ensure the resolved path is under workDir
	rel, err := filepath.Rel(workDir, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path traversal not allowed: %s", path)
	}

	return resolved, nil
}

// IsForbidden checks if a path matches any forbidden patterns.
func IsForbidden(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
		// Also check full relative path for glob patterns
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
	}
	return false
}
