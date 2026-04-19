/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ParseToolCalls attempts to extract tool calls from text content.
// Some models return tool calls as JSON text instead of structured tool_calls.
func ParseToolCalls(content string) ([]ToolCall, bool) {
	content = strings.TrimSpace(content)

	// Try parsing as a single tool call object
	var single struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(content), &single); err == nil && single.Name != "" {
		return []ToolCall{{
			ID: "fallback-0",
			Function: ToolCallFunc{
				Name:      single.Name,
				Arguments: single.Arguments,
			},
		}}, true
	}

	// Try parsing as an array of tool calls
	var arr []struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(content), &arr); err == nil && len(arr) > 0 {
		calls := make([]ToolCall, 0, len(arr))
		for _, item := range arr {
			if item.Name == "" {
				continue
			}
			calls = append(calls, ToolCall{
				ID: fmt.Sprintf("fallback-%d", len(calls)),
				Function: ToolCallFunc{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
		if len(calls) > 0 {
			return calls, true
		}
	}

	// Try parsing XML-like format: <function=name><parameter=key>value</parameter></function>
	if calls := parseXMLToolCalls(content); len(calls) > 0 {
		return calls, true
	}

	return nil, false
}

var (
	reFuncBlock = regexp.MustCompile(`(?s)<function=(\w+)>(.*?)</function>`)
	reParam     = regexp.MustCompile(`(?s)<parameter=(\w+)>\s*(.*?)\s*</parameter>`)
)

// CleanToolCallText removes tool call markup from text, keeping surrounding prose.
func CleanToolCallText(content string) string {
	// Remove XML-style tool calls
	cleaned := reFuncBlock.ReplaceAllString(content, "")
	// Remove </tool_call> tags
	cleaned = strings.ReplaceAll(cleaned, "</tool_call>", "")
	cleaned = strings.ReplaceAll(cleaned, "<tool_call>", "")
	// Remove JSON tool calls (heuristic: entire content was a tool call)
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
}

// parseXMLToolCalls handles the XML-like tool call format some models use.
func parseXMLToolCalls(content string) []ToolCall {
	funcMatches := reFuncBlock.FindAllStringSubmatch(content, -1)
	if len(funcMatches) == 0 {
		return nil
	}

	calls := make([]ToolCall, 0, len(funcMatches))
	for _, fm := range funcMatches {
		name := fm[1]
		body := fm[2]

		args := make(map[string]any)
		paramMatches := reParam.FindAllStringSubmatch(body, -1)
		for _, pm := range paramMatches {
			args[pm[1]] = strings.TrimSpace(pm[2])
		}

		calls = append(calls, ToolCall{
			ID: fmt.Sprintf("fallback-%d", len(calls)),
			Function: ToolCallFunc{
				Name:      name,
				Arguments: args,
			},
		})
	}

	return calls
}
