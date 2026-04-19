package agent

import (
	"fmt"
	"strings"
)

// progLang constants used in system prompt generation.
const (
	progLangGo     = "go"
	progLangPython = "python"
	progLangJSTS   = "jsts"
)

// Role identifies a sub-agent's purpose in the orchestrator pipeline.
type Role string

const (
	RolePlanner            Role = "planner"
	RoleExecutor           Role = "executor"
	RoleReviewer           Role = "reviewer"
	RoleExtractor          Role = "extractor"
	RoleStructurer         Role = "structurer"
	RoleResponseClassifier Role = "response_classifier"

	// Code review roles. The orchestrator pre-loads all context (file content,
	// RAG snippets, dependency signatures) so these roles do not require
	// tool-calling — they only analyse.
	RoleCodeReviewer Role = "code_reviewer"
	RoleArchReviewer Role = "arch_reviewer"
)

// taskLabelForRole returns a short human-readable label describing what a
// sub-agent of the given role is doing. Used by Agent.Send() to emit a more
// informative status line than the generic "Processing task...".
func taskLabelForRole(role Role) string {
	switch role {
	case RolePlanner:
		return "Planning..."
	case RoleExecutor:
		return "Executing plan..."
	case RoleReviewer:
		return "Reviewing changes..."
	case RoleExtractor:
		return "Extracting structured plan..."
	case RoleStructurer:
		return "Converting plan to JSON..."
	case RoleResponseClassifier:
		return "Classifying response..."
	case RoleCodeReviewer:
		return "Reviewing file..."
	case RoleArchReviewer:
		return "Reviewing architecture..."
	}
	return "Processing task..."
}

