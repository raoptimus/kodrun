/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rules

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// Priority determines the order rules are included in the prompt.
type Priority int

const (
	PriorityHigh   Priority = 0
	PriorityNormal Priority = 1
	PriorityLow    Priority = 2
)

// Scope determines when a rule is active.
type Scope string

const (
	ScopeAll    Scope = "all"
	ScopeCoding Scope = "coding"
	ScopeReview Scope = "review"
	ScopeFix    Scope = "fix"

	frontMatterDescription = "description"
)

// Rule represents a loaded rule file.
type Rule struct {
	Path        string
	Content     string
	Priority    Priority
	Scope       Scope
	ModTime     time.Time
	RefPaths    []string // which docs this rule references
	description string   // optional description from front matter
}

// Command represents a user-defined command.
type Command struct {
	Name        string
	Description string
	Template    string
	Args        []CommandArg
}

// CommandArg describes a command argument.
type CommandArg struct {
	Name     string
	Required bool
}

// Fixed directories for rules, commands, and docs.
var (
	RulesDirs   = ".kodrun/rules"
	CommandsDir = ".kodrun/commands"
	DocsDir     = ".kodrun/docs"
)

// Loader loads and caches rules from directories.
type Loader struct {
	dirs           []string
	workDir        string
	rules          []*Rule
	commands       map[string]*Command
	refDocs        map[string]string // path → resolved content (deduplicated)
	refOrder       []string          // insertion order for deterministic output
	maxRefSize     int               // max size of a single doc (0 = no limit)
	unresolvedRefs []UnresolvedRef   // @-references that failed to resolve during Load
}

// UnresolvedRef describes a single @-reference in a rule file that could not
// be resolved to an existing file on disk.
type UnresolvedRef struct {
	RulePath string // path to the rule file containing the broken reference
	RefPath  string // the @-target as written in the rule (without the leading @)
}

const minKeyValueParts = 2 // expected parts when splitting "key: value"

// refPattern matches @path references in rule content.
// Matches: @.kodrun/docs/file.md, @.kodrun/docs/example.go, etc.
var refPattern = regexp.MustCompile(`@([^\s,]+\.\w+)`)

// NewLoader creates a rules loader.
// workDir is the project root used to resolve @file references.
// maxRefSize limits the size of each referenced doc (0 = no limit).
func NewLoader(workDir string, maxRefSize int) *Loader {
	return &Loader{
		dirs:       []string{RulesDirs, CommandsDir},
		workDir:    workDir,
		commands:   make(map[string]*Command),
		refDocs:    make(map[string]string),
		maxRefSize: maxRefSize,
	}
}

// Load reads all rule files (.md) from configured directories.
func (l *Loader) Load(ctx context.Context) error {
	l.rules = nil
	l.commands = make(map[string]*Command)
	l.refDocs = make(map[string]string)
	l.refOrder = nil
	l.unresolvedRefs = nil

	for _, dir := range l.dirs {
		absDir := dir
		if !filepath.IsAbs(dir) {
			absDir = filepath.Join(l.workDir, dir)
		}

		if _, err := os.Stat(absDir); os.IsNotExist(err) {
			continue
		}

		err := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return filepath.SkipDir
			}
			if d.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}

			if err := l.loadRule(ctx, dir, path, d); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	sort.Slice(l.rules, func(i, j int) bool {
		return l.rules[i].Priority < l.rules[j].Priority
	})

	return nil
}

// loadRule reads a single rule file and appends it to l.rules.
func (l *Loader) loadRule(ctx context.Context, dir, path string, d fs.DirEntry) error {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return fmt.Errorf("read rule %s: %w", path, readErr)
	}
	if len(data) == 0 {
		return nil
	}

	info, infoErr := d.Info()
	if infoErr != nil {
		return fmt.Errorf("stat rule %s: %w", path, infoErr)
	}
	if info == nil {
		return nil
	}

	content := string(data)
	priority, scope, body, desc := parseFrontMatter(content)

	// Collect @file references: deduplicate into refDocs, replace with labels
	body, refPaths := l.collectReferences(ctx, path, body)

	// Check if this is a command definition
	if strings.Contains(dir, "commands") {
		cmd := parseCommand(path, content)
		if cmd != nil {
			l.commands[cmd.Name] = cmd
		}
	}

	l.rules = append(l.rules, &Rule{
		Path:        path,
		Content:     body,
		Priority:    priority,
		Scope:       scope,
		ModTime:     info.ModTime(),
		RefPaths:    refPaths,
		description: desc,
	})

	return nil
}

