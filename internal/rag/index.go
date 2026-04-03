package rag

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
)

const maxEmbedInputBytes = 2000 // ~500-650 tokens, safely under nomic-embed-text 2048 token context

func truncateInput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	truncated := s[:maxBytes]
	if idx := strings.LastIndex(truncated, "\n"); idx > maxBytes/2 {
		return truncated[:idx]
	}
	return truncated
}

// IndexEntry stores a chunk with its embedding.
type IndexEntry struct {
	Chunk     Chunk     `json:"chunk"`
	Embedding []float64 `json:"embedding"`
	Hash      string    `json:"hash"`
}

// Index is an in-memory vector index with disk persistence.
type Index struct {
	mu      sync.RWMutex
	entries []IndexEntry
	path    string
	model   string
	client  *ollama.Client
	updated time.Time
}

// NewIndex creates a new RAG index.
func NewIndex(client *ollama.Client, model, indexPath string) *Index {
	return &Index{
		client: client,
		model:  model,
		path:   indexPath,
	}
}

// Load reads the index from disk.
func (idx *Index) Load() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	dataPath := filepath.Join(idx.path, "index.json")
	data, err := os.ReadFile(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.WithMessage(err, "read index")
	}

	var entries []IndexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return errors.WithMessage(err, "decode index")
	}

	idx.entries = entries
	return nil
}

// Save writes the index to disk.
func (idx *Index) Save() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if err := os.MkdirAll(idx.path, 0o755); err != nil {
		return errors.WithMessage(err, "create index dir")
	}

	data, err := json.Marshal(idx.entries)
	if err != nil {
		return errors.WithMessage(err, "encode index")
	}

	return os.WriteFile(filepath.Join(idx.path, "index.json"), data, 0o644)
}

// ProgressFunc is invoked from Build/BuildWithProgress while embedding
// chunks. done counts chunks that have already been embedded; total is the
// number of new chunks that need embedding (not the input chunk count, since
// already-indexed chunks are skipped). label is a short human-readable hint.
type ProgressFunc func(label string, done, total int)

// Build indexes the given chunks, skipping already-indexed ones.
// Returns the number of new chunks indexed.
func (idx *Index) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return idx.BuildWithProgress(ctx, chunks, nil)
}

// BuildWithProgress is Build with an optional progress callback. The callback
// is invoked once with (0, total) when embedding starts, then after each
// embedding batch with (done, total), and finally with (total, total) when
// the embedding loop finishes (before merge/prune).
func (idx *Index) BuildWithProgress(ctx context.Context, chunks []Chunk, progress ProgressFunc) (int, error) {
	// Phase 1: Read existing hashes under RLock.
	idx.mu.RLock()
	existing := make(map[string]bool, len(idx.entries))
	for _, e := range idx.entries {
		existing[e.Hash] = true
	}
	idx.mu.RUnlock()

	// Find new chunks.
	newChunks := make([]Chunk, 0, len(chunks)/2)
	newHashes := make([]string, 0, len(chunks)/2)
	for _, c := range chunks {
		h := chunkHash(c)
		if !existing[h] {
			newChunks = append(newChunks, c)
			newHashes = append(newHashes, h)
		}
	}

	if len(newChunks) == 0 {
		// Still need to prune stale entries.
		idx.pruneStale(chunks)
		if progress != nil {
			progress("up to date", 0, 0)
		}
		return 0, nil
	}

	// Phase 2: Embed in batches WITHOUT holding the lock.
	const batchSize = 4
	total := len(newChunks)
	if progress != nil {
		progress("embedding", 0, total)
	}
	newEntries := make([]IndexEntry, 0, len(newChunks))
	for i := 0; i < len(newChunks); i += batchSize {
		select {
		case <-ctx.Done():
			return len(newEntries), ctx.Err()
		default:
		}

		end := i + batchSize
		if end > len(newChunks) {
			end = len(newChunks)
		}

		batch := newChunks[i:end]
		inputs := make([]string, len(batch))
		for j, c := range batch {
			// Prefix with file path for context, truncate to stay within embedding model limits
			inputs[j] = truncateInput(fmt.Sprintf("File: %s\n%s", c.FilePath, c.Content), maxEmbedInputBytes)
		}

		resp, err := idx.client.Embed(ctx, ollama.EmbedRequest{
			Model:    idx.model,
			Input:    inputs,
			Truncate: true,
		})
		if err != nil {
			return len(newEntries), errors.WithMessagef(err, "embed batch %d", i/batchSize)
		}

		if len(resp.Embeddings) != len(batch) {
			return len(newEntries), errors.Errorf("embed batch: got %d embeddings, expected %d", len(resp.Embeddings), len(batch))
		}

		for j, emb := range resp.Embeddings {
			newEntries = append(newEntries, IndexEntry{
				Chunk:     batch[j],
				Embedding: emb,
				Hash:      newHashes[i+j],
			})
		}
		if progress != nil {
			progress("embedding", len(newEntries), total)
		}
	}

	// Phase 3: Merge results under exclusive lock.
	idx.mu.Lock()
	idx.entries = append(idx.entries, newEntries...)
	idx.updated = time.Now()
	idx.mu.Unlock()

	// Prune stale entries.
	idx.pruneStale(chunks)

	return len(newChunks), nil
}

// pruneStale removes entries for chunks that are no longer present.
func (idx *Index) pruneStale(chunks []Chunk) {
	chunkSet := make(map[string]bool, len(chunks))
	for _, c := range chunks {
		chunkSet[chunkHash(c)] = true
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	filtered := idx.entries[:0]
	for _, e := range idx.entries {
		if chunkSet[e.Hash] {
			filtered = append(filtered, e)
		}
	}
	idx.entries = filtered
}

// Search finds the top-k most similar chunks to the query.
func (idx *Index) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.entries) == 0 {
		// No entries to search; nil slice is valid for []SearchResult.
		return nil, nil
	}

	resp, err := idx.client.Embed(ctx, ollama.EmbedRequest{
		Model:    idx.model,
		Input:    []string{truncateInput(query, maxEmbedInputBytes)},
		Truncate: true,
	})
	if err != nil {
		return nil, errors.WithMessage(err, "embed query")
	}

	if len(resp.Embeddings) == 0 {
		return nil, errors.New("no embedding returned for query")
	}

	queryEmb := resp.Embeddings[0]

	type scored struct {
		entry IndexEntry
		score float64
	}
	results := make([]scored, 0, len(idx.entries))
	for _, e := range idx.entries {
		sim := cosineSimilarity(queryEmb, e.Embedding)
		results = append(results, scored{entry: e, score: sim})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if topK > len(results) {
		topK = len(results)
	}

	out := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = SearchResult{
			Chunk: results[i].entry.Chunk,
			Score: results[i].score,
		}
	}

	return out, nil
}

// Size returns the number of indexed entries.
func (idx *Index) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// Updated returns the last index update time.
func (idx *Index) Updated() time.Time {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.updated
}

// SearchResult holds a chunk and its similarity score.
type SearchResult struct {
	Chunk Chunk   `json:"chunk"`
	Score float64 `json:"score"`
}

func chunkHash(c Chunk) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s:%d:%d:%s", c.FilePath, c.StartLine, c.EndLine, c.Content)
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
