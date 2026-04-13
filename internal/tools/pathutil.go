package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// SafePath resolves a path relative to workDir and ensures it doesn't escape.
func SafePath(_ context.Context, workDir, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", errors.Errorf("absolute paths not allowed: %s", path)
	}

	resolved := filepath.Join(workDir, path)
	resolved = filepath.Clean(resolved)

	// Ensure the resolved path is under workDir
	rel, err := filepath.Rel(workDir, resolved)
	if err != nil {
		return "", errors.WithMessage(err, "resolve path")
	}

	if strings.HasPrefix(rel, "..") {
		return "", errors.Errorf("path traversal not allowed: %s", path)
	}

	// Resolve symlinks and re-check to prevent symlink-based traversal.
	if realPath, err := filepath.EvalSymlinks(resolved); err == nil {
		realWorkDir := workDir
		if rwd, e := filepath.EvalSymlinks(workDir); e == nil {
			realWorkDir = rwd
		}
		realRel, relErr := filepath.Rel(realWorkDir, realPath)
		if relErr != nil || strings.HasPrefix(realRel, "..") {
			return "", errors.Errorf("symlink escapes work directory: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return "", errors.WithMessage(err, "resolve symlink")
	}

	return resolved, nil
}

// IsForbiddenDir checks if a directory should be skipped during Walk.
// Hidden directories (starting with ".") are always forbidden.
// Additionally matches patterns like ".git/**" against directory name.
func IsForbiddenDir(_ context.Context, relPath string, patterns []string) bool {
	dirName := filepath.Base(relPath)
	if strings.HasPrefix(dirName, ".") {
		return true
	}
	for _, pattern := range patterns {
		if dirPattern, ok := strings.CutSuffix(pattern, "/**"); ok {
			if matched, err := filepath.Match(dirPattern, dirName); err == nil && matched {
				return true
			}
		}
	}
	return false
}

// HasHiddenPathComponent returns true if any directory component in path
// starts with ".". It does NOT check the final filename component —
// only the directory segments leading to it.
func HasHiddenPathComponent(path string) bool {
	dir := filepath.Dir(path)
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if part == "." || part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// IsPathBlocked returns a non-empty reason if the path should be blocked.
// It checks both forbidden patterns and hidden directory components.
func IsPathBlocked(ctx context.Context, path, resolved string, patterns []string) string {
	if IsForbidden(ctx, path, patterns) || IsForbidden(ctx, resolved, patterns) {
		return fmt.Sprintf("access to %s is forbidden", path)
	}
	if HasHiddenPathComponent(path) {
		return fmt.Sprintf("access to %s is forbidden: path contains hidden directory", path)
	}
	return ""
}

// IsForbidden checks if a path matches any forbidden patterns.
func IsForbidden(_ context.Context, path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matched, err := filepath.Match(pattern, filepath.Base(path)); err == nil && matched {
			return true
		}
		// Also check full relative path for glob patterns
		if matched, err := filepath.Match(pattern, path); err == nil && matched {
			return true
		}
	}
	return false
}
