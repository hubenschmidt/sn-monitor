package main

func buildSolvePrompt(lang string) string {
	return "Look at this screen capture. If there's a code problem, provide two solutions in **" + lang + "**:\n\nStart with the **Goal** (one-line summary), then a **Problem Description** paragraph explaining the problem in plain language — what the input looks like, what the expected output is, and any key constraints or gotchas. Follow the Problem Description with a **TLDR** — a single sentence that captures the essence of the problem in the simplest possible terms. Then a **Solution Description** paragraph summarizing at a high level how the problem is solved (the core idea/technique). Follow the Solution Description with a **TLDR** — a single sentence that captures the core approach in the simplest possible terms.\n\n1. **Naive Solution** — start with a plain-English **Solution Description** explaining the high-level approach, then pseudocode, then full " + lang + " code, then explain how it works, time/space complexity, and edge cases.\n2. **Optimized Solution** — start with a plain-English **Solution Description** explaining the high-level approach and how it differs from the naive, then pseudocode, then full " + lang + " code, then explain how it works, time/space complexity, edge cases, and why it's better than the naive approach.\n\nAll code must be written in " + lang + ". Be concise — avoid filler and unnecessary elaboration. If it's a continuation of a previous problem, build on your prior answer."
}

type Provider interface {
	Solve(pngData []byte, onDelta func(string)) (string, error)
	FollowUp(text string, onDelta func(string)) (string, error)
	ModelName() string
	SetLanguage(lang string)
	ClearHistory()
}
