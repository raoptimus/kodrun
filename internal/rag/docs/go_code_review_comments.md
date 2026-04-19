# Go Code Review Comments

This page collects common comments made during reviews of Go code, so that a single detailed explanation can be referred to by shorthands. This is a laundry list of common style issues, not a comprehensive style guide.

You can view this as a supplement to Effective Go.

## Gofmt

Run gofmt on your code to automatically fix the majority of mechanical style issues. Almost all Go code in the wild uses `gofmt`. The rest of this document addresses non-mechanical style points.

An alternative is to use goimports, a superset of `gofmt` which additionally adds (and removes) import lines as necessary.

## Comment Sentences

Comments documenting declarations should be full sentences, even if that seems a little redundant. This approach makes them format well when extracted into godoc documentation. Comments should begin with the name of the thing being described and end in a period:

```go
// Request represents a request to run a command.
type Request struct { ...

// Encode writes the JSON encoding of req to w.
func Encode(w io.Writer, req *Request) { ...
```

## Contexts

Values of the context.Context type carry security credentials, tracing information, deadlines, and cancellation signals across API and process boundaries. Go programs pass Contexts explicitly along the entire function call chain from incoming RPCs and HTTP requests to outgoing requests.

Most functions that use a Context should accept it as their first parameter:

```go
func F(ctx context.Context, /* other arguments */) {}
```

Don't add a Context member to a struct type; instead add a ctx parameter to each method on that type that needs to pass it along.

Don't create custom Context types or use interfaces other than Context in function signatures.

## Copying

To avoid unexpected aliasing, be careful when copying a struct from another package. In general, do not copy a value of type `T` if its methods are associated with the pointer type, `*T`.

## Crypto Rand

Do not use package `math/rand` to generate keys, even throwaway ones. Use `crypto/rand.Reader` instead. If you need text, use `crypto/rand.Text`.

## Declaring Empty Slices

When declaring an empty slice, prefer:

```go
var t []string
```

over:

```go
t := []string{}
```

The former declares a nil slice value, while the latter is non-nil but zero-length. They are functionally equivalent—their `len` and `cap` are both zero—but the nil slice is the preferred style.

Note that there are limited circumstances where a non-nil but zero-length slice is preferred, such as when encoding JSON objects (a `nil` slice encodes to `null`, while `[]string{}` encodes to the JSON array `[]`).

## Doc Comments

All top-level, exported names should have doc comments, as should non-trivial unexported type or function declarations.

## Don't Panic

Don't use panic for normal error handling. Use error and multiple return values.

## Error Strings

Error strings should not be capitalized (unless beginning with proper nouns or acronyms) or end with punctuation, since they are usually printed following other context. That is, use `fmt.Errorf("something bad")` not `fmt.Errorf("Something bad")`, so that `log.Printf("Reading %s: %v", filename, err)` formats without a spurious capital letter mid-message.

## Examples

When adding a new package, include examples of intended usage: a runnable Example, or a simple test demonstrating a complete call sequence.

## Goroutine Lifetimes

When you spawn goroutines, make it clear when — or whether — they exit.

Goroutines can leak by blocking on channel sends or receives: the garbage collector will not terminate a goroutine even if the channels it is blocked on are unreachable.

Even when goroutines do not leak, leaving them in-flight when they are no longer needed can cause other subtle and hard-to-diagnose problems.

Try to keep concurrent code simple enough that goroutine lifetimes are obvious. If that just isn't feasible, document when and why the goroutines exit.

## Handle Errors

Do not discard errors using `_` variables. If a function returns an error, check it to make sure the function succeeded. Handle the error, return it, or, in truly exceptional situations, panic.

## Imports

Avoid renaming imports except to avoid a name collision. Imports are organized in groups, with blank lines between them. The standard library packages are always in the first group.

```go
package main

import (
    "fmt"
    "hash/adler32"
    "os"

    "github.com/foo/bar"
    "rsc.io/goversion/version"
)
```

## Import Blank

Packages that are imported only for their side effects (using the syntax `import _ "pkg"`) should only be imported in the main package of a program, or in tests that require them.

