package rules

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
)

// Rule represents a loaded rule file.
type Rule struct {
	Path     string
	Content  string
	Priority Priority
	Scope    Scope
	ModTime  time.Time
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

// Loader loads and caches rules from directories.
type Loader struct {
	dirs     []string
	rules    []*Rule
	commands map[string]*Command
}

// NewLoader creates a rules loader.
func NewLoader(dirs []string) *Loader {
	return &Loader{
		dirs:     dirs,
		commands: make(map[string]*Command),
	}
}

// Load reads all .md files from configured directories.
func (l *Loader) Load() error {
	l.rules = nil
	l.commands = make(map[string]*Command)

	for _, dir := range l.dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".md" {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			content := string(data)
			priority, scope, body := parseFrontMatter(content)

			// Check if this is a command definition
			if strings.Contains(dir, "commands") {
				cmd := parseCommand(path, content)
				if cmd != nil {
					l.commands[cmd.Name] = cmd
				}
			}

			l.rules = append(l.rules, &Rule{
				Path:     path,
				Content:  body,
				Priority: priority,
				Scope:    scope,
				ModTime:  info.ModTime(),
			})

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

// Rules returns loaded rules filtered by scope.
func (l *Loader) Rules(scope Scope) []*Rule {
	var filtered []*Rule
	for _, r := range l.rules {
		if r.Scope == ScopeAll || r.Scope == scope {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// AllRulesContent returns concatenated content of all rules for a scope.
func (l *Loader) AllRulesContent(scope Scope) string {
	rules := l.Rules(scope)
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.Content)
		b.WriteByte('\n')
	}
	return b.String()
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

func parseFrontMatter(content string) (Priority, Scope, string) {
	priority := PriorityNormal
	scope := ScopeAll

	if !strings.HasPrefix(content, "---") {
		return priority, scope, content
	}

	end := strings.Index(content[3:], "---")
	if end == -1 {
		return priority, scope, content
	}

	frontMatter := content[3 : end+3]
	body := content[end+6:]

	for _, line := range strings.Split(frontMatter, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "priority":
			switch val {
			case "high":
				priority = PriorityHigh
			case "low":
				priority = PriorityLow
			}
		case "scope":
			scope = Scope(val)
		}
	}

	return priority, scope, strings.TrimSpace(body)
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
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"")

		switch key {
		case "command":
			cmd.Name = strings.TrimPrefix(val, "/")
		case "description":
			cmd.Description = val
		}
	}

	if cmd.Name == "" {
		// Use filename as command name
		cmd.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}

	return cmd
}
