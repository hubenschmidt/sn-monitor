package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SummarizeFn sends text to the LLM and returns a summary.
// Stateless — does not append to conversation history.
type SummarizeFn func(text string) (string, error)

const (
	silenceThreshold   = 500.0
	minChunkDuration    = 12 * time.Second
	maxChunkDuration    = 25 * time.Second
	micMinChunkDuration = 5 * time.Second
	micMaxChunkDuration = 15 * time.Second
	pollInterval        = 200 * time.Millisecond
	silenceWindow       = 32000 // 2s at 16kHz — wider to avoid fragmenting on natural pauses
	summarizeThreshold = 3000  // chars of raw text before triggering summarization
	maxSummaryChars    = 16000 // ~4K tokens budget for summary history
	maxRawChars        = 16000 // ~4K tokens budget for recent raw
	maxSummarizeRetry  = 3
)

// TranscriptEntry is a UI-stable transcript chunk with a unique ID.
// Separate from rawChunks (which feed the summarization pipeline and get drained).
type TranscriptEntry struct {
	ID   int
	Text string
}

// AudioCapture records audio continuously, transcribes in chunks,
// and accumulates transcript text for later use by the LLM.
type AudioCapture struct {
	mode       CaptureMode
	monSource  string
	whisperURL string
	renderer   Renderer
	summarize  SummarizeFn
	recorder   *Recorder
	active     atomic.Bool
	stopCh     chan struct{}

	mu           sync.Mutex
	rawChunks    []string
	rawCharCount int
	summaries    []string
	summarizing  atomic.Bool
	retryCount   int
	entries      []TranscriptEntry
	selected     map[int]bool
	nextID       int
}

func NewAudioCapture(mode CaptureMode, monSource string, whisperURL string, renderer Renderer, summarize SummarizeFn) *AudioCapture {
	return &AudioCapture{
		mode:       mode,
		monSource:  monSource,
		whisperURL: whisperURL,
		renderer:   renderer,
		summarize:  summarize,
	}
}

func (ac *AudioCapture) Active() bool {
	return ac.active.Load()
}

func (ac *AudioCapture) Toggle() {
	if ac.active.Load() {
		ac.stop()
		return
	}
	ac.start()
}

func (ac *AudioCapture) StartSoundCheck() {
	ac.recorder = NewRecorder(CaptureModeSystem, ac.monSource)
	if err := ac.recorder.Start(); err != nil {
		ac.renderer.SetStatus("audio capture error: " + err.Error())
		return
	}
	ac.stopCh = make(chan struct{})
	ac.active.Store(true)
}

func (ac *AudioCapture) start() {
	ac.mu.Lock()
	ac.rawChunks = nil
	ac.rawCharCount = 0
	ac.summaries = nil
	ac.retryCount = 0
	ac.entries = nil
	ac.selected = make(map[int]bool)
	ac.nextID = 0
	ac.mu.Unlock()

	ac.recorder = NewRecorder(ac.mode, ac.monSource)
	if err := ac.recorder.Start(); err != nil {
		ac.renderer.SetStatus("audio capture error: " + err.Error())
		return
	}

	ac.stopCh = make(chan struct{})
	ac.active.Store(true)
	ac.renderer.SetStatus("audio capture ON — recording...")
}

func (ac *AudioCapture) stop() {
	ac.active.Store(false)
	close(ac.stopCh)
	ac.recorder.Stop()

	ac.mu.Lock()
	n := ac.rawCharCount
	for _, s := range ac.summaries {
		n += len(s)
	}
	ac.mu.Unlock()

	ac.renderer.SetStatus(fmt.Sprintf("audio capture OFF — %d chars accumulated", n))
}

