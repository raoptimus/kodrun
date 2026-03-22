package runner

import (
	"regexp"
	"strconv"
	"strings"
)

// ParsedError represents a parsed Go error with file location.
type ParsedError struct {
	File    string
	Line    int
	Col     int
	Message string
	Raw     string
}

var (
	// go build / go vet: ./path/file.go:10:5: error message
	reBuild = regexp.MustCompile(`^\.?/?(.+\.go):(\d+):(\d+):\s*(.+)$`)
	// go test failure: file_test.go:32: message
	reTest = regexp.MustCompile(`^\s*(.+_test\.go):(\d+):\s*(.+)$`)
	// golangci-lint: path/file.go:12:1: message (linter)
	reLint = regexp.MustCompile(`^(.+\.go):(\d+):(\d+):\s*(.+)$`)
	// test FAIL line
	reTestFail = regexp.MustCompile(`^---\s*FAIL:\s*(\w+)\s*\(`)
)

// ParseErrors extracts structured errors from command output.
func ParseErrors(output string) []ParsedError {
	var errors []ParsedError
	seen := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var pe ParsedError

		if m := reBuild.FindStringSubmatch(line); m != nil {
			pe.File = m[1]
			pe.Line, _ = strconv.Atoi(m[2])
			pe.Col, _ = strconv.Atoi(m[3])
			pe.Message = m[4]
		} else if m := reLint.FindStringSubmatch(line); m != nil {
			pe.File = m[1]
			pe.Line, _ = strconv.Atoi(m[2])
			pe.Col, _ = strconv.Atoi(m[3])
			pe.Message = m[4]
		} else if m := reTest.FindStringSubmatch(line); m != nil {
			pe.File = m[1]
			pe.Line, _ = strconv.Atoi(m[2])
			pe.Message = m[3]
		} else {
			continue
		}

		pe.Raw = line
		key := pe.File + ":" + strconv.Itoa(pe.Line) + ":" + pe.Message
		if !seen[key] {
			seen[key] = true
			errors = append(errors, pe)
		}
	}

	return errors
}

// FormatErrors converts parsed errors to a human-readable string.
func FormatErrors(errors []ParsedError) string {
	if len(errors) == 0 {
		return ""
	}

	var b strings.Builder
	for i, e := range errors {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Raw)
	}
	return b.String()
}

// AffectedFiles returns unique file paths from parsed errors.
func AffectedFiles(errors []ParsedError) []string {
	seen := make(map[string]bool)
	var files []string
	for _, e := range errors {
		if e.File != "" && !seen[e.File] {
			seen[e.File] = true
			files = append(files, e.File)
		}
	}
	return files
}
