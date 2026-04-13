---
description: "Template for converters — entity to domain model, model to entity, model to filter"
tags: [converter, dal, entity, model]
paths: ["**/convert/**", "**/converter/**"]
lang: go
related: [go-model, go-repository, go-service]
---

## Entity to Model

```go
func Model<Type>FromEntity(e *entity.<Type>) (model.<Type>, error) {
    t, err := model<Enum>FromEntity(e.Type)
    if err != nil {
        return model.<Type>{}, err
    }

    return model.<Type>{
        ID:   e.ID,
        Name: e.Name,
        Type: t,
    }, nil
}
```

## Model to Entity (write struct)

```go
func EntityCreate<Type>FromModel(m *model.Create<Type>) entity.Create<Type> {
    return entity.Create<Type>{
        ID:        uuid.Must(uuid.NewV7()),
        Name:      m.Name,
        CreatedAt: time.Now(),
    }
}
```

## Model to Filter

```go
func <Filter>FilterFromModel(search *model.<Search>) filter.<Filter> {
    return filter.<Filter>{
        Query: search.Query,
        Pagination: filter.Pagination{
            Offset: uint64((search.Pagination.Page - 1) * search.Pagination.PageSize),
            Limit:  uint64(search.Pagination.PageSize),
        },
    }
}
```
