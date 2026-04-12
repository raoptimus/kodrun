.PHONY: build run test lint clean install init

BINARY    := kodrun
BUILD_DIR := .build
REPORT_DIR := .report
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -ldflags "-X main.version=$(VERSION)"

## build: Собрать бинарник в .build/
build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/kodrun

## run: Собрать и запустить в интерактивном режиме (TUI)
run: build
	$(BUILD_DIR)/$(BINARY)

## run-plain: Собрать и запустить без TUI (plain stdout)
run-plain: build
	$(BUILD_DIR)/$(BINARY) --no-tui

## task: Собрать и выполнить одноразовую задачу (TASK="описание задачи")
task: build
	$(BUILD_DIR)/$(BINARY) --no-tui -- "$(TASK)"

## go-build: Запустить go build через агент с авто-исправлением
go-build: build
	$(BUILD_DIR)/$(BINARY) build

## go-test: Запустить go test через агент с авто-исправлением
go-test: build
	$(BUILD_DIR)/$(BINARY) test

## go-lint: Запустить golangci-lint через агент с авто-исправлением
go-lint: build
	$(BUILD_DIR)/$(BINARY) lint

## test: Unit tests with race detector & coverage
test-unit:
	go test -race -count=1 -coverprofile=${REPORT_DIR}/coverage.out ./...

test-integration: ## Run Integration Tests only
	@go test $$(go list ./... | grep -v mock) \
		-buildvcs=false \
		-run Integration \
		-tags=integration \
		-v

## lint: golangci-lint (конфиг: .golangci.yml)
lint:
	golangci-lint run ./...

## install: Установить бинарник в $GOPATH/bin
install:
	go install $(LDFLAGS) ./cmd/kodrun

## init: Создать .kodrun/ с примерами rules/docs/commands
init: build
	$(BUILD_DIR)/$(BINARY) init

## clean: Удалить артефакты сборки
clean:
	rm -rf $(BUILD_DIR) $(REPORT_DIR)

## help: Показать список целей
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
