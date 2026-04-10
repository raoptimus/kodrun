package agent

import (
	"fmt"
	"strings"
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

	// Specialised reviewer sub-roles used by the parallel /code-review
	// pipeline. Each focuses on a single review axis so that its system
	// prompt stays small and its context is not diluted by unrelated
	// concerns. Their outputs are merged by RoleExtractor.
	RoleReviewerRules        Role = "reviewer_rules"
	RoleReviewerIdiomatic    Role = "reviewer_idiomatic"
	RoleReviewerBestPractice Role = "reviewer_best_practice"
	RoleReviewerSecurity     Role = "reviewer_security"
	RoleReviewerStructure    Role = "reviewer_structure"
	RoleReviewerArchitecture Role = "reviewer_architecture"
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
	case RoleReviewerRules:
		return "Reviewing: project rules & naming"
	case RoleReviewerIdiomatic:
		return "Reviewing: language idiomaticity"
	case RoleReviewerBestPractice:
		return "Reviewing: best practices"
	case RoleReviewerSecurity:
		return "Reviewing: security"
	case RoleReviewerStructure:
		return "Reviewing: structure & layering"
	case RoleReviewerArchitecture:
		return "Reviewing: architecture"
	}
	return "Processing task..."
}

func reviewerShortLabel(role Role) string {
	label := taskLabelForRole(role)
	if after, ok := strings.CutPrefix(label, "Reviewing: "); ok {
		return after
	}
	return string(role)
}

// SpecialistReviewerRoles is the default ordered list of reviewer sub-roles
// used by the parallel /code-review pipeline.
var SpecialistReviewerRoles = []Role{
	RoleReviewerRules,
	RoleReviewerIdiomatic,
	RoleReviewerBestPractice,
	RoleReviewerSecurity,
	RoleReviewerStructure,
	RoleReviewerArchitecture,
}

