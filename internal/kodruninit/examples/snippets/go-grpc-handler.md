---
description: "Template for gRPC handler — struct with validator + service interfaces, constructor, Handle method, error-to-status mapping"
tags: [grpc, handler, server]
requires: [grpc]
paths: ["**/server/grpc/**/*.go", "**/handler/**/*.go"]
lang: go
related: [go-validator, go-service, go-converter]
---

## Handler struct

```go
type <Operation>Validator interface {
    Validate(context.Context, *rpc.<Operation>Request) error
}

type <Operation>Service interface {
    <Operation>(context.Context, *model.<Input>) (<return_type>, error)
}

type <Operation> struct {
    validator <Operation>Validator
    service   <Operation>Service
}

func New<Operation>(
    validator <Operation>Validator,
    service <Operation>Service,
) *<Operation> {
    return &<Operation>{
        validator: validator,
        service:   service,
    }
}
```

## Handle — get one

```go
func (h *<Operation>) Handle(
    ctx context.Context,
    req *rpc.<Operation>Request,
) (*rpc.<Operation>Response, error) {
    if err := h.validator.Validate(ctx, req); err != nil {
        return nil, err
    }

    result, err := h.service.<Operation>(ctx, convert.ModelFromGRPC(req))
    if err != nil {
        if errors.Is(err, model.Err<Entity>NotFound) {
            return nil, status.Error(codes.NotFound, err.Error())
        }

        return nil, err
    }

    return convert.GRPCResponseFromModel(&result)
}
```

## Error mapping

| Sentinel | gRPC code |
|----------|-----------|
| `ErrNotFound` | `codes.NotFound` |
| `ErrAlreadyExists` | `codes.AlreadyExists` |
| `ErrNotAllowed` | `codes.PermissionDenied` |
