# ASR Voice Follow-Up — Feature Specification

## 1. Problem Statement

sn-monitor captures a screen, sends the image to an LLM, and renders the solution. The interaction is currently one-shot per capture: Left+Right hotkey triggers capture → solve → render. There is no way to ask follow-up questions without re-capturing and relying on conversation history to infer context.

Voice follow-ups solve this:

- **"Explain further"** — user presses a hotkey, speaks a clarification, and the LLM responds using the existing conversation context (no new screenshot).
- **"Solve with new capture"** — existing Left+Right flow, unchanged.
- **Faster iteration** — voice is faster than typing, and sn-monitor has no text input surface (overlay is read-only, terminal is occupied by the event loop).

## 2. Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      sn-monitor                          │
│                                                          │
│  ┌──────────┐   ┌───────────┐   ┌────────────────────┐  │
│  │ hotkey.go │──▶│  main.go  │──▶│ Provider.Solve()   │  │
│  │          │   │           │   │ Provider.FollowUp() │  │
│  └──────────┘   └─────┬─────┘   └────────────────────┘  │
│                        │                                  │
│  ┌──────────┐   ┌──────┴──────┐   ┌──────────────────┐  │
│  │ wav.go   │◀──│  asr.go     │──▶│  whisper-server   │  │
│  │ (encode) │   │ (Recorder,  │   │  :8178             │  │
│  └──────────┘   │  Transcribe)│   │  POST /inference   │  │
│                  └─────────────┘   └──────────────────┘  │
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │              Renderer (overlay / terminal)        │    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
```

The ASR pipeline slots between hotkey detection and the existing Provider interface. Voice audio is captured locally via PortAudio, encoded to WAV in-memory, POSTed to whisper-server for transcription, then passed to `Provider.FollowUp()`.

## 3. Flows

### 3.1 Screen Capture Flow (existing, unchanged)

```
User presses Left+Right
  → captureMonitor(idx) → PNG bytes
  → Provider.Solve(png, onDelta)
  → Renderer.StreamStart/Delta/Done
```

### 3.2 Voice Follow-Up Flow (new)

```
User presses Up+Down (hold to record, release to send)
  → hotkey.go sends HotkeyFollowUp on hotkeyAction channel
  → main.go: handleFollowUp()
    → Renderer.SetStatus("recording...")
    → Recorder.Start() — PortAudio begins capturing 16kHz mono
    → (user speaks while holding keys)
    → hotkey.go detects both keys released → sends HotkeyStopRecord
    → Recorder.Stop() → []int16 PCM samples
    → wav.Encode(samples, 16000) → []byte WAV
    → asr.Transcribe(wavBytes) → transcript string
    → (if confidence < threshold or empty, Renderer.SetStatus("no speech"), return)
    → Renderer.SetStatus("thinking...")
    → Provider.FollowUp(transcript, onDelta)
    → Renderer.StreamStart/Delta/Done
```

### 3.3 "Explain Further" Flow (new, shortcut)

```
User presses Left+Up
  → hotkey.go sends HotkeyExplain on hotkeyAction channel
  → main.go: handleExplain()
    → Provider.FollowUp("Explain further in more detail.", onDelta)
    → Renderer.StreamStart/Delta/Done
```

This is a convenience shortcut that skips ASR entirely and sends a canned follow-up prompt.

## 4. Hotkey Mapping

| Combo | Constant | Action |
|-------|----------|--------|
| Left+Right arrows | `HotkeyCapture` | Screen capture → solve (existing) |
| Up+Down arrows (hold) | `HotkeyFollowUp` | Start voice recording |
| Up+Down arrows (release) | `HotkeyStopRecord` | Stop recording → transcribe → follow-up |
| Left+Up arrows | `HotkeyExplain` | Send canned "explain further" follow-up |

### Key Code Constants

```go
keyLeft  = 105  // KEY_LEFT  (existing)
keyRight = 106  // KEY_RIGHT (existing)
keyUp    = 103  // KEY_UP    (new)
keyDown  = 108  // KEY_DOWN  (new)
```

### Channel Change

Currently `listenHotkey` writes to `chan struct{}` — a single undifferentiated signal. This changes to a typed action channel:

```go
type HotkeyAction int

const (
    HotkeyCapture    HotkeyAction = iota  // Left+Right
    HotkeyFollowUp                         // Up+Down pressed
    HotkeyStopRecord                       // Up+Down released
    HotkeyExplain                          // Left+Up
)

