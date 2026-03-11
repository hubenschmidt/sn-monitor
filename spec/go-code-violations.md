# Go Code Rule Violations

Violations of `/go-code-rules`: no `continue`, `break`, `switch`, `else`, nested conditionals.

## devices.go

| Line | Violation | Fix |
|------|-----------|-----|
| 26 | `continue` in loop filter | Combine two guards into single `if !(…) { append }` |
| 30 | `continue` in loop filter | Same — merge with above |

## solve.go (Anthropic provider)

| Line | Violation | Fix |
|------|-----------|-----|
| 61–62 | `continue` — content_block_delta filter | Single guard: `if type == … && delta == … { write+call }` |
| 64–65 | `continue` — text_delta filter | Merged into above |
| 100–101 | `continue` (Summarize) | Same pattern |
| 103–104 | `continue` (Summarize) | Same |
| 130–131 | `continue` (FollowUp) | Same |
| 133–134 | `continue` (FollowUp) | Same |

## solve_openai.go (OpenAI provider)

No `continue`/`break` but stream loops use separate `if` blocks for delta vs completed that should be a single filter for consistency.

## main.go

| Line | Violation | Fix |
|------|-----------|-----|
| 670–742 | `handleAction` — long sequential if-return chain | Refactor to map[HotkeyAction]func() dispatch |
| 781 | `} else {` in handleProcess | Guard clause: `if hasScreen { …; return }` then fallthrough |
| 731–736 | Inline mic cleanup in HotkeyClear | Extract to `stopMic()` helper |