// collectReferences scans @path refs in text, resolves and deduplicates them into
// refDocs, and replaces inline references with [see: filename] labels.
// Returns the modified text and list of reference paths.
// Any @-reference that fails to resolve is recorded in l.unresolvedRefs so the
// caller can surface a warning instead of silently dropping the doc from RAG.
func (l *Loader) collectReferences(ctx context.Context, rulePath, content string) (resolvedContent string, refPaths []string) {
	seen := make(map[string]bool)

	result := refPattern.ReplaceAllStringFunc(content, func(match string) string {
		refPath := match[1:] // strip leading @

		resolvedPath, data, err := l.resolveRef(ctx, refPath)
		if err != nil {
			l.unresolvedRefs = append(l.unresolvedRefs, UnresolvedRef{
				RulePath: rulePath,
				RefPath:  refPath,
			})
			return match // leave as-is if not found
		}

		base := filepath.Base(resolvedPath)

		// Track this ref for the rule
		if !seen[resolvedPath] {
			seen[resolvedPath] = true
			refPaths = append(refPaths, resolvedPath)
		}

		// Store in global deduplicated map
		if _, exists := l.refDocs[resolvedPath]; !exists {
			docContent := l.formatDoc(ctx, resolvedPath, string(data))
			l.refDocs[resolvedPath] = docContent
			l.refOrder = append(l.refOrder, resolvedPath)
		}

		return fmt.Sprintf("[see: %s]", base)
	})

	return result, refPaths
}

// resolveRef reads a referenced file. The @-syntax always resolves relative
// to the project root (workDir); the path is used as-is without any rewriting.
func (l *Loader) resolveRef(ctx context.Context, refPath string) (resolved string, data []byte, err error) {
	data, err = l.readRef(ctx, refPath)
	if err != nil {
		return "", nil, err
	}
	return refPath, data, nil
}

// formatDoc formats and optionally truncates a doc for inclusion in ReferenceDocs.
func (l *Loader) formatDoc(_ context.Context, path, content string) string {
	if l.maxRefSize > 0 && len(content) > l.maxRefSize {
		content = content[:l.maxRefSize] + fmt.Sprintf(
			"\n[truncated — use read_file(%q) for full content]", path,
		)
	}

	base := filepath.Base(path)
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return fmt.Sprintf("```go\n// %s\n%s\n```", base, content)
	default:
		return fmt.Sprintf("--- %s ---\n%s", base, content)
	}
}

// readRef reads a referenced file. The @-syntax always resolves relative to
// the project root (workDir): a leading "/" is stripped so that @/.kodrun/...
// behaves the same as @.kodrun/..., and the resolved path must stay inside
// workDir to prevent escaping via "..".
func (l *Loader) readRef(_ context.Context, refPath string) ([]byte, error) {
	cleaned := strings.TrimPrefix(refPath, "/")
	if filepath.IsAbs(cleaned) {
		return nil, errors.Errorf("@-reference must be relative to project root, got absolute path %q", refPath)
	}
	absPath := filepath.Join(l.workDir, cleaned)
	rel, err := filepath.Rel(l.workDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, errors.Errorf("@-reference %q escapes project root", refPath)
	}
	return os.ReadFile(absPath)
}

// Rules returns loaded rules filtered by scope.
// AllRules returns all loaded rules regardless of scope.
func (l *Loader) AllRules() []*Rule {
	return l.rules
}

