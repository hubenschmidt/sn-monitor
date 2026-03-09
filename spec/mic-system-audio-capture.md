# Multi-Source Audio Capture + Continuous Transcription — Feature Specification

## 1. Problem Statement

sn-monitor's voice follow-up (spec/asr.md) captures mic-only audio for push-to-talk follow-up questions. For meeting transcription (Google Meet, Zoom, Teams), we need to capture:

- **System audio** — what other participants are saying (routed through PulseAudio/PipeWire sinks)
- **Mic audio** — what the local user is saying
- **Both mixed** — a single stream combining all voices for transcription

Additionally, push-to-talk is insufficient for meeting transcription. A **continuous recording mode** is needed that chunks audio at fixed intervals and streams transcripts in real time.

## 2. Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                        sn-monitor                             │
│                                                               │
│  ┌────────────┐    ┌──────────────────────────┐               │
│  │ devices.go │───▶│        Recorder           │              │
│  │ enumerate  │    │  mode: mic|system|both    │              │
│  │ + select   │    │                           │              │
│  └────────────┘    │  micStream ──┐            │              │
│                    │              ├─▶ mixer ──▶ samples[]     │
│                    │  monStream ──┘            │              │
│                    │  (stereo→mono downmix)    │              │
│                    └──────────┬───────────────┘              │
│                               │                               │
│               ┌───────────────┼───────────────┐               │
│               │               │               │               │
│        push-to-talk    DrainSamples()   continuous mode       │
│        (Up+Down hold)        │          (Right+Down toggle)   │
│               │               │               │               │
│               ▼               ▼               ▼               │
│         ┌──────────┐   ┌──────────┐   ┌──────────────────┐   │
│         │ wav.go   │   │ wav.go   │   │ continuous.go    │   │
│         │ Encode   │   │ Encode   │   │ 30s chunk ticker │   │
│         └────┬─────┘   └────┬─────┘   │ RMS speech gate  │   │
│              │              │         └────────┬─────────┘   │
│              ▼              ▼                  ▼               │
│         ┌────────────────────────────────────────┐            │
│         │  asr.go — Transcribe() → whisper POST  │            │
│         └────────────────────────────────────────┘            │
│                          │                                     │
│                          ▼                                     │
│         ┌────────────────────────────────────────┐            │
│         │  Renderer (overlay / terminal)          │            │
│         └────────────────────────────────────────┘            │
└───────────────────────────────────────────────────────────────┘
```

## 3. Capture Modes

| Mode | Source | Use Case |
|------|--------|----------|
| `mic` | Default input device | Voice follow-ups (existing) |
| `system` | PulseAudio monitor source | Transcribe meeting audio only |
| `both` | Mic + monitor, mixed | Full meeting transcription (all speakers) |

Selected at startup via CLI menu (same pattern as monitor/model/renderer selection).

## 4. Flows

### 4.1 Push-to-Talk Follow-Up (existing, mode-aware)

```
User presses Up+Down (hold)
  → Recorder.Start() opens stream(s) based on mode
  → (user speaks / meeting audio plays)
  → Up+Down released → Recorder.Stop() → samples
  → EncodeWAV → Transcribe → Provider.FollowUp
```

Unchanged flow, but Recorder now opens the appropriate stream(s) for the selected mode.

### 4.2 Continuous Transcription (new)

```
User presses Right+Down
  → ContinuousTranscriber.Toggle()
  → IF starting:
      → Renderer.SetStatus("continuous transcription ON")
      → Recorder.Start()
      → chunkLoop goroutine begins:
          every 30s:
            samples = Recorder.DrainSamples()
            if rms(samples) < silenceThreshold → skip
            wav = EncodeWAV(samples)
            text = Transcribe(wav)
            append to running transcript
            Renderer.StreamDelta(text)
  → IF stopping:
      → Recorder.Stop()
      → Renderer.SetStatus("continuous transcription OFF")
```

Push-to-talk is disabled while continuous mode is active.

## 5. Hotkey Mapping (updated)

| Combo | Constant | Action |
|-------|----------|--------|
| Left+Right | `HotkeyCapture` | Screen capture → solve (existing) |
| Up+Down (hold) | `HotkeyFollowUp` / `HotkeyStopRecord` | Voice follow-up (existing) |
| Left+Up | `HotkeyExplain` | Canned "explain further" (existing) |
| **Right+Down** | **`HotkeyContinuous`** | **Toggle continuous transcription (new)** |

## 6. Device Enumeration

### PulseAudio/PipeWire Monitor Sources

Every PulseAudio sink has a corresponding `.monitor` source that captures all audio playing through it. These appear as input devices in PortAudio.

```
pactl list short sources
→ alsa_output.pci-0000_c9_00.6.analog-stereo.monitor
→ alsa_output.pci-0000_c9_00.1.hdmi-stereo-extra1.monitor
```

### `ListMonitorSources()` Logic

```go
func ListMonitorSources() ([]*portaudio.DeviceInfo, error)
```

- Calls `portaudio.Devices()` (PortAudio already initialized in `main()`)
- Filters: name contains `.monitor` or `Monitor of`, and `MaxInputChannels > 0`
- Returns matching devices

### `SelectAudioMode()` Logic

```go
func SelectAudioMode(scanner *bufio.Scanner) (CaptureMode, *portaudio.DeviceInfo, error)
```

- Prints: `1: Mic only  2: System audio  3: Mic + System`
- If user picks system or both → calls `ListMonitorSources()`
- If zero sources → error
- If one source → auto-select
- If multiple → numbered list for user to pick

## 7. Recorder Refactor

### Struct

```go
type CaptureMode int

