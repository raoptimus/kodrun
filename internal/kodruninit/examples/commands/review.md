---
command: /review
description: "Perform a code review of a file"
---

Perform a detailed code review of the file {{file}}.

Check for:
- Bugs and logic errors
- Error handling (wrapping, sentinel errors)
- Code style and architecture rule violations
- Missing tests for new code
- Performance issues
- Potential race conditions
- Resource leaks (unclosed files, connections)

Provide specific remarks with line numbers and suggested fixes.
