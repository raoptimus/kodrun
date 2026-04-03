# KodRun

> **Beta** — this project is under active development. APIs and configuration may change.

CLI agent for writing and maintaining Go code. Runs fully locally via [Ollama](https://ollama.com) API. Reads rules, snippets, and documentation from the project working directory, executes tools (file operations, build/test/lint), and automatically fixes errors through LLM.

Tested with **qwen3-coder:30b** model. For RAG (semantic code search), the **nomic-embed-text** embedding model is recommended.

## Features

- Chat with LLM that has access to project files and Go tools
- Automatic fix of compilation errors, tests, and linter issues
- Rule system: code style, architecture, custom commands
- Snippet system: reusable code templates with placeholders
- RAG: semantic search over the project codebase
- MCP (Model Context Protocol) support for external tool servers
- Fullscreen TUI (bubbletea) and plain stdout mode for pipes/scripts
- Path traversal protection and forbidden patterns for security
- Context management with auto-compaction on overflow
- Multi-provider support (multiple Ollama instances)

## Quick Start

### Requirements

- Go 1.25+
- [Ollama](https://ollama.com) with a loaded model

### Installation

```bash
go install github.com/raoptimus/kodrun/cmd/kodrun@latest
```

Or from source:

```bash
git clone https://github.com/raoptimus/kodrun.git
cd kodrun
make install
```

### Launch

```bash
# Make sure Ollama is running
ollama serve

# Pull the model (if not already pulled)
ollama pull qwen3-coder:30b

# (Optional) Pull embedding model for RAG
ollama pull nomic-embed-text

# Interactive mode (TUI)
kodrun

# Initialize a new project
kodrun init
```

## Usage

### Initialize a New Project

The `kodrun init` command creates the `.kodrun/` directory structure with starter rules, snippets, commands, and an `AGENTS.md` file:

```bash
cd your-go-project
kodrun init
```

This creates:

```
.kodrun/
  rules/       — code style and architecture rules
  docs/        — project documentation
  commands/    — custom chat commands
  snippets/    — reusable code templates
```

### Interactive Mode

```bash
# TUI with fullscreen interface
kodrun

# Plain stdout (no TUI)
kodrun --no-tui
```

### One-shot Task

```bash
# From command line
kodrun -- "write a unit test for ParseConfig function"

# Via make
make task TASK="add godoc to public functions in auth.go"

# Via pipe
echo "write tests for auth.go" | kodrun --no-tui
```

### Subcommands with Auto-fix

```bash
# Build the project; on errors — LLM fixes and retries
kodrun build

# Run tests with auto-fix
kodrun test ./internal/config/...

# Linter with auto-fix
kodrun lint

# Fix a specific file
kodrun fix internal/agent/agent.go
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--model` | Ollama model | from config |
| `--work-dir` | Working directory | `.` |
| `--no-tui` | Plain stdout mode | `false` |
| `--no-fix` | Disable auto-fix | `false` |
| `--config` | Config file path | auto-detect |
| `--verbose` | Verbose output | `false` |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `KODRUN_MODEL` | Ollama model (overrides config) |
| `KODRUN_OLLAMA_URL` | Ollama API URL |
| `KODRUN_WORK_DIR` | Working directory |
| `KODRUN_NO_TUI` | `1` or `true` — disable TUI |

## Configuration

Config is resolved in the following order (each subsequent level overrides the previous):

1. Built-in defaults
2. `~/.config/kodrun/config.yaml` — global config
3. `.kodrun.yaml` in the project root — project config
4. Environment variables
5. Command-line flags

See [`examples/kodrun.yaml`](examples/kodrun.yaml) for a full project config example and [`examples/global-config.yaml`](examples/global-config.yaml) for a global config example.

### Minimal `.kodrun.yaml`

```yaml
ollama:
  model: "qwen3-coder:30b"
  context_size: 32768

agent:
  auto_fix: true

providers:
  embed:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"

rag:
  enabled: true
  provider: embed
```

## Rules

KodRun loads `.md` files from directories listed in `rules.dirs` and includes them in the agent's system prompt. This lets you customize agent behavior for your specific project.

### Rule File Format

Standard Markdown with optional front matter:

```markdown
---
priority: high       # high | normal | low — inclusion order in prompt
scope: coding        # coding | review | fix | all
---

# Style Rules

- Use `errors.Is` instead of direct comparison
- All public functions must have godoc
```

### Example: `.kodrun/rules/style.md`

```markdown
---
priority: high
scope: coding
---

# Go Style Rules

- Use `errors.Is`/`errors.As` instead of direct error comparison
- All public functions must have godoc comments
- Wrap errors with `fmt.Errorf("operation: %w", err)`
- Define sentinel errors as package-level `var`
```

See more examples in [`examples/rules/`](examples/rules/).

## Snippets

Snippets are reusable code templates that the LLM can use when generating code. They help maintain consistency across the project.

### Snippet File Format

YAML front matter followed by the template code:

```markdown
---
description: "Standard HTTP handler"
tags: [http, handler]
lang: go
placeholders:
  EntityName: "Name of the entity"
---

func (h *Handler) Get{{EntityName}}(w http.ResponseWriter, r *http.Request) {
    // handler implementation
}
```

Place snippet files in `.kodrun/snippets/` (or directories listed in `snippets.dirs`).

The agent can retrieve snippets via the `get_snippet` tool based on name, tags, or file path context.

## Custom Commands

Files in `.kodrun/commands/` define commands callable via `/command` in chat:

### Example: `.kodrun/commands/review.md`

```markdown
---
command: /review
description: "Run code review on a file"
---

Perform a detailed code review of {{file}}.
Check for:
- Bugs and logic errors
- Error handling
- Style violations
- Missing tests
- Performance issues
```

See more examples in [`examples/commands/`](examples/commands/).

## RAG (Semantic Search)

KodRun can index your project files and perform semantic search to find relevant code context. This is especially useful for large codebases.

```yaml
providers:
  embed:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"      # the embedding model lives here

rag:
  enabled: true
  provider: embed                  # references the profile above
  index_dirs: ["."]
  chunk_size: 512
  top_k: 5
```

Make sure to pull the embedding model first:

```bash
ollama pull nomic-embed-text
```

## Available Tools

### File Operations

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents |
| `write_file` | Write a file (creates directories) |
| `edit_file` | Find & replace text in a file |
| `list_dir` | List files in a directory |
| `find_files` | Find files by glob pattern |
| `grep` | Search files by regex |
| `delete_file` | Delete a file |
| `create_dir` | Create a directory |
| `move_file` | Move/rename a file |
| `bash` | Execute a shell command |

### Go Tools

| Tool | Description |
|------|-------------|
| `go_build` | `go build` with error parsing |
| `go_test` | `go test` with error parsing |
| `go_lint` | `golangci-lint run` |
| `go_fmt` | `gofmt -w` |
| `go_vet` | `go vet` |
| `go_mod_tidy` | `go mod tidy` |

### Security

- All paths are resolved relative to the working directory
- Directory escape is blocked (path traversal protection)
- `forbidden_patterns` from config block access to sensitive files

## Development

```bash
make build          # Build binary
make install        # Install to $GOPATH/bin
make test           # Unit tests
make lint           # Linter
make clean          # Clean artifacts
make help           # List targets
```

## License

MIT
