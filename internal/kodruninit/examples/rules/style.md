---
priority: high
scope: coding
---

# Go Style Rules

- Use `errors.Is`/`errors.As` instead of direct error comparison
- All public functions must have godoc comments
- Wrap errors via `fmt.Errorf("operation: %w", err)`
- Sentinel errors are defined as `var` at the package level
- Never call `errors.New()` at runtime
- Naming: CamelCase for exported, camelCase for unexported
- UUID: use `uuid.Must(uuid.NewV7())`
- Time: use testable wrappers instead of `time.Now()` directly
