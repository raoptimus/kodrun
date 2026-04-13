---
description: "Template for table-driven test — struct slice data provider, subtest loop, testify assertions"
tags: [test, table-driven, unit-test]
paths: ["**/*_test.go"]
lang: go
related: [go-service, go-repository, go-validator]
---

## Success path

```go
func Test<Func>_Successfully(t *testing.T) {
    type args struct {
        input *pkg.Input
    }
    tests := []struct {
        name string
        args args
        want pkg.Output
    }{
        {
            name: "descriptive case name",
            args: args{input: &pkg.Input{ID: id}},
            want: pkg.Output{ID: id},
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()

            got, err := Func(tt.args.input)
            require.NoError(t, err)
            require.Equal(t, tt.want, got)
        })
    }
}
```

| `t.Parallel()` | When |
|----------------|------|
| Include | Default — independent test cases |
| Omit | Shared mutable state or stateful test helpers |

## Error path — separate function

```go
func Test<Func>_Failure(t *testing.T) {
    tests := []struct {
        name string
        args args
    }{
        {
            name: "invalid input",
            args: args{input: &pkg.Input{Type: "invalid"}},
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()

            _, err := Func(tt.args.input)
            require.Error(t, err)
        })
    }
}
```
