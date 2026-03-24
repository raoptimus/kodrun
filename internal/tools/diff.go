package tools

import (
	"fmt"
	"strings"
)

// LineStats computes the number of added and removed lines between old and new content.
func LineStats(oldContent, newContent string) (added, removed int) {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	oldSet := make(map[string]int, len(oldLines))
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := make(map[string]int, len(newLines))
	for _, l := range newLines {
		newSet[l]++
	}

	for line, count := range oldSet {
		nc := newSet[line]
		if count > nc {
			removed += count - nc
		}
	}
	for line, count := range newSet {
		oc := oldSet[line]
		if count > oc {
			added += count - oc
		}
	}
	return added, removed
}

// FileActionType returns the action type based on whether the file existed before.
func FileActionType(existed bool) string {
	if existed {
		return "Update"
	}
	return "Add"
}

// SimpleDiff generates a compact diff string showing changed lines.
// Returns at most maxLines of output. Uses a simple line-by-line comparison.
func SimpleDiff(oldContent, newContent, path string, maxLines int) string {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	if len(oldLines)+len(newLines) > 5000 {
		return fmt.Sprintf("(diff too large: %d → %d lines)", len(oldLines), len(newLines))
	}

	// Find changed regions using simple forward scan
	type chunk struct {
		oldStart int
		removed  []string
		added    []string
	}

	var chunks []chunk
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			i++
			j++
			continue
		}
		// Found a difference — scan forward to find next common line
		c := chunk{oldStart: i + 1}
		oi, nj := i, j
		for oi < len(oldLines) && !containsFrom(newLines, nj, oldLines[oi]) {
			c.removed = append(c.removed, oldLines[oi])
			oi++
		}
		for nj < len(newLines) && (oi >= len(oldLines) || newLines[nj] != oldLines[oi]) {
			c.added = append(c.added, newLines[nj])
			nj++
		}
		chunks = append(chunks, c)
		i, j = oi, nj
	}
	// Remaining lines
	if i < len(oldLines) || j < len(newLines) {
		c := chunk{oldStart: i + 1}
		for i < len(oldLines) {
			c.removed = append(c.removed, oldLines[i])
			i++
		}
		for j < len(newLines) {
			c.added = append(c.added, newLines[j])
			j++
		}
		chunks = append(chunks, c)
	}

	if len(chunks) == 0 {
		return ""
	}

	var b strings.Builder
	lines := 0
	for _, c := range chunks {
		if lines >= maxLines {
			remaining := 0
			for _, cc := range chunks {
				remaining += len(cc.removed) + len(cc.added)
			}
			b.WriteString(fmt.Sprintf("... (%d more lines)\n", remaining))
			break
		}
		b.WriteString(fmt.Sprintf("@@ line %d @@\n", c.oldStart))
		lines++
		for _, l := range c.removed {
			if lines >= maxLines {
				b.WriteString("...\n")
				break
			}
			b.WriteString("-" + l + "\n")
			lines++
		}
		for _, l := range c.added {
			if lines >= maxLines {
				b.WriteString("...\n")
				break
			}
			b.WriteString("+" + l + "\n")
			lines++
		}
	}

	return b.String()
}

func containsFrom(lines []string, start int, target string) bool {
	end := start + 10 // look ahead up to 10 lines
	if end > len(lines) {
		end = len(lines)
	}
	for k := start; k < end; k++ {
		if lines[k] == target {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
