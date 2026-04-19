# KodRun

[![Go Reference](https://pkg.go.dev/badge/github.com/raoptimus/kodrun.svg)](https://pkg.go.dev/github.com/raoptimus/kodrun/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/raoptimus/kodrun)](https://goreportcard.com/report/github.com/raoptimus/kodrun)
[![License](https://img.shields.io/github/license/raoptimus/kodrun)](https://github.com/raoptimus/kodrun/blob/main/LICENSE)

> **Beta** — this project is under active development. APIs and configuration may change.

CLI agent for writing and maintaining code in **Go, Python and JavaScript/TypeScript**. Runs locally via [Ollama](https://ollama.com) or any **OpenAI-compatible API** (vLLM, llama.cpp, LiteLLM, etc.). Loads rules, snippets and documentation from the project working directory, executes tools (file operations, build/test/lint, git) and automatically fixes errors through the LLM.

Tested with **qwen3-coder:30b**, **qwen3.6**, **qwen3.5:35b-a3b**. For RAG (semantic code search) the **nomic-embed-text** embedding model is recommended.

## Features

- **Multi-provider LLM support** — Ollama (NDJSON streaming) and OpenAI-compatible APIs (SSE streaming) via a unified `Client` interface. Backends register via `init()` factory pattern.
- **Multi-role orchestrator** — `planner` / `executor` / `reviewer` / `extractor` / `structurer` / `step_executor` / `response_classifier`, each role wired to its own provider profile.
- **Parallel DAG plan execution** — approved plans run as a dependency graph of sub-agents with per-file locking.
- **Multi-provider config** — one entry per (URL, model, temperature) profile; any role can point at any profile.
- **Language-aware system prompts** — tool names, build/lint/test commands, and coding conventions in system prompts are adapted to the detected project language (Go, Python, JS/TS). No more Go bias in non-Go projects.
- **Automatic project language & tech stack detection** — Go, Python, JS/TS; per-language tools auto-registered. Tech stack (gRPC, HTTP, Postgres, ClickHouse, MongoDB, Redis, Kafka) detected from dependencies for snippet filtering.
- **Rules + snippets + custom commands** — `.kodrun/` driven, with `@`-reference validation at startup.
- **RAG with multi-index** — semantic search over conventions + docs + snippets + embedded references. Architecture overview snippets are pinned verbatim in `/code-review`. Configurable backend: local (in-memory with disk persistence) or Muninn DB.
- **`/code-review` command** — per-file code review pipeline. Pre-loads file contents, RAG snippets and dependency signatures, then reviews each file in parallel (no tool-calling required during review). Unchanged files are served from disk cache (`.kodrun/cache/review/`). Includes a separate architecture review pass. Results are merged, deduplicated and presented as a structured plan. Live LLM streaming in transcript view (`Ctrl+O`).
- **Edit nudge** — auto-correction for models that respond with prose instead of tool calls in EDIT mode; prevents plan-shaped text from being silently accepted.
- **`web_fetch` tool** — download web pages, convert HTML to markdown, and optionally index via RAG for semantic search.
- **TUI** — fullscreen bubbletea interface, markdown rendering, confirm card with diff preview, step-level confirmation, RAG indexing progress, cache stats. Plain stdout mode for pipes/scripts.
- **Tool-call result cache** — repeated read-only calls are served from cache.
- **MCP (Model Context Protocol)** support for external tool servers.
- **Security** — path traversal protection, `forbidden_patterns`, executor write-whitelist scoped to the approved plan.
- **Context management** — auto-compaction on overflow.
- **CI** — GitHub Actions pipeline with lint, test and coverage upload.

## Quick Start

### Requirements

- Go 1.25+
- [Ollama](https://ollama.com) with a loaded chat model and (optional) embedding model

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

# Pull the chat model
ollama pull qwen3-coder:30b

# (Optional) Pull the embedding model for RAG
ollama pull nomic-embed-text

# Interactive mode (TUI)
kodrun

# Initialize a new project (creates .kodrun/ starter layout)
cd your-project
kodrun init
```

Language (Go / Python / JS-TS) is detected automatically from project markers (`go.mod`, `pyproject.toml`, `package.json`, …). Override via `agent.project_language` if needed.

## Usage

### Interactive mode

```bash
# TUI with fullscreen interface
kodrun

# Plain stdout (no TUI) — useful in pipes and CI
kodrun --no-tui
```

### One-shot task

```bash
# From the command line
kodrun -- "write a unit test for ParseConfig"

# Via make
make task TASK="add godoc to public functions in auth.go"

# Via pipe
echo "write tests for auth.go" | kodrun --no-tui
```

### Subcommands with auto-fix

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

### Built-in slash commands

Available inside the interactive TUI. Type `/` to see the full list.

| Command | Description |
|---------|-------------|
| `/code-review` | Per-file code review with pre-loaded context (file contents, RAG snippets, dependency signatures), disk cache for unchanged files, and architecture review pass. |
| `/orchestrate` | Run the full Plan → Execute → Review pipeline on a task. |
| `/edit` | Switch to edit mode (full toolset). |
| `/init` | Create the `.kodrun/` starter structure in the current project. |
| `/diff` | Show the current uncommitted diff. |
| `/compact` | Summarize the conversation to free up context. |
| `/clear` | Clear conversation context. |
| `/resume`, `/sessions` | Resume last saved session or list saved sessions. |
| `/reindex`, `/rag`, `/add_doc` | Manage the RAG index. |
| `/exit` | Exit KodRun. |

Project-specific commands defined in `.kodrun/commands/*.md` are listed alongside the built-ins.

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--model` | Ollama model (overrides config) | from config |
| `--work-dir` | Working directory | `.` |
| `--no-tui` | Plain stdout mode | `false` |
| `--no-fix` | Disable auto-fix | `false` |
| `--config` | Config file path | auto-detect |
| `--verbose` | Verbose output | `false` |

### Environment variables

| Variable | Description |
|----------|-------------|
| `KODRUN_MODEL` | Ollama model (overrides config) |
| `KODRUN_OLLAMA_URL` | Ollama API URL (legacy; prefer `providers.*.base_url`) |
| `KODRUN_WORK_DIR` | Working directory |
| `KODRUN_NO_TUI` | `1` or `true` — disable TUI |

## Configuration

Config is resolved in this order (each level overrides the previous):

1. Built-in defaults
2. `~/.config/kodrun/config.yaml` — global config
3. `.kodrun/kodrun.yaml` in the project root — project config
4. Environment variables
5. Command-line flags

See [`examples/kodrun.yaml`](examples/kodrun.yaml) for a fully annotated project config and [`examples/global-config.yaml`](examples/global-config.yaml) for the global-config example.

### ⚠️ Migrating from beta1

- `rag.embedding_model` is **removed** — use `providers.embed.model` + `rag.provider: embed`.
- The `ollama:` section is now a legacy fallback, only used when `providers:` is empty. Prefer `providers:`.
- `temperature` now lives on the provider profile, not on `agent:`.
- Rule `@`-references must point at `.kodrun/` (not `.claude/`). Broken references are logged via `slog.Warn` at startup.

### ⚠️ Migrating from beta2

- `agent.max_workers` is **renamed** to `agent.max_tool_workers`.
- RAG **no longer indexes project source code**. Only `.kodrun/rules/`, `.kodrun/snippets/`, `.kodrun/docs/` and embedded language standards are indexed. Source files are read live via `read_file`.
- `rag.index_dirs`, `rag.exclude_dirs` and `rag.max_chunks_per_file` are **deprecated** (kept for config compatibility, ignored at runtime).
- New option `agent.specialist_timeout` (default `5m`) — wall-time cap for a single review phase.
- New option `rag.review_budget_bytes` (default `24576`) — hard cap on RAG prefetch block in `/code-review` prompts.

### ⚠️ Migrating from beta3

- **V1 specialist fan-out pipeline removed.** `/code-review` now uses a V2 pipeline that pre-loads all context (file contents, RAG snippets, dependency signatures) per file and reviews in parallel without tool-calling. The `agent.orchestrator` toggle and `agent.specialist_timeout` option are no longer used.
- **Review cache.** Per-file review results are cached on disk in `.kodrun/cache/review/`. Unchanged files are not re-reviewed on subsequent runs.
- **Tech stack detection.** Project technologies (gRPC, HTTP, Postgres, ClickHouse, MongoDB, Redis, Kafka) are auto-detected from `go.mod` and `.proto` files. Snippets with a `requires` field are filtered by detected tech stack.
- New option `agent.think` (default `false`) — enables thinking mode for planners and reviewers.

### ⚠️ Migrating to v1.2.0-beta

- **OpenAI-compatible backend.** New provider type `openai` for vLLM, llama.cpp, LiteLLM and other OpenAI-compatible servers. Set `type: openai` and `api_key` on the provider profile.
- **Legacy `internal/ollama` package removed.** All consumers migrated to the unified `internal/llm` interface. If you have custom forks, update imports from `internal/ollama` to `internal/llm/ollama`.
- **Language-aware prompts.** System prompts for planner, executor, and reviewer roles now use language-specific tool names (`go_build` / `tsc` / `ruff`, etc.) and conventions instead of hardcoded Go references. No config changes needed — this is automatic.
- **Plan revision preserves context.** When you add a remark after planning, the revision agent now receives the original task context (including RAG and source code), not just the extracted plan text.
- **RAG backend selection.** New option `rag.backend` (`local` or `muninn`). Default is `local` (unchanged). Muninn backend delegates embeddings to an external Muninn DB server.
- **Live LLM streaming.** During `/code-review`, press `Ctrl+O` to open transcript view with real-time LLM output streaming.

### Minimal `.kodrun/kodrun.yaml`

```yaml
providers:
  default:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    context_size: 32768
    temperature: 0.7
  embed:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"

agent:
  provider: default
  auto_fix: true

rag:
  enabled: true
  provider: embed
```

### Providers & roles

A **provider** is one combination of `base_url` / `model` / `timeout` / `context_size` / `temperature`. Roles inside the orchestrator are mapped to provider profiles through the `agent.*_provider` fields. Temperature lives on the profile — to get different temperatures for different roles, define multiple profiles.

```yaml
providers:
  default:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    context_size: 32768
    temperature: 0.7        # chat / executor
  thinking:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    context_size: 32768
    temperature: 0.6        # planner + reviewer
  precise:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    context_size: 32768
    temperature: 0.2        # deterministic edits
  embed:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"

agent:
  provider: default           # chat + default fallback for unset roles
  thinking_provider: thinking # planner + reviewer
  executor_provider: precise  # executor + step_executor
  extractor_provider: default # extractor + structurer (json/temp=0 forced automatically)
```

Role → provider mapping:

| Role | Wired via | Purpose |
|------|-----------|---------|
| `planner` | `thinking_provider` | Writes the markdown plan |
| `reviewer` | `thinking_provider` | Final pass over executor changes |
| `executor`, `step_executor` | `executor_provider` | Applies the plan, whitelist-locked |
| `extractor`, `structurer` | `extractor_provider` | Markdown → human / markdown → JSON `Plan{Steps[]}` (always json + temp=0) |
| `response_classifier` | `provider` | Background TUI response classification |

### Parallel plan execution

Two independent levels of parallelism:

```yaml
agent:
  max_tool_workers: 4   # parallel read-only tool calls inside ONE sub-agent
  max_parallel_tasks: 1   # parallel STEPS of an approved plan (DAG) — default sequential
  max_replans: 2          # hard cap on REPLAN cycles within one run
```

- `max_tool_workers` caps how many read-only tool calls run concurrently inside a single chat turn.
- `max_parallel_tasks` controls true plan parallelism: the approved plan is compiled into a DAG and independent steps are executed by separate sub-agents with per-file locking. Each parallel sub-agent gets a fresh history, its own read-path whitelist and its own per-step RAG bundle. Raise to 2–3 on a fast model/GPU to enable parallelism.

## Rules

KodRun loads `.md` files from `.kodrun/rules/` and includes them in the agent's system prompt. Rules are the primary way to customise agent behaviour for your project.

### Rule file format

Standard Markdown with optional front matter:

```markdown
---
priority: high       # high | normal | low — inclusion order in the prompt
scope: coding        # coding | review | fix | all
globs: "internal/service/**"   # optional — path-scoped rule
---

# Style rules

- Use `errors.Is` instead of direct comparison
- All public functions must have godoc
```

Rules can reference shared documentation and examples via `@`-syntax:

```markdown
Conventions: @.kodrun/docs/service.md
Example:     @.kodrun/docs/example_service.go
Style guide: @.kodrun/docs/styleguide.md
```

`@`-references are resolved at load time and deduplicated. **Broken references are logged as warnings at startup** (`rule references missing file`) — typos no longer silently drop docs from the RAG index.

### Example: `.kodrun/rules/style.md`

```markdown
---
priority: high
scope: coding
---

# Go style rules

- Use `errors.Is`/`errors.As` instead of direct error comparison
- All public functions must have godoc comments
- Wrap errors with `fmt.Errorf("operation: %w", err)`
- Define sentinel errors as package-level `var`
```

See more examples in [`examples/rules/`](examples/rules/).

## Snippets

Snippets are reusable code templates in `.kodrun/snippets/`. The agent retrieves them via the `snippets()` tool (by name, tag or file-path glob).

### Snippet file format

YAML front matter followed by the template body:

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

#### Front matter fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `description` | string | yes | Short description of the snippet |
| `tags` | string[] | no | Tags for filtering and search |
| `requires` | string[] | no | Technologies the project must use for this snippet to apply (see below) |
| `paths` | string[] | no | Glob patterns for automatic matching by file path |
| `lang` | string | no | Language identifier (`go`, `python`, `protobuf`, etc.) |
| `placeholders` | map | no | Named placeholders to be replaced in the template |
| `related` | string[] | no | Names of related snippets |

#### Technology filtering with `requires`

Snippets can declare which technologies the project must use for the snippet to be relevant. If the project does not use a required technology, the snippet is excluded from RAG indexing and the `snippets()` tool results.

Detected technologies (for Go projects, based on `go.mod` dependencies and `.proto` files):

`grpc`, `http`, `postgres`, `clickhouse`, `mongodb`, `redis`, `kafka`

Example — a gRPC server snippet that is only relevant to projects using gRPC:

```markdown
---
description: "Template for gRPC server setup"
tags: [app, grpc, server]
requires: [grpc]
paths: ["**/app/**/application.go"]
lang: go
---
```

Snippets without `requires` are always included regardless of the project's tech stack.

### Pinned architecture overviews

Snippets tagged with any of `architecture`, `overview` or `structure` are **pinned verbatim at the top of the `/code-review` RAG prefetch block**, regardless of semantic ranking. Use this for a project-wide map:

```markdown
---
description: "Project architecture overview — layers, dependency flow, archetypes"
tags: [architecture, structure, overview]
lang: go
---

# Architecture

Go microservice, clean architecture. Layers:

- `cmd/<svc>/main.go` — bootstrap (CLI, Sentry, OTEL). No business logic.
- `internal/app/<svc>/application.go` — composition root (DI wiring).
- `internal/domain/` — business logic. Does not depend on transport or infrastructure.
- `internal/dal/` — data access layer.
- `internal/server/` — gRPC and HTTP transport.

Dependency direction: `transport → domain ← dal ← client`.
```

During `/code-review` the full text is injected into the architecture review prompt so the reviewer model sees the project map when analysing cross-cutting concerns.

## Custom commands

Files in `.kodrun/commands/` define commands callable via `/command` in chat.

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

## RAG (semantic search)

KodRun indexes project files, rules, snippets and embedded reference docs, then performs semantic search to find relevant context. Especially useful for large codebases.

```yaml
providers:
  embed:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"      # the embedding model lives on the provider

rag:
  enabled: true
  provider: embed                  # references the profile above
  index_dirs: ["."]
  chunk_size: 512
  chunk_overlap: 32
  top_k: 5
```

Pull the embedding model first:

```bash
ollama pull nomic-embed-text
```

The RAG index is built in the background on startup. Progress is shown in the TUI status bar; the final count appears as `RAG indexed N new chunks (M total)`.

## Available tools

Tools are **auto-registered based on the detected project language**. Read-only tools are also subject to the result cache.

### File operations

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents |
| `read_changed_files` | Diff with context for all git-changed source files (filtered by project language) |
| `write_file` | Write a file (creates directories) |
| `edit_file` | Find & replace text in a file |
| `list_dir` | List files in a directory |
| `find_files` | Find files by glob pattern |
| `grep` | Search files by regex |
| `delete_file` | Delete a file |
| `create_dir` | Create a directory |
| `move_file` | Move/rename a file |
| `file_stat` | File metadata (size, permissions, modification time) without reading contents |
| `bash` | Execute a shell command |
| `web_fetch` | Fetch a web page, convert HTML to markdown (+ optional RAG indexing) |

### Git

| Tool | Description |
|------|-------------|
| `git_status` | Show git status and current branch |
| `git_diff` | Show diff (optionally staged or against a ref) |
| `git_log` | Show recent commits |
| `git_commit` | Stage files and create a commit |

### Go (auto-registered for Go projects)

| Tool | Description |
|------|-------------|
| `go_build` | `go build` with error parsing |
| `go_test` | `go test` with error parsing |
| `go_lint` | `golangci-lint run` |
| `go_fmt` | `gofmt -w` |
| `go_vet` | `go vet` |
| `go_mod_tidy` | `go mod tidy` |
| `go_doc` | `go doc` lookup by package/symbol, or semantic search over indexed godoc via `query` |
| `go_structure` | Show Go file/package structure (types, interfaces, functions, constants) with line numbers |

### Python (auto-registered for Python projects)

| Tool | Description |
|------|-------------|
| `python_run` | Run a Python script |
| `pytest` | Run pytest |
| `pip_install` | Install a package |
| `ruff` | Ruff linter |
| `black` | Black formatter |

### JavaScript / TypeScript (auto-registered for JS/TS projects)

| Tool | Description |
|------|-------------|
| `npm_install` | Install npm dependencies |
| `npm_run` | Run an npm script |
| `npm_test` | Run `npm test` |
| `tsc` | TypeScript compiler |
| `eslint` | ESLint |

### Knowledge tools (always available)

| Tool | Description |
|------|-------------|
| `search_docs` | Semantic search over the RAG index |
| `get_rule` | Fetch a project rule by name |
| `snippets` | Query `.kodrun/snippets/` by name, tag, path or query |

## Security

- All paths are resolved relative to the working directory; directory escape is blocked (path traversal protection).
- `forbidden_patterns` in config block access to sensitive files.
- The executor honours a write-whitelist scoped to the approved plan — sub-agents cannot touch files outside their step.
- Rule `@`-references must resolve inside the project root; any attempt to escape is rejected.

## Development

```bash
make build          # Build the binary
make install        # Install to $GOPATH/bin
make test           # Unit tests
make lint           # Linter
make clean          # Clean artifacts
make help           # List all targets
```

## License

BSD 3-Clause License
[LICENSE](LICENSE)