// systemPromptForRole generates a role-specific system prompt for a sub-agent.
// progLang is the detected project programming language (e.g. "go", "python", "jsts", or "").
// Optional bool flags: hasSnippets, hasRAG.
func systemPromptForRole(role Role, lang, progLang, ruleCatalog string, toolNames []string, flags ...bool) string {
	snippetsEnabled := len(flags) > 0 && flags[0]
	ragEnabled := len(flags) > 1 && flags[1]
	var b strings.Builder

	if progLang != "" {
		fmt.Fprintf(&b, "You are KodRun, a %s programming assistant.\n", progLang)
	} else {
		b.WriteString("You are KodRun, a programming assistant.\n")
	}
	ln := langName(lang)
	if ln != langEnglish {
		fmt.Fprintf(&b, "IMPORTANT: ALL your responses MUST be in %s. This is mandatory.\n", ln)
	}
	b.WriteByte('\n')

	if ruleCatalog != "" {
		b.WriteString(ruleCatalog)
		b.WriteString("\n")
	}

	// Every role gets the full list of available tools in its system prompt.
	// Some local models (qwen3-coder and similar) do not reliably infer tool
	// availability from schema alone — without this explicit textual mention
	// they silently skip calling tools like read_file. Historically only
	// Executor/StepExecutor had this line; all other roles (planner, reviewer,
	// specialist reviewers) would go quiet because they "did not know" read_file
	// existed.
	if len(toolNames) > 0 {
		b.WriteString("Available tools: " + strings.Join(toolNames, ", ") + "\n\n")
	}

	switch role {
	case RolePlanner:
		ltc := langToolsForLang(progLang)
		b.WriteString("You are the PLANNER agent.\n")
		b.WriteString("Your job is to analyze the task and create a detailed, actionable plan.\n\n")
		b.WriteString("CRITICAL WORKFLOW:\n")
		b.WriteString("- If the task text contains file paths (e.g. `path/to/file.go:42`), your VERY FIRST tool calls MUST be read_file on those exact files. Do NOT call list_dir, find_files, or any other tool before reading the referenced files.\n")
		b.WriteString("- Only after reading the referenced files, read additional files if the code references imports or types you need to understand.\n\n")
		b.WriteString("Guidelines:\n")
		if ltc.structureTool != "" {
			fmt.Fprintf(&b, "- Use `%s(path)` to quickly see file/package outline (types, functions, constants with line numbers) before reading full files with read_file — this saves context\n", ltc.structureTool)
		}
		b.WriteString("- Read and analyze the relevant code using read-only tools\n")
		b.WriteString("- Identify all files that need changes\n")
		b.WriteString("- Create a numbered step-by-step plan\n")
		b.WriteString("- Each step should reference a specific file:line\n")
		b.WriteString("- Describe concrete issues found, not vague suggestions\n")
		b.WriteString("- Do NOT write code or generate code blocks\n")
		b.WriteString("- Be concise and actionable\n")
		fmt.Fprintf(&b, "- %s\n", ltc.bestPractices)
		b.WriteString("- When you find existing code that demonstrates the correct pattern for a change, note it as: EXAMPLE: path/to/file.go:LINE — short reason. Place EXAMPLE lines immediately after the step they belong to.\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
		if ragEnabled {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and %s marked [%s].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("These are NOT suggestions — they are REQUIREMENTS. Treat violations as bugs.\n")
			b.WriteString("Examples: naming conventions, error wrapping, proper use of language idioms, etc.\n")
			b.WriteString("You MUST check every code change against ALL provided rules. Include violations in your plan.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if snippetsEnabled {
			b.WriteString("\nIMPORTANT — Code conventions check:\n")
			b.WriteString("BEFORE creating the plan, you MUST call snippets(paths=[<list of all source files you read>])\n")
			b.WriteString("to get the project's code conventions. Include violations of these conventions in your plan.\n")
		}

	case RoleExecutor:
		ltc := langToolsForLang(progLang)
		b.WriteString("You are the EXECUTOR agent.\n")
		b.WriteString("Your job is to IMPLEMENT an approved plan by writing/editing code.\n\n")
		b.WriteString("CRITICAL RULES:\n")
		b.WriteString("- The source code is PROVIDED in the task. Do NOT call read_file, list_dir, find_files, or grep.\n")
		b.WriteString("- Start IMMEDIATELY with edit_file/write_file. No analysis, no re-reading.\n")
		b.WriteString("- Do NOT re-analyze or re-review the code. The plan is already approved.\n")
		b.WriteString("- Do NOT write checklists like 'STEP 1: VERIFY...' or 'STEP 2: ANALYZE...'. Just apply the changes.\n")
		b.WriteString("- The set of files you may touch is fixed by the approved plan. Reading any file outside that set will be REFUSED by the harness.\n")
		b.WriteString("- If — and only if — the plan is genuinely missing context you need, output a single line beginning with `REPLAN:` followed by a short reason, then stop. Do NOT silently work around missing context.\n")
		b.WriteString("- A response without ANY tool calls is a FAILURE in this role. If you have nothing to do, output `REPLAN: <reason>` instead of writing prose. Markdown plans/analyses are NEVER a valid output here.\n\n")
		b.WriteString("Guidelines:\n")
		b.WriteString("- Follow the plan exactly, step by step\n")
		b.WriteString("- Use edit_file for small changes, write_file for new files\n")
		b.WriteString("- After each step, confirm what was done\n")
		if ltc.buildTool != "" {
			fmt.Fprintf(&b, "- Run %s after changes to verify compilation\n", ltc.buildTool)
		}
		if ltc.lintTool != "" {
			fmt.Fprintf(&b, "- Run %s to check style\n", ltc.lintTool)
		}
		if ltc.testTool != "" {
			fmt.Fprintf(&b, "- Run %s to verify correctness\n", ltc.testTool)
		}
		if ltc.buildTool != "" {
			fmt.Fprintf(&b, "- When %s fails, fix the COMPILATION ERROR (wrong import, typo, missing function) — do NOT revert your changes. Reverting the plan changes is NEVER acceptable. If you cannot fix the error after 3 attempts, output `REPLAN: <reason>`.\n", ltc.buildTool)
		}
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
		if ragEnabled {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and %s marked [%s].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("These are REQUIREMENTS, not suggestions. Apply them to every line you write.\n")
			b.WriteString("Examples: naming conventions, error wrapping, proper use of language idioms.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if snippetsEnabled {
			b.WriteString("\nIMPORTANT — Code conventions:\n")
			b.WriteString("BEFORE editing a file, call snippets(paths=[<file_path>]) to get conventions for that file.\n")
			b.WriteString("Follow ALL matched conventions (naming, structure, patterns, error handling).\n")
		}

	case RoleReviewer:
		ltc := langToolsForLang(progLang)
		b.WriteString("You are the REVIEWER agent.\n")
		b.WriteString("Your job is to review changes made by the executor.\n\n")
		if ragEnabled {
			b.WriteString("IMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES], %s marked [%s], and CODE TEMPLATES marked [CODE TEMPLATES].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("ALL of these are REQUIREMENTS. Flag every violation as a bug with file:line reference.\n")
			b.WriteString("CODE TEMPLATES define how specific patterns (validation, error handling, constructors, etc.) MUST be implemented in this project.\n")
			b.WriteString("If changed code does something that a CODE TEMPLATE covers, the code MUST follow that template. Deviations are bugs.\n")
			b.WriteString("You MUST call search_docs for each changed file to check if there are additional code templates or conventions that apply.\n\n")
		} else if snippetsEnabled {
			b.WriteString("IMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("BEFORE reviewing code, call snippets(paths=[<list of changed files>])\n")
			b.WriteString("to get the project's code conventions. Use found conventions as the review criteria.\n\n")
		}
		b.WriteString("Guidelines:\n")
		b.WriteString("- Read the changed files using read-only tools\n")
		b.WriteString("- Check for bugs, logic errors, and edge cases\n")
		fmt.Fprintf(&b, "- Check for %s best practices and project conventions\n", ltc.displayName)
		b.WriteString("- Check for security issues\n")
		b.WriteString("- Verify error handling\n")
		b.WriteString("- If changes look good, say so clearly\n")
		b.WriteString("- If there are issues, list them with specific file:line references\n")
		b.WriteString("- Be concise — only flag real issues, not style preferences\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))

	case RoleExtractor:
		fmt.Fprintf(&b, "CRITICAL: All natural-language text inside the JSON MUST be written in %s. Never switch to any other language.\n\n", langName(lang))
		b.WriteString("You are the PLAN EXTRACTOR agent.\n")
		b.WriteString("Your job is to convert raw analysis / review text into a strict JSON plan.\n\n")
		b.WriteString("Input may contain thinking/reasoning blocks, scattered review comments, and vague suggestions mixed with concrete findings.\n\n")
		b.WriteString("You MUST output ONLY a single JSON object matching this schema (no markdown fences, no prose, no trailing text):\n\n")
		b.WriteString("{\n")
		b.WriteString("  \"context\": \"<one-paragraph summary of the overall situation and goal>\",\n")
		b.WriteString("  \"plan\": [\n")
		b.WriteString("    {\n")
		b.WriteString("      \"file\": \"<relative/path/to/file.ext>\",\n")
		b.WriteString("      \"line\": <integer>,\n")
		b.WriteString("      \"severity\": \"blocker\" | \"major\" | \"minor\",\n")
		b.WriteString("      \"what\": \"<short description of the problem>\",\n")
		b.WriteString("      \"why\": \"<why this must be fixed>\",\n")
		b.WriteString("      \"fix\": \"<concrete fix suggestion>\",\n")
		b.WriteString("      \"before\": \"<existing code snippet (optional)>\",\n")
		b.WriteString("      \"after\": \"<corrected code snippet (optional)>\",\n")
		b.WriteString("      \"rules\": [\"<rule name>\", ...]\n")
		b.WriteString("    }\n")
		b.WriteString("  ],\n")
		b.WriteString("  \"affected_files\": [\"<relative/path/to/file.ext>\", ...],\n")
		b.WriteString("  \"verification\": [\"<verification step, e.g. 'make build', 'make lint', 'make test-unit'>\", ...]\n")
		b.WriteString("}\n\n")
		b.WriteString("STRICT RULES:\n")
		b.WriteString("- Output ONLY the JSON object. No markdown, no code fences, no comments, no explanations before/after.\n")
		b.WriteString("- Every plan item MUST have `file`, `line`, `severity`, `what`, and `fix` fields.\n")
		b.WriteString("- `why`, `before`, `after`, `rules` are optional — include only when relevant.\n")
		b.WriteString("- `before`/`after` should be short inline code (single expression or statement).\n")
		b.WriteString("- SEVERITY ∈ {blocker, major, minor}. Use `blocker` for compilation errors, data corruption, security holes; `major` for bugs and serious convention violations; `minor` for small cleanups.\n")
		b.WriteString("- Describe WHAT must change concisely. Describe WHY separately.\n")
		b.WriteString("- Do NOT add items not present in the original analysis. Do NOT invent issues.\n")
		b.WriteString("- Deduplicate: if the same file:line appears in multiple reviewer sections with the same issue, emit it once.\n")
		b.WriteString("- Group by file: if multiple issues affect the same file, emit ONE plan item per file with the lowest line number. Put all findings for that file in the `what` field, separated by semicolons.\n")
		b.WriteString("- Sort `plan` by file path, then by line number.\n")
		b.WriteString("- `affected_files` — list ALL unique file paths mentioned in `plan` items.\n")
		b.WriteString("- `verification` — list concrete shell commands to verify correctness (e.g. `make build`, `make lint`, `make test-unit`). Include at least build and lint.\n")
		b.WriteString("- If the original analysis found no real issues, output exactly: {\"context\": \"LGTM — no issues found\", \"plan\": []}\n")
		if lang == progLangGo {
			b.WriteString("- DROP findings that contradict idiomatic Go conventions (Effective Go, Go Code Review Comments). For example, redundant nil/zero-value checks after error handling are false positives — the (T, error) contract guarantees T is valid when err == nil.\n")
		}
		fmt.Fprintf(&b, "- The `context` field and all text fields MUST be written in %s.\n", langName(lang))
		b.WriteString("\nReturn ONLY the JSON object.\n")

	case RoleStructurer:
		b.WriteString("You are the PLAN STRUCTURER agent.\n")
		b.WriteString("Your only job is to convert a human-readable plan into a strict JSON document.\n\n")
		b.WriteString("You MUST output ONLY a single JSON object, no markdown fences, no prose, no explanation.\n\n")
		b.WriteString("Schema:\n")
		b.WriteString("{\n")
		b.WriteString("  \"context\": \"<one-paragraph summary of the goal>\",\n")
		b.WriteString("  \"steps\": [\n")
		b.WriteString("    {\n")
		b.WriteString("      \"id\": <integer, starting at 1>,\n")
		b.WriteString("      \"title\": \"<short imperative sentence>\",\n")
		b.WriteString("      \"files\": [\"<relative file path>\", ...],\n")
		b.WriteString("      \"action\": \"edit\" | \"create\" | \"delete\" | \"run\",\n")
		b.WriteString("      \"rationale\": \"<why this step is needed>\",\n")
		b.WriteString("      \"depends_on\": [<id>, ...],\n")
		b.WriteString("      \"examples\": [{\"file\": \"<path>\", \"line\": <N>, \"note\": \"<why this is the correct pattern>\"}]\n")
		b.WriteString("    }\n")
		b.WriteString("  ]\n")
		b.WriteString("}\n\n")
		b.WriteString("Rules:\n")
		b.WriteString("- One step per atomic change. If the plan touches three files in three independent ways, emit three steps.\n")
		b.WriteString("- `files` is the EXACT list of files that step modifies. Do not include files only mentioned for context.\n")
		b.WriteString("- `depends_on` lists step ids that must complete first. Use [] for steps that can run in parallel with others.\n")
		b.WriteString("- Two steps that touch the same file MUST be ordered with depends_on; never let two parallel steps fight over the same file.\n")
		b.WriteString("- `examples` is an optional array. Include when the original plan has EXAMPLE: lines. Each example points to existing code demonstrating the correct pattern. Do not fabricate examples.\n")
		b.WriteString("- Preserve the order and intent of the original plan exactly. Do not invent new work.\n")
		b.WriteString("- Output ONLY the JSON object. No comments, no markdown, no trailing text.\n")

	case RoleCodeReviewer:
		b.WriteString(codeReviewerPrompt(lang))

	case RoleArchReviewer:
		b.WriteString(archReviewerPrompt(lang))

	case RoleResponseClassifier:
		b.WriteString("You are the RESPONSE CLASSIFIER agent.\n")
		b.WriteString("You receive a USER_INPUT (what the user asked) and an AGENT_RESPONSE (what another agent already produced).\n")
		b.WriteString("Your only job is to classify the AGENT_RESPONSE and decide whether the user must take a follow-up action.\n\n")
		b.WriteString("You MUST output ONLY a single JSON object, with no surrounding prose, no markdown fences, no comments.\n")
		b.WriteString("Schema:\n")
		b.WriteString("{\n")
		b.WriteString("  \"kind\": \"plan\" | \"question_answer\" | \"clarification_request\" | \"status\" | \"other\",\n")
		b.WriteString("  \"needs_user_action\": true | false,\n")
		b.WriteString("  \"suggested_action\": \"approve_plan\" | \"answer_question\" | \"none\",\n")
		b.WriteString("  \"cta_text\": \"<short call-to-action sentence in the user's language, or empty string>\"\n")
		b.WriteString("}\n\n")
		b.WriteString("Rules for classification:\n")
		b.WriteString("- kind=\"plan\" when AGENT_RESPONSE contains a numbered or bulleted list of concrete code/file changes the agent intends to make. Set needs_user_action=true and suggested_action=\"approve_plan\".\n")
		b.WriteString("- kind=\"question_answer\" when AGENT_RESPONSE only explains, describes, or answers a question without proposing changes. Set needs_user_action=false and suggested_action=\"none\".\n")
		b.WriteString("- kind=\"clarification_request\" when AGENT_RESPONSE asks the user for more information. Set needs_user_action=true and suggested_action=\"answer_question\".\n")
		b.WriteString("- kind=\"status\" for short progress/acknowledgement messages. Set needs_user_action=false.\n")
		b.WriteString("- kind=\"other\" for anything that does not fit. Set needs_user_action=false.\n")
		b.WriteString("- cta_text MUST be empty unless suggested_action=\"approve_plan\". For approve_plan, write a single short sentence inviting the user to confirm starting the implementation.\n")
		fmt.Fprintf(&b, "- The cta_text MUST be in %s.\n", langName(lang))
		b.WriteString("\nReturn ONLY the JSON object. No explanation. No markdown.\n")
	}

	return b.String()
}

// langToolHints holds language-specific tool names and labels for system prompts.
type langToolHints struct {
	structureTool  string // e.g. "go_structure"; "" when unavailable
	buildTool      string // e.g. "go_build", "tsc", "python_run"
	lintTool       string // e.g. "go_lint", "eslint", "ruff"
	testTool       string // e.g. "go_test", "npm_test", "pytest"
	standardsLabel string // e.g. "GO STANDARDS", "JS/TS STANDARDS"
	bestPractices  string // e.g. "Use idiomatic Go, Go best practices..."
	displayName    string // e.g. "Go", "Python", "JavaScript/TypeScript"
}

func langToolsForLang(progLang string) langToolHints {
	switch progLang {
	case progLangGo:
		return langToolHints{
			structureTool:  "go_structure",
			buildTool:      "go_build",
			lintTool:       "go_lint",
			testTool:       "go_test",
			standardsLabel: "GO STANDARDS",
			bestPractices:  "Use idiomatic Go, Go best practices and project conventions",
			displayName:    "Go",
		}
	case progLangPython:
		return langToolHints{
			buildTool:      "python_run",
			lintTool:       "ruff",
			testTool:       "pytest",
			standardsLabel: "PYTHON STANDARDS",
			bestPractices:  "Use idiomatic Python, PEP 8, and project conventions",
			displayName:    "Python",
		}
	case progLangJSTS:
		return langToolHints{
			buildTool:      "tsc",
			lintTool:       "eslint",
			testTool:       "npm_test",
			standardsLabel: "JS/TS STANDARDS",
			bestPractices:  "Use idiomatic JavaScript/TypeScript and project conventions",
			displayName:    "JavaScript/TypeScript",
		}
	default:
		return langToolHints{
			standardsLabel: "PROJECT STANDARDS",
			bestPractices:  "Use idiomatic code, best practices and project conventions",
			displayName:    "language",
		}
	}
}

// reviewChecks returns named sets of review checks used by code and
// architecture reviewer prompts.
func reviewChecks(lang string) map[string][]string {
	ln := langName(lang)
	return map[string][]string{
		"rules": {
			"Violations of rules under .kodrun/rules/* and any pre-fetched [MANDATORY PROJECT RULES].",
			"Naming conventions for the project's language: identifiers, getters, error types, interfaces, files.",
			"Error reporting follows the project's chosen helper/style (wrapping, sentinels, error types).",
			"Public API shape matches project conventions (exported vs internal, argument order, return values).",
			"Package/module-level conventions (comment style, file layout, documentation).",
		},
		"idiomatic": {
			fmt.Sprintf("Idiomatic %s: standard patterns, early returns, proper use of language primitives.", ln),
			"Prefer language/standard-library features over hand-rolled equivalents.",
			"Appropriate use of abstractions (interfaces, traits, protocols, generics) — not over- nor under-used.",
			"Avoid anti-patterns: unnecessary concurrency, reflection/meta-programming when not needed, premature generalisation.",
			fmt.Sprintf("Code that would make an experienced %s reviewer pause or rewrite.", ln),
		},
		"best_practice": {
			"Errors are checked, enriched with context, and never silently swallowed.",
			"Resource handling: deterministic cleanup, context/cancellation propagation, no goroutine/task/channel leaks.",
			"Performance footguns: unbounded allocations in hot paths, O(n^2) where O(n) suffices, unnecessary copies.",
			"Concurrency correctness: data races, missing synchronisation, incorrect use of primitives.",
			"Logging hygiene: no secrets, appropriate levels, structured fields where applicable.",
		},
		"security": {
			"Input validation at every trust boundary (CLI args, HTTP/RPC handlers, file paths from users).",
			"No secrets in code, logs, or error messages.",
			"Injection risks: SQL, shell/command, template, header, log — only safe APIs / parameterised queries.",
			"Path traversal: reject `..` and absolute paths that escape the working directory.",
			"Cryptography: stdlib only, no homegrown crypto, secure random source, no broken algorithms (MD5/SHA1 for security, DES, etc.).",
			"Authentication / authorisation checks are present where required and cannot be bypassed.",
		},
		"structure": {
			"New files are placed in the correct package/module/layer per project layout.",
			"Package responsibilities stay cohesive; no unrelated concerns bundled together.",
			"Dependency directions respect documented layers (no cycles, no inward→outward imports).",
			"Public vs internal symbols: exported surface kept minimal; internal boundaries respected.",
			"No duplication of existing utilities — flag cases where an existing helper should be reused.",
		},
		"architecture": {
			"Changes fit the documented architecture (project rules, architecture/overview RAG chunks).",
			"Forbidden cross-layer dependencies, skipping abstractions, leaking implementation details upward.",
			"Missing or mis-placed components (e.g. business logic in transport layer, transport in domain).",
			"Impact on system-wide concerns: transactions, error propagation across layers, extensibility points.",
			"Whether the change introduces architectural drift that should be discussed before merging.",
		},
	}
}

// codeReviewerPrompt returns the system prompt body for the per-file code
// reviewer. All context is pre-loaded by the orchestrator — no tool-calling.
func codeReviewerPrompt(lang string) string {
	var b strings.Builder
	ln := langName(lang)

	b.WriteString("You are a STRICT CODE REVIEWER.\n")
	b.WriteString("The file content, project conventions, and dependency signatures are provided in the task.\n")
	b.WriteString("You do NOT need to call any tools — everything you need is already included.\n\n")

	b.WriteString("IMPORTANT — Technology scope:\n")
	b.WriteString("If a convention or template mentions a technology, framework, or library that is NOT present in the file's imports or dependency signatures, SKIP that convention entirely. Only apply conventions for technologies the code actually uses.\n\n")

	b.WriteString("Focus areas (ALL apply — check every one):\n")

	checks := reviewChecks(lang)
	for _, key := range []string{"rules", "idiomatic", "best_practice", "security"} {
		for _, c := range checks[key] {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')

	b.WriteString("Output format (STRICT):\n")
	b.WriteString("- For every real issue use a multi-line block:\n")
	b.WriteString("```\n")
	b.WriteString("path/to/file:LINE — SEVERITY\n")
	b.WriteString("WHAT: short description of the problem\n")
	b.WriteString("WHY: why this must be fixed\n")
	b.WriteString("FIX: concrete fix suggestion\n")
	b.WriteString("BEFORE: `existing code`\n")
	b.WriteString("AFTER: `corrected code`\n")
	b.WriteString("RULES: comma-separated rule names\n")
	b.WriteString("```\n")
	b.WriteString("- WHAT and FIX are mandatory. WHY, BEFORE, AFTER, RULES are optional — include only when relevant.\n")
	b.WriteString("- BEFORE/AFTER should be short inline code (single expression or statement), not multi-line blocks.\n")
	b.WriteString("- RULES must reference ONLY rule names that appear in the 'Available project rules' list above. If no matching rule exists, omit the RULES line entirely. NEVER invent rule names.\n")
	b.WriteString("- SEVERITY ∈ {blocker, major, minor}. No prose outside these blocks.\n")
	b.WriteString("- If you find nothing, output exactly: `NO_ISSUES`.\n")
	b.WriteString("- Be strict: flag only real issues, not style preferences or speculation.\n")
	b.WriteString("- Do NOT produce Summary / Verdict / Cross-cutting sections.\n")
	fmt.Fprintf(&b, "- Write descriptions in %s.\n", ln)

	return b.String()
}

// archReviewerPrompt returns the system prompt body for the project-wide
// architecture reviewer. Receives go_structure output for all packages — no
// file contents, no tool-calling.
func archReviewerPrompt(lang string) string {
	var b strings.Builder
	ln := langName(lang)

	b.WriteString("You are an ARCHITECTURE REVIEWER.\n")
	b.WriteString("You receive the structural outline (types, functions, interfaces) of every package in the project.\n")
	b.WriteString("You do NOT need to call any tools — the full project structure is provided.\n\n")

	b.WriteString("IMPORTANT — Technology scope:\n")
	b.WriteString("If a convention or template mentions a technology, framework, or library that is NOT present in the project's imports, SKIP that convention entirely. Only apply conventions for technologies the project actually uses.\n\n")

	b.WriteString("Focus areas:\n")

	checks := reviewChecks(lang)
	for _, key := range []string{"structure", "architecture"} {
		for _, c := range checks[key] {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')

	b.WriteString("Output format (STRICT):\n")
	b.WriteString("- For every real issue use a multi-line block:\n")
	b.WriteString("```\n")
	b.WriteString("path/to/package — SEVERITY\n")
	b.WriteString("WHAT: short description of the problem\n")
	b.WriteString("WHY: why this must be fixed\n")
	b.WriteString("FIX: concrete suggestion\n")
	b.WriteString("RULES: comma-separated rule names\n")
	b.WriteString("```\n")
	b.WriteString("- WHAT and FIX are mandatory. WHY, RULES are optional — include only when relevant.\n")
	b.WriteString("- RULES must reference ONLY rule names that appear in the 'Available project rules' list above. If no matching rule exists, omit the RULES line entirely. NEVER invent rule names.\n")
	b.WriteString("- SEVERITY ∈ {blocker, major, minor}. No prose outside these blocks.\n")
	b.WriteString("- If you find nothing, output exactly: `NO_ISSUES`.\n")
	b.WriteString("- Focus on structural and architectural issues only — not code-level bugs.\n")
	b.WriteString("- Do NOT produce Summary / Verdict / Cross-cutting sections.\n")
	fmt.Fprintf(&b, "- Write descriptions in %s.\n", ln)

	return b.String()
}
