/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package snippets

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	minFilenameTagHits = 2
	minTokenLen        = 2 // minimum length for a token to be considered meaningful
)

type matchScore struct {
	index int
	hits  int
}

// MatchOpts holds all filter parameters for snippet matching.
type MatchOpts struct {
	Paths   []string
	Tags    []string
	TagMode string
	Query   string
	Section string
}

// MatchOutput holds match results and metadata.
type MatchOutput struct {
	Snippets         []Snippet
	FilenameFallback bool
}

// TagGroup maps tag names to the snippet names that carry them.
type TagGroup struct {
	Tag      string   `json:"tag"`
	Snippets []string `json:"snippets"`
}

// FilterByTags applies tag filtering with the given mode.
func FilterByTags(snippets []Snippet, tags []string, tagMode string) []Snippet {
	if len(tags) == 0 {
		return snippets
	}
	if strings.EqualFold(tagMode, "or") {
		return MatchTagsOr(snippets, tags)
	}
	return MatchTags(snippets, tags)
}

// MatchTags returns snippets containing all tags.
func MatchTags(snippets []Snippet, tags []string) []Snippet {
	if len(tags) == 0 {
		return nil
	}
	var result []Snippet
	for i := range snippets {
		if hasAllTags(snippets[i].tagSet, tags) {
			result = append(result, snippets[i])
		}
	}
	return result
}

// MatchTagsOr returns snippets containing any tag.
func MatchTagsOr(snippets []Snippet, tags []string) []Snippet {
	if len(tags) == 0 {
		return nil
	}
	var result []Snippet
	for i := range snippets {
		if hasAnyTag(snippets[i].tagSet, tags) {
			result = append(result, snippets[i])
		}
	}
	return result
}

// GroupByTags returns snippets grouped by tag.
func GroupByTags(snippets []Snippet) []TagGroup {
	tagMap := make(map[string][]string)
	for i := range snippets {
		for _, tag := range snippets[i].Tags {
			tagMap[tag] = append(tagMap[tag], snippets[i].Name)
		}
	}

	groups := make([]TagGroup, 0, len(tagMap))
	for tag, names := range tagMap {
		sort.Strings(names)
		groups = append(groups, TagGroup{Tag: tag, Snippets: names})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Tag < groups[j].Tag
	})
	return groups
}

// MatchWithOpts matches snippets by paths, tags, and query.
func MatchWithOpts(snippets []Snippet, opts *MatchOpts) MatchOutput {
	queryLower := strings.ToLower(opts.Query)
	queryTokens := tokenizeQuery(queryLower)
	hasQuery := opts.Query != "" && len(queryTokens) > 0
	if len(opts.Paths) == 0 && len(opts.Tags) == 0 && !hasQuery {
		return MatchOutput{}
	}

	tagMode := strings.ToLower(opts.TagMode)
	if tagMode == "" {
		tagMode = "and"
	}
	hasPaths := len(opts.Paths) > 0
	scored := make(map[int]int)
	var globMatched bool

	for i := range snippets {
		s := &snippets[i]

		var tagHit, queryHit bool
		if len(opts.Tags) > 0 {
			if tagMode == "or" {
				tagHit = hasAnyTag(s.tagSet, opts.Tags)
			} else {
				tagHit = hasAllTags(s.tagSet, opts.Tags)
			}
		}
		if hasQuery {
			queryHit = snippetMatchesQuery(s, queryLower, queryTokens)
		}
		if hasQuery && !queryHit {
			continue
		}

		if !hasPaths {
			if len(opts.Tags) > 0 && !tagHit {
				continue
			}
			scored[i] = countHits(false, tagHit, queryHit)
			continue
		}

		pathHit := matchesAnyPath(s.globs, opts.Paths)
		if pathHit {
			globMatched = true
		}
		if len(opts.Tags) > 0 && !pathHit && !tagHit {
			continue
		}
		if len(opts.Tags) == 0 && !pathHit {
			continue
		}

		hits := countHits(pathHit, tagHit, queryHit)
		if prev, ok := scored[i]; !ok || hits > prev {
			scored[i] = hits
		}
	}

	var usedFallback bool
	if hasPaths && !globMatched && len(opts.Tags) == 0 && !hasQuery {
		for _, path := range opts.Paths {
			tokens := filenameTokens(path)
			for i := range snippets {
				if matchFilenameTags(&snippets[i], tokens) {
					if _, ok := scored[i]; !ok {
						scored[i] = 0
					}
				}
			}
		}
		usedFallback = len(scored) > 0
	}

	entries := make([]matchScore, 0, len(scored))
	for idx, hits := range scored {
		entries = append(entries, matchScore{index: idx, hits: hits})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].hits != entries[j].hits {
			return entries[i].hits > entries[j].hits
		}
		return entries[i].index < entries[j].index
	})

	result := make([]Snippet, len(entries))
	for i := range entries {
		snippet := snippets[entries[i].index]
		if opts.Section != "" {
			snippet.Content = filterSections(&snippet, opts.Section)
		}
		result[i] = snippet
	}

	return MatchOutput{
		Snippets:         result,
		FilenameFallback: usedFallback,
	}
}

