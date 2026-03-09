package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

const (
	asrSampleRate   = 16000
	asrChannels     = 1
	asrFramesPerBuf = 1024
)

type CaptureMode int

const (
	CaptureModeMic    CaptureMode = iota
	CaptureModeSystem
	CaptureModeBoth
)

// Recorder captures audio from mic and/or system via parec (PulseAudio).
type Recorder struct {
	mode      CaptureMode
	monSource string // PulseAudio source name for monitor
	micCmd    *exec.Cmd
	monCmd    *exec.Cmd
	mu        sync.Mutex
	samples   []int16
	stopCh    chan struct{}
}

func NewRecorder(mode CaptureMode, monSource string) *Recorder {
	return &Recorder{mode: mode, monSource: monSource}
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.samples = r.samples[:0]
	r.stopCh = make(chan struct{})

	if err := r.startMicCapture(); err != nil {
		return err
	}

	if err := r.startMonitorCapture(); err != nil {
		r.closeStreams()
		return err
	}

	return nil
}

// startMicCapture spawns parec to capture mic audio via the default PulseAudio source.
func (r *Recorder) startMicCapture() error {
	if r.mode == CaptureModeSystem {
		return nil
	}

	r.micCmd = exec.Command("parec",
		"--format=s16le",
		"--channels=1",
		"--rate=16000",
		"--device=@DEFAULT_SOURCE@",
	)

	stdout, err := r.micCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("parec mic pipe: %w", err)
	}

	if err := r.micCmd.Start(); err != nil {
		return fmt.Errorf("parec mic start: %w", err)
	}

	go r.readLoopParec(stdout)
	return nil
}

// startMonitorCapture spawns parec to capture system audio as s16le mono 16kHz.
func (r *Recorder) startMonitorCapture() error {
	if r.mode == CaptureModeMic {
		return nil
	}

	r.monCmd = exec.Command("parec",
		"--format=s16le",
		"--channels=1",
		"--rate=16000",
		"--device="+r.monSource,
	)

	stdout, err := r.monCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("parec pipe: %w", err)
	}

	if err := r.monCmd.Start(); err != nil {
		return fmt.Errorf("parec start: %w", err)
	}

	go r.readLoopParec(stdout)
	return nil
}

// readLoopParec reads s16le PCM from parec stdout and appends to samples.
func (r *Recorder) readLoopParec(stdout io.ReadCloser) {
	buf := make([]byte, asrFramesPerBuf*2) // 2 bytes per int16 sample
	for {
		select {
		case <-r.stopCh:
			return
		default:
		}
		n, err := stdout.Read(buf)
		if err != nil {
			return
		}
		samples := bytesToInt16(buf[:n])
		r.mu.Lock()
		r.samples = append(r.samples, samples...)
		r.mu.Unlock()
	}
}

func (r *Recorder) Stop() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopCh == nil {
		return nil
	}
	close(r.stopCh)
	r.stopCh = nil
	r.closeStreams()

	out := make([]int16, len(r.samples))
	copy(out, r.samples)
	return out
}

func (r *Recorder) DrainSamples() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]int16, len(r.samples))
	copy(out, r.samples)
	r.samples = r.samples[:0]
	return out
}

func (r *Recorder) closeStreams() {
	if r.micCmd != nil && r.micCmd.Process != nil {
		r.micCmd.Process.Kill()
		r.micCmd.Wait()
		r.micCmd = nil
	}
	if r.monCmd != nil && r.monCmd.Process != nil {
		r.monCmd.Process.Kill()
		r.monCmd.Wait()
		r.monCmd = nil
	}
}

func bytesToInt16(b []byte) []int16 {
	count := len(b) / 2
	out := make([]int16, count)
	for i := 0; i < count; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

type whisperResponse struct {
	Text string `json:"text"`
}

func Transcribe(wavData []byte, whisperURL string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	part, err := w.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(wavData); err != nil {
		return "", fmt.Errorf("write wav: %w", err)
	}

	w.WriteField("response_format", "json")
	w.WriteField("temperature", "0.0")
	w.Close()

	resp, err := http.Post(whisperURL+"/inference", w.FormDataContentType(), &body)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper %d: %s", resp.StatusCode, string(b))
	}

	var result whisperResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	text := strings.TrimSpace(result.Text)
	if text == "" {
		return "", fmt.Errorf("no speech detected")
	}
	return text, nil
}
