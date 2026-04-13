---
command: /test
description: "Write unit tests for a file"
---

Write unit tests for the file {{file}}.

Rules:
- Use table-driven tests
- Cover equivalence classes and boundary conditions
- Mock external dependencies (mockery / hand-written mocks)
- Hardcode expected values
- Check both happy path and error cases
- Run go_test after writing to make sure the tests pass
