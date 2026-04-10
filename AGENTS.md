# AGENTS.md

KodRun — CLI-агент для написания и сопровождения Go-кода. Работает локально через Ollama API. Читает правила и snippets из `.kodrun/`, выполняет файловые и Go-инструменты, автоматически исправляет ошибки через LLM.

> **Важно:** правила, сниппеты и доки kodrun лежат строго в `.kodrun/rules/`, `.kodrun/snippets/`, `.kodrun/docs/`. Никаких `.claude/rules/` — это путь чужого инструмента (Claude Code) и в kodrun не используется.

Модуль: `github.com/raoptimus/go-agent`
Go: 1.25+

## Архитектура

```
cmd/kodrun/main.go              — CLI (cobra), точка входа, graceful shutdown, panic recovery
internal/
  agent/                         — агентный цикл (LLM → tools → result)
    agent.go                     — Agent: chat loop, tool calling, Mode (plan/edit), events, confirm, allowedReadPaths whitelist, temperature/format
    context.go                   — ContextManager: суммаризация при переполнении, adaptive token estimation
    permission.go                — PermissionManager: AllowOnce/Session/Augment, Fingerprint, StepConfirmFunc (per-step confirm), sync.RWMutex
    worker_pool.go               — WorkerPool: параллельное выполнение read-only tool calls с семафорой
    orchestrator.go              — Orchestrator: Plan→Execute→Review pipeline, confirmAndExecute (unified confirm+execute), DAG executor, structurePlan, runPlanDAG
    role.go                      — Role (planner/executor/reviewer/extractor/structurer/step_executor), systemPromptForRole
    plan.go                      — Plan/Step типы, PlanFromMarkdown, parseStructuredPlan (JSON)
    subagent.go                  — runStep: фабрика чистого sub-agent'а на один Step с whitelist + per-step RAG
    dag.go                       — stepDAG (топосорт), fileLockSet (per-path mutex), runPlanDAG (parallel scheduler, per-step TUI groups, per-step confirm)
    output.go                    — PlainOutput: plain stdout режим
  config/                        — конфигурация (viper, YAML, env vars), Validate()
  ollama/                        — HTTP-клиент Ollama API
    client.go                    — Chat (streaming), ChatSync, Ping, Models, Embed, retry
    types.go                     — Message, ToolCall, ChatRequest/Response, JSONSchema
    parser.go                    — парсинг tool calls из текста (JSON + XML), fallback ID generation
  tools/                         — реализация инструментов
    registry.go                  — Tool interface, Registry (sync.RWMutex), ToolResult, Cacheable/PathResolver, ResultCache wiring + write-tool invalidation
    cache.go                     — ResultCache: per-run кеш read-only tool результатов, mtime-инвалидация, prefix-инвалидация на write
    pathutil.go                  — SafePath (path traversal + symlink защита), IsForbidden
    file_read.go                 — read_file
    file_write.go                — write_file (с diff stats)
    file_edit.go                 — edit_file (find & replace, с diff)
    file_list.go                 — list_dir
    file_find.go                 — find_files (glob)
    file_grep.go                 — grep (regex)
    file_ops.go                  — delete_file, create_dir, move_file
    file_read_batch.go           — read_changed_files: diff с контекстом для всех изменённых code-файлов (whitelist по языку)
    go_tools.go                  — go_build, go_test, go_lint, go_fmt, go_vet, go_mod_tidy, go_doc (с query для godoc RAG search), bash
    web_fetch.go                 — web_fetch: скачивание HTML→markdown с опциональным RAG-индексом
    rule_tool.go                 — get_rule: доступ к правилам проекта
    snippet_tool.go              — snippets: поиск project snippets по path/tag/query
    rag_tool.go                  — search_docs: RAG semantic search
    diff.go                      — SimpleDiff, LineStats, FileActionType
  mcp/                           — MCP (Model Context Protocol) клиент
    jsonrpc.go                   — JSON-RPC 2.0 types и codec
    transport.go                 — Transport interface, StdioTransport (subprocess + pipes, timeout)
    client.go                    — MCPClient: Initialize, ListTools, CallTool
    tool_adapter.go              — ToolAdapter: bridges MCP tools → tools.Tool interface
    manager.go                   — Manager: lifecycle всех MCP серверов, idempotent Close (sync.Once)
  rag/                           — RAG (Retrieval-Augmented Generation)
    multi_index.go               — MultiIndex: common + godoc + web sub-indexes, adapters for GoDocIndexer/WebIndexer
    chunker.go                   — ChunkFiles(ctx): рекурсивная индексация, split с перекрытием, context-aware
    index.go                     — Index: embed, cosine search, JSON persistence, sync.Mutex
  rules/                         — загрузка правил из .md файлов
    loader.go                    — Loader: front matter (priority, scope), commands
  snippets/                      — загрузка и матчинг snippets
    loader.go                    — Loader: front matter, каталог, startup load
    matcher.go                   — match по path globs, tags, query
  runner/                        — парсинг ошибок и авто-исправление
    parser.go                    — ParseErrors: regex для build/test/lint
    fixer.go                     — Fixer: LLM → edit_file → повтор
  tui/                           — bubbletea TUI
    model.go                     — Model: viewport, textarea, event handling, plan/edit mode, task panic recovery
    i18n.go                      — Locale: локализация UI строк (en/ru)
    history.go                   — история команд (.kodrun/history)
  kodruninit/                   — kodrun init
    init.go                      — создание .kodrun/ со стартовой структурой
examples/                        — примеры конфигураций, правил, команд
```