// SectionHeadings returns the headings present in a snippet.
func SectionHeadings(s *Snippet) []string {
	if len(s.sections) == 0 {
		return nil
	}
	headings := make([]string, 0, len(s.sections))
	for _, sec := range s.sections {
		headings = append(headings, sec.heading)
	}
	return headings
}

// CompactContent extracts mostly structured content from a snippet.
func CompactContent(s *Snippet) string {
	if len(s.sections) == 0 {
		return extractStructured("", s.Content)
	}

	var parts []string
	if preamble := preambleText(s.Content, s.sections[0]); preamble != "" {
		if blocks := extractStructured("", preamble); blocks != "" {
			parts = append(parts, blocks)
		}
	}
	for _, sec := range s.sections {
		if blocks := extractStructured(sec.heading, sec.body); blocks != "" {
			parts = append(parts, blocks)
		}
	}
	if len(parts) == 0 {
		return s.Content
	}
	return strings.Join(parts, "\n\n")
}

func matchesAnyPath(globs []compiledGlob, paths []string) bool {
	for _, path := range paths {
		if matchesAnyCompiledGlob(globs, path, strings.Split(path, "/")) {
			return true
		}
	}
	return false
}

func countHits(pathHit, tagHit, queryHit bool) int {
	hits := 0
	if pathHit {
		hits++
	}
	if tagHit {
		hits++
	}
	if queryHit {
		hits++
	}
	return hits
}

func filterSections(s *Snippet, heading string) string {
	h := strings.ToLower(heading)
	var parts []string
	for _, sec := range s.sections {
		if strings.Contains(strings.ToLower(sec.heading), h) {
			parts = append(parts, sec.body)
		}
	}
	if len(parts) == 0 {
		return s.Content
	}
	return strings.Join(parts, "\n\n")
}

func filenameTokens(path string) []string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.FieldsFunc(stem, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})

	seen := make(map[string]struct{})
	var result []string
	for _, part := range parts {
		for _, token := range splitCamel(part) {
			token = strings.ToLower(token)
			if len(token) < minTokenLen {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			result = append(result, token)
		}
	}
	return result
}

func splitCamel(s string) []string {
	runes := []rune(s)
	if len(runes) <= 1 {
		return []string{s}
	}

	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		curr := unicode.IsUpper(runes[i])
		prev := unicode.IsUpper(runes[i-1])
		switch {
		case curr && !prev:
			words = append(words, string(runes[start:i]))
			start = i
		case !curr && prev && i-start > 1:
			words = append(words, string(runes[start:i-1]))
			start = i - 1
		}
	}
	words = append(words, string(runes[start:]))
	return words
}

func matchFilenameTags(s *Snippet, tokens []string) bool {
	hits := 0
	for _, token := range tokens {
		if _, ok := s.tagSet[token]; ok {
			hits++
			if hits >= minFilenameTagHits {
				return true
			}
		}
	}
	return false
}

func snippetMatchesQuery(s *Snippet, queryLower string, queryTokens []string) bool {
	if strings.Contains(s.nameLower, queryLower) || strings.Contains(s.descLower, queryLower) {
		return true
	}
	return matchQueryTokens(s.searchTokens, queryTokens)
}

