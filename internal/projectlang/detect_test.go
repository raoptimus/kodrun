package projectlang

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetector_Detect(t *testing.T) {
	t.Parallel()

	type setup func(t *testing.T, dir string)

	tests := []struct {
		name  string
		setup setup
		want  Language
	}{
		{
			name:  "empty directory",
			setup: func(_ *testing.T, _ string) {},
			want:  LangUnknown,
		},
		{
			name: "go.mod present",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod", "module x\n")
			},
			want: LangGo,
		},
		{
			name: "pyproject.toml present",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "pyproject.toml", "")
			},
			want: LangPython,
		},
		{
			name: "requirements.txt present",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "requirements.txt", "")
			},
			want: LangPython,
		},
		{
			name: "package.json present",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "package.json", "{}")
			},
			want: LangJSTS,
		},
		{
			name: "go and package.json — go wins",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod", "module x\n")
				writeFile(t, dir, "package.json", "{}")
			},
			want: LangGo,
		},
		{
			name: "python and jsts — python wins",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "pyproject.toml", "")
				writeFile(t, dir, "package.json", "{}")
			},
			want: LangPython,
		},
		{
			name: "directory named go.mod is ignored",
			setup: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "go.mod"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			want: LangUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)
			got := New(dir).Detect()
			if got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetector_NilSafety(t *testing.T) {
	t.Parallel()
	var d *Detector
	if got := d.Detect(); got != LangUnknown {
		t.Errorf("nil Detector.Detect() = %q, want LangUnknown", got)
	}
	if got := New("").Detect(); got != LangUnknown {
		t.Errorf("empty workDir Detect() = %q, want LangUnknown", got)
	}
	if got := New("/nonexistent/path/that/does/not/exist").Detect(); got != LangUnknown {
		t.Errorf("nonexistent workDir Detect() = %q, want LangUnknown", got)
	}
}

func TestState_EnsureDetected(t *testing.T) {
	t.Parallel()

	t.Run("override short-circuits detection", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module x\n")
		s := NewState(New(dir), LangPython)
		got, changed := s.EnsureDetected()
		if got != LangPython {
			t.Errorf("got %q, want LangPython", got)
		}
		if changed {
			t.Errorf("changed = true, want false (override means not 'newly detected')")
		}
	})

	t.Run("detects on first call when initially unknown", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		s := NewState(New(dir), LangUnknown)
		// Empty dir → still unknown.
		got, changed := s.EnsureDetected()
		if got != LangUnknown || changed {
			t.Errorf("empty: got=%q changed=%v, want unknown,false", got, changed)
		}
		// Now create marker.
		writeFile(t, dir, "go.mod", "module x\n")
		got, changed = s.EnsureDetected()
		if got != LangGo || !changed {
			t.Errorf("after marker: got=%q changed=%v, want go,true", got, changed)
		}
		// Subsequent call: not changed.
		got, changed = s.EnsureDetected()
		if got != LangGo || changed {
			t.Errorf("idempotent: got=%q changed=%v, want go,false", got, changed)
		}
	})
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