## Ключевые компоненты

### Агентный цикл (`internal/agent/agent.go`)
`Agent.Send()` — основной цикл: отправляет сообщения в Ollama, получает ответ, если есть tool calls — разделяет на parallel (read-only через WorkerPool) и sequential (write с confirm), выполняет через `Registry`, добавляет результаты в историю, повторяет. Завершается при финальном текстовом ответе или `max_iterations`. Автоматический compact при 99% контекста.

### Orchestrator (`internal/agent/orchestrator.go`)
Координирует sub-agents: **Planner** (ModePlan, think=true, свободный reasoning) → **Extractor/Structurer** (deterministic, format=json, temperature=0) → approval gate → **Executor** (ModeEdit, whitelist, REPLAN sentinel) → **Reviewer** (опционально). Каждый sub-agent — отдельный Agent с shared Client/Registry и отдельной историей. Включается через `orchestrator: true` в конфиге или команду `/orchestrate`.

`Run()` (planner path) и `RunCodeReview()` (specialist review path) используют общий `confirmAndExecute()` — единую вторую половину pipeline: показ плана → confirm dialog (с опциональной ревизией) → execute → review. Это устраняет дублирование confirm + execute логики.

При `agent.max_parallel_tasks > 1` Orchestrator превращает план в JSON DAG (через Structurer) и выполняет независимые шаги через `runPlanDAG`: bounded семафор, per-file `fileLockSet`, чистые sub-agent'ы на каждый Step с собственным whitelist и RAG bundle. Каждый шаг оборачивается в TUI-группу `Executor(step.Title)`. Перед запуском каждого шага вызывается `StepConfirmFunc` (если настроен) — пользователь может выполнить, пропустить или отменить все оставшиеся шаги. На сбое structurer'а (невалидный JSON и т.п.) — graceful fallback на sequential single-agent путь.

### Plan→Execute контракт и кеш (Blocks 1, 2, 5)