// BuildContext assembles summaries + recent raw chunks for the LLM.
// Drains both buffers.
func (ac *AudioCapture) BuildContext() string {
	ac.TranscribeNow()

	ac.mu.Lock()
	defer ac.mu.Unlock()

	var b strings.Builder

	// Append summaries, dropping oldest if over budget
	totalSummaryChars := 0
	startIdx := 0
	for _, s := range ac.summaries {
		totalSummaryChars += len(s)
	}
	for totalSummaryChars > maxSummaryChars && startIdx < len(ac.summaries) {
		totalSummaryChars -= len(ac.summaries[startIdx])
		startIdx++
	}
	for i := startIdx; i < len(ac.summaries); i++ {
		b.WriteString(ac.summaries[i])
		b.WriteString("\n\n")
	}

	// Append recent raw chunks
	if len(ac.rawChunks) > 0 {
		if b.Len() > 0 {
			b.WriteString("---\nRecent transcript:\n")
		}
		raw := strings.Join(ac.rawChunks, " ")
		if len(raw) > maxRawChars {
			raw = raw[len(raw)-maxRawChars:]
		}
		b.WriteString(raw)
	}

	// Drain
	ac.summaries = nil
	ac.rawChunks = nil
	ac.rawCharCount = 0
	ac.retryCount = 0
	ac.entries = nil
	ac.selected = make(map[int]bool)

	return strings.TrimSpace(b.String())
}

// DrainTranscript is a backward-compatible wrapper around BuildContext.
func (ac *AudioCapture) DrainTranscript() string {
	return ac.BuildContext()
}

// AppendTranscript adds external transcript text (e.g. from mic recording).
func (ac *AudioCapture) AppendTranscript(text string) {
	ac.mu.Lock()
	ac.rawChunks = append(ac.rawChunks, text)
	ac.rawCharCount += len(text)
	ac.mu.Unlock()

	ac.maybeStartSummarize()
}

// TranscriptLen returns the current total length of all accumulated text.
func (ac *AudioCapture) TranscriptLen() int {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	n := ac.rawCharCount
	for _, s := range ac.summaries {
		n += len(s)
	}
	return n
}

// TranscribeNow drains audio samples, transcribes, and appends to raw chunks.
func (ac *AudioCapture) TranscribeNow() {
	if ac.recorder == nil {
		return
	}
	samples := ac.recorder.DrainSamples()
	if len(samples) == 0 {
		return
	}
	chunkRMS := rms(samples)
	if !hasSpeech(samples, silenceWindow, silenceThreshold) {
		fmt.Printf("[audio-capture] dropped chunk: %d samples, rms=%.0f (no speech window above %.0f)\n", len(samples), chunkRMS, silenceThreshold)
		return
	}
	fmt.Printf("[audio-capture] sending chunk: %d samples, rms=%.0f\n", len(samples), chunkRMS)

	wavData := EncodeWAV(samples, asrSampleRate)
	text, err := Transcribe(wavData, ac.whisperURL)
	if err != nil {
		fmt.Printf("[audio-capture] transcribe error: %v\n", err)
		return
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}

	ac.mu.Lock()
	ac.rawChunks = append(ac.rawChunks, trimmed)
	ac.rawCharCount += len(trimmed)
	id := ac.nextID
	ac.nextID++
	ac.entries = append(ac.entries, TranscriptEntry{ID: id, Text: trimmed})
	n := ac.rawCharCount
	for _, s := range ac.summaries {
		n += len(s)
	}
	ac.mu.Unlock()

	ac.renderer.AppendTranscriptChunk("audio", trimmed, id)
	ac.renderer.SetStatus(fmt.Sprintf("audio capture — %d chars accumulated", n))

	ac.maybeStartSummarize()
}

// maybeStartSummarize triggers a background summarization if threshold is met.
func (ac *AudioCapture) maybeStartSummarize() {
	if ac.summarize == nil {
		return
	}
	if ac.summarizing.Load() {
		return
	}

	ac.mu.Lock()
	needsSummarize := ac.rawCharCount >= summarizeThreshold
	ac.mu.Unlock()

	if !needsSummarize {
		return
	}

	ac.summarizing.Store(true)
	go ac.doSummarize()
}

func (ac *AudioCapture) doSummarize() {
	defer ac.summarizing.Store(false)

	ac.mu.Lock()
	if ac.rawCharCount < summarizeThreshold {
		ac.mu.Unlock()
		return
	}
	text := strings.Join(ac.rawChunks, " ")
	ac.mu.Unlock()

	summary, err := ac.summarize(text)
	if err != nil {
		fmt.Printf("[audio-capture] summarize error: %v\n", err)
		ac.mu.Lock()
		ac.retryCount++
		if ac.retryCount >= maxSummarizeRetry {
			// Drop oldest raw chunks to prevent unbounded growth
			half := len(ac.rawChunks) / 2
			dropped := 0
			for _, c := range ac.rawChunks[:half] {
				dropped += len(c)
			}
			ac.rawChunks = ac.rawChunks[half:]
			ac.rawCharCount -= dropped
			ac.retryCount = 0
		}
		ac.mu.Unlock()
		return
	}

	ac.mu.Lock()
	ac.summaries = append(ac.summaries, strings.TrimSpace(summary))
	ac.rawChunks = nil
	ac.rawCharCount = 0
	ac.retryCount = 0
	ac.mu.Unlock()

	ac.renderer.SetStatus("transcript segment summarized")
}

