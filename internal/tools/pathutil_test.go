package tools

import (
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
			_, err := SafePath(tt.workDir, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafePath() error = %v, wantErr %v", err, tt.wantErr)
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
			if got := IsForbidden(tt.path, patterns); got != tt.want {
				t.Errorf("IsForbidden(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