- **ResultCache** (`internal/tools/cache.go`) — общий per-run кеш read-only tool результатов. Tools опционально реализуют `Cacheable` (CachePolicy) и `PathResolver`. Mtime-инвалидация на чтении, prefix-инвалидация на любой write через `Invalidators`. Метрики `Hits/Misses/HitRate` транслируются в TUI через `EventCacheStats`.
- **Whitelist + REPLAN** (`internal/agent/agent.go: SetAllowedReadPaths/guardReadWhitelist`) — executor (и каждый step sub-agent) залочен на список файлов из плана; попытка `read_file/list_dir/find_files/grep` вне whitelist возвращается с refusal-сообщением, инструктирующим вывести `REPLAN: <reason>` и остановиться.
- **Planner/Extractor split** (`config.ExtractorProvider()`, `Agent.SetTemperature/SetFormat`, `ChatRequest.Format`) — extractor и structurer всегда работают с `temperature=0` + `format=json` поверх той же модели; отдельный провайдер опционален через `agent.extractor_provider`.

### MCP Client (`internal/mcp/`)
JSON-RPC 2.0 поверх stdio. StdioTransport запускает subprocess, MCPClient делает Initialize → ListTools → CallTool. ToolAdapter реализует tools.Tool для прозрачной интеграции в Registry. Manager оркестрирует lifecycle. 60s timeout на Receive. Конфигурируется через `mcp:` секцию.

### WorkerPool (`internal/agent/worker_pool.go`)
Параллельное выполнение read-only tool calls с bounded concurrency (семафора). Panic recovery в goroutines. Результаты в порядке исходных индексов. Настройка: `max_tool_workers` в конфиге (default 4).

### RAG (`internal/rag/`)
**Scope:** индексируются ТОЛЬКО проектные конвенции — `.kodrun/rules/`, `.kodrun/snippets/`, `.kodrun/docs/` и встроенные language standards (напр. Effective Go). Исходный код проекта **не индексируется** намеренно: чанки кода устаревают между реиндексациями и reviewer начинает цитировать несуществующий код. Актуальное состояние файлов читается live через `read_file`.

Chunker разбивает rule/snippet/doc-файлы на куски с перекрытием (context-aware, прерывается при отмене контекста). Index генерирует embeddings через Ollama `/api/embed` батчами с проверкой ctx.Done(), хранит в JSON, ищет по cosine similarity. Tool `search_docs` доступен LLM.

### Ollama клиент (`internal/ollama/client.go`)
HTTP-клиент со streaming (NDJSON), retry с exponential backoff (429/503). `ChatSync` агрегирует стрим и вызывает `ParseToolCalls` для fallback-парсинга (JSON + XML форматы, auto-generated fallback IDs).

### Tool Registry (`internal/tools/registry.go`)
`Tool` interface: `Name()`, `Description()`, `Schema()`, `Execute()`. Registry с `sync.RWMutex` для thread-safe доступа. Генерирует `ToolDef[]` для Ollama API.

### Безопасность (`internal/tools/pathutil.go`)
SafePath: resolve relative → Clean → Rel check → EvalSymlinks (symlink traversal protection). IsForbidden: glob matching против `forbidden_patterns`.

### TUI (`internal/tui/model.go`)
Bubbletea: viewport для логов, textarea для ввода (auto-resize, Ctrl+J для новой строки), toolbar со статистикой. Режимы plan/edit (Shift+Tab). Три уровня подтверждения: tool confirm (1-allow once/2-allow session/3-edit/4-deny), plan confirm (auto-accept/manual approve/augment), step confirm (execute/skip/cancel all). Локализация через `i18n.go` (en/ru). Цветной diff, summary со статистикой. Task goroutine обёрнута в panic recovery. Названия tools отображаются **жирным** в **PascalCase** (`toolDisplayName` + `toolBoldStyle`).

### Graceful Shutdown (`cmd/kodrun/main.go`)
Обработка SIGINT + SIGTERM через `signal.NotifyContext`. Состояние терминала сохраняется перед запуском bubbletea (`term.GetState`) и восстанавливается при любом выходе. Top-level panic recovery в `main()` с ручным сбросом terminal escape sequences (mouse reporting, alt screen). Все event-отправки через non-blocking `emit()` (select + ctx.Done). Фоновые горутины запускаются через `safeGo()` с panic recovery и WaitGroup. Shutdown timeout 3 секунды. MCP Close идемпотентен (sync.Once).

