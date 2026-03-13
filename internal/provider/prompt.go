package provider

import (
	"fmt"
	"strings"
)

const CodeRules = `**Code rules (defaults — the user's instructions override these if they conflict):**
- KISS — keep solutions simple and straightforward.
- YAGNI — only implement what is needed, no speculative features.
- Use guard clauses and early returns instead of if/else chains.
- Use object/map lookups instead of switch-case or nested conditionals.
- Flatten all conditional logic — no nested if, else, or switch. No continue or break.
- Prefer small, focused functions over large functions with many branches.
- Group imports: stdlib, external, internal (with blank lines between groups).
- If JavaScript/TypeScript: use modern ES6+ syntax (arrow functions, const/let, template literals, destructuring, for...of).
- Be concise — avoid filler and unnecessary elaboration.`

func BuildContextReceipt(imageCount int, hasContext bool, hasTranscript bool) string {
	parts := []string{}
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("%d screenshot(s)", imageCount))
	}
	if hasContext {
		parts = append(parts, "source files")
	}
	if hasTranscript {
		parts = append(parts, "audio transcript")
	}
	if len(parts) == 0 {
		return ""
	}
	return "**You have been provided the following context: " + strings.Join(parts, ", ") +
		".** Begin your response with a `> Context:` line confirming each piece of context you received (e.g. number of screenshots, source file names, transcript presence). Then proceed with your answer.\n\n"
}

func BuildSolvePrompt(lang, contextText, transcript string, imageCount int) string {
	// 1. Context receipt
	prompt := BuildContextReceipt(imageCount, contextText != "", transcript != "")

	// 2. All user context together — equal weight
	prompt += "Analyze all of the following inputs together. Each input modality (screenshots, source files, audio transcript) carries equal weight. The user's instructions from any modality always take priority over default code rules or formatting guidelines — never refuse or override what the user asks for.\n\n"
	if contextText != "" {
		prompt += "**Source files:**\n\n" + contextText + "\n\n"
	}
	if transcript != "" {
		prompt += "**Audio transcript:**\n\n" + transcript + "\n\n"
	}
	if imageCount > 0 {
		prompt += "**Screenshots:** See the attached image(s).\n\n"
	}

	prompt += "Solve the problem in **" + lang + "** based on the inputs above.\n\n"

	// 3. Formatting — applied after solving
	prompt += `**After solving, format your response as follows:**

` + CodeRules + `

Start with the **Goal** (one-line summary), then a **Problem Description** paragraph explaining the problem in plain language — what the input looks like, what the expected output is, and any key constraints or gotchas. Follow the Problem Description with a **TLDR** — a single sentence that captures the essence of the problem in the simplest possible terms. Then a **Solution Description** paragraph summarizing at a high level how the problem is solved (the core idea/technique). Follow the Solution Description with a **TLDR** — a single sentence that captures the core approach in the simplest possible terms.

Then provide: pseudocode, then full ` + lang + ` code, then explain how it works, time/space complexity, and edge cases.

**JavaScript style rule:** If the language is JavaScript (ECMAScript 6) or later, you MUST use modern ES6+ syntax throughout: arrow functions, const/let (never var), template literals, destructuring, spread/rest operators, Map/Set where appropriate, and for...of instead of index-based loops. Never use the function keyword for callbacks or inline functions.

**Control flow rule:** Use guard clauses and early returns instead of if/else chains. Use object/map lookups instead of switch-case or nested conditionals. Flatten all conditional logic — no nested if, else, or switch.

All code must be written in ` + lang + `. Be concise — avoid filler and unnecessary elaboration. If it's a continuation of a previous problem, build on your prior answer.`
	return prompt
}