func matchQueryTokens(searchTokens, queryTokens []string) bool {
	if len(queryTokens) == 0 {
		return false
	}
	for _, queryToken := range queryTokens {
		found := false
		for _, searchToken := range searchTokens {
			if strings.HasPrefix(searchToken, queryToken) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func tokenizeForSearch(nameLower, descLower string, tags []string) []string {
	combined := nameLower + " " + descLower + " " + strings.Join(tags, " ")
	parts := strings.FieldsFunc(combined, isTokenSeparator)
	seen := make(map[string]struct{})
	var tokens []string
	for _, part := range parts {
		for _, word := range splitCamel(part) {
			word = strings.ToLower(word)
			if len(word) < minTokenLen {
				continue
			}
			if _, ok := seen[word]; ok {
				continue
			}
			seen[word] = struct{}{}
			tokens = append(tokens, word)
		}
	}
	return tokens
}

func tokenizeQuery(queryLower string) []string {
	parts := strings.FieldsFunc(queryLower, isTokenSeparator)
	var tokens []string
	for _, part := range parts {
		if len(part) >= minTokenLen {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func isTokenSeparator(r rune) bool {
	return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.'
}

func matchesAnyCompiledGlob(globs []compiledGlob, path string, pathParts []string) bool {
	for i := range globs {
		if matchCompiledGlob(&globs[i], path, pathParts) {
			return true
		}
	}
	return false
}

func matchCompiledGlob(g *compiledGlob, path string, pathParts []string) bool {
	if g.simple {
		matched, err := filepath.Match(g.raw, path)
		if err != nil {
			return false
		}
		return matched
	}
	if len(g.segParts) == 0 {
		return true
	}
	return matchSegments(pathParts, g.segParts, 0, 0)
}

func matchSegments(pathParts []string, segParts [][]string, pi, si int) bool {
	if si >= len(segParts) {
		return true
	}
	sp := segParts[si]
	for start := pi; start+len(sp) <= len(pathParts); start++ {
		if matchSequence(pathParts, sp, start) && matchSegments(pathParts, segParts, start+len(sp), si+1) {
			return true
		}
	}
	return false
}

func matchSequence(pathParts, segParts []string, offset int) bool {
	for i, part := range segParts {
		matched, err := filepath.Match(part, pathParts[offset+i])
		if err != nil || !matched {
			return false
		}
	}
	return true
}

func hasAllTags(tagSet map[string]struct{}, required []string) bool {
	for _, tag := range required {
		if _, ok := tagSet[tag]; !ok {
			return false
		}
	}
	return true
}

func hasAnyTag(tagSet map[string]struct{}, candidates []string) bool {
	for _, tag := range candidates {
		if _, ok := tagSet[tag]; ok {
			return true
		}
	}
	return false
}

func preambleText(content string, firstSection section) string {
	prefix := "## " + firstSection.heading
	idx := strings.Index(content, prefix)
	if idx <= 0 {
		prefix = "### " + firstSection.heading
		idx = strings.Index(content, prefix)
	}
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(content[:idx])
}

func extractStructured(heading, text string) string {
	lines := strings.Split(text, "\n")
	var parts []string
	var block []string
	inFence := false
	headingWritten := false

	writeHeading := func() {
		if heading != "" && !headingWritten {
			parts = append(parts, "// "+heading)
			headingWritten = true
		}
	}
	flushTable := func() {
		if len(block) == 0 {
			return
		}
		writeHeading()
		parts = append(parts, strings.Join(block, "\n"))
		block = nil
	}

	for _, line := range lines {
		isFence := strings.HasPrefix(line, "```")
		isTableRow := !inFence && strings.HasPrefix(line, "|")
		switch {
		case isFence && !inFence:
			flushTable()
			inFence = true
			block = []string{line}
		case isFence && inFence:
			block = append(block, line)
			inFence = false
			writeHeading()
			parts = append(parts, strings.Join(block, "\n"))
			block = nil
		case inFence:
			block = append(block, line)
		case isTableRow:
			block = append(block, line)
		default:
			flushTable()
		}
	}
	flushTable()
	return strings.Join(parts, "\n\n")
}