## Конфигурация

Приоритет: дефолты → `~/.config/kodrun/config.yaml` → `.kodrun.yaml` → env → CLI-флаги.

Env vars: `KODRUN_MODEL`, `KODRUN_OLLAMA_URL`, `KODRUN_WORK_DIR`, `KODRUN_NO_TUI`.

### Per-role provider wiring

`temperature`, `format` и `timeout` живут на уровне `ProviderConfig`, не `AgentConfig`. Чтобы дать роли свою температуру или увеличить таймаут — заведи отдельный профиль в `providers:` и сошлись на него из соответствующего `*_provider` ключа. По умолчанию `timeout: 5m`; для fan-out code-review с 6 параллельными специалистами может потребоваться `10m`.

| Роль (sub-agent) | Ключ конфига | Fallback | Что делает |
|---|---|---|---|
| `planner`, `reviewer` | `agent.thinking_provider` | `agent.provider` | Анализ кода, ревью изменений; обычно средняя температура (0.5–0.7) |
| `extractor`, `structurer` | `agent.extractor_provider` | `agent.provider` (с overlay `format=json` + `temperature=0`) | Markdown→JSON Plan{Steps[]} |
| `executor`, `step_executor` | `agent.executor_provider` | `agent.provider` | Применяют шаги под whitelist'ом; рекомендуется низкая температура (0.2–0.3) |
| `response_classifier` | `agent.provider` | — | Классификация ответов в TUI |

Полный рабочий пример — `examples/kodrun.yaml` (3 активных профиля провайдеров с разной температурой).

**Tip для слабых локальных моделей в EDIT-режиме.** Если модель на задаче «применить план» отвечает markdown-описанием вместо вызова `write_file`/`edit_file`, переключи `executor_provider` на профиль с низкой температурой (`precise: temperature: 0.2`). На high-temperature (0.7+) локальные code-модели систематически склоняются к нарративу. Рантайм всё равно отловит такой ответ через nudge-механизм (`internal/agent/agent.go:715`, до 2 follow-up запросов с просьбой вызвать инструменты), но дешевле сразу понизить температуру.

```yaml
agent:
  executor_provider: precise   # вместо default
```

### Concurrency

Два независимых уровня параллелизма:

| Параметр | Уровень | Default | Что регулирует |
|---|---|---|---|
| `agent.max_tool_workers` | внутри одного sub-agent'а | 4 | Сколько read-only tool calls выполняются параллельно за один chat turn |
| `agent.max_parallel_tasks` | между sub-agent'ами | 1 | Сколько шагов плана выполняются параллельно в DAG-режиме (>1 включает DAG executor) |
| `agent.max_replans` | per Run | 2 | Лимит REPLAN-циклов от executor'а до отказа |
| `agent.specialist_timeout` | per specialist | 5m | Wall-time лимит для одного ревьюера |

### Прочие ключевые настройки

- `agent.language` — язык ответов (en/ru/de/fr/es/zh/ja)
- `agent.orchestrator` — включить Plan→Execute→Review pipeline
- `agent.review` — включить фазу Review
- `agent.auto_compact` — авто-compact при 99% контекста
- `agent.prefetch_code` — пре-загрузить .go файлы в executor prompt
- `agent.default_mode` — стартовый режим (`plan` / `edit`)
- `agent.think` — стримить «thinking» блоки моделей, которые их поддерживают
- `mcp:` — конфигурация MCP серверов
- `rag:` — конфигурация RAG: `provider`, `enabled`, `chunk_size`/`chunk_overlap` (дефолт 128/16), `top_k`, `review_budget_bytes` (cap RAG-инъекции в `/code-review`, дефолт 24 KiB), `index_path`. Поля `index_dirs`, `exclude_dirs`, `max_chunks_per_file` **deprecated** и игнорируются — kodrun больше не индексирует исходный код проекта, только `.kodrun/rules/`, `.kodrun/snippets/`, `.kodrun/docs/` и встроенные language standards.

