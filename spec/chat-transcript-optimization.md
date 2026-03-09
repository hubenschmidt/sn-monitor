# Chat / Transcript Separation — Feature Specification

## 1. Current State

The overlay (`overlay.go`) has a single `#content` div that serves double-duty:

- **Chat output** — LLM responses from `Solve`, `FollowUp`, streamed via `StreamDelta`/`AppendStreamDelta`.
- **Transcript view** — toggled manually via `→↑ transcript` (`HotkeyToggleView`). Swaps `#content` innerHTML to show accumulated transcript, stashes prior HTML in `window._savedContent`.
- **Mixed concerns** — mic recording, audio capture, transcript accumulation, and LLM chat all write to the same status bar and share the same content area. There is no visual indication of what is "live" vs. historical.

### Pain points

- Toggling between transcript and chat loses scroll position and context.
- No way to see transcript accumulating in real time while also viewing chat.
- "Send" label (`→↓ send`) is ambiguous — it sends the transcript buffer to the LLM, not a chat message.
- "Explain" (`←↑`) is a global hotkey/button that only makes sense right after a solve response.
- Recording state is only shown by relabeling the mic button — no persistent visual indicator for audio capture.

## 2. Target Architecture

### 2.1 Tabbed View

Replace the single `#content` div with a two-tab layout:

| Tab | Default | Contents |
|-----|---------|----------|
| **Chat** | Yes | LLM output only (solve responses, follow-ups). Append-only chronological feed. |
| **Transcript** | No | Live-updating labeled audio/mic transcript. Auto-scrolls as chunks arrive. |

Tab bar sits between `#drag-handle` and the content area. Active tab is visually highlighted. Inactive tab shows a badge count of new items since last viewed.

### 2.2 Chat Tab

- All `StreamStart`/`StreamDelta`/`StreamDone` and `AppendStream*` output renders here.
- Each LLM response block gets a `?` icon (top-right corner) that triggers "explain further" for **that specific response** — replaces the global `←↑ explain` hotkey.
- Clicking `?` calls `Provider.FollowUp("Explain further in more detail.")` and appends the response below the clicked block.

### 2.3 Transcript Tab

- Two sub-sections, each with a source label:
  - `audio:` — system audio chunks from `AudioCapture.TranscribeNow()`
  - `mic:` — mic recording chunks from `handleMicStop()`
- Chunks rendered as they arrive (push model — `TranscribeNow` and `handleMicStop` call a new `Renderer.AppendTranscriptChunk(source, text)` method).
- Each chunk shows a timestamp and source tag:
  ```
  [14:32:05 audio] The candidate should implement a binary search...
  [14:32:18 mic]   Can you repeat the constraint on input size?
  ```
- Auto-scrolls to bottom. No manual toggle needed — the tab is always live.

### 2.4 Recording Indicators

| Element | Idle | Active |
|---------|------|--------|
| Audio button (`←↓ audio`) | Default styling | Red background + pulsing dot CSS animation |
| Mic button (`↑↓ mic`) | Default styling | Red background + pulsing dot CSS animation (already partially implemented via `SetMicRecording`) |

Both indicators are always visible in the footer regardless of active tab.

### 2.5 "Send" Renamed to "Process"

- Button label changes from `→↓ send` to `→↓ process`.
- Semantics: sends the full transcript buffer **plus** existing chat context to the LLM.
- `keyLabels[HotkeyAudioSend]` updated to `"→↓ process"`.

### 2.6 Explain → Per-Response `?` Icon

- Remove `HotkeyExplain` from `keyOrder` (no longer a footer button or global hotkey).
- Remove the `←↑` key combo from `processEvent`.
- Each rendered LLM response in the Chat tab gets a clickable `?` icon that sends "explain further" scoped to that response.
- The `?` icon calls `_action('explain')` with a response index, so the handler knows which response to reference.

### 2.7 Phase 2: Speaker Diarization

- Mic input gains speaker diarization (identify distinct speakers in multi-person mic audio).
- Transcript chunks tagged with speaker labels: `mic/speaker-1:`, `mic/speaker-2:`.
- Requires a diarization model or whisper-server extension — out of scope for Phase 1.

## 3. Files Affected

| File | Change | Description |
|------|--------|-------------|
| `renderer.go` | Modify | Add `AppendTranscriptChunk(source, text string)` to `Renderer` interface. Add `SetAudioRecording(recording bool)`. Remove `ShowTranscript`/`RestoreChat` (replaced by tabs). |
| `overlay.go` | Modify | Replace single `#content` with tab layout HTML/CSS/JS. Add tab switching logic. Implement `AppendTranscriptChunk` to push labeled chunks to Transcript tab. Add `?` icon to each chat response block. Add pulsing red dot CSS for both recording indicators. Implement `SetAudioRecording`. Remove `ShowTranscript`/`RestoreChat`. |
| `hotkey.go` | Modify | Remove `HotkeyExplain` and `HotkeyToggleView` from `keyOrder`, `keyLabels`, `actionNames`. Remove `←↑` and `→↑` combos from `processEvent`. |
| `main.go` | Modify | Remove `viewingTranscript` state. Remove `handleToggleView` and `handleExplain`. Update `handleAction` to drop those cases. Update `handleMicStop` to call `renderer.AppendTranscriptChunk("mic", text)`. Rename "send" label to "process" in `helpLines`. |
| `continuous.go` | Modify | `TranscribeNow` calls `renderer.AppendTranscriptChunk("audio", text)` after each successful transcription. |
| `provider.go` | No change | `FollowUp` interface already supports the `?` explain flow. |

## 4. Data Flow

```
                    ┌──────────────────────────────────────────────┐
                    │                  Overlay                      │
                    │                                              │
                    │   ┌──────────┐         ┌───────────────┐    │
                    │   │ Chat Tab │         │ Transcript Tab │    │
                    │   │ (default)│         │               │    │
                    │   └────▲─────┘         └──────▲────────┘    │
                    │        │                      │             │
                    │        │ StreamDelta/          │ AppendTranscript │
                    │        │ AppendStreamDelta     │ Chunk("audio"|"mic") │
                    └────────┼──────────────────────┼─────────────┘
                             │                      │
               ┌─────────────┴──────┐    ┌──────────┴──────────┐
               │   LLM Pipeline     │    │  Transcription       │
               │                    │    │  Pipeline            │
               │  Provider.Solve()  │    │                      │
               │  Provider.FollowUp │    │  AudioCapture        │
               │  ("?" icon click)  │    │  .TranscribeNow()    │
               │                    │    │  → "audio:" chunks   │
               └────────▲───────────┘    │                      │
                        │                │  handleMicStop()     │
                        │                │  → "mic:" chunks     │
                   ┌────┴────┐           └──────────────────────┘
                   │ Process │                     ▲
                   │ button  │                     │
                   │ (→↓)    │        ┌────────────┴────────────┐
                   └────┬────┘        │     Audio Sources        │
                        │             │                          │
                        │             │  System audio (pactl     │
                        └─────────────│  monitor → Recorder)     │
                     sends transcript │                          │
                     + chat context   │  Mic (PortAudio default  │
                     to LLM           │  input → Recorder)       │
                                      └──────────────────────────┘
```

### Key separation

1. **Transcript pipeline** writes to the Transcript tab only — never to Chat.
2. **LLM pipeline** writes to the Chat tab only — never to Transcript.
3. **Process button** bridges the two: reads transcript buffer, sends to LLM, response appears in Chat.
4. **`?` explain icon** is scoped to a single Chat response — no global hotkey needed.
