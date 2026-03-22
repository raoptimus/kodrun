# GoAgent

CLI-агент для написания и сопровождения Go-кода. Работает полностью локально через [Ollama](https://ollama.com) API. Читает правила и документацию из рабочей директории проекта, выполняет инструменты (файловые операции, build/test/lint), автоматически исправляет ошибки через LLM.

## Возможности

- Чат с LLM, который имеет доступ к файлам проекта и Go-инструментам
- Автоматическое исправление ошибок компиляции, тестов и линтера
- Система правил: стиль кода, архитектура, пользовательские команды
- Полноэкранный TUI (bubbletea) и plain stdout режим для pipe/скриптов
- Path traversal защита и forbidden patterns для безопасности
- Управление контекстом с суммаризацией при переполнении окна

## Быстрый старт

### Требования

- Go 1.23+
- [Ollama](https://ollama.com) с загруженной моделью

### Установка

```bash
# Из исходников
git clone https://github.com/raoptimus/go-agent.git
cd go-agent
make install

# Или собрать локально
make build
```

### Запуск

```bash
# Убедитесь, что Ollama запущена
ollama serve

# Загрузите модель (если ещё не загружена)
ollama pull qwen3-coder:30b

# Интерактивный режим (TUI)
goagent

# Или через make
make run
```

## Использование

### Интерактивный режим

```bash
# TUI с полноэкранным интерфейсом
goagent

# Plain stdout (без TUI)
goagent --no-tui
```

### Одноразовая задача

```bash
# Из командной строки
goagent -- "написать unit тест для функции ParseConfig"

# Через make
make task TASK="добавить godoc к публичным функциям в auth.go"

# Через pipe
echo "написать тесты для auth.go" | goagent --no-tui
```

### Subcommands с авто-исправлением

```bash
# Собрать проект, при ошибках — LLM исправит и повторит
goagent build

# Запустить тесты с авто-исправлением
goagent test ./internal/config/...

# Линтер с авто-исправлением
goagent lint

# Исправить конкретный файл
goagent fix internal/agent/agent.go
```

### Инициализация правил

```bash
# Создать .goagent/ со стартовой структурой
goagent init
```

### Флаги

| Флаг | Описание | По умолчанию |
|------|----------|--------------|
| `--model` | Модель Ollama | из конфига |
| `--work-dir` | Рабочая директория | `.` |
| `--no-tui` | Режим plain stdout | `false` |
| `--no-fix` | Отключить авто-исправление | `false` |
| `--config` | Путь к конфигу | автоопределение |
| `--verbose` | Подробный вывод | `false` |

### Переменные окружения

| Переменная | Описание |
|------------|----------|
| `GOAGENT_MODEL` | Модель Ollama (приоритет над конфигом) |
| `GOAGENT_OLLAMA_URL` | URL Ollama API |
| `GOAGENT_WORK_DIR` | Рабочая директория |
| `GOAGENT_NO_TUI` | `1` или `true` — отключить TUI |

## Конфигурация

Конфиг ищется в следующем порядке (каждый следующий переопределяет предыдущий):

1. Встроенные дефолты
2. `~/.config/goagent/config.yaml` — глобальный конфиг
3. `.goagent.yaml` в корне проекта — проектный конфиг
4. Переменные окружения
5. Флаги командной строки

### Пример `.goagent.yaml`

```yaml
ollama:
  base_url: "http://localhost:11434"
  model: "qwen3-coder:30b"
  timeout: 300s
  context_size: 32768

agent:
  max_iterations: 50
  auto_fix: true
  auto_commit: false

tools:
  allowed_dirs:
    - "."
  forbidden_patterns:
    - "*.env"
    - ".git/**"
    - "*.pem"
    - "*.key"

rules:
  dirs:
    - ".goagent/rules"
    - ".goagent/docs"
    - ".goagent/commands"
```

### Пример глобального конфига `~/.config/goagent/config.yaml`

```yaml
ollama:
  base_url: "http://localhost:11434"
  model: "qwen3-coder:30b"
  timeout: 600s

agent:
  max_iterations: 100
```

## Система правил

GoAgent загружает `.md` файлы из директорий, указанных в `rules.dirs`, и включает их в системный промпт агента. Это позволяет настроить поведение агента под конкретный проект.

### Структура `.goagent/`

```
.goagent/
  rules/          — правила стиля и архитектуры
  docs/           — документация проекта
  commands/       — пользовательские команды
```

Создаётся командой `goagent init`.

### Формат файлов правил

Обычный Markdown с опциональным front matter:

```markdown
---
priority: high       # high | normal | low — порядок включения в промпт
scope: coding        # coding | review | fix | all
---

# Правила стиля

- Использовать `errors.Is` вместо прямого сравнения
- Все публичные функции должны иметь godoc
```

### Примеры правил

#### `.goagent/rules/style.md` — стиль кода

```markdown
---
priority: high
scope: coding
---

# Go Style Rules

- Использовать `errors.Is`/`errors.As` вместо прямого сравнения ошибок
- Все публичные функции должны иметь godoc комментарии
- Обёртка ошибок через `fmt.Errorf("operation: %w", err)`
- Sentinel-ошибки определяются как `var` на уровне пакета
- Никогда не вызывать `errors.New()` в рантайме (определять статические переменные)
```

#### `.goagent/rules/architecture.md` — архитектурные правила

```markdown
---
priority: high
scope: all
---

# Архитектура проекта

Clean Architecture с направлением зависимостей: transport → domain ← dal ← client.
Domain ничего не знает о транспорте и инфраструктуре.

Слои:
- domain/model/ — доменные модели
- domain/service/ — бизнес-логика одной сущности
- domain/usecase/ — оркестрация 2+ сервисов
- dal/entity/ — структуры БД
- dal/repository/ — SQL-запросы
- server/grpc/ — gRPC handlers
- client/ — обёртки над внешними SDK
```

#### `.goagent/rules/testing.md` — правила тестирования

```markdown
---
priority: normal
scope: coding
---

# Тестирование

- Используй table-driven tests (TDT)
- Внешние зависимости всегда мокаются
- Ожидаемые значения хардкодятся, не вычисляются
- Тестируем только локальную логику, не поведение зависимостей
- Проверяй граничные условия на стыках классов эквивалентности
- Минимальная зависимость от деталей реализации
```

#### `.goagent/rules/service.md` — правила для доменных сервисов

```markdown
---
priority: normal
scope: coding
---

# Доменные сервисы

- Каждый сервис работает только с одной сущностью
- Зависимости — интерфейсы, определённые рядом с потребителем
- Интерфейсы содержат только используемые методы
- Конвертация model ↔ entity всегда через пакет convert/
- Методы создания и обновления не возвращают значение — только error
- Операции с несколькими сущностями → usecase, не service
```

### Пользовательские команды

Файлы в `.goagent/commands/` определяют команды, вызываемые через `/command` в чате:

#### `.goagent/commands/review.md`

```markdown
---
command: /review
description: "Провести code review файла"
---

Проведи детальный code review файла {{file}}.
Проверь:
- Баги и логические ошибки
- Обработку ошибок
- Нарушения стиля кода
- Отсутствие тестов
- Проблемы производительности

Дай конкретные, actionable замечания.
```

#### `.goagent/commands/refactor.md`

```markdown
---
command: /refactor
description: "Рефакторинг по описанию"
---

Выполни рефакторинг: {{description}}

Требования:
- Сохранить существующее поведение
- Запустить go_build и go_test после изменений
- Минимальные изменения
```

#### `.goagent/commands/test.md`

```markdown
---
command: /test
description: "Написать тесты для файла"
---

Напиши unit-тесты для файла {{file}}.

Правила:
- Table-driven tests
- Покрой классы эквивалентности и граничные условия
- Мокай внешние зависимости
- Хардкодь ожидаемые значения
- Запусти go_test после написания
```

## Доступные инструменты

Агент имеет доступ к следующим инструментам:

### Файловые операции

| Инструмент | Описание |
|------------|----------|
| `read_file` | Прочитать содержимое файла |
| `write_file` | Записать файл (создаёт директории) |
| `edit_file` | Заменить текст в файле (find & replace) |
| `list_dir` | Список файлов в директории |
| `find_files` | Поиск файлов по glob-паттерну |
| `grep` | Поиск по regex в файлах |
| `delete_file` | Удалить файл |
| `create_dir` | Создать директорию |
| `move_file` | Переместить/переименовать файл |
| `bash` | Выполнить shell-команду |

### Go-инструменты

| Инструмент | Описание |
|------------|----------|
| `go_build` | `go build` с парсингом ошибок |
| `go_test` | `go test` с парсингом ошибок |
| `go_lint` | `golangci-lint run` |
| `go_fmt` | `gofmt -w` |
| `go_vet` | `go vet` |
| `go_mod_tidy` | `go mod tidy` |

### Безопасность

- Все пути резолвятся относительно рабочей директории
- Выход за пределы work_dir запрещён (path traversal защита)
- `forbidden_patterns` из конфига блокируют доступ к чувствительным файлам

## Архитектура проекта

```
cmd/goagent/main.go           — CLI (cobra), точка входа
internal/
  agent/
    agent.go                  — агентный цикл (LLM → tools → result)
    context.go                — управление контекстом (суммаризация)
    output.go                 — plain stdout вывод
  config/
    config.go                 — конфигурация (viper, YAML, env)
  ollama/
    client.go                 — HTTP-клиент Ollama API (streaming, retry)
    types.go                  — типы запросов/ответов
    parser.go                 — парсинг tool calls из текста (JSON + XML)
  tools/
    registry.go               — реестр инструментов
    pathutil.go                — path traversal защита
    file_read.go               — read_file
    file_write.go              — write_file
    file_edit.go               — edit_file
    file_list.go               — list_dir
    file_find.go               — find_files
    file_grep.go               — grep
    file_ops.go                — delete_file, create_dir, move_file
    go_tools.go                — go_build, go_test, go_lint, go_fmt, go_vet, bash
  rules/
    loader.go                  — загрузка правил из .md файлов
  runner/
    parser.go                  — парсинг ошибок Go (build/test/lint)
    fixer.go                   — авто-исправление через LLM
  tui/
    model.go                   — bubbletea TUI (viewport, textinput)
  goagentinit/
    init.go                    — goagent init (создание .goagent/)
```

## Разработка

```bash
make build          # Собрать бинарник
make test           # Юнит-тесты
make lint           # Линтер
make clean          # Очистить артефакты
make help           # Список целей
```

## Лицензия

MIT
