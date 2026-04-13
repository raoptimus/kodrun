---
priority: high
scope: all
---

# Error Handling

- Always wrap errors with context: `fmt.Errorf("operation: %w", err)`
- Define sentinel errors as `var` at the package level
- Use `errors.Is`/`errors.As` to check errors
- Never call `errors.New()` at runtime
- Never ignore errors — handle them explicitly or document the reason for ignoring
- Domain errors go in `model/errors.go`, data errors go in `entity/errors.go`