const (
    CaptureModeMic    CaptureMode = iota
    CaptureModeSystem
    CaptureModeBoth
)

type Recorder struct {
    mode      CaptureMode
    monDevice *portaudio.DeviceInfo
    micStream *portaudio.Stream
    monStream *portaudio.Stream
    mu        sync.Mutex
    samples   []int16
    stopCh    chan struct{}
    stopped   bool
}
```

### `NewRecorder(mode, monDevice)` Constructor

Stores mode and device reference. No PortAudio calls.

### `Start()` — Opens Streams Based on Mode

| Mode | Mic Stream | Monitor Stream |
|------|-----------|----------------|
| `mic` | `OpenDefaultStream(1, 0, 16000, 1024, buf)` | — |
| `system` | — | `OpenStream(StreamParameters{Device: monDevice})` |
| `both` | `OpenDefaultStream(...)` | `OpenStream(...)` |

Monitor stream may be stereo (2 channels) — buffer sized accordingly.

Launches the appropriate read loop goroutine:
- `readLoopMic(buf)` — appends mic samples
- `readLoopMonitor(buf, channels)` — downmixes stereo→mono, appends
- `readLoopDual(micBuf, monBuf, channels)` — reads both, mixes, appends

### `Stop()` — Closes Open Streams

Closes `r.stopCh`, stops/closes whichever streams are non-nil.

### `DrainSamples()` — For Continuous Mode

```go
func (r *Recorder) DrainSamples() []int16
```

Copies `r.samples` and resets to empty under lock. Does NOT stop recording.

## 8. Audio Mixing

### Stereo→Mono Downmix

```go
func downmixToMono(buf []int16, channels int) []int16
```

For interleaved stereo: `mono[i] = (left + right) / 2` using int32 math.
If `channels == 1`, returns a copy unchanged.

### Dual-Stream Mix

```go
func mixSamples(a, b []int16) []int16
```

Averages two mono buffers sample-by-sample: `mixed[i] = (a[i] + b[i]) / 2`.
Uses the shorter buffer's length.

## 9. Continuous Transcriber

```go
type ContinuousTranscriber struct {
    recorder   *Recorder
    whisperURL string
    renderer   Renderer
    active     atomic.Bool
}
```

### `Toggle()`

Starts if inactive, stops if active. Shows status via renderer.

### `chunkLoop()`

Runs in a goroutine after `Start()`:

1. `time.NewTicker(30 * time.Second)`
2. On each tick: `samples = recorder.DrainSamples()`
3. Compute `rms(samples)` — if below threshold, skip (silence)
4. `wav = EncodeWAV(samples, 16000)`
5. `text, err = Transcribe(wav, whisperURL)`
6. Render transcript chunk via renderer

### RMS Speech Gate

```go
func rms(samples []int16) float64
```

`sqrt(sum(s^2) / N)` — if below ~500 (configurable), chunk is silence.

## 10. PortAudio Lifecycle Change

**Before**: `Initialize()` in `Recorder.Start()`, `Terminate()` in `Recorder.Stop()`.

**After**: Single `portaudio.Initialize()` in `main()` with `defer portaudio.Terminate()`. This enables:
- Device enumeration at startup without extra init/terminate
- Multiple Recorder instances (push-to-talk + continuous) without conflicts
- Clean shutdown

## 11. File Changes

| File | Action | Description |
|------|--------|-------------|
| `devices.go` | **New** | `ListMonitorSources()`, `SelectAudioMode()` |
| `continuous.go` | **New** | `ContinuousTranscriber`, `chunkLoop`, `rms` |
| `asr.go` | **Modify** | `CaptureMode` type, multi-stream Recorder, `DrainSamples()`, `downmixToMono()`, `mixSamples()`, remove per-recording init/terminate |
| `hotkey.go` | **Modify** | Add `HotkeyContinuous`, Right+Down combo detection |
| `main.go` | **Modify** | Top-level `portaudio.Initialize()`, audio mode selection, `ContinuousTranscriber` creation, updated `handleAction`, help text |

## 12. Dependencies

No new Go modules or system packages beyond what spec/asr.md already requires (`gordonklaus/portaudio`, `libportaudio-dev`).

## 13. Verification

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Build: `PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH go build` | Compiles |
| 2 | Run, select "Mic only" mode | Existing push-to-talk works (regression) |
| 3 | Run, select "System audio", pick a monitor source | Right+Down starts continuous → transcript streams from system audio |
| 4 | Run, select "Mic + System" | Right+Down captures both speakers mixed |
| 5 | During continuous mode, press Up+Down | Status shows "disable continuous mode first" |
| 6 | Right+Down again to stop | Status shows "continuous transcription OFF" |
| 7 | Play audio through speakers while in system mode | whisper transcribes the played audio |
