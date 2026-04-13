---
description: "Template for request validator — Options struct, validation rules, constructor, Validate method"
tags: [validator, validation]
paths: ["**/validate/**", "**/validator/**"]
lang: go
related: [go-grpc-handler, go-service]
---

## Validator template

```go
var Default<Operation>Options = <Operation>Options{
    MinPageSize: 1,
    MaxPageSize: 1000,
}

type <Operation>Options struct {
    MinPageSize int64
    MaxPageSize int64
}

type <Operation> struct {
    rules validator.RuleSet
}

func New<Operation>(opts *<Operation>Options) *<Operation> {
    return &<Operation>{
        rules: validator.RuleSet{
            "Name": {
                validator.NewRequired(),
                validator.NewStringLength(1, 255),
            },
            "PageSize": {
                validator.NewNumber(opts.MinPageSize, opts.MaxPageSize),
            },
        },
    }
}

func (v *<Operation>) Validate(ctx context.Context, req *request.<Operation>) error {
    return validator.Validate(ctx, req, v.rules)
}
```

## Sentinel errors

```go
// validate/errors.go
var ErrNotSetValue = errors.New("not set value")

// Usage: return errors.Wrap(ErrNotSetValue, "field name")
```
