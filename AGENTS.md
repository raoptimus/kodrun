# AGENTS.md

This file provides guidance when working with code in this repository.

## Copyright header

Every `.go` file MUST start with the following copyright header:

```go
/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */
```

When creating new `.go` files, always add this header before the `package` declaration.

## Releasing

When preparing a new version, update the `version` constant in `cmd/kodrun/main.go` before tagging.

## Build & Development Commands

```bash
make build          # Build binary into .build/
make install        # Install to $GOPATH/bin (run after code changes)
make lint           # golangci-lint (config: .golangci.yml) — run before committing
make test-unit      # Unit tests with race detector & coverage
make clean          # Remove build artifacts
```

Run a single test:
```bash
go test -v -run TestFunctionName ./internal/package/...
```

Run tests for a specific package:
```bash
go test -v -count=1 ./internal/llm/ollama/...
```

## Lint & Code Quality

- Linter config: `.golangci.yml` with strict settings (err113, gosec, ireturn, gocritic, etc.)
- Never use `//nolint` directives — fix the actual code
- Run `make lint` before committing; verify 0 issues

## Configuration files

When modifying `internal/config/config.go`, also update:
- `examples/kodrun.yaml`
- `examples/global-config.yaml`

## Project conventions

- KodRun's own rules, snippets, and docs live in `.kodrun/` (never `.claude/rules/`)
- Error wrapping: `github.com/pkg/errors` (not `fmt.Errorf`)
- CLI framework: `github.com/urfave/cli/v3`
- Go 1.25+, module: `github.com/raoptimus/kodrun`

## Internationalization

KodRun supports multiple human languages (`language: "ru"/"en"/...` in config) and multiple programming languages (`project_language: "go"/"python"/"jsts"`). Never hardcode user-facing strings in a specific language — use `planLabel(lang, key)` from `internal/agent/i18n.go` or the TUI `Locale` from `internal/tui/i18n.go`. Detection strings (parsing LLM output for plan markers, vague phrases, etc.) must always include all supported languages regardless of the configured language.

## Architecture

KodRun is a CLI agent for writing and maintaining Go/Python/JS-TS code. It runs locally via LLM providers (Ollama, OpenAI-compatible servers like vllm). Three operating modes: **plan** (read-only analysis), **edit** (full tool access), **chat** (free-form discussion with read-only tools).

### Package layout

- **`cmd/kodrun/main.go`** — CLI entry point (urfave/cli/v3), wires all components, graceful shutdown. Blank-imports `llm/ollama` and `llm/openai` for factory registration.
- **`internal/llm/`** — LLM provider abstraction layer:
  - `client.go` — `Client` interface (Ping, Models, Chat, ChatSync, Embed)
  - `factory.go` — `RegisterFactory`/`NewClient` pattern; backends register via `init()`
  - `aggregate.go` — shared `AggregateChatStream` (stream-to-sync with fallback tool-call parsing)
  - `parser.go` — `ParseToolCalls` (JSON + XML formats), `CleanToolCallText`
  - `errors.go` — `DetectErrorJSON`, `IsDialError`
  - `ollama/` — Ollama backend (NDJSON streaming, `/api/chat`, `/api/tags`, `/api/embed`)
  - `openai/` — OpenAI-compatible backend (SSE streaming, `/v1/chat/completions`, `/v1/models`, `/v1/embeddings`, Bearer auth)
- **`internal/agent/`** — Agent loop and orchestrator:
  - `agent.go` — core chat loop: LLM call -> tool execution -> result injection, Mode (plan/edit/chat), events, confirm, allowedReadPaths whitelist
  - `orchestrator.go` — Plan -> Execute -> Review pipeline with DAG parallelism
  - `orchestrator_review.go` — V2 code review pipeline (pre-loads context per file, reviews in parallel without tool-calling)
  - `dag.go` — DAG executor with topological sort and per-file locking
  - `context.go` — context management with auto-compaction on overflow
  - `permission.go` — per-tool permission management (AllowOnce/Session/Augment)
  - `role.go` — role definitions (planner/executor/reviewer/extractor/structurer/step_executor)
  - `worker_pool.go` — parallel read-only tool call execution
- **`internal/tools/`** — Tool registry and implementations. Tools are auto-registered based on detected project language. Each tool is a separate file (e.g., `file_read.go`, `go_tools.go`, `git_tools.go`).
- **`internal/config/`** — YAML config via viper/mapstructure. Multi-provider support with role-to-provider mapping.
- **`internal/rag/`** — RAG indexing and semantic search over `.kodrun/` conventions.
- **`internal/rules/`** — Rule loader from `.kodrun/rules/` with `@`-reference resolution.
- **`internal/snippets/`** — Snippet loader from `.kodrun/snippets/` with tech-stack filtering.
- **`internal/tui/`** — Bubbletea fullscreen TUI.
- **`internal/mcp/`** — MCP (Model Context Protocol) support for external tool servers.
- **`internal/runner/`** — Build/test/lint runner with auto-fix loop.
- **`internal/projectlang/`** — Automatic project language detection.

