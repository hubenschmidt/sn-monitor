package model

import (
	"sync"
	"time"
)

// --- Hotkey ---

type HotkeyAction int

const (
	HotkeyCapture      HotkeyAction = iota // Left+Right
	HotkeyFollowUp                         // Up+Down (toggle voice recording)
	HotkeyExplain                          // Left+Up
	HotkeyAudioCapture                     // Left+Down (toggle audio capture)
	HotkeyAudioSend                        // Right+Down (send accumulated transcript to LLM)
	HotkeyToggleView                       // Right+Up (toggle transcript/chat view)
	HotkeyClear                            // Right+Left+Up+Down (clear conversation history)
	HotkeySoundCheck                       // overlay-button only
	HotkeyImplement                        // overlay-button only
	HotkeyOptimize                         // inline button only
	HotkeySimplify                         // inline button only
)

var KeyLabels = map[HotkeyAction]string{
	HotkeyCapture:      "←→ screen",
	HotkeyAudioCapture: "←↓ audio",
	HotkeyFollowUp:     "↑↓ mic",
	HotkeyAudioSend:    "→↓ process",
	HotkeyClear:        "clear all",
	HotkeySoundCheck:   "🔊 check",
	HotkeyImplement:    "⚙ impl",
}

var KeyOrder = []HotkeyAction{
	HotkeyCapture,
	HotkeyAudioCapture,
	HotkeyFollowUp,
	HotkeyAudioSend,
	HotkeyImplement,
	HotkeyClear,
}

var ActionNames = map[HotkeyAction]string{
	HotkeyCapture:      "capture",
	HotkeyFollowUp:     "voice",
	HotkeyExplain:      "explain",
	HotkeyAudioCapture: "audio",
	HotkeyAudioSend:    "send",
	HotkeySoundCheck:   "soundcheck",
	HotkeyImplement:    "implement",
	HotkeyOptimize:     "optimize",
	HotkeySimplify:     "simplify",
	HotkeyClear:        "clear",
}

// --- Audio ---

type CaptureMode int

const (
	CaptureModeMic CaptureMode = iota
	CaptureModeSystem
	CaptureModeBoth
)

// SummarizeFn sends text to the LLM and returns a summary.
// Stateless — does not append to conversation history.
type SummarizeFn func(text string) (string, error)

// TranscriptEntry is a UI-stable transcript chunk with a unique ID.
type TranscriptEntry struct {
	ID     int
	Text   string
	Source string
	Time   time.Time
}

// --- Provider ---

type Provider interface {
	Solve(images [][]byte, transcript string, onDelta func(string)) (string, error)
	FollowUp(text string, onDelta func(string)) (string, error)
	Summarize(text string) (string, error)
	ModelName() string
	SetLanguage(lang string)
	SetContextDir(dir string)
	ContextDir() string
	ClearHistory()
	HistoryLen() int
	RemoveHistoryPair(userIndex int)
}

// --- Renderer ---

type Renderer interface {
	Render(markdown string) error
	SetStatus(status string)
	StreamStart()
	StreamDelta(delta string)
	StreamDone()
	AppendStreamStart()
	AppendStreamDelta(delta string)
	AppendStreamDone()
	AppendTranscriptChunk(source, text string, id int)
	ClearTranscriptCheckboxes()
	SetMicRecording(recording bool)
	SetAudioRecording(recording bool)
	SetSoundCheck(active bool)
	UpdateVU(micLevel, audioLevel float64)
	AppendScreenshot(id int, data []byte)
	RemoveScreenshot(id int)
	ClearScreenshotCheckboxes()
	SetScreenCount(count int)
	SetCurrentTraceID(id int)
	AddObserveTrace(trace Trace)
	RemoveObserveTrace(traceID int)
	ClearContextData()
	Clear()
	Close()
}

// --- Trace ---

type Trace struct {
	ID                int
	Time              time.Time
	ScreenIDs         []int
	ScreenCount       int
	ScreenTimes       []time.Time
	HasTranscript     bool
	HasContext         bool
	ContextDir        string
	ContextFiles      []string
	TranscriptSnippet string
	HistoryIndex      int
}

// --- Screenshot ---

type ScreenshotEntry struct {
	ID   int
	Data []byte
	Time time.Time
}

const MaxScreenshots = 10

// --- AppState ---

// AppState holds accumulated inputs (screenshots, etc.) until the user triggers Process.
type AppState struct {
	Mu            sync.Mutex
	Shots         []ScreenshotEntry
	ArchivedShots map[int]ScreenshotEntry
	Selected      map[int]bool
	NextID        int
	Traces        []Trace
	NextTraceID   int
}

func (s *AppState) AddScreenshot(data []byte) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Selected == nil {
		s.Selected = make(map[int]bool)
	}
	id := s.NextID
	s.NextID++
	s.Shots = append(s.Shots, ScreenshotEntry{ID: id, Data: data, Time: time.Now()})
	s.Selected[id] = true
	for len(s.Shots) > MaxScreenshots {
		delete(s.Selected, s.Shots[0].ID)
		s.Shots = s.Shots[1:]
	}
	return id
}

func (s *AppState) RemoveScreenshot(id int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	delete(s.Selected, id)
	delete(s.ArchivedShots, id)
	for i, e := range s.Shots {
		if e.ID == id {
			s.Shots = append(s.Shots[:i], s.Shots[i+1:]...)
			return
		}
	}
}

func (s *AppState) ToggleScreenshot(id int, on bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Selected == nil {
		s.Selected = make(map[int]bool)
	}
	if on {
		s.Selected[id] = true
		return
	}
	delete(s.Selected, id)
}

