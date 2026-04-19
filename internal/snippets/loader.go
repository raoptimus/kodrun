/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package snippets

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"
	yaml "go.yaml.in/yaml/v3"
)

// Snippet represents a parsed snippet file.
type Snippet struct {
	Name         string
	Description  string            `yaml:"description"`
	Tags         []string          `yaml:"tags"`
	Paths        []string          `yaml:"paths"`
	Requires     []string          `yaml:"requires"`
	Related      []string          `yaml:"related"`
	Placeholders map[string]string `yaml:"placeholders"`
	Lang         string            `yaml:"lang"`
	Content      string
	SourcePath   string // absolute path to the source .md file

	nameLower    string
	descLower    string
	searchTokens []string
	tagSet       map[string]struct{}
	globs        []compiledGlob
	sections     []section
}

type compiledGlob struct {
	raw      string
	segParts [][]string
	simple   bool
}

type section struct {
	heading string
	body    string
}

// SnippetsDir is the fixed directory for project snippets.
var SnippetsDir = ".kodrun/snippets"

// Loader loads snippets from configured directories.
type Loader struct {
	dirs     []string
	workDir  string
	snippets []Snippet
}

// NewLoader creates a snippets loader.
func NewLoader(workDir string) *Loader {
	return &Loader{dirs: []string{SnippetsDir}, workDir: workDir}
}

// Load reads snippet files from configured directories.
func (l *Loader) Load(_ context.Context) error {
	l.snippets = nil

	for _, dir := range l.dirs {
		absDir := dir
		if !filepath.IsAbs(absDir) {
			absDir = filepath.Join(l.workDir, dir)
		}

		info, err := os.Stat(absDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.WithMessagef(err, "stat snippets dir %q", absDir)
		}
		if !info.IsDir() {
			continue
		}

		if err := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return errors.WithMessagef(err, "read %s", path)
			}

			snippet, err := parseSnippet(path, data)
			if err != nil {
				return errors.WithMessagef(err, "parse %s", path)
			}

			l.snippets = append(l.snippets, snippet)
			return nil
		}); err != nil {
			return err
		}
	}

	sort.Slice(l.snippets, func(i, j int) bool {
		return l.snippets[i].Name < l.snippets[j].Name
	})

	return nil
}

// Snippets returns a copy of loaded snippets.
func (l *Loader) Snippets() []Snippet {
	out := make([]Snippet, len(l.snippets))
	copy(out, l.snippets)
	return out
}

// SourcePathByName returns the source file path for a snippet by name.
func (l *Loader) SourcePathByName(name string) string {
	for i := range l.snippets {
		if l.snippets[i].Name == name {
			return l.snippets[i].SourcePath
		}
	}
	return ""
}

func parseSnippet(filename string, data []byte) (Snippet, error) {
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return Snippet{}, errors.New("missing YAML frontmatter")
	}

	rest := content[4:]
	yamlPart, body, found := strings.Cut(rest, "\n---\n")
	if !found {
		return Snippet{}, errors.New("unterminated YAML frontmatter")
	}

	var snippet Snippet
	if err := yaml.Unmarshal([]byte(yamlPart), &snippet); err != nil {
		return Snippet{}, errors.WithMessage(err, "parse YAML")
	}

	snippet.Name = strings.TrimSuffix(filepath.Base(filename), ".md")
	snippet.SourcePath = filename
	snippet.Content = strings.TrimSpace(body)
	snippet.nameLower = strings.ToLower(snippet.Name)
	snippet.descLower = strings.ToLower(snippet.Description)
	snippet.searchTokens = tokenizeForSearch(snippet.nameLower, snippet.descLower, snippet.Tags)
	snippet.tagSet = make(map[string]struct{}, len(snippet.Tags))
	for _, tag := range snippet.Tags {
		snippet.tagSet[tag] = struct{}{}
	}
	snippet.globs = make([]compiledGlob, len(snippet.Paths))
	for i, p := range snippet.Paths {
		snippet.globs[i] = compileGlob(p)
	}
	snippet.sections = parseSections(snippet.Content)

	return snippet, nil
}

func parseSections(content string) []section {
	lines := strings.Split(content, "\n")
	var sections []section
	var current *section

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			if current != nil {
				current.body = strings.TrimSpace(current.body)
				sections = append(sections, *current)
			}

			heading := strings.TrimLeft(line, "#")
			heading = strings.TrimSpace(heading)
			current = &section{heading: heading, body: line + "\n"}
			continue
		}
		if current != nil {
			current.body += line + "\n"
		}
	}

	if current != nil {
		current.body = strings.TrimSpace(current.body)
		sections = append(sections, *current)
	}

	return sections
}

func compileGlob(pattern string) compiledGlob {
	if !strings.Contains(pattern, "**") {
		return compiledGlob{raw: pattern, simple: true}
	}

	parts := strings.Split(pattern, "**")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			segments = append(segments, part)
		}
	}

	segParts := make([][]string, len(segments))
	for i, segment := range segments {
		segParts[i] = strings.Split(segment, "/")
	}

	return compiledGlob{raw: pattern, segParts: segParts}
}