### Key patterns

- **Provider factory**: Backends register via `llm.RegisterFactory("type", factory)` in `init()`. Main imports backends with blank import (`_ "...ollama"`). `llm.NewClient(cfg)` dispatches by `cfg.Type` (default: "ollama").
- **Config hierarchy**: built-in defaults -> `~/.config/kodrun/config.yaml` -> `.kodrun/kodrun.yaml` -> env vars -> CLI flags. Legacy `ollama:` section is used only when `providers:` is empty.
- **Provider config**: `ProviderConfig` has `Type` ("ollama"|"openai"), `APIKey` (Bearer token), `BaseURL`, `Model`, `Timeout`, `ContextSize`, `Temperature`, `Format`. Multiple named providers are mapped to roles via `agent.*_provider` fields.
- **Streaming**: Ollama uses NDJSON (one JSON object per line), OpenAI uses SSE (`data: {...}\n\n`, `data: [DONE]`). Both feed into `AggregateChatStream` for sync consumption.

## Orchestrator pipeline (implementation tasks)

```
User task ("смотри задачу в TODO.md")
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ 1. PLANNER (RolePlanner)                            │
│    Sub-agent with read-only tools                   │
│    System prompt: role.go RolePlanner               │
│                                                     │
│    Input:  user task + RAG context + file hints      │
│    Actions: read_file, list_dir, grep, go_structure │
│    Output: markdown plan with CONSTRAINTS + steps   │
│                                                     │
│    Step format:                                     │
│      N. <title>                                     │
│         file: <path>                                │
│         context: <details for executor>             │
│         rationale: <why>                            │
│    Code: orchestrator.go:runPlannerWithTools()       │
│          orchestrator.go:runPlannerPrefetch()        │
└─────────────────────────────────────────────────────┘
    │
    │ (review mode only: extractPlan → RoleExtractor)
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ 2. CONFIRM (TUI)                                    │
│    User sees rendered plan, chooses:                │
│    • accept  → execute                              │
│    • augment → runPlannerRevision() → re-confirm    │
│    • deny    → abort                                │
└─────────────────────────────────────────────────────┘
    │ accept
    ▼
┌─────────────────────────────────────────────────────┐
│ 3. STRUCTURER (RoleStructurer)                      │
│    Converts markdown plan → JSON Plan{Steps[]}      │
│    No tools, json format, temperature=0             │
│                                                     │
│    Output JSON:                                     │
│    {                                                │
│      "context": "<comprehensive project summary>",  │
│      "steps": [{                                    │
│        "id", "title", "context", "files",           │
│        "action", "rationale", "depends_on",         │
│        "rule_names", "examples"                     │
│      }]                                             │
│    }                                                │
│    Code: orchestrator.go:structurePlan()             │
└─────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ 4. DAG EXECUTOR                                     │
│    Topological sort of steps by depends_on          │
│    Per-file locking (no two steps edit same file)   │
│    Pre-compute per-step RAG bundles                 │
│    Code: dag.go:runPlanDAG()                        │
│                                                     │
│    For each step (parallel if independent):         │
│    ┌───────────────────────────────────────────┐    │
│    │ STEP SUB-AGENT (RoleExecutor)             │    │
│    │                                           │    │
│    │ Receives:                                 │    │
│    │   ## Goal                                 │    │
│    │   <Plan.Context — project-level summary>  │    │
│    │                                           │    │
│    │   ## Step N: <title>                      │    │
│    │   Context: <Step.Context — step details>  │    │
│    │   Action: <edit/create/delete/run>        │    │
│    │   Rationale: <why>                        │    │
│    │   Files: <whitelist>                      │    │
│    │                                           │    │
│    │   ## Source Code (pre-read from disk)      │    │
│    │   ## Examples (from step.Examples)         │    │
│    │   ## RAG bundle (rules + semantic search)  │    │
│    │                                           │    │
│    │ Cannot read files outside whitelist        │    │
│    │ Cannot see original task or other steps    │    │
│    │ Code: subagent.go:runStep()               │    │
│    └───────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ 5. REVIEWER (RoleReviewer, optional)                │
│    Reads changed files, checks against plan         │
│    Output: LGTM or list of issues                   │
│    Code: orchestrator.go:runReviewer()               │
└─────────────────────────────────────────────────────┘
```

### Context flow (what each role sees)

```
Original task ──► Planner (full task + RAG + project files)
                      │
                      ▼ markdown plan
                  Structurer (plan text only)
                      │
                      ▼ Plan.Context + Step[].Context
                  Step Executor (Plan.Context + Step.Context + source code + RAG)
                                  ▲
                                  │
                  Executor CANNOT see: original task, other steps, planner output
```

