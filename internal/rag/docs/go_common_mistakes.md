# Go Code Review: Common False Positives and Anti-Patterns

This document describes patterns that are frequently flagged during code review but are actually correct idiomatic Go. A reviewer should NOT flag these as issues.

## Redundant nil checks after error handling

In Go, functions that return `(T, error)` follow a strict contract: if `err == nil`, the value `T` is valid and usable. Adding a nil check for `T` after handling the error is redundant and non-idiomatic.

**Correct — no nil check needed:**

```go
app, err := NewApplication(opts)
if err != nil {
    return err
}
// app is guaranteed to be valid here — do NOT add `if app == nil`
app.Run()
```

**Wrong — redundant nil check:**

```go
app, err := NewApplication(opts)
if err != nil {
    return err
}
if app == nil {  // WRONG: this is redundant, the error check already guarantees validity
    return errors.New("app is nil")
}
```

The only exception is when a function explicitly documents that it may return `(nil, nil)` — which is extremely rare and itself considered a design smell in Go.

## Redundant zero-value checks after error handling

The same principle applies to all zero values, not just nil:

```go
count, err := ParseCount(input)
if err != nil {
    return err
}
// count is valid here — do NOT add `if count == 0` unless 0 is a domain error
```

## Unnecessary else after return

The `else` clause is unnecessary when the `if` body ends with a `return`, `continue`, `break`, or `goto`:

**Correct:**

```go
if err != nil {
    return err
}
// normal path
```

**Wrong:**

```go
if err != nil {
    return err
} else {
    // normal path — unnecessary else
}
```

## Suggesting error wrapping when it adds no context

Not every error needs wrapping. Wrapping is useful when it adds context that helps debugging. Wrapping with the same function name that the error came from is noise:

**Appropriate wrapping — adds context:**

```go
cfg, err := loadConfig(path)
if err != nil {
    return fmt.Errorf("initializing server: %w", err)
}
```

**Inappropriate wrapping — adds no useful context:**

```go
func loadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("loadConfig: %w", err)  // function name is already in stack trace
    }
}
```

## Suggesting named return values for clarity

Named return values should be used sparingly. Do not suggest adding named returns to short functions or functions where the return types are obvious:

```go
// Clear enough — don't suggest named returns
func (s *Service) Get(ctx context.Context, id int64) (*User, error) {
```

Named returns are appropriate when:
- Multiple return values have the same type
- The meaning of return values is ambiguous without names
- A deferred function needs to modify the return value

## Suggesting context.TODO() replacement

`context.TODO()` is intentionally part of the Go standard library. It signals "I know this needs a proper context but don't have one yet." Do not flag it as a bug — it is a valid placeholder in evolving codebases.

## Defensive copies of slices/maps in internal code

Adding defensive copies of slices or maps passed between internal packages (not at API boundaries) is usually unnecessary overhead. Trust internal code. Only suggest defensive copies at public API boundaries or when there is a documented concurrency concern.

## Suggesting sync.Mutex when there is no concurrent access

Do not suggest adding mutexes to types that are not accessed concurrently. Adding unnecessary synchronization adds complexity and can hurt performance. Only flag missing synchronization when concurrent access is evident from the code.

## Suggesting interface extraction for single implementations

Do not suggest extracting interfaces when there is only one implementation and no clear need for polymorphism. In Go, interfaces should be defined by the consumer, not the producer. Premature interface extraction adds unnecessary abstraction.

## Suggesting error variable naming changes

In Go, `err` is the conventional name for error variables. Do not suggest renaming `err` to more descriptive names like `readErr`, `parseErr` unless there are multiple errors in the same scope that need disambiguation.

## Suggesting getter prefixes

In Go, getter methods do not use the `Get` prefix. A field called `owner` has a getter called `Owner()`, not `GetOwner()`. Do not suggest adding `Get` prefixes to getter methods.

## Flagging single-method interfaces as too small

Single-method interfaces are idiomatic in Go (`io.Reader`, `io.Writer`, `io.Closer`, `fmt.Stringer`). Do not flag them as "too small" or suggest combining them. Small interfaces are a strength of Go's design.
