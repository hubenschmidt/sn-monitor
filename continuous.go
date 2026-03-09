package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	chunkInterval    = 10 * time.Second
	silenceThreshold = 500.0
)

// AudioCapture records audio continuously, transcribes in chunks,
// and accumulates transcript text for later use by the LLM.
type AudioCapture struct {
	mode       CaptureMode
	monSource  string
	whisperURL string
	renderer   Renderer
	recorder   *Recorder
	active     atomic.Bool
	stopCh     chan struct{}
	mu         sync.Mutex
	transcript strings.Builder
}

func NewAudioCapture(mode CaptureMode, monSource string, whisperURL string, renderer Renderer) *AudioCapture {
	return &AudioCapture{
		mode:       mode,
		monSource:  monSource,
		whisperURL: whisperURL,
		renderer:   renderer,
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

func (ac *AudioCapture) start() {
	ac.mu.Lock()
	ac.transcript.Reset()
	ac.mu.Unlock()

	ac.recorder = NewRecorder(CaptureModeSystem, ac.monSource)
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
	n := ac.transcript.Len()
	ac.mu.Unlock()

	ac.renderer.SetStatus(fmt.Sprintf("audio capture OFF — %d chars accumulated", n))
}

// DrainTranscript returns accumulated transcript and resets the buffer.
func (ac *AudioCapture) DrainTranscript() string {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	text := ac.transcript.String()
	ac.transcript.Reset()
	return strings.TrimSpace(text)
}

// AppendTranscript adds external transcript text (e.g. from mic recording) to the buffer.
func (ac *AudioCapture) AppendTranscript(text string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.transcript.Len() > 0 {
		ac.transcript.WriteString(" ")
	}
	ac.transcript.WriteString(text)
}

// TranscriptLen returns the current length of the accumulated transcript.
func (ac *AudioCapture) TranscriptLen() int {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.transcript.Len()
}

// TranscribeNow drains audio samples, transcribes, and appends to accumulated text.
// Called on a ticker from the chunk loop.
func (ac *AudioCapture) TranscribeNow() {
	samples := ac.recorder.DrainSamples()
	if len(samples) == 0 {
		return
	}
	if rms(samples) < silenceThreshold {
		return
	}

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
	if ac.transcript.Len() > 0 {
		ac.transcript.WriteString(" ")
	}
	ac.transcript.WriteString(trimmed)
	n := ac.transcript.Len()
	ac.mu.Unlock()

	ac.renderer.AppendTranscriptChunk("audio", trimmed)
	ac.renderer.SetStatus(fmt.Sprintf("audio capture — %d chars accumulated", n))
}

// RunChunkLoop transcribes audio on a fixed interval while active.
// Must be called in a goroutine.
func (ac *AudioCapture) RunChunkLoop() {
	ticker := time.NewTicker(chunkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ac.stopCh:
			return
		case <-ticker.C:
			ac.TranscribeNow()
		}
	}
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
