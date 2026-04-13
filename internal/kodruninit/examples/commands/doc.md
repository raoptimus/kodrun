---
command: /doc
description: "Add godoc documentation"
---

Add godoc comments to all public functions, types, and methods in the file {{file}}.

Rules:
- The comment must start with the function/type name
- Description — one or two sentences explaining what it does, not how
- For errors — when the error is returned
- Run go_build after adding