// systemPromptForRole generates a role-specific system prompt for a sub-agent.
// Optional bool flags: hasSnippets, hasRAG.
func systemPromptForRole(role Role, lang, ruleCatalog string, toolNames []string, flags ...bool) string {
	snippetsEnabled := len(flags) > 0 && flags[0]
	ragEnabled := len(flags) > 1 && flags[1]
	var b strings.Builder

	b.WriteString("You are KodRun, a Go programming assistant.\n")
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
		b.WriteString("You are the PLANNER agent.\n")
		b.WriteString("Your job is to analyze the task and create a detailed, actionable plan.\n\n")
		b.WriteString("CRITICAL WORKFLOW:\n")
		b.WriteString("- If the task text contains file paths (e.g. `path/to/file.go:42`), your VERY FIRST tool calls MUST be read_file on those exact files. Do NOT call list_dir, find_files, or any other tool before reading the referenced files.\n")
		b.WriteString("- Only after reading the referenced files, read additional files if the code references imports or types you need to understand.\n\n")
		b.WriteString("Guidelines:\n")
		b.WriteString("- Use `go_structure(path)` to quickly see file/package outline (types, functions, constants with line numbers) before reading full files with read_file — this saves context\n")
		b.WriteString("- Read and analyze the relevant code using read-only tools\n")
		b.WriteString("- Identify all files that need changes\n")
		b.WriteString("- Create a numbered step-by-step plan\n")
		b.WriteString("- Each step should reference a specific file:line\n")
		b.WriteString("- Describe concrete issues found, not vague suggestions\n")
		b.WriteString("- Do NOT write code or generate code blocks\n")
		b.WriteString("- Be concise and actionable\n")
		b.WriteString("- Use idiomatic Go, Go best practices and project conventions\n")
		b.WriteString("- When you find existing code that demonstrates the correct pattern for a change, note it as: EXAMPLE: path/to/file.go:LINE — short reason. Place EXAMPLE lines immediately after the step they belong to.\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
		if ragEnabled {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			b.WriteString("The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and GO STANDARDS marked [GO STANDARDS].\n")
			b.WriteString("These are NOT suggestions — they are REQUIREMENTS. Treat violations as bugs.\n")
			b.WriteString("Examples: naming conventions (getter=Owner not GetOwner), error wrapping, context.Context as first arg, etc.\n")
			b.WriteString("You MUST check every code change against ALL provided rules. Include violations in your plan.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if snippetsEnabled {
			b.WriteString("\nIMPORTANT — Code conventions check:\n")
			b.WriteString("BEFORE creating the plan, you MUST call snippets(paths=[<list of all .go files you read>])\n")
			b.WriteString("to get the project's code conventions. Include violations of these conventions in your plan.\n")
		}

	case RoleExecutor:
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
		b.WriteString("- Run go_build after changes to verify compilation\n")
		b.WriteString("- Run go_lint to check style\n")
		b.WriteString("- Run go_test to verify correctness\n")
		b.WriteString("- When go_build fails, fix the COMPILATION ERROR (wrong import, typo, missing function) — do NOT revert your changes. Reverting the plan changes is NEVER acceptable. If you cannot fix the error after 3 attempts, output `REPLAN: <reason>`.\n")
		fmt.Fprintf(&b, "- Always respond in %s\n", langName(lang))
		if ragEnabled {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			b.WriteString("The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and GO STANDARDS marked [GO STANDARDS].\n")
			b.WriteString("These are REQUIREMENTS, not suggestions. Apply them to every line you write.\n")
			b.WriteString("Examples: getter naming (Owner not GetOwner), error wrapping with pkg/errors, context.Context as first arg.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if snippetsEnabled {
			b.WriteString("\nIMPORTANT — Code conventions:\n")
			b.WriteString("BEFORE editing a file, call snippets(paths=[<file_path>]) to get conventions for that file.\n")
			b.WriteString("Follow ALL matched conventions (naming, structure, patterns, error handling).\n")
		}

	case RoleReviewer:
		b.WriteString("You are the REVIEWER agent.\n")
		b.WriteString("Your job is to review changes made by the executor.\n\n")
		if ragEnabled {
			b.WriteString("IMPORTANT — Project rules and conventions (from RAG):\n")
			b.WriteString("The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES], GO STANDARDS marked [GO STANDARDS], and CODE TEMPLATES marked [CODE TEMPLATES].\n")
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
		b.WriteString("- Check for Go best practices and project conventions\n")
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
		b.WriteString("    \"<relative/path/to/file.ext:LINE — SEVERITY — what must change and why>\",\n")
		b.WriteString("    \"...\"\n")
		b.WriteString("  ]\n")
		b.WriteString("}\n\n")
		b.WriteString("STRICT RULES:\n")
		b.WriteString("- Output ONLY the JSON object. No markdown, no code fences, no comments, no explanations before/after.\n")
		b.WriteString("- Every `plan` item MUST start with `path:LINE — ` (real file and line from the analysis).\n")
		b.WriteString("- SEVERITY ∈ {blocker, major, minor}. Use `blocker` for compilation errors, data corruption, security holes; `major` for bugs and serious convention violations; `minor` for small cleanups.\n")
		b.WriteString("- Describe WHAT must change and WHY (convention, bug, template mismatch, etc.). Not vague `check X` or `verify Y`.\n")
		b.WriteString("- Do NOT add items not present in the original analysis. Do NOT invent issues.\n")
		b.WriteString("- Deduplicate: if the same file:line appears in multiple reviewer sections with the same issue, emit it once.\n")
		b.WriteString("- Group by file: if multiple issues affect the same file, emit ONE plan item per file with the lowest line number. Concatenate ALL findings for that file into the description — do NOT drop any details. Use semicolons to separate individual changes within the description.\n")
		b.WriteString("- Sort `plan` by file path, then by line number.\n")
		b.WriteString("- Do NOT include code blocks, patches, or diffs inside the strings.\n")
		b.WriteString("- If the original analysis found no real issues, output exactly: {\"context\": \"LGTM — no issues found\", \"plan\": []}\n")
		fmt.Fprintf(&b, "- The `context` field and every `plan` entry MUST be written in %s.\n", langName(lang))
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

	case RoleReviewerRules, RoleReviewerIdiomatic, RoleReviewerBestPractice,
		RoleReviewerSecurity, RoleReviewerStructure, RoleReviewerArchitecture:
		b.WriteString(specialistReviewerPrompt(role, lang, ragEnabled, snippetsEnabled))

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

// specialistReviewerPrompt returns the body of the system prompt for one of
// the focused reviewer sub-roles used by the parallel /code-review pipeline.
// The common header (assistant identity, language, rule catalog) is written
// by the caller (systemPromptForRole).
func specialistReviewerPrompt(role Role, lang string, ragEnabled, snippetsEnabled bool) string {
	var b strings.Builder

	focus, rules := specialistReviewerFocus(role, lang)

	b.WriteString("You are a SPECIALIST REVIEWER sub-agent.\n")
	fmt.Fprintf(&b, "Your ONLY focus: %s.\n", focus)
	b.WriteString("You are one of several parallel reviewers; another specialist covers every other aspect.\n")
	b.WriteString("Do NOT comment on anything outside your focus — even if you notice issues there.\n\n")

	b.WriteString("What to check:\n")
	for _, r := range rules {
		b.WriteString("- ")
		b.WriteString(r)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	b.WriteString("TOOLS AVAILABLE TO YOU: `file_stat(path)` returns file metadata (size, line count) without reading contents; `read_file(path, offset, limit)` reads a file with optional pagination; `go_structure(path)` shows file/package outline (types, functions, constants with line numbers) without reading full bodies — use it to understand structure before read_file; `list_dir(path)` lists a directory; `grep(pattern)` searches repository content; `search_docs(query)` searches the RAG knowledge base; `read_changed_files()` returns the unified diff for all changed files (optional, use if you need diff context). You MUST use these tools — they are not optional.\n\n")

	b.WriteString("MANDATORY WORKFLOW (no exceptions — follow in order):\n")
	b.WriteString("1. STAT FIRST. Your VERY FIRST action MUST be to call `file_stat(path)` on every file listed in the task. This tells you each file's size and line count without consuming context.\n")
	b.WriteString("2. READ SMART. After stat results arrive, read each file:\n")
	b.WriteString("   - Files with total_lines ≤ 200: call `read_file(path)` to read the entire file.\n")
	b.WriteString("   - Files with total_lines > 200: call `read_file(path, offset, limit)` to read ONLY the changed regions (line numbers are in the diff). Use a margin of ±30 lines around each changed hunk for context.\n")
	b.WriteString("3. Only after you have read ALL listed files (or relevant sections) may you analyze and produce findings.\n")
	b.WriteString("4. If no files are listed in the task or all files are empty, output exactly `NO_ISSUES` and stop immediately. Do NOT call any other tools.\n")
	b.WriteString("5. Outputting `NO_ISSUES` without first calling `file_stat` + `read_file` on every listed file is FORBIDDEN and counts as a failure.\n")
	b.WriteString("6. If a file read fails or errors, note it and continue with the remaining files; do not invent findings.\n")
	b.WriteString("7. A finding on a file you did not read is a hallucination and MUST NOT appear in your output.\n\n")

	b.WriteString("Output format (STRICT):\n")
	b.WriteString("- For every real issue, write one line: `path/to/file:LINE — SEVERITY — short description`.\n")
	b.WriteString("- SEVERITY ∈ {blocker, major, minor}. No prose outside these lines.\n")
	b.WriteString("- If you find nothing in your focus area, output exactly: `NO_ISSUES`.\n")
	b.WriteString("- Be strict: flag only real issues in your focus, not style preferences or speculation.\n")
	b.WriteString("- When a correct implementation of the flagged pattern exists elsewhere in the project, append on the next line: EXAMPLE: path/to/file.go:LINE — reason. This helps the executor see the correct pattern.\n")
	b.WriteString("- Do NOT propose code fixes or diffs — another agent will consolidate findings.\n")
	b.WriteString("- Do NOT produce Summary / Verdict / Cross-cutting sections — those belong to the aggregator, not to you.\n")
	fmt.Fprintf(&b, "- Write descriptions in %s.\n", langName(lang))

	if ragEnabled {
		b.WriteString("\nProject rules and conventions are pre-fetched in the task context ([MANDATORY PROJECT RULES], [CODE TEMPLATES], and any language standards block present).\n")
		b.WriteString("Treat them as REQUIREMENTS when they apply to your focus area. You may call search_docs for additional targeted lookups.\n")
	} else if snippetsEnabled {
		b.WriteString("\nBefore reviewing, call snippets(paths=[<changed files>]) to load code conventions relevant to your focus area.\n")
	}

	return b.String()
}

// specialistReviewerFocus returns the short focus label and the list of
// concrete, language-agnostic checks for a specialist reviewer role. The
// wording intentionally avoids language-specific APIs; the idiomaticity
// specialist is the only one that pivots on the detected project language.
func specialistReviewerFocus(role Role, lang string) (focus string, tools []string) {
	ln := langName(lang)
	switch role {
	case RoleReviewerRules:
		return "project rules and naming conventions", []string{
			"Violations of rules under .kodrun/rules/* and any pre-fetched [MANDATORY PROJECT RULES].",
			"Naming conventions for the project's language: identifiers, getters, error types, interfaces, files.",
			"Error reporting follows the project's chosen helper/style (wrapping, sentinels, error types).",
			"Public API shape matches project conventions (exported vs internal, argument order, return values).",
			"Package/module-level conventions (comment style, file layout, documentation).",
		}
	case RoleReviewerIdiomatic:
		return fmt.Sprintf("%s idiomaticity", ln), []string{
			fmt.Sprintf("Idiomatic %s: standard patterns, early returns, proper use of language primitives.", ln),
			"Prefer language/standard-library features over hand-rolled equivalents.",
			"Appropriate use of abstractions (interfaces, traits, protocols, generics) — not over- nor under-used.",
			"Avoid anti-patterns: unnecessary concurrency, reflection/meta-programming when not needed, premature generalisation.",
			fmt.Sprintf("Code that would make an experienced %s reviewer pause or rewrite.", ln),
		}
	case RoleReviewerBestPractice:
		return "best practices: error handling, resources, performance", []string{
			"Errors are checked, enriched with context, and never silently swallowed.",
			"Resource handling: deterministic cleanup, context/cancellation propagation, no goroutine/task/channel leaks.",
			"Performance footguns: unbounded allocations in hot paths, O(n^2) where O(n) suffices, unnecessary copies.",
			"Concurrency correctness: data races, missing synchronisation, incorrect use of primitives.",
			"Logging hygiene: no secrets, appropriate levels, structured fields where applicable.",
		}
	case RoleReviewerSecurity:
		return "security", []string{
			"Input validation at every trust boundary (CLI args, HTTP/RPC handlers, file paths from users).",
			"No secrets in code, logs, or error messages.",
			"Injection risks: SQL, shell/command, template, header, log — only safe APIs / parameterised queries.",
			"Path traversal: reject `..` and absolute paths that escape the working directory.",
			"Cryptography: stdlib only, no homegrown crypto, secure random source, no broken algorithms (MD5/SHA1 for security, DES, etc.).",
			"Authentication / authorisation checks are present where required and cannot be bypassed.",
		}
	case RoleReviewerStructure:
		return "package/module structure and dependency boundaries", []string{
			"New files are placed in the correct package/module/layer per project layout.",
			"Package responsibilities stay cohesive; no unrelated concerns bundled together.",
			"Dependency directions respect documented layers (no cycles, no inward→outward imports).",
			"Public vs internal symbols: exported surface kept minimal; internal boundaries respected.",
			"No duplication of existing utilities — flag cases where an existing helper should be reused.",
		}
	case RoleReviewerArchitecture:
		return "architectural invariants", []string{
			"Changes fit the documented architecture (AGENTS.md, architecture/overview RAG chunks).",
			"Forbidden cross-layer dependencies, skipping abstractions, leaking implementation details upward.",
			"Missing or mis-placed components (e.g. business logic in transport layer, transport in domain).",
			"Impact on system-wide concerns: transactions, error propagation across layers, extensibility points.",
			"Whether the change introduces architectural drift that should be discussed before merging.",
		}
	}
	return "code quality", nil
}
