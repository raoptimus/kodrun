package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/snippets"
)

const (
	snippetActionMatch = "match"
	snippetModeAND     = "AND"
)

type matchResult struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Tags         []string          `json:"tags"`
	Related      []string          `json:"related,omitempty"`
	Placeholders map[string]string `json:"placeholders,omitempty"`
	Sections     []string          `json:"sections,omitempty"`
	Content      string            `json:"content,omitempty"`
}

type listResult struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Paths       []string `json:"paths"`
	Related     []string `json:"related,omitempty"`
}

type matchResponse struct {
	Matches  []matchResult `json:"matches"`
	Hint     string        `json:"hint,omitempty"`
	Fallback string        `json:"fallback,omitempty"`
	Tip      string        `json:"tip,omitempty"`
}

// SnippetTool exposes project snippets to the agent.
type SnippetTool struct {
	loader *snippets.Loader
}

// NewSnippetTool creates a new snippets tool.
func NewSnippetTool(loader *snippets.Loader) *SnippetTool {
	return &SnippetTool{loader: loader}
}

func (t *SnippetTool) Name() string { return "snippets" }
func (t *SnippetTool) Description() string {
	return "MUST call before writing, modifying, or reviewing code — returns code template snippets. " +
		"Pass file paths to match against snippet globs. Actions: match (default), list, tags."
}

func (t *SnippetTool) Schema() ollama.JSONSchema {
	stringArray := &ollama.JSONSchema{
		Type:  "array",
		Items: &ollama.JSONSchema{Type: "string"},
	}
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"action":   {Type: "string", Description: "match (default), list, or tags", Enum: []string{snippetActionMatch, "list", "tags"}},
			"paths":    {Type: "array", Description: "File paths to match against snippet globs", Items: stringArray.Items},
			"tags":     {Type: "array", Description: "Filter snippets by tags", Items: stringArray.Items},
			"tag_mode": {Type: "string", Description: "How to combine tags: and (default) or or", Enum: []string{"and", "or"}},
			"query":    {Type: "string", Description: "Text search in snippet names, descriptions, and tags"},
			"section":  {Type: "string", Description: "Only include sections whose heading contains this substring"},
			"full":     {Type: "boolean", Description: "Return full snippet content including prose"},
		},
	}
}

func (t *SnippetTool) Execute(_ context.Context, params map[string]any) (*ToolResult, error) {
	if t.loader == nil {
		return nil, &ToolError{Msg: "snippets loader is not configured"}
	}

	action := stringParam(params, "action")
	if action == "" {
		action = snippetActionMatch
	}

	all := t.loader.Snippets()
	switch action {
	case snippetActionMatch:
		return t.match(all, params)
	case "list":
		return t.list(all, params)
	case "tags":
		return t.tags(all)
	default:
		return nil, &ToolError{Msg: "unknown action: " + action}
	}
}

func (t *SnippetTool) match(all []snippets.Snippet, params map[string]any) (*ToolResult, error) {
	query := stringParam(params, "query")
	section := stringParam(params, "section")
	tagMode := stringParam(params, "tag_mode")
	full := boolParam(params, "full")
	tags := extractStringSlice(params, "tags")
	paths := extractStringSlice(params, "paths")

	if len(paths) == 0 && len(tags) == 0 && query == "" {
		return nil, &ToolError{Msg: "at least one of paths, tags, or query is required"}
	}

	out := snippets.MatchWithOpts(all, &snippets.MatchOpts{
		Paths:   paths,
		Tags:    tags,
		TagMode: tagMode,
		Query:   query,
		Section: section,
	})

	results := make([]matchResult, len(out.Snippets))
	for i := range out.Snippets {
		s := &out.Snippets[i]
		results[i] = matchResult{
			Name:         s.Name,
			Description:  s.Description,
			Tags:         s.Tags,
			Related:      s.Related,
			Placeholders: s.Placeholders,
			Sections:     snippets.SectionHeadings(s),
		}
		if full {
			results[i].Content = s.Content
		} else {
			results[i].Content = snippets.CompactContent(s)
		}
	}

	resp := matchResponse{Matches: results}
	switch {
	case len(results) == 0:
		resp.Hint = buildNoMatchHint(all, paths, tags, tagMode)
	case out.FilenameFallback:
		resp.Fallback = "No path globs matched. Results inferred from filename tokens; verify relevance."
	default:
		resp.Tip = buildMatchTip(results)
	}

	return marshalToolResult(resp)
}