**ВАЖНО:** при изменении полей `config.Config`, `AgentConfig`, `ProviderConfig` или дефолтов — обязательно синхронизируй `examples/kodrun.yaml` и `examples/global-config.yaml`. Эти файлы — единственная пользовательская документация по конфигу, рассинхрон ломает onboarding.

## Команды

```bash
make build          # Собрать бинарник в .build/
make run            # Запустить в TUI режиме
make run-plain      # Запустить без TUI (plain stdout)
make task TASK="…"  # Одноразовая задача
make test           # Юнит-тесты с race detector и coverage
make lint           # golangci-lint
make install        # Установить в $GOPATH/bin
make init           # Создать .kodrun/ с примерами
make help           # Список целей
```

## CLI

```bash
kodrun                          # Интерактивный TUI
kodrun --no-tui                 # Plain stdout
kodrun -- "задача"              # Одноразовая задача
kodrun build                    # go build + авто-fix
kodrun test [pkg]               # go test + авто-fix
kodrun lint                     # golangci-lint + авто-fix
kodrun fix <file>               # Исправить файл через LLM
kodrun init                     # Создать .kodrun/
```

Флаги: `--model`, `--work-dir`, `--no-tui`, `--no-fix`, `--config`, `--verbose`.

TUI команды: `/compact`, `/edit`, `/clear`, `/reindex`, `/rag`, `/orchestrate`, `/init`, `/exit`.

## Безопасность

- Пути резолвятся относительно `work_dir`, абсолютные запрещены
- Path traversal блокируется (`../` за пределы work_dir)
- Symlink traversal: EvalSymlinks + re-check
- `forbidden_patterns`: `*.env`, `*.pem`, `*.key`, `.git/**`, `.build/**`, `.idea/**`, бинарники
- Plan mode: только read-only tools, write operations блокируются
- MCP tools: confirm по умолчанию, `auto_approve` через конфиг
- Registry: thread-safe (sync.RWMutex)
- PermissionManager: thread-safe (sync.RWMutex)
- Panic recovery: все goroutines защищены (safeGo, task goroutine, main)
- Terminal restore: при panic/signal терминал восстанавливается через ANSI escape + term.Restore

## Тестирование

```bash
go test -race ./...              # Все тесты с race detector
go test ./internal/agent/...     # Agent + WorkerPool + Orchestrator + Permission
go test ./internal/mcp/...       # MCP client + transport + adapter
go test ./internal/tui/...       # TUI visual tests (renderInput width/background)
go test ./internal/tools/...     # Tool tests (SafePath, edit, read, etc.)
```

## Важные правила для golang (ОБЯЗАТЕЛЬНЫЕ)

- Все публичные функции должны принимать `context.Context` первым аргументом. Если внешний пакет не использует context — передаём `_ context.Context`
- Все ошибки должны обрабатываться. Запрещено `_ = err` кроме defer cleanup
- Нельзя возвращать nil без ошибки (для non-slice типов)
- Для ошибок используется пакет `github.com/pkg/errors` (НЕ `fmt.Errorf`, НЕ stdlib `errors`)
- Все ошибки оборачиваются: `errors.Wrap(err, "context")`, `errors.WithMessage(err, "context")`, `errors.Errorf("msg %s", val)`
- Кастомные ошибки при возврате оборачиваются: `errors.WithStack(ErrCustom)`
- Для проверки ошибок: `errors.Is(err, ErrTarget)`, `errors.As(err, &target)`
- Для логирования используем `log/slog`. Во все ошибки добавляем контекстную информацию
- Нельзя допускать panic. Все goroutines должны иметь panic recovery
- See [effective_go.md](effective_go.md) for full go documentation.
