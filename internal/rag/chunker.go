package rag

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaxChunkBytes is the maximum size in bytes for a single chunk.
// ~500-650 tokens at ~3-4 chars/token, safely under nomic-embed-text's 2048 token context.
const MaxChunkBytes = 2000

// Chunk represents a text chunk with metadata.
type Chunk struct {
	FilePath string    `json:"file_path"`
	Content  string    `json:"content"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
}

// ChunkFile reads a single file and splits it into chunks.
func ChunkFile(filePath string, chunkSize, chunkOverlap int) ([]Chunk, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	if len(content) == 0 {
		// Empty file produces no chunks; nil slice is valid for []Chunk.
		return nil, nil
	}
	return splitIntoChunks(filePath, content, chunkSize, chunkOverlap), nil
}

// ChunkOptions carries optional chunking controls. A zero value is safe:
// empty ExcludeDirs means "skip only the built-in noise directories"
// (.git, vendor, node_modules, any dot-prefixed dir), and MaxChunksPerFile=0
// disables the per-file cap.
type ChunkOptions struct {
	// ExcludeDirs are directory names or workDir-relative paths to skip
	// in addition to the built-in defaults. An entry like ".claude" skips
	// every directory with that base name; "tmp/fixtures" skips that
	// specific relative path under workDir.
	ExcludeDirs []string
	// MaxChunksPerFile caps how many chunks a single file can contribute.
	// 0 means unlimited.
	MaxChunksPerFile int
}

// ChunkFiles walks the given directories and splits files into chunks.
// It respects forbidden patterns and only processes text-like files.
func ChunkFiles(ctx context.Context, workDir string, dirs []string, chunkSize, chunkOverlap int) ([]Chunk, error) {
	return ChunkFilesOpts(ctx, workDir, dirs, chunkSize, chunkOverlap, ChunkOptions{})
}

// ChunkFilesOpts is ChunkFiles with explicit ChunkOptions.
func ChunkFilesOpts(ctx context.Context, workDir string, dirs []string, chunkSize, chunkOverlap int, opts ChunkOptions) ([]Chunk, error) {
	var chunks []Chunk

	// Pre-compute exclude sets for O(1) lookup.
	excludeBase := map[string]bool{}
	var excludeRel []string
	for _, e := range opts.ExcludeDirs {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.ContainsRune(e, filepath.Separator) {
			excludeRel = append(excludeRel, filepath.Clean(e))
		} else {
			excludeBase[e] = true
		}
	}

	seen := make(map[string]bool)
	for _, dir := range dirs {
		absDir := dir
		if !filepath.IsAbs(dir) {
			absDir = filepath.Join(workDir, dir)
		}

		err := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil // skip inaccessible
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
					return filepath.SkipDir
				}
				if excludeBase[base] {
					return filepath.SkipDir
				}
				if len(excludeRel) > 0 {
					rel, _ := filepath.Rel(workDir, path)
					rel = filepath.Clean(rel)
					for _, ex := range excludeRel {
						if rel == ex || strings.HasPrefix(rel, ex+string(filepath.Separator)) {
							return filepath.SkipDir
						}
					}
				}
				return nil
			}
			if !isIndexableFile(path) {
				return nil
			}
			relPath, _ := filepath.Rel(workDir, path)
			if relPath == "" {
				relPath = path
			}
			if seen[relPath] {
				return nil
			}
			seen[relPath] = true

			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			content := string(data)
			if len(content) == 0 {
				return nil
			}

			fileChunks := splitIntoChunks(relPath, content, chunkSize, chunkOverlap)
			if opts.MaxChunksPerFile > 0 && len(fileChunks) > opts.MaxChunksPerFile {
				fileChunks = fileChunks[:opts.MaxChunksPerFile]
			}
			chunks = append(chunks, fileChunks...)
			return nil
		})
		if err != nil {
			return chunks, err
		}
	}

	return chunks, nil
}

// ChunkText splits arbitrary text into chunks. The source parameter is used
// as FilePath in the resulting chunks (e.g. "web://example.com/page").
func ChunkText(source, content string, chunkSize, chunkOverlap int) []Chunk {
	return splitIntoChunks(source, content, chunkSize, chunkOverlap)
}

func splitIntoChunks(filePath, content string, chunkSize, overlap int) []Chunk {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	var chunks []Chunk
	i := 0
	for i < len(lines) {
		end := i
		byteCount := 0
		for end < len(lines) && (end-i) < chunkSize {
			lineLen := len(lines[end]) + 1 // +1 for newline
			if byteCount+lineLen > MaxChunkBytes && end > i {
				break
			}
			byteCount += lineLen
			end++
		}

		chunkContent := strings.Join(lines[i:end], "\n")
		if strings.TrimSpace(chunkContent) != "" {
			chunks = append(chunks, Chunk{
				FilePath:  filePath,
				Content:   chunkContent,
				StartLine: i + 1,
				EndLine:   end,
			})
		}

		if end >= len(lines) {
			break
		}
		next := end - overlap
		if next <= i {
			next = end // ensure forward progress
		}
		i = next
	}

	return chunks
}

var indexableExts = map[string]bool{
	".go": true, ".md": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".toml": true, ".sh": true, ".bash": true,
	".py": true, ".js": true, ".ts": true, ".rs": true, ".c": true,
	".h": true, ".sql": true, ".proto": true, ".graphql": true,
	".html": true, ".css": true, ".xml": true, ".mod": true, ".sum": true,
}

func isIndexableFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, ".") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if indexableExts[ext] {
		return true
	}
	return base == "makefile" || base == "dockerfile" || base == "readme"
}
