---
priority: normal
scope: coding
---

# Testing Rules

## Principles

- Table-driven tests (TDT) — the primary format
- External dependencies are always mocked
- Expected values are hardcoded, never computed dynamically
- Test only local logic, not the behavior of dependencies
- Minimize dependency on implementation details
- Do not assert mock call counts unless it is part of the contract

## Equivalence Classes

All conditions leading to the same result form one class. It is enough to check one value from each class.

## Boundary Conditions

Check values at the boundaries of equivalence classes:
- Minimum valid value
- Maximum valid value
- First invalid value beyond the boundary

## Test Structure

```go
func TestSomething(t *testing.T) {
    tests := []struct {
        name    string
        input   InputType
        want    OutputType
        wantErr error
    }{
        // happy path
        // edge cases
        // error cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // arrange, act, assert
        })
    }
}
```
