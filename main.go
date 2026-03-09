package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/joho/godotenv"
	"github.com/openai/openai-go/shared"
)

func main() {
	godotenv.Load()

	monitors, err := listMonitors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Available monitors:")
	for _, m := range monitors {
		fmt.Printf("  %d: %s — %s (%s)\n", m.Index+1, m.Model, m.Output, m.Geom)
	}

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Print("\nSelect monitor number: ")
	scanner.Scan()
	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(monitors) {
		fmt.Fprintf(os.Stderr, "invalid selection\n")
		os.Exit(1)
	}
	selected := choice - 1

	provider, err := selectProvider(scanner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	renderer := selectRenderer(scanner)

	lang := selectLanguage(scanner)
	provider.SetLanguage(lang)

	captureMode, monSource, err := SelectAudioMode(scanner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audio mode error: %v\n", err)
		os.Exit(1)
	}

	whisperURL := os.Getenv("WHISPER_URL")
	if whisperURL == "" {
		whisperURL = "http://localhost:8178"
	}

	whisperProc := ensureWhisperServer(whisperURL)
	if whisperProc != nil {
		defer whisperProc.Process.Kill()
	}

	fmt.Printf("\nModel: %s | Language: %s\n", provider.ModelName(), lang)
	fmt.Printf("Watching %s (%s).\n", monitors[selected].Model, monitors[selected].Output)
	for _, desc := range helpLines {
		fmt.Printf("  %-8s = %s\n", keyLabels[desc.action], desc.text)
	}
	fmt.Println("  Ctrl+C           = quit")
	fmt.Println()

	ch := make(chan HotkeyAction, 1)
	go func() {
		if err := listenHotkey(ch); err != nil {
			fmt.Fprintf(os.Stderr, "hotkey error: %v\n", err)
			os.Exit(1)
		}
	}()

	recorder := NewRecorder(CaptureModeMic, "")
	ac := NewAudioCapture(captureMode, monSource, whisperURL, renderer)
	micRecording := false
	dispatch := func() {
		for action := range ch {
			handleAction(action, selected, provider, renderer, recorder, ac, whisperURL, lang, &micRecording)
		}
	}

	overlay := findOverlay(renderer)
	if overlay == nil {
		dispatch()
		return
	}

	overlay.SetActionHandler(func(a HotkeyAction) {
		select {
		case ch <- a:
		default:
		}
	})

	// Overlay mode: webview.Run() must be on the main thread
	go dispatch()
	overlay.Run()
}

func selectProvider(scanner *bufio.Scanner) (Provider, error) {
	fmt.Println("\nSelect model:")
	fmt.Println("  1: Claude Opus 4.6 (Anthropic)")
	fmt.Println("  2: GPT-5.3 Codex (OpenAI)")
	fmt.Println("  3: GPT-5.4 (OpenAI)")
	fmt.Print("Choice [1]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "" || input == "1" {
		return newAnthropicProvider("claude-opus-4-6")
	}
	if input == "2" {
		return newOpenAIProvider("gpt-5.3-codex")
	}
	if input == "3" {
		return newOpenAIProvider("gpt-5.4")
	}
	return nil, fmt.Errorf("invalid model selection: %s", input)
}

func newAnthropicProvider(model string) (Provider, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	return NewAnthropicProvider(anthropic.Model(model)), nil
}

func newOpenAIProvider(model string) (Provider, error) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	return NewOpenAIProvider(shared.ResponsesModel(model)), nil
}

func selectRenderer(scanner *bufio.Scanner) Renderer {
	fmt.Println("\nOutput mode:")
	fmt.Println("  1: Terminal")
	fmt.Println("  2: Overlay")
	fmt.Println("  3: Both")
	fmt.Print("Choice [1]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "2" {
		return NewOverlayRenderer()
	}
	if input == "3" {
		return &MultiRenderer{renderers: []Renderer{
			&TerminalRenderer{},
			NewOverlayRenderer(),
		}}
	}
	return &TerminalRenderer{}
}

type helpLine struct {
	action HotkeyAction
	text   string
}

var helpLines = []helpLine{
	{HotkeyCapture, "screen capture → solve"},
	{HotkeyAudioCapture, "toggle audio capture (system audio)"},
	{HotkeyFollowUp, "toggle mic recording"},
	{HotkeyAudioSend, "process accumulated transcript via LLM"},
	{HotkeyClear, "clear history"},
}

var languages = map[string]string{
	"1": "Python",
	"2": "JavaScript",
	"3": "TypeScript",
	"4": "Go",
	"5": "Java",
	"6": "C++",
	"7": "Rust",
}

func selectLanguage(scanner *bufio.Scanner) string {
	fmt.Println("\nCode language:")
	fmt.Println("  1: Python")
	fmt.Println("  2: JavaScript")
	fmt.Println("  3: TypeScript")
	fmt.Println("  4: Go")
	fmt.Println("  5: Java")
	fmt.Println("  6: C++")
	fmt.Println("  7: Rust")
	fmt.Print("Choice [1]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return "Python"
	}
	lang, ok := languages[input]
	if !ok {
		fmt.Println("  defaulting to Python")
		return "Python"
	}
	return lang
}

func findOverlay(r Renderer) *OverlayRenderer {
	if o, ok := r.(*OverlayRenderer); ok {
		return o
	}
	m, ok := r.(*MultiRenderer)
	if !ok {
		return nil
	}
	for _, sub := range m.renderers {
		if o, ok := sub.(*OverlayRenderer); ok {
			return o
		}
	}
	return nil
}