func listenHotkey(ch chan<- HotkeyAction) error
```

## 5. Provider Interface Change

Current:

```go
type Provider interface {
    Solve(pngData []byte, onDelta func(string)) (string, error)
    ModelName() string
}
```

New:

```go
type Provider interface {
    Solve(pngData []byte, onDelta func(string)) (string, error)
    FollowUp(text string, onDelta func(string)) (string, error)
    ModelName() string
}
```

### Implementation Notes

| Provider | `FollowUp` Strategy |
|----------|---------------------|
| `AnthropicProvider` | Append `text` as a user message to `p.history`, call `Messages.NewStreaming` with full history (no image block). Append assistant response to history on success. |
| `OpenAIProvider` | Send `text` as a user message with `PreviousResponseID` set. Server-side conversation continues. |

`FollowUp` reuses the existing conversation state — no new screenshot is attached. If `Solve` has not been called yet (no prior context), `FollowUp` returns an error.

## 6. ASR Integration — whisper-server

sn-monitor already depends on a running whisper-server instance (same one used in `asr-llm-tts-poc`).

### Transcribe Request

```
POST http://localhost:8178/inference
Content-Type: multipart/form-data

Fields:
  file:              WAV audio (16kHz, mono, 16-bit PCM)
  response_format:   "json"
  temperature:       "0.0"
```

### Transcribe Response

```json
{
  "text": "Can you explain the time complexity of the second solution?"
}
```

### `asr.go` — Public API

```go
func Transcribe(wavData []byte, whisperURL string) (string, error)
```

- Builds multipart form, POSTs to `whisperURL + "/inference"`.
- Parses JSON response, returns trimmed `text` field.
- Returns error on HTTP failure or empty response.

### Configuration

| Env Var | Default | Purpose |
|---------|---------|---------|
| `WHISPER_URL` | `http://localhost:8178` | whisper-server base URL |

## 7. Audio Capture — PortAudio

### `asr.go` — Recorder Struct

```go
type Recorder struct {
    stream   *portaudio.Stream
    samples  []int16
    mu       sync.Mutex
    done     chan struct{}
}

func NewRecorder() *Recorder
func (r *Recorder) Start() error   // opens PortAudio stream, 16kHz mono
func (r *Recorder) Stop() []int16  // stops stream, returns captured samples
```

### Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Sample rate | 16000 Hz | whisper-server expects 16kHz |
| Channels | 1 (mono) | Speech, single mic |
| Sample format | int16 | Standard PCM, matches WAV output |
| Frames per buffer | 1024 | ~64ms chunks, good latency/overhead balance |

### Lifecycle

1. `NewRecorder()` — allocates struct, no PortAudio calls yet.
2. `Start()` — calls `portaudio.Initialize()`, opens default input stream, begins appending samples to `r.samples` in the callback.
3. `Stop()` — closes stream, calls `portaudio.Terminate()`, returns accumulated `[]int16`.

PortAudio `Initialize`/`Terminate` are called per-recording to avoid holding system resources while idle.

## 8. WAV Encoding — `wav.go`

```go
func EncodeWAV(samples []int16, sampleRate int) []byte
```

Writes a minimal 44-byte RIFF/WAV header + raw PCM data to a `bytes.Buffer`:

| Field | Value |
|-------|-------|
| AudioFormat | 1 (PCM) |
| NumChannels | 1 |
| SampleRate | 16000 |
| BitsPerSample | 16 |
| ByteRate | 32000 |
| BlockAlign | 2 |

No external WAV library needed — the header is 44 bytes of fixed-layout binary.

## 9. File Changes

| File | Change |
|------|--------|
| `wav.go` | **New.** `EncodeWAV(samples, rate) []byte` |
| `asr.go` | **New.** `Recorder` struct + `Transcribe()` function |
| `provider.go` | Add `FollowUp(text string, onDelta func(string)) (string, error)` to interface |
| `solve.go` | Implement `FollowUp` on `AnthropicProvider` |
| `solve_openai.go` | Implement `FollowUp` on `OpenAIProvider` |
| `hotkey.go` | Add `HotkeyAction` type, `keyUp`/`keyDown` constants, multi-combo detection, typed channel |
| `main.go` | Switch on `HotkeyAction`, add `handleFollowUp()` and `handleExplain()`, load `WHISPER_URL` |

## 10. Dependencies

| Dependency | Type | Purpose |
|------------|------|---------|
| `gordonklaus/portaudio` | Go module | PortAudio bindings for audio capture |
| `libportaudio-dev` | System (apt) | PortAudio C library |

Install:

```bash
sudo apt install libportaudio-dev
go get github.com/gordonklaus/portaudio
```

### Build

Existing build command unchanged — PortAudio links via CGo automatically. The existing `PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH` shim is unaffected (PortAudio has its own `.pc` file from the system package).

## 11. Verification

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | `sudo apt install libportaudio-dev` | Package installs cleanly |
| 2 | `go get github.com/gordonklaus/portaudio` | Module added to go.mod |
| 3 | Build: `PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH go build` | Compiles without errors |
| 4 | Start whisper-server on `:8178` | Server healthy |
| 5 | Run sn-monitor, press Left+Right | Existing capture flow works (regression check) |
| 6 | Press Up+Down (hold), speak, release | Status shows "recording..." → "thinking..." → streamed response appears |
| 7 | Press Left+Up | "Explain further" follow-up streams without recording |
| 8 | Press Up+Down with no speech | Status shows "no speech detected", no API call |
