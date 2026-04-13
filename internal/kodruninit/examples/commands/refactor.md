---
command: /refactor
description: "Refactor by description"
---

Perform the refactoring: {{description}}

Requirements:
- Preserve existing behavior (do not break the API)
- Run go_build and go_test after changes
- Minimal changes — do not touch code unrelated to the refactoring
- If tests break — fix them
