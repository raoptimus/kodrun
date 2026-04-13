---
description: "Template for domain service — struct, constructor, and CRUD method signatures"
tags: [domain, service, crud]
paths: ["**/domain/service/**", "**/service/**"]
lang: go
related: [go-repository, go-model, go-table-test]
---

## Struct + constructor

```go
type <Entity>Repository interface {
    <Entity>(ctx context.Context, id uuid.UUID) (entity.<Entity>, error)
    Create(ctx context.Context, e *entity.Create<Entity>) error
    Update(ctx context.Context, e *entity.Update<Entity>) error
}

type <Entity> struct {
    repo <Entity>Repository
}

func New<Entity>(repo <Entity>Repository) *<Entity> {
    return &<Entity>{repo: repo}
}
```

## Get one

```go
func (s *<Entity>) <Entity>(ctx context.Context, id uuid.UUID) (model.<Entity>, error) {
    e, err := s.repo.<Entity>(ctx, id)
    if err != nil {
        if errors.Is(err, entity.ErrNotFound) {
            err = errors.WithStack(model.Err<Entity>NotFound)
        }

        return model.<Entity>{}, err
    }

    return convert.Model<Entity>FromEntity(&e)
}
```

## Create

```go
func (s *<Entity>) Create(ctx context.Context, create *model.Create<Entity>) error {
    entityCreate := convert.EntityCreate<Entity>FromModel(create)

    if err := s.repo.Create(ctx, &entityCreate); err != nil {
        if errors.Is(err, entity.ErrAlreadyExists) {
            err = errors.WithStack(model.Err<Entity>AlreadyExists)
        }

        return err
    }

    return nil
}
```

## Service test

```go
func Test<Entity>_Create_Successfully(t *testing.T) {
    ctx := t.Context()
    mockRepo := NewMock<Entity>Repository(t)

    mockRepo.EXPECT().
        Create(ctx, mock.AnythingOfType("*entity.Create<Entity>")).
        Return(nil)

    svc := New<Entity>(mockRepo)
    err := svc.Create(ctx, &model.Create<Entity>{Name: "test"})

    require.NoError(t, err)
}
```
