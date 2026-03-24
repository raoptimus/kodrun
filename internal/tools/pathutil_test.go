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