## In-Band Errors

Go's support for multiple return values provides a better solution than in-band error values. A function should return an additional value to indicate whether its other return values are valid. This return value may be an error, or a boolean when no explanation is needed.

```go
// Lookup returns the value for key or ok=false if there is no mapping for key.
func Lookup(key string) (value string, ok bool)
```

Return values like nil, "", 0, and -1 are fine when they are valid results for a function.

## Indent Error Flow

Try to keep the normal code path at a minimal indentation, and indent the error handling, dealing with it first:

```go
if err != nil {
    // error handling
    return // or continue, etc.
}
// normal code
```

Don't write:

```go
if err != nil {
    // error handling
} else {
    // normal code
}
```

## Initialisms

Words in names that are initialisms or acronyms (e.g. "URL" or "NATO") have a consistent case. For example, "URL" should appear as "URL" or "url", never as "Url". Write "appID" instead of "appId".

## Interfaces

Go interfaces generally belong in the package that uses values of the interface type, not the package that implements those values. The implementing package should return concrete (usually pointer or struct) types.

Do not define interfaces on the implementor side of an API "for mocking"; instead, design the API so that it can be tested using the public API of the real implementation.

Do not define interfaces before they are used: without a realistic example of usage, it is too difficult to see whether an interface is even necessary.

## Line Length

There is no rigid line length limit in Go code, but avoid uncomfortably long lines. Break lines because of the semantics of what you're writing, not because of the length of the line.

## Mixed Caps

Use MixedCaps or mixedCaps rather than underscores to write multiword names. An unexported constant is `maxLength` not `MaxLength` or `MAX_LENGTH`.

## Named Result Parameters

Consider what it will look like in godoc. Don't name result parameters just to avoid declaring a var inside the function.

Named result parameters are useful when:
- A function returns two or three parameters of the same type
- The meaning of a result isn't clear from context
- You need to change a result in a deferred closure

## Package Comments

Package comments must appear adjacent to the package clause, with no blank line.

```go
// Package math provides basic constants and mathematical functions.
package math
```

## Package Names

All references to names in your package will be done using the package name, so you can omit that name from the identifiers. Avoid meaningless package names like util, common, misc, api, types, and interfaces.

## Pass Values

Don't pass pointers as function arguments just to save a few bytes. If a function refers to its argument `x` only as `*x` throughout, then the argument shouldn't be a pointer. This advice does not apply to large structs.

## Receiver Names

The name of a method's receiver should be a reflection of its identity; often a one or two letter abbreviation of its type suffices (such as "c" or "cl" for "Client"). Don't use generic names such as "me", "this" or "self". Be consistent: if you call the receiver "c" in one method, don't call it "cl" in another.

## Receiver Type

Choosing whether to use a value or pointer receiver on methods:

- If the receiver is a map, func or chan, don't use a pointer to them.
- If the method needs to mutate the receiver, the receiver must be a pointer.
- If the receiver is a struct that contains a sync.Mutex or similar synchronizing field, the receiver must be a pointer.
- If the receiver is a large struct or array, a pointer receiver is more efficient.
- If the receiver is a small array or struct that is naturally a value type, with no mutable fields and no pointers, a value receiver makes sense.
- Don't mix receiver types. Choose either pointers or struct types for all available methods.
- When in doubt, use a pointer receiver.

## Synchronous Functions

Prefer synchronous functions over asynchronous ones. Synchronous functions keep goroutines localized within a call, making it easier to reason about their lifetimes and avoid leaks and data races.

If callers need more concurrency, they can add it easily by calling the function from a separate goroutine.

## Useful Test Failures

Tests should fail with helpful messages saying what was wrong, with what inputs, what was actually got, and what was expected:

```go
if got != tt.want {
    t.Errorf("Foo(%q) = %d; want %d", tt.in, got, tt.want)
}
```

## Variable Names

Variable names in Go should be short rather than long. This is especially true for local variables with limited scope. Prefer `c` to `lineCount`. Prefer `i` to `sliceIndex`.

The basic rule: the further from its declaration that a name is used, the more descriptive the name must be.