func (t *SnippetTool) list(all []snippets.Snippet, params map[string]any) (*ToolResult, error) {
	tags := extractStringSlice(params, "tags")
	tagMode := stringParam(params, "tag_mode")
	filtered := snippets.FilterByTags(all, tags, tagMode)

	results := make([]listResult, len(filtered))
	for i := range filtered {
		results[i] = listResult{
			Name:        filtered[i].Name,
			Description: filtered[i].Description,
			Tags:        filtered[i].Tags,
			Paths:       filtered[i].Paths,
			Related:     filtered[i].Related,
		}
	}
	return marshalToolResult(results)
}

func (t *SnippetTool) tags(all []snippets.Snippet) (*ToolResult, error) {
	return marshalToolResult(snippets.GroupByTags(all))
}

func marshalToolResult(v any) (*ToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &ToolResult{Output: string(data)}, nil
}

func extractStringSlice(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func buildNoMatchHint(all []snippets.Snippet, paths, tags []string, tagMode string) string {
	var parts []string

	if len(paths) > 0 {
		parts = append(parts, fmt.Sprintf("No snippets matched paths %v.", paths))

		var tagOnly int
		seen := make(map[string]struct{})
		var tagOnlyTags []string
		for i := range all {
			if len(all[i].Paths) != 0 {
				continue
			}
			tagOnly++
			for _, tag := range all[i].Tags {
				if _, ok := seen[tag]; ok {
					continue
				}
				seen[tag] = struct{}{}
				tagOnlyTags = append(tagOnlyTags, tag)
			}
		}
		if tagOnly > 0 {
			parts = append(parts, fmt.Sprintf("%d snippets have no path globs (tag-only). Try tags from: %s", tagOnly, strings.Join(tagOnlyTags, ", ")))
		}
	}

	if len(tags) > 0 {
		modeLabel := snippetModeAND
		if strings.EqualFold(tagMode, "or") {
			modeLabel = "OR"
		}
		parts = append(parts, fmt.Sprintf("No snippets matched tags %v (%s mode). Use snippets(action=\"tags\") to inspect available tags.", tags, modeLabel))
		if modeLabel == snippetModeAND && len(tags) > 1 {
			parts = append(parts, "Tip: try tag_mode=\"or\".")
		}
	}

	if len(parts) == 0 {
		return "No matching snippets found. Use snippets(action=\"list\") or snippets(action=\"tags\") to explore."
	}
	return strings.Join(parts, " ")
}

func buildMatchTip(results []matchResult) string {
	var hasPlaceholders bool
	relatedSet := make(map[string]struct{})
	for i := range results {
		if len(results[i].Placeholders) > 0 {
			hasPlaceholders = true
		}
		for _, rel := range results[i].Related {
			relatedSet[rel] = struct{}{}
		}
	}

	var parts []string
	if hasPlaceholders {
		parts = append(parts, "Replace <Placeholder> tokens with project-specific names using the placeholders field.")
	}
	if len(relatedSet) > 0 {
		names := make([]string, 0, len(relatedSet))
		for name := range relatedSet {
			names = append(names, name)
		}
		parts = append(parts, "To get related conventions, call snippets(query=\"<name>\") for: "+strings.Join(names, ", "))
	}
	return strings.Join(parts, " ")
}
