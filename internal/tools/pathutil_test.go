/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"context"
	"testing"
)

func TestSafePath(t *testing.T) {
	tests := []struct {
		name    string
		workDir string
		path    string
		wantErr bool
	}{
		{
			name:    "valid relative path",
			workDir: "/home/user/project",
			path:    "internal/config.go",
			wantErr: false,
		},
		{
			name:    "path traversal blocked",
			workDir: "/home/user/project",
			path:    "../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "absolute path blocked",
			workDir: "/home/user/project",
			path:    "/etc/passwd",
			wantErr: true,
		},
		{
			name:    "dot path",
			workDir: "/home/user/project",
			path:    ".",
			wantErr: false,
		},
		{
			name:    "hidden traversal",
			workDir: "/home/user/project",
			path:    "foo/../../..",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafePath(context.Background(), tt.workDir, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsForbiddenDir(t *testing.T) {
	patterns := []string{"node_modules/**", "vendor/**"}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"hidden dir .git", ".git", true},
		{"hidden dir .idea", ".idea", true},
		{"hidden dir .build", ".build", true},
		{"hidden dir .claude", ".claude", true},
		{"hidden dir itself", ".hidden", true},
		{"normal dir", "internal", false},
		{"normal nested dir", "internal/tools", false},
		{"pattern match node_modules", "node_modules", true},
		{"pattern match vendor", "vendor", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsForbiddenDir(context.Background(), tt.path, patterns); got != tt.want {
				t.Errorf("IsForbiddenDir(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestHasHiddenPathComponent(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".kodrun/history", true},
		{".git/config", true},
		{".git/hooks/pre-commit", true},
		{"src/.hidden/main.go", true},
		{"foo/.bar/baz", true},
		{".kodrun.yaml", false},
		{".env", false},
		{"src/main.go", false},
		{"internal/tools/file.go", false},
		{".", false},
		{"file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := HasHiddenPathComponent(tt.path); got != tt.want {
				t.Errorf("HasHiddenPathComponent(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsPathBlocked(t *testing.T) {
	patterns := []string{"*.env", "*.pem"}

	tests := []struct {
		name    string
		path    string
		blocked bool
	}{
		{"forbidden by pattern", "prod.env", true},
		{"forbidden by pattern pem", "server.pem", true},
		{"hidden dir path", ".git/config", true},
		{"hidden dir nested", "src/.secret/data.go", true},
		{"normal file", "src/main.go", false},
		{"hidden file not dir", ".kodrun.yaml", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := IsPathBlocked(context.Background(), tt.path, "/work/"+tt.path, patterns)
			if tt.blocked && reason == "" {
				t.Errorf("IsPathBlocked(%q) expected blocked, got empty", tt.path)
			}
			if !tt.blocked && reason != "" {
				t.Errorf("IsPathBlocked(%q) expected allowed, got %q", tt.path, reason)
			}
		})
	}
}

func TestIsForbidden(t *testing.T) {
	patterns := []string{"*.env", ".git/**"}

	tests := []struct {
		path string
		want bool
	}{
		{".env", true},
		{"prod.env", true},
		{"main.go", false},
		{"internal/config.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsForbidden(context.Background(), tt.path, patterns); got != tt.want {
				t.Errorf("IsForbidden(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