func handleAction(action HotkeyAction, monitorIdx int, provider Provider, renderer Renderer, recorder *Recorder, ac *AudioCapture, whisperURL string, lang string, micRecording *bool) {
	if action == HotkeyCapture {
		handleCapture(monitorIdx, provider, renderer)
		return
	}
	if action == HotkeyFollowUp && *micRecording {
		handleMicStop(recorder, renderer, ac, whisperURL)
		renderer.SetMicRecording(false)
		*micRecording = false
		return
	}
	if action == HotkeyFollowUp {
		handleMicStart(recorder, renderer)
		renderer.SetMicRecording(true)
		*micRecording = true
		return
	}
	if action == HotkeyExplain {
		handleExplain(provider, renderer)
		return
	}
	if action == HotkeyAudioCapture {
		ac.Toggle()
		renderer.SetAudioRecording(ac.Active())
		if ac.Active() {
			go ac.RunChunkLoop()
		}
		return
	}
	if action == HotkeyAudioSend {
		handleAudioSend(ac, provider, renderer, lang)
		return
	}
	if action == HotkeyClear {
		if *micRecording {
			recorder.Stop()
			renderer.SetMicRecording(false)
			*micRecording = false
		}
		provider.ClearHistory()
		renderer.Clear()
		return
	}
}

func audioSendPrefix(lang string) string {
	return `The following is a raw audio transcript from the user. Start your response with a "**🎤 User said:** " line that summarizes what the user asked/said in 1-2 sentences, then a blank line, then respond naturally and conversationally. If code is requested, use ` + lang + `. Do NOT repeat or regenerate previous solutions — just answer the question or continue the discussion. Be concise.

Transcript:
`
}

func handleAudioSend(ac *AudioCapture, provider Provider, renderer Renderer, lang string) {
	// Flush any remaining audio before draining transcript
	ac.TranscribeNow()
	transcript := ac.DrainTranscript()
	if transcript == "" {
		renderer.SetStatus("no transcript accumulated")
		return
	}

	renderer.SetStatus("sending transcript to LLM...")
	renderer.AppendStreamStart()
	_, err := provider.FollowUp(audioSendPrefix(lang)+transcript, func(delta string) {
		renderer.AppendStreamDelta(delta)
	})
	if err != nil {
		renderer.SetStatus("follow-up error: " + err.Error())
		return
	}
	renderer.AppendStreamDone()
	renderer.SetStatus("")
}

func handleMicStart(recorder *Recorder, renderer Renderer) {
	renderer.SetStatus("mic recording...")
	if err := recorder.Start(); err != nil {
		renderer.SetStatus("mic error: " + err.Error())
	}
}

func handleMicStop(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string) {
	samples := recorder.Stop()
	if len(samples) == 0 {
		renderer.SetStatus("no audio captured")
		return
	}

	renderer.SetStatus("transcribing mic...")
	wavData := EncodeWAV(samples, asrSampleRate)
	transcript, err := Transcribe(wavData, whisperURL)
	if err != nil {
		renderer.SetStatus("asr error: " + err.Error())
		return
	}

	trimmed := strings.TrimSpace(transcript)
	if trimmed == "" {
		renderer.SetStatus("no speech detected")
		return
	}

	ac.AppendTranscript(trimmed)
	renderer.AppendTranscriptChunk("mic", trimmed)
	renderer.SetStatus(fmt.Sprintf("mic added — %d chars accumulated", ac.TranscriptLen()))
}

func handleExplain(provider Provider, renderer Renderer) {
	renderer.SetStatus("thinking...")
	renderer.AppendStreamStart()
	_, err := provider.FollowUp("Explain further in more detail.", func(delta string) {
		renderer.AppendStreamDelta(delta)
	})
	if err != nil {
		renderer.SetStatus("follow-up error: " + err.Error())
		return
	}
	renderer.AppendStreamDone()
	renderer.SetStatus("")
}

// ensureWhisperServer starts whisper-server if not already running.
// Returns the process handle (caller should defer Kill) or nil if already running.
func ensureWhisperServer(whisperURL string) *exec.Cmd {
	if whisperHealthy(whisperURL) {
		fmt.Println("whisper-server already running")
		return nil
	}

	model := os.Getenv("WHISPER_MODEL")
	if model == "" {
		model = os.ExpandEnv("$HOME/.local/share/whisper/ggml-large-v3.bin")
	}

	// Extract port from URL, default 8178
	port := "8178"
	if parts := strings.SplitAfter(whisperURL, ":"); len(parts) == 3 {
		port = strings.TrimRight(parts[2], "/")
	}

	cmd := exec.Command("whisper-server", "-m", model, "--port", port)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start whisper-server: %v\n", err)
		return nil
	}

	fmt.Printf("starting whisper-server (pid %d)...\n", cmd.Process.Pid)

	// Wait for it to become healthy
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		if whisperHealthy(whisperURL) {
			fmt.Println("whisper-server ready")
			return cmd
		}
	}

	fmt.Fprintln(os.Stderr, "warning: whisper-server did not become healthy in 30s")
	return cmd
}

func whisperHealthy(whisperURL string) bool {
	resp, err := http.Get(whisperURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func handleCapture(monitorIdx int, provider Provider, renderer Renderer) {
	renderer.SetStatus("capturing...")
	imgData, err := captureMonitor(monitorIdx)
	if err != nil {
		renderer.SetStatus("capture error: " + err.Error())
		return
	}
	renderer.SetStatus("solving...")
	renderer.AppendStreamStart()
	_, err = provider.Solve(imgData, func(delta string) {
		renderer.AppendStreamDelta(delta)
	})
	if err != nil {
		renderer.SetStatus("solve error: " + err.Error())
		return
	}
	renderer.AppendStreamDone()
	renderer.SetStatus("")
}