// AddEntry appends a transcript entry and returns its ID.
func (ac *AudioCapture) AddEntry(text string) int {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	id := ac.nextID
	ac.nextID++
	ac.entries = append(ac.entries, TranscriptEntry{ID: id, Text: text})
	return id
}

// ToggleSelection marks or unmarks a transcript entry for selective sending.
func (ac *AudioCapture) ToggleSelection(id int, on bool) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.selected == nil {
		ac.selected = make(map[int]bool)
	}
	if on {
		ac.selected[id] = true
		return
	}
	delete(ac.selected, id)
}

// BuildSelectedContext joins only the selected entry texts.
// Returns "" if nothing is selected.
func (ac *AudioCapture) BuildSelectedContext() string {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if len(ac.selected) == 0 {
		return ""
	}
	var parts []string
	for _, e := range ac.entries {
		if ac.selected[e.ID] {
			parts = append(parts, e.Text)
		}
	}
	return strings.Join(parts, " ")
}

// ClearSelections clears all selected transcript entries.
func (ac *AudioCapture) ClearSelections() {
	ac.mu.Lock()
	ac.selected = make(map[int]bool)
	ac.mu.Unlock()
}

// ClearAll resets all accumulated state (raw chunks, summaries, entries, selections).
func (ac *AudioCapture) ClearAll() {
	ac.mu.Lock()
	ac.rawChunks = nil
	ac.rawCharCount = 0
	ac.summaries = nil
	ac.retryCount = 0
	ac.entries = nil
	ac.selected = make(map[int]bool)
	ac.nextID = 0
	ac.mu.Unlock()
}

// RunChunkLoop polls for silence-based chunk boundaries while active.
// Must be called in a goroutine.
func (ac *AudioCapture) RunChunkLoop() {
	runChunkLoop(minChunkDuration, maxChunkDuration, ac.recorder, ac.stopCh, ac.TranscribeNow)
}

// runChunkLoop is a generic chunk-boundary poller parameterized by timing,
// recorder, stop channel, and a transcribe callback.
func runChunkLoop(minDur, maxDur time.Duration, recorder *Recorder, stopCh <-chan struct{}, transcribe func()) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	chunkStart := time.Now()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}

		elapsed := time.Since(chunkStart)
		forced := elapsed >= maxDur
		silent := elapsed >= minDur && recorder.PeekTailRMS(silenceWindow) < silenceThreshold

		if forced || silent {
			transcribe()
			chunkStart = time.Now()
		}
	}
}

// PeekTailRMS returns the normalized (0–1) RMS level of the last n audio samples.
// Returns 0 when not actively recording.
func (ac *AudioCapture) PeekTailRMS(n int) float64 {
	if !ac.active.Load() {
		return 0
	}
	return ac.recorder.PeekTailRMS(n) / 32768.0
}

func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// hasSpeech returns true if any sliding window of windowSize samples
// exceeds threshold RMS. Unlike whole-chunk rms(), this detects speech
// even when surrounded by silence.
func hasSpeech(samples []int16, windowSize int, threshold float64) bool {
	if len(samples) == 0 {
		return false
	}
	if windowSize > len(samples) {
		return rms(samples) >= threshold
	}

	// Compute initial window sum-of-squares
	var sumSq float64
	for i := 0; i < windowSize; i++ {
		v := float64(samples[i])
		sumSq += v * v
	}
	if math.Sqrt(sumSq/float64(windowSize)) >= threshold {
		return true
	}

	// Slide window, updating sum-of-squares incrementally
	for i := windowSize; i < len(samples); i++ {
		add := float64(samples[i])
		drop := float64(samples[i-windowSize])
		sumSq += add*add - drop*drop
		if math.Sqrt(sumSq/float64(windowSize)) >= threshold {
			return true
		}
	}
	return false
}
