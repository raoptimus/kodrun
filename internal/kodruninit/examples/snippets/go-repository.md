---
description: "Template for DAL repository — struct with connection, constructor, transaction wrapper"
tags: [dal, repository, database]
paths: ["**/repository/**", "**/dal/**"]
placeholders:
  Entity: "PascalCase type name (e.g. UserProfile)"
lang: go
related: [go-service, go-model, go-converter]
---

## Repository struct + constructor

```go
package repository

type <Entity>Connection interface {
    Select(ctx context.Context, ptr any, sql string, args ...any) error
    Exec(ctx context.Context, sql string, args ...any) error
}

type <Entity> struct {
    conn <Entity>Connection
}

func New<Entity>(conn <Entity>Connection) *<Entity> {
    return &<Entity>{conn: conn}
}
```

## Select one

```go
func (r *<Entity>) <Entity>(ctx context.Context, id uuid.UUID) (entity.<Entity>, error) {
    var e entity.<Entity>

    if err := r.conn.Select(ctx, &e,
        `SELECT id, name, created_at FROM <table> WHERE id = $1`,
        id,
    ); err != nil {
        return entity.<Entity>{}, errors.Wrap(err, "select <entity>")
    }

    return e, nil
}
```

## Insert

```go
func (r *<Entity>) Create(ctx context.Context, e *entity.Create<Entity>) error {
    if err := r.conn.Exec(ctx,
        `INSERT INTO <table> (id, name, created_at) VALUES ($1, $2, $3)`,
        e.ID, e.Name, e.CreatedAt,
    ); err != nil {
        return errors.Wrap(err, "insert <entity>")
    }

    return nil
}
```

## Transaction

```go
func (r *<Entity>) Transaction(ctx context.Context, txFn func(ctx context.Context) error) error {
    if err := r.conn.Transaction(ctx, txFn); err != nil {
        return errors.Wrap(err, "execute <entity> transaction")
    }

    return nil
}
```
