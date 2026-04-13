.PHONY: build run test lint clean install init

BINARY    := kodrun
BUILD_DIR := .build
REPORT_DIR := .report
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -ldflags "-X main.version=$(VERSION)"

## build: Build binary into .build/
build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/kodrun

## run: Build and run in interactive mode (TUI)
run: build
	$(BUILD_DIR)/$(BINARY)

## run-plain: Build and run without TUI (plain stdout)
run-plain: build
	$(BUILD_DIR)/$(BINARY) --no-tui

## task: Build and run a one-shot task (TASK="task description")
task: build
	$(BUILD_DIR)/$(BINARY) --no-tui -- "$(TASK)"

## go-build: Run go build via agent with auto-fix
go-build: build
	$(BUILD_DIR)/$(BINARY) build

## go-test: Run go test via agent with auto-fix
go-test: build
	$(BUILD_DIR)/$(BINARY) test

## go-lint: Run golangci-lint via agent with auto-fix
go-lint: build
	$(BUILD_DIR)/$(BINARY) lint

## test: Unit tests with race detector & coverage
test-unit:
	@[ -d ${REPORT_DIR} ] || mkdir -p ${REPORT_DIR}
	go test -race -count=1 -coverprofile=${REPORT_DIR}/coverage.out ./...

test-integration: ## Run Integration Tests only
	@[ -d ${REPORT_DIR} ] || mkdir -p ${REPORT_DIR}
	@go test $$(go list ./... | grep -v mock) \
		-buildvcs=false \
		-run Integration \
		-tags=integration \
		-v

## lint: golangci-lint (config: .golangci.yml)
lint:
	golangci-lint run ./...

## install: Install binary into $GOPATH/bin
install:
	go install $(LDFLAGS) ./cmd/kodrun

## init: Create .kodrun/ with example rules/docs/commands
init: build
	$(BUILD_DIR)/$(BINARY) init

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR) $(REPORT_DIR)

## help: Show available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
