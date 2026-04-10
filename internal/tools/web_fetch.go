package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/rag"
)

const (
	webFetchTimeout      = 30 * time.Second
	webFetchMaxBodySize  = 10 << 20 // 10 MB
	webFetchChunkSize    = 40       // lines per chunk
	webFetchChunkOverlap = 5
	webFetchMaxOutput    = 4000 // bytes, for truncated fallback
	webFetchTopMatches   = 5    // text-match fallback results
)

// WebIndexer is the interface for indexing and searching web content via RAG.
type WebIndexer interface {
	Build(ctx context.Context, chunks []rag.Chunk) (int, error)
	Search(ctx context.Context, query string, topK int) ([]rag.SearchResult, error)
}

// WebFetchTool downloads a web page, converts HTML to markdown, and returns
// relevant content. When a WebIndexer is provided (RAG enabled), the content
// is embedded into a session-scoped index and searched semantically. Without
// RAG, a simple text-match fallback is used.
type WebFetchTool struct {
	indexer WebIndexer // nil when RAG is disabled
	topK    int
}

// NewWebFetchTool creates a new web_fetch tool. The indexer is optional:
// pass nil when RAG is disabled.
func NewWebFetchTool(indexer WebIndexer, topK int) *WebFetchTool {
	if topK <= 0 {
		topK = webFetchTopMatches
	}
	return &WebFetchTool{indexer: indexer, topK: topK}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return "Fetch a web page, convert HTML to markdown, and return relevant content. " +
		"With RAG enabled the content is indexed for semantic search; without RAG a keyword search is used."
}

func (t *WebFetchTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"url":   {Type: "string", Description: "URL of the web page to fetch"},
			"query": {Type: "string", Description: "Optional search query to find relevant sections in the page"},
		},
		Required: []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	rawURL := stringParam(params, "url")
	if rawURL == "" {
		return nil, &ToolError{Msg: "url is required"}
	}
	query := stringParam(params, "query")

	markdown, err := t.fetchAndConvert(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	if len(strings.TrimSpace(markdown)) == 0 {
		return &ToolResult{Output: "Page returned empty content."}, nil
	}

	source := "web://" + rawURL
	chunks := rag.ChunkText(source, markdown, webFetchChunkSize, webFetchChunkOverlap)

	if t.indexer != nil {
		return t.executeWithRAG(ctx, chunks, query)
	}
	return t.executeWithTextMatch(chunks, query), nil
}

func (t *WebFetchTool) fetchAndConvert(ctx context.Context, rawURL string) (string, error) {
	client := &http.Client{Timeout: webFetchTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "kodrun/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, webFetchMaxBodySize)
	markdownBytes, err := htmltomd.ConvertReader(limited)
	if err != nil {
		return "", fmt.Errorf("convert html to markdown: %w", err)
	}

	return string(markdownBytes), nil
}

func (t *WebFetchTool) executeWithRAG(ctx context.Context, chunks []rag.Chunk, query string) (*ToolResult, error) {
	if _, err := t.indexer.Build(ctx, chunks); err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}

	if query == "" {
		// No query — return first chunks as summary.
		return t.summarizeChunks(chunks), nil
	}

	results, err := t.indexer.Search(ctx, query, t.topK)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		return &ToolResult{Output: "No relevant results found for the query."}, nil
	}

	return t.formatSearchResults(results), nil
}

func (t *WebFetchTool) executeWithTextMatch(chunks []rag.Chunk, query string) *ToolResult {
	if query == "" {
		return t.summarizeChunks(chunks)
	}

	words := strings.Fields(strings.ToLower(query))
	var matched []rag.Chunk
	for _, c := range chunks {
		lower := strings.ToLower(c.Content)
		for _, w := range words {
			if strings.Contains(lower, w) {
				matched = append(matched, c)
				break
			}
		}
	}

	if len(matched) == 0 {
		return &ToolResult{Output: "No matching content found for the query."}
	}

	if len(matched) > webFetchTopMatches {
		matched = matched[:webFetchTopMatches]
	}

	var b strings.Builder
	for i, c := range matched {
		fmt.Fprintf(&b, "--- Match %d (lines %d-%d) ---\n%s\n\n", i+1, c.StartLine, c.EndLine, c.Content)
	}

	return &ToolResult{
		Output: b.String(),
		Meta:   map[string]any{"matches": len(matched)},
	}
}

func (t *WebFetchTool) summarizeChunks(chunks []rag.Chunk) *ToolResult {
	var b strings.Builder
	for _, c := range chunks {
		if b.Len()+len(c.Content) > webFetchMaxOutput {
			remaining := webFetchMaxOutput - b.Len()
			if remaining > 0 {
				b.WriteString(c.Content[:remaining])
			}
			b.WriteString("\n\n[truncated]")
			break
		}
		b.WriteString(c.Content)
		b.WriteByte('\n')
	}

	return &ToolResult{
		Output: b.String(),
		Meta:   map[string]any{"truncated": b.Len() >= webFetchMaxOutput},
	}
}

func (t *WebFetchTool) formatSearchResults(results []rag.SearchResult) *ToolResult {
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "--- Result %d (%.2f, lines %d-%d) ---\n%s\n\n",
			i+1, r.Score, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
	}

	return &ToolResult{
		Output: b.String(),
		Meta:   map[string]any{"results": len(results)},
	}
}