**Key insight**: Step executor sees ONLY `Plan.Context` (## Goal) and `Step.Context` (Context:). If planner/structurer produce thin context, executor works blind.

## Roles

| Role | Provider field | Purpose |
|------|----------------|---------|
| `planner` | `thinking_provider` | Reads code via read-only tools, writes markdown plan with CONSTRAINTS + structured steps |
| `executor` | `executor_provider` | Applies the approved plan via edit_file/write_file |
| `step_executor` | `executor_provider` | Executes a single DAG step in isolation (same prompt as executor) |
| `reviewer` | `thinking_provider` | Final review pass over executor changes |
| `extractor` | `extractor_provider` | Converts raw planner output to structured JSON findings (review mode only, json + temp=0) |
| `structurer` | `extractor_provider` | Converts markdown plan to JSON `Plan{Steps[]}` for DAG execution (json + temp=0) |
| `response_classifier` | `provider` | Classifies TUI responses (plan / Q&A / clarification) |
| `code_reviewer` | `thinking_provider` | Per-file code review (no tool-calling, context pre-loaded) |
| `arch_reviewer` | `thinking_provider` | Architecture review from go_structure output |

### Role prompt generation

All role prompts generated dynamically in `role.go:systemPromptForRole()`:
- Language-aware: build/lint/test tools injected based on detected `progLang` (go/python/jsts/unknown)
- RAG-aware: mandatory rules injection when RAG enabled
- Tool-aware: available tool names listed in system prompt

## Code review pipeline (`/code-review`)

```
Changed files (git diff)
    │
    ▼
┌──────────────────────┐
│ Context pre-loading   │  file contents + RAG snippets + dependency signatures
└──────────────────────┘
    │
    ├──►  code_reviewer (per file, parallel, no tools)
    │
    ├──►  arch_reviewer (project-wide structure, no tools)
    │
    ▼
┌──────────────────────┐
│ Extractor (merge)     │  deduplicate, structure, render
└──────────────────────┘
```

- Unchanged files served from disk cache (`.kodrun/cache/review/`)
- Live LLM streaming visible in transcript view (`Ctrl+O`)
- Results presented as structured plan with severity levels

## Orchestrator pipeline (review mode)

```
User task
    │
    ▼
Planner (read-only analysis) ──► Extractor (normalize to JSON findings)
    │                                  │
    │                                  ▼
    │                            Rendered plan with what/context/why/fix
    ▼
Confirm ──► Executor (fix findings) ──► Reviewer
```

In review mode, Extractor runs INSTEAD of Structurer. Each finding has: `file`, `line`, `severity`, `what`, `context`, `why`, `fix`.

## Language-aware prompts

System prompts adapt to the detected project language:

| Language | Build tool | Lint tool | Test tool | Structure tool |
|----------|-----------|-----------|-----------|---------------|
| Go | `go_build` | `go_lint` | `go_test` | `go_structure` |
| Python | `python_run` | `ruff` | `pytest` | -- |
| JS/TS | `tsc` | `eslint` | `npm_test` | -- |

| Unknown | -- | -- | -- | -- |

Tool names, coding conventions, and standards labels in planner/executor/reviewer prompts are set according to the detected language. When language is unknown, system prompts contain no language-specific guidelines — the agent follows user instructions without bias toward any particular language or stack. Override with `agent.project_language` in config.

## Operating modes

| Mode | Tools | System prompt focus | Toggle |
|------|-------|-------------------|--------|
| **plan** | read-only (read_file, grep, git_status, ...) | Analyze code, create numbered plans. No code generation. | Shift+Tab |
| **edit** | all | Execute plans, write/edit files, run build/lint/test. | Shift+Tab |
| **chat** | read-only | Answer questions, explain code, discuss architecture. No plans, no file edits. | Shift+Tab |

Shift+Tab cycles: plan → edit → chat → plan. Config: `agent.default_mode: "plan"|"edit"|"chat"`.

## Provider wiring

```yaml
providers:
  default:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    temperature: 0.7
  thinking:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    temperature: 0.6
  precise:
    base_url: "http://localhost:11434"
    model: "qwen3-coder:30b"
    temperature: 0.2
  embed:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"

agent:
  provider: default
  thinking_provider: thinking
  executor_provider: precise
  extractor_provider: default
```

## DAG parallelism

When `agent.max_parallel_tasks > 1`, the structurer converts the plan into a DAG. Independent steps run in parallel sub-agents with:
- Per-file locking (two steps cannot edit the same file concurrently)
- Fresh history per sub-agent
- Own read-path whitelist scoped to the step's files
- Per-step RAG bundle (pre-fetched once, shared across the DAG run)

## LLM backends

| Backend | Type | Streaming | Auth |
|---------|------|-----------|------|
| Ollama | `ollama` (default) | NDJSON | none |
| OpenAI-compatible (vLLM, llama.cpp, LiteLLM) | `openai` | SSE | Bearer token (`api_key`) |

Both backends feed into `AggregateChatStream` for unified sync consumption with fallback tool-call parsing.