// UnresolvedRefs returns all @-references collected during Load that did not
// resolve to an existing file. The caller can surface these as warnings so a
// typo in a rule (e.g. @.koderun/... instead of @.kodrun/...) does not silently
// drop docs from RAG indexing.
func (l *Loader) UnresolvedRefs() []UnresolvedRef {
	return l.unresolvedRefs
}

func (l *Loader) Rules(_ context.Context, scope Scope) []*Rule {
	var filtered []*Rule
	for _, r := range l.rules {
		if r.Scope == ScopeAll || r.Scope == scope {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// AllRulesContent returns concatenated content of all rules for a scope.
func (l *Loader) AllRulesContent(ctx context.Context, scope Scope) string {
	rules := l.Rules(ctx, scope)
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// ReferenceDocs returns deduplicated documentation referenced by rules of
// the given scope. Each doc is included at most once, in insertion order.
func (l *Loader) ReferenceDocs(ctx context.Context, scope Scope) string {
	rules := l.Rules(ctx, scope)

	// Collect unique ref paths from matching rules
	needed := make(map[string]bool)
	for _, r := range rules {
		for _, p := range r.RefPaths {
			needed[p] = true
		}
	}

	var b strings.Builder
	for _, path := range l.refOrder {
		if !needed[path] {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(l.refDocs[path])
	}
	return b.String()
}

// ReferenceDocPaths returns all unique reference doc paths and their content.
func (l *Loader) ReferenceDocPaths() map[string]string {
	result := make(map[string]string, len(l.refDocs))
	for path, content := range l.refDocs {
		result[path] = content
	}
	return result
}

// RuleSummary is a compact representation of a rule for the catalog.
type RuleSummary struct {
	Name        string   // file name without extension (e.g. "service")
	Description string   // auto-extracted or from front matter
	RefFiles    []string // base names of referenced docs
}

// RuleCatalog returns a compact list of rule summaries for the given scope.
func (l *Loader) RuleCatalog(ctx context.Context, scope Scope) []RuleSummary {
	rules := l.Rules(ctx, scope)
	summaries := make([]RuleSummary, 0, len(rules))
	for _, r := range rules {
		name := strings.TrimSuffix(filepath.Base(r.Path), ".md")
		desc := l.extractDescription(ctx, r)

		var refFiles []string
		for _, p := range r.RefPaths {
			refFiles = append(refFiles, filepath.Base(p))
		}

		summaries = append(summaries, RuleSummary{
			Name:        name,
			Description: desc,
			RefFiles:    refFiles,
		})
	}
	return summaries
}

// RuleCatalogString returns a formatted catalog string for the system prompt.
// When useTool is false, returns empty string — rules are not exposed to the model.
func (l *Loader) RuleCatalogString(ctx context.Context, scope Scope, useTool bool) string {
	if !useTool {
		return ""
	}

	summaries := l.RuleCatalog(ctx, scope)
	if len(summaries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Available project rules (call get_rule(name) before writing code):\n")
	for _, s := range summaries {
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Description)
		if len(s.RefFiles) > 0 {
			b.WriteString(" [see: ")
			b.WriteString(strings.Join(s.RefFiles, ", "))
			b.WriteString("]")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// GetRuleContent returns the full text of a rule and all its referenced docs.
func (l *Loader) GetRuleContent(ctx context.Context, name string, scope Scope) (string, error) {
	rules := l.Rules(ctx, scope)
	for _, r := range rules {
		ruleName := strings.TrimSuffix(filepath.Base(r.Path), ".md")
		if ruleName != name {
			continue
		}

		var b strings.Builder
		b.WriteString("# Rule: ")
		b.WriteString(name)
		b.WriteString("\n\n")
		b.WriteString(r.Content)
		b.WriteString("\n")

		// Append all referenced docs
		for _, refPath := range r.RefPaths {
			if doc, ok := l.refDocs[refPath]; ok {
				b.WriteString("\n\n")
				b.WriteString(doc)
			}
		}

		return b.String(), nil
	}

	// Try without scope filter as fallback
	for _, r := range l.rules {
		ruleName := strings.TrimSuffix(filepath.Base(r.Path), ".md")
		if ruleName == name {
			var b strings.Builder
			b.WriteString("# Rule: ")
			b.WriteString(name)
			b.WriteString("\n\n")
			b.WriteString(r.Content)
			b.WriteString("\n")

			for _, refPath := range r.RefPaths {
				if doc, ok := l.refDocs[refPath]; ok {
					b.WriteString("\n\n")
					b.WriteString(doc)
				}
			}

			return b.String(), nil
		}
	}

	return "", errors.Errorf("rule %q not found", name)
}

// extractDescription derives a description for a rule.
// Priority: 1) front matter "description" field, 2) first referenced doc heading + first sentence, 3) rule file name.
func (l *Loader) extractDescription(_ context.Context, r *Rule) string {
	// Check front matter description
	if r.description != "" {
		return r.description
	}

	// Try to extract from first referenced doc
	if len(r.RefPaths) > 0 {
		if doc, ok := l.refDocs[r.RefPaths[0]]; ok {
			if desc := extractDocDescription(doc); desc != "" {
				return desc
			}
		}
	}

	// Fallback to file name
	return strings.TrimSuffix(filepath.Base(r.Path), ".md")
}

// extractDocDescription extracts heading + first sentence from a formatted doc.
func extractDocDescription(doc string) string {
	// Doc format is either "--- filename ---\ncontent" or "```go\n// filename\ncontent\n```"
	lines := strings.Split(doc, "\n")

	var heading, firstSentence string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			heading = strings.TrimPrefix(line, "# ")
			continue
		}
		if heading != "" && firstSentence == "" {
			// Take first sentence (up to first period or end of line)
			firstSentence = line
			if idx := strings.Index(firstSentence, "."); idx >= 0 {
				firstSentence = firstSentence[:idx]
			}
			break
		}
	}

	if heading != "" && firstSentence != "" {
		return heading + " — " + firstSentence
	}
	if heading != "" {
		return heading
	}
	return ""
}

// GetCommand returns a command by name.
func (l *Loader) GetCommand(name string) (*Command, bool) {
	cmd, ok := l.commands[name]
	return cmd, ok
}

// Commands returns all loaded commands.
func (l *Loader) Commands() map[string]*Command {
	return l.commands
}

func parseFrontMatter(content string) (outPriority Priority, outScope Scope, outBody, outDesc string) {
	outPriority = PriorityNormal
	outScope = ScopeAll

	if !strings.HasPrefix(content, "---") {
		return outPriority, outScope, content, outDesc
	}

	end := strings.Index(content[3:], "---")
	if end == -1 {
		return outPriority, outScope, content, outDesc
	}

	frontMatter := content[3 : end+3]
	bodyText := content[end+6:]

	for _, line := range strings.Split(frontMatter, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", minKeyValueParts)
		if len(parts) != minKeyValueParts {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "priority":
			switch val {
			case "high":
				outPriority = PriorityHigh
			case "low":
				outPriority = PriorityLow
			}
		case "scope":
			outScope = Scope(val)
		case frontMatterDescription:
			outDesc = strings.Trim(val, "\"")
		}
	}

	return outPriority, outScope, strings.TrimSpace(bodyText), outDesc
}

func parseCommand(path, content string) *Command {
	if !strings.HasPrefix(content, "---") {
		return nil
	}

	end := strings.Index(content[3:], "---")
	if end == -1 {
		return nil
	}

	frontMatter := content[3 : end+3]
	body := strings.TrimSpace(content[end+6:])

	cmd := &Command{
		Template: body,
	}

	for _, line := range strings.Split(frontMatter, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", minKeyValueParts)
		if len(parts) != minKeyValueParts {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"")

		switch key {
		case "command":
			cmd.Name = strings.TrimPrefix(val, "/")
		case frontMatterDescription:
			cmd.Description = val
		}
	}

	if cmd.Name == "" {
		// Use filename as command name
		cmd.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}

	return cmd
}
