package tui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const historyFile = ".kodrun/history"

// LoadHistory reads command history from .kodrun/history file.
// Deduplicates entries keeping the last occurrence and limits to maxSize.
func LoadHistory(workDir string, maxSize int) []string {
	path := filepath.Join(workDir, historyFile)

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}

	lines = dedup(lines)

	if len(lines) > maxSize {
		lines = lines[len(lines)-maxSize:]
	}

	return lines
}

// dedup removes duplicate entries keeping the last occurrence of each.
func dedup(lines []string) []string {
	seen := make(map[string]struct{}, len(lines))
	result := make([]string, 0, len(lines))

	// Iterate from end to preserve last occurrence.
	for i := len(lines) - 1; i >= 0; i-- {
		if _, ok := seen[lines[i]]; ok {
			continue
		}
		seen[lines[i]] = struct{}{}
		result = append(result, lines[i])
	}

	// Reverse to restore chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

// appendCount tracks writes since last trim to avoid trimming on every append.
var appendCount int

// AppendHistory adds an entry to the history file.
func AppendHistory(workDir string, entry string, maxSize int) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}

	path := filepath.Join(workDir, historyFile)

	// Ensure .kodrun/ directory exists
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.WriteString(entry + "\n")

	// Periodically trim and deduplicate the file.
	appendCount++
	if appendCount%50 == 0 {
		TrimHistory(workDir, maxSize)
	}
}

// TrimHistory deduplicates and truncates the history file to maxSize entries.
func TrimHistory(workDir string, maxSize int) {
	path := filepath.Join(workDir, historyFile)

	lines := LoadHistory(workDir, maxSize)
	if len(lines) == 0 {
		return
	}

	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	for _, line := range lines {
		_, _ = f.WriteString(line + "\n")
	}
}
