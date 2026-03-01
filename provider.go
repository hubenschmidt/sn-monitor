package main

const solvePrompt = "Look at this screen capture. If there's a code problem, provide two solutions:\n\n1. **Naive Solution** — pseudocode, then full code, then explain how it works, time/space complexity, and edge cases.\n2. **Optimized Solution** — pseudocode, then full code, then explain how it works, time/space complexity, edge cases, and why it's better than the naive approach.\n\nIf it's a continuation of a previous problem, build on your prior answer."

type Provider interface {
	Solve(pngData []byte, onDelta func(string)) (string, error)
	ModelName() string
}
