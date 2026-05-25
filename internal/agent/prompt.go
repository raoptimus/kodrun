/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// langName maps an ISO language code to its English name. Empty / unknown
// codes default to English.
func langName(code string) string {
	switch code {
	case "ru":
		return "Russian"
	case "en":
		return langEnglish
	case "de":
		return "German"
	case "fr":
		return "French"
	case "es":
		return "Spanish"
	case "zh":
		return "Chinese"
	case "ja":
		return "Japanese"
	case "":
		return langEnglish
	default:
		return code
	}
}

// buildSystemPrompt assembles the mode-specific system prompt for the agent's
// current state. Used by Send() at the start of each iteration to prime the
// LLM with role rules, available tools, and language directives.
func (a *Agent) buildSystemPrompt() string {
	lang := langName(a.language)
	pl := a.currentProgLang()
	ltc := langToolsForLang(pl)

	var b strings.Builder
	if pl != "" {
		fmt.Fprintf(&b, "You are KodRun, a %s programming assistant.\n", pl)
	} else {
		b.WriteString("You are KodRun, a programming assistant.\n")
	}
	fmt.Fprintf(&b, "IMPORTANT: ALL your responses MUST be in %s. This is mandatory.\n\n", lang)

	if a.ruleCatalog != "" {
		b.WriteString(a.ruleCatalog)
		b.WriteString("\n")
	}

	switch a.mode {
	case ModePlan:
		b.WriteString("You are in PLAN mode (READ-ONLY).\n")
		b.WriteString("You can ONLY analyze code and create plans. You CANNOT modify files.\n")
		b.WriteString("You MUST NOT call any tools besides: " + strings.Join(a.reg.NamesFiltered(a.readOnlyTools()), ", ") + "\n\n")
		b.WriteString("IMPORTANT — Questions vs Tasks:\n")
		switch pl {
		case progLangGo:
			b.WriteString("- If the user asks a QUESTION (about Go, naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		case progLangPython:
			b.WriteString("- If the user asks a QUESTION (about Python naming, PEP conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		case progLangJSTS:
			b.WriteString("- If the user asks a QUESTION (about TypeScript naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		default:
			b.WriteString("- If the user asks a QUESTION (about naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		}
		b.WriteString("- If the user gives a TASK (fix, refactor, add feature, etc.) — create a numbered plan.\n\n")
		b.WriteString("STRICT RULES (for tasks):\n")
		b.WriteString("- NEVER generate code blocks, patches, diffs, or file contents\n")
		b.WriteString("- NEVER show code that should be written or changed\n")
		b.WriteString("- NEVER call write_file, edit_file, delete_file, bash, or any write tool\n")
		b.WriteString("- If asked to fix, edit, or write code — describe WHAT to change, not HOW in code\n")
		b.WriteString("- Your plan must be a numbered list with text descriptions only\n")
		b.WriteString("- Do NOT read binary files, build artifacts, or IDE config directories\n\n")
		b.WriteString("Guidelines:\n")
		switch pl {
		case progLangGo:
			b.WriteString("- Read and analyze *.go source files and project docs\n")
		case progLangPython:
			b.WriteString("- Read and analyze *.py source files and project docs\n")
		case progLangJSTS:
			b.WriteString("- Read and analyze *.ts, *.tsx, *.js, *.jsx source files and project docs\n")
		default:
			b.WriteString("- Read and analyze source files and project docs\n")
		}
		b.WriteString("- Identify files that need changes\n")
		b.WriteString("- Propose a step-by-step plan\n")
		b.WriteString("- Estimate complexity and risks\n")
		b.WriteString("- Be concise and actionable\n")
		b.WriteString("- Verification section MUST only include commands that match the actual project stack and task scope. Do NOT invent commands for tools, servers, linters or formatters not present in the project.\n")
		switch pl {
		case progLangGo:
			b.WriteString("- Reference Effective Go, Go Code Review Comments, Go Common Mistakes and project conventions\n")
		case progLangPython:
			b.WriteString("- Reference PEP 8, PEP 20 and project conventions\n")
		case progLangJSTS:
			b.WriteString("- Reference TypeScript best practices and project conventions\n")
		default:
			// No language-specific best practices when language is unknown.
		}
		if a.hasRAG {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and %s marked [%s].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("These are NOT suggestions — they are REQUIREMENTS. Treat violations as bugs.\n")
			b.WriteString("These include naming conventions, error handling, code structure, and all documented standards.\n")
			b.WriteString("You MUST check code against ALL provided rules. Include violations in your plan.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if a.hasSnippets {
			b.WriteString("\nIMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("You MUST call snippets BEFORE creating the plan. This is not optional.\n")
			switch pl {
			case progLangGo:
				b.WriteString("1. Call snippets(paths=[<list of all .go files you read>]) to get code conventions\n")
			case progLangPython:
				b.WriteString("1. Call snippets(paths=[<list of all .py files you read>]) to get code conventions\n")
			default:
				b.WriteString("1. Call snippets(paths=[<list of all source files you read>]) to get code conventions\n")
			}
			b.WriteString("2. Read and understand the found conventions\n")
			b.WriteString("3. Only then create the plan, incorporating found conventions as requirements\n")
		}
	case ModeChat:
		b.WriteString("You are in CHAT mode.\n")
		b.WriteString("Answer questions, explain code, discuss architecture and design decisions.\n")
		b.WriteString("You can read files for context using read-only tools: " + strings.Join(a.reg.NamesFiltered(a.readOnlyTools()), ", ") + "\n")
		b.WriteString("Do NOT create numbered plans, do NOT write or edit files, do NOT call write tools.\n")
		b.WriteString("Be concise and helpful.\n")
	default:
		b.WriteString("You are in EDIT mode. EDIT mode is for ACTING on the code, not for describing changes.\n\n")
		b.WriteString("CRITICAL — Action vs description:\n")
		b.WriteString("- If the user's input is a TASK to change code (fix, refactor, create, edit, move, restructure, apply plan, implement, ...) you MUST start your response with tool calls (write_file, edit_file, bash, read_file as needed). Do NOT output a markdown plan, do NOT write \"ANALYSIS\" / \"АНАЛИЗ\" or \"IMPLEMENTATION PLAN\" / \"ПЛАН ИСПРАВЛЕНИЙ\" sections, do NOT explain what you are about to do — just call the tools.\n")
		b.WriteString("- A textual response without tool calls is allowed ONLY when the user asked a pure question (no action verb). When in doubt, assume it is a task and call tools.\n")
		b.WriteString("- If you need to read a file before editing it, call read_file as the first tool — that still counts as \"starting with a tool\".\n")
		b.WriteString("- If the user pasted a numbered plan, your job is to EXECUTE it, not to rewrite it back. Skip directly to the first edit_file/write_file. Do not produce an \"Implementation plan\" / \"План исправлений\" of your own.\n")
		b.WriteString("- PLAN mode is the place for descriptions. EDIT mode is for actions. Stay in your lane.\n\n")
		b.WriteString("Available tools: " + strings.Join(a.reg.Names(), ", ") + "\n\n")
		b.WriteString("Guidelines:\n")
		switch pl {
		case progLangGo:
			b.WriteString("- Write idiomatic Go code following Effective Go, Go Code Review Comments, Go Common Mistakes. Use Go 1.25+.\n")
			b.WriteString("- Handle errors properly\n")
		case progLangPython:
			b.WriteString("- Write idiomatic Python code following PEP 8 and project conventions.\n")
			b.WriteString("- Handle errors properly\n")
		case progLangJSTS:
			b.WriteString("- Write idiomatic TypeScript/JavaScript code following project conventions.\n")
			b.WriteString("- Handle errors properly\n")
		default:
			// No language-specific guidelines when language is unknown.
		}
		b.WriteString("- Use edit_file for targeted changes, write_file for new files\n")
		b.WriteString("- Be concise in responses\n")
		b.WriteString("- Do NOT repeat or quote file contents in your responses. Reference files by path only.\n")
		if a.hasRAG {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and %s marked [%s].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("These are REQUIREMENTS, not suggestions. Apply them to every line you write.\n")
			b.WriteString("These include naming conventions, error handling, code structure, and all documented standards.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if a.hasSnippets {
			b.WriteString("\nIMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("You MUST call snippets BEFORE writing or modifying any file. This is not optional.\n")
			b.WriteString("1. Call snippets(paths=[<file_paths>]) to get code conventions\n")
			b.WriteString("2. Read and understand the found conventions\n")
			b.WriteString("3. Only then write/edit code, following ALL found conventions (naming, structure, patterns, error handling)\n")
			b.WriteString("4. If no snippets match, proceed without conventions\n")
		}
		if ltc.buildTool != "" || ltc.lintTool != "" || ltc.testTool != "" {
			b.WriteString("\nAfter completing EVERY task you MUST run this verification sequence:\n")
			step := 1
			if ltc.buildTool != "" {
				fmt.Fprintf(&b, "%d. Run %s to verify compilation. If errors — fix them and re-run.\n", step, ltc.buildTool)
				step++
			}
			if ltc.lintTool != "" {
				fmt.Fprintf(&b, "%d. Run %s to check code quality. If errors — fix them and re-run.\n", step, ltc.lintTool)
				step++
			}
			if ltc.testTool != "" {
				fmt.Fprintf(&b, "%d. Run %s to verify correctness. If errors — fix them and re-run.\n", step, ltc.testTool)
				step++
			}
			fmt.Fprintf(&b, "%d. Update AGENTS.md if you changed architecture, added/removed files, or modified public APIs.\n", step)
			b.WriteString("   Use read_file to read AGENTS.md first, then edit_file to update only the relevant sections.\n")
			b.WriteString("   Do NOT rewrite the entire file — only update what changed.\n")
		}
	}

	// Repeat language directive at the end for reinforcement (important for local models).
	if lang != langEnglish {
		fmt.Fprintf(&b, "\nREMINDER: You MUST respond in %s. Never switch to any other language.\n", lang)
	}

	return b.String()
}

// buildProjectContext returns project-level context (AGENTS.md, go.mod) that
// is prepended to the conversation so the model has anchor information about
// the workspace.
func (a *Agent) buildProjectContext() string {
	var b strings.Builder

	agentsMD := filepath.Join(a.workDir, "AGENTS.md")
	if data, err := os.ReadFile(agentsMD); err == nil {
		b.WriteString("Project documentation (AGENTS.md):\n")
		b.Write(data)
		b.WriteString("\n\n")
	}

	goMod := filepath.Join(a.workDir, "go.mod")
	if data, err := os.ReadFile(goMod); err == nil {
		b.WriteString("go.mod:\n```\n")
		b.Write(data)
		b.WriteString("\n```\n\n")
	}

	return b.String()
}
