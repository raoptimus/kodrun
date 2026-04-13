---
description: "Template for domain model — struct, create/search variants, typed enums, sentinel errors"
tags: [domain, model, struct, enum]
paths: ["**/domain/model/**", "**/model/**"]
lang: go
related: [go-service, go-converter, go-repository]
---

## Entity struct

```go
type <Entity> struct {
    ID        uuid.UUID
    Name      string
    Status    <Entity>Status
    CreatedAt time.Time
}

type Create<Entity> struct {
    Name string
}

type <Entity>Search struct {
    Query      string
    Pagination Pagination
}
```

## Enums

```go
type <Entity>Status string

const (
    <Entity>StatusActive  <Entity>Status = "active"
    <Entity>StatusBlocked <Entity>Status = "blocked"
)

func (s <Entity>Status) String() string { return string(s) }

var All<Entity>Statuses = []<Entity>Status{
    <Entity>StatusActive,
    <Entity>StatusBlocked,
}
```

## Sentinel errors

```go
package model

import "github.com/pkg/errors"

var (
    Err<Entity>NotFound      = errors.New("<entity> not found")
    Err<Entity>AlreadyExists = errors.New("<entity> already exists")
)
```
