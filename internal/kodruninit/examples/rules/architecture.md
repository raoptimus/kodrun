---
priority: high
scope: all
---

# Project Architecture

Clean Architecture with dependency direction: transport → domain ← dal ← client.
Domain knows nothing about transport or infrastructure.

## Layers

- **domain/model/** — domain models and errors
- **domain/service/** — business logic for a single entity, model↔entity converters in service/convert/
- **domain/usecase/** — orchestration of 2+ services
- **dal/entity/** — database structures, filters (entity/filter/)
- **dal/repository/** — SQL queries to PostgreSQL
- **server/grpc/** — gRPC handlers with convert/ and validate/
- **client/** — wrappers over external SDKs

## Rules

- Interfaces are defined next to the consumer and contain only the methods used
- model ↔ entity conversion always goes through the convert/ package, never inline
- Each service works with only one entity
- Operations involving multiple entities → usecase
- A child package must not import its parent (no upward references)