// SelectedScreenshots returns checked screenshots, or all if none checked.
func (s *AppState) SelectedScreenshots() [][]byte {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.Shots) == 0 {
		return nil
	}
	if len(s.Selected) > 0 {
		var out [][]byte
		for _, e := range s.Shots {
			if s.Selected[e.ID] {
				out = append(out, e.Data)
			}
		}
		return out
	}
	out := make([][]byte, len(s.Shots))
	for i, e := range s.Shots {
		out[i] = e.Data
	}
	return out
}

func (s *AppState) SelectedScreenshotTimes() []time.Time {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	var times []time.Time
	for _, e := range s.Shots {
		if len(s.Selected) == 0 || s.Selected[e.ID] {
			times = append(times, e.Time)
		}
	}
	return times
}

func (s *AppState) SelectedScreenshotIDs() []int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	var ids []int
	for _, e := range s.Shots {
		if len(s.Selected) == 0 || s.Selected[e.ID] {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

func (s *AppState) ClearSelections() {
	s.Mu.Lock()
	s.Selected = nil
	s.Mu.Unlock()
}

func (s *AppState) ScreenshotCount() int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return len(s.Shots)
}

func (s *AppState) ClearScreenshots() {
	s.Mu.Lock()
	s.archiveShots()
	s.Shots = nil
	s.Selected = nil
	s.Mu.Unlock()
}

func (s *AppState) Clear() {
	s.Mu.Lock()
	s.Shots = nil
	s.Selected = nil
	s.ArchivedShots = nil
	s.Traces = nil
	s.Mu.Unlock()
}

func (s *AppState) archiveShots() {
	if len(s.Shots) == 0 {
		return
	}
	if s.ArchivedShots == nil {
		s.ArchivedShots = make(map[int]ScreenshotEntry, len(s.Shots))
	}
	for _, e := range s.Shots {
		s.ArchivedShots[e.ID] = e
	}
}

func (s *AppState) GetScreenshotData(id int) []byte {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for _, e := range s.Shots {
		if e.ID == id {
			return e.Data
		}
	}
	if e, ok := s.ArchivedShots[id]; ok {
		return e.Data
	}
	return nil
}

func (s *AppState) RestoreScreenshot(id int) *ScreenshotEntry {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	e, ok := s.ArchivedShots[id]
	if !ok {
		return nil
	}
	delete(s.ArchivedShots, id)
	s.Shots = append(s.Shots, e)
	if s.Selected == nil {
		s.Selected = make(map[int]bool)
	}
	s.Selected[id] = true
	return &e
}

func (s *AppState) AddTrace(screenIDs []int, screenTimes []time.Time, hasTranscript, hasContext bool, contextDir string, contextFiles []string, transcriptSnippet string, historyIndex int) Trace {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	id := s.NextTraceID
	s.NextTraceID++
	if len(transcriptSnippet) > 200 {
		transcriptSnippet = transcriptSnippet[:200]
	}
	t := Trace{
		ID:                id,
		Time:              time.Now(),
		ScreenIDs:         screenIDs,
		ScreenCount:       len(screenIDs),
		ScreenTimes:       screenTimes,
		HasTranscript:     hasTranscript,
		HasContext:         hasContext,
		ContextDir:        contextDir,
		ContextFiles:      contextFiles,
		TranscriptSnippet: transcriptSnippet,
		HistoryIndex:      historyIndex,
	}
	s.Traces = append(s.Traces, t)
	return t
}

func (s *AppState) RemoveTrace(id int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for i, t := range s.Traces {
		if t.ID == id {
			s.Traces = append(s.Traces[:i], s.Traces[i+1:]...)
			return
		}
	}
}

func (s *AppState) GetTrace(id int) *Trace {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for i, t := range s.Traces {
		if t.ID == id {
			return &s.Traces[i]
		}
	}
	return nil
}

func (s *AppState) TracesSnapshot() []Trace {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	out := make([]Trace, len(s.Traces))
	copy(out, s.Traces)
	return out
}

func (s *AppState) AdjustTraceIndicesAfter(idx, delta int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for i := range s.Traces {
		if s.Traces[i].HistoryIndex > idx {
			s.Traces[i].HistoryIndex += delta
		}
	}
}

// --- Sandbox ---

type SandboxResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Error    string
}

// --- System devices ---

type MonitorInfo struct {
	Index  int
	Output string
	Model  string
	Geom   string
	X, Y   int
	Width  int
	Height int
}

type PulseSource struct {
	ID   string
	Name string
}

type MouseInfo struct {
	ID   string `json:"ID"`
	Name string `json:"Name"`
}

// --- Log ---

type LogEntry struct {
	Time    time.Time
	Level   string
	Message string
	Index   int
}

// --- Config ---

type AppConfig struct {
	Name              string `json:"name"`
	Monitor           int    `json:"monitor"`
	OverlayMonitor    int    `json:"overlay_monitor"`
	OverlayFullscreen bool   `json:"overlay_fullscreen,omitempty"`
	Provider          string `json:"provider"`
	Renderer          string `json:"renderer"`
	Language          string `json:"language"`
	AudioMode         string `json:"audio_mode"`
	MonSource         string `json:"mon_source,omitempty"`
	WhisperModel      string `json:"whisper_model,omitempty"`
	ContextDir        string `json:"context_dir,omitempty"`
}

type ConfigFile struct {
	Configs []AppConfig `json:"configs"`
}
