package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/joho/godotenv"
	"github.com/openai/openai-go/shared"
)

const configPath = "config.json"

// AppState holds accumulated inputs (screenshot, etc.) until the user triggers Process.
type AppState struct {
	mu         sync.Mutex
	screenshot []byte
}

func (s *AppState) SetScreenshot(data []byte) {
	s.mu.Lock()
	s.screenshot = data
	s.mu.Unlock()
}

func (s *AppState) ConsumeScreenshot() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.screenshot
	s.screenshot = nil
	return data, len(data) > 0
}

func (s *AppState) Clear() {
	s.mu.Lock()
	s.screenshot = nil
	s.mu.Unlock()
}

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

var providerLabels = map[string]string{
	"1": "Claude Opus 4.6", "2": "GPT-5.3 Codex", "3": "GPT-5.4",
}

var rendererLabels = map[string]string{
	"1": "Terminal", "2": "Overlay", "3": "Both",
}

var audioModeLabels = map[string]string{
	"1": "Mic only", "2": "System audio", "3": "Both",
}

var whisperModels = map[string]struct {
	label string
	file  string
	url   string
}{
	"1": {"large-v3-turbo (faster)", "ggml-large-v3-turbo.bin", "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin"},
	"2": {"large-v3 (more accurate)", "ggml-large-v3.bin", "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin"},
}

func loadConfigs() []AppConfig {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	var cf ConfigFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil
	}
	return cf.Configs
}

func saveConfigs(configs []AppConfig) {
	data, err := json.MarshalIndent(ConfigFile{Configs: configs}, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: marshal config: %v\n", err)
		return
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write config: %v\n", err)
	}
}

func promptSavedConfig(scanner *bufio.Scanner, configs []AppConfig, monitors []MonitorInfo) *AppConfig {
	for {
		if len(configs) == 0 {
			return nil
		}
		printConfigs(configs, monitors)
		fmt.Printf("  %d: New configuration\n", len(configs)+1)
		fmt.Print("Choice [new] (d<N> to delete): ")
		scanner.Scan()

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			return nil
		}
		if !strings.HasPrefix(input, "d") && !strings.HasPrefix(input, "D") {
			idx, err := strconv.Atoi(input)
			if err != nil || idx < 1 || idx > len(configs) {
				return nil
			}
			return &configs[idx-1]
		}
		configs = handleDeleteConfig(input[1:], configs)
		fmt.Println()
	}
}

func handleDeleteConfig(idxStr string, configs []AppConfig) []AppConfig {
	idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
	if err != nil || idx < 1 || idx > len(configs) {
		fmt.Println("  invalid delete index")
		return configs
	}
	name := configs[idx-1].Name
	configs = append(configs[:idx-1], configs[idx:]...)
	saveConfigs(configs)
	fmt.Printf("  deleted %q\n", name)
	return configs
}

func labelOr(m map[string]string, key, fallback string) string {
	if v := m[key]; v != "" {
		return v
	}
	return fallback
}

func monitorLabel(idx int, monitors []MonitorInfo, fallback string) string {
	if idx >= 0 && idx < len(monitors) {
		return fmt.Sprintf("%s (%s)", monitors[idx].Model, monitors[idx].Output)
	}
	return fallback
}

func printConfigs(configs []AppConfig, monitors []MonitorInfo) {
	fmt.Println("\nSaved configurations:")
	for i, c := range configs {
		prov := labelOr(providerLabels, c.Provider, "?")
		rend := labelOr(rendererLabels, c.Renderer, "?")
		lang := labelOr(languages, c.Language, "?")
		audio := labelOr(audioModeLabels, c.AudioMode, "?")
		if c.MonSource != "" {
			audio += " (" + c.MonSource + ")"
		}
		wm := whisperModel(c.WhisperModel)
		ctxDir := c.ContextDir
		if ctxDir == "" {
			ctxDir = "(none)"
		}
		fsLabel := "no"
		if c.OverlayFullscreen {
			fsLabel = "yes"
		}
		fmt.Printf("  %d: %q\n", i+1, c.Name)
		fmt.Printf("     Monitor: %s | Model: %s | Output: %s\n", monitorLabel(c.Monitor, monitors, fmt.Sprintf("#%d", c.Monitor+1)), prov, rend)
		fmt.Printf("     Language: %s | Audio: %s | Whisper: %s\n", lang, audio, wm.label)
		fmt.Printf("     Overlay: %s (fullscreen: %s) | Context: %s\n", monitorLabel(c.OverlayMonitor, monitors, fmt.Sprintf("#%d", c.OverlayMonitor+1)), fsLabel, ctxDir)
	}
}

func promptSaveConfig(scanner *bufio.Scanner, cfg AppConfig) {
	fmt.Print("\nSave this configuration? (name or Enter to skip): ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())
	if name == "" {
		return
	}

	cfg.Name = name
	configs := loadConfigs()
	// Replace existing config with same name
	found := false
	for i, c := range configs {
		if c.Name == name {
			configs[i] = cfg
			found = true
		}
	}
	if !found {
		configs = append(configs, cfg)
	}
	saveConfigs(configs)
	fmt.Printf("Configuration %q saved\n", name)
}

func providerFromChoice(choice string) (Provider, error) {
	if choice == "" || choice == "1" {
		return newAnthropicProvider("claude-opus-4-6")
	}
	if choice == "2" {
		return newOpenAIProvider("gpt-5.3-codex")
	}
	if choice == "3" {
		return newOpenAIProvider("gpt-5.4")
	}
	return nil, fmt.Errorf("invalid provider choice: %s", choice)
}

func rendererFromChoice(choice string) Renderer {
	if choice == "2" {
		return NewOverlayRenderer()
	}
	if choice == "3" {
		return &MultiRenderer{renderers: []Renderer{
			&TerminalRenderer{},
			NewOverlayRenderer(),
		}}
	}
	return &TerminalRenderer{}
}

func languageFromChoice(choice string) string {
	if choice == "" {
		return "Python"
	}
	lang, ok := languages[choice]
	if !ok {
		return "Python"
	}
	return lang
}

func audioModeFromChoice(choice, monSource string) (CaptureMode, string, error) {
	if choice == "" || choice == "1" {
		return CaptureModeMic, "", nil
	}
	if choice == "2" {
		return CaptureModeSystem, monSource, nil
	}
	if choice == "3" {
		return CaptureModeBoth, monSource, nil
	}
	return 0, "", fmt.Errorf("invalid audio mode: %s", choice)
}

func main() {
	godotenv.Load()

	monitors, err := listMonitors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(os.Stdin)

	var selected int
	var overlayMonitor int
	var overlayFullscreen bool
	var provider Provider
	var renderer Renderer
	var lang string
	var captureMode CaptureMode
	var monSource string
	var whisperChoice string
	var contextDir string

	saved := promptSavedConfig(scanner, loadConfigs(), monitors)
	if saved != nil {
		selected, overlayMonitor, overlayFullscreen, provider, renderer, lang, captureMode, monSource, whisperChoice, contextDir, err = applyConfig(*saved, monitors)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
	}

	if saved == nil {
		fmt.Println("Available monitors:")
		for _, m := range monitors {
			fmt.Printf("  %d: %s — %s (%s)\n", m.Index+1, m.Model, m.Output, m.Geom)
		}

		fmt.Print("\nSelect monitor number: ")
		scanner.Scan()
		choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err != nil || choice < 1 || choice > len(monitors) {
			fmt.Fprintf(os.Stderr, "invalid selection\n")
			os.Exit(1)
		}
		selected = choice - 1

		provider, err = selectProvider(scanner)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		renderer = selectRenderer(scanner)

		overlayMonitor = selectOverlayMonitor(scanner, monitors, renderer)
		overlayFullscreen = selectOverlayFullscreen(scanner, renderer)

		lang = selectLanguage(scanner)

		captureMode, monSource, err = SelectAudioMode(scanner)
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio mode error: %v\n", err)
			os.Exit(1)
		}

		whisperChoice = selectWhisperModel(scanner)

		contextDir = selectContextDir(scanner)

		promptSaveConfig(scanner, AppConfig{
			Monitor:           selected,
			OverlayMonitor:    overlayMonitor,
			OverlayFullscreen: overlayFullscreen,
			Provider:          providerChoiceFromScanner(provider),
			Renderer:          rendererChoiceFromScanner(renderer),
			Language:          languageChoiceFromString(lang),
			AudioMode:         audioModeChoiceFromMode(captureMode),
			MonSource:         monSource,
			WhisperModel:      whisperChoice,
			ContextDir:        contextDir,
		})
	}

	provider.SetLanguage(lang)
	provider.SetContextDir(contextDir)

	whisperURL := os.Getenv("WHISPER_URL")
	if whisperURL == "" {
		whisperURL = "http://localhost:8178"
	}

	whisperModel := whisperModelPath(whisperChoice)
	ensureWhisperModel(whisperModel, whisperModelURL(whisperChoice))

	whisperProc := ensureWhisperServer(whisperURL, whisperModel)
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
	summarizeFn := func(text string) (string, error) {
		return provider.Summarize(summarizePrompt + text)
	}
	ac := NewAudioCapture(captureMode, monSource, whisperURL, renderer, summarizeFn)
	var appState AppState
	var micStopCh chan struct{}
	var soundCheckOn atomic.Bool
	var llmBusy atomic.Bool
	dispatch := func() {
		for action := range ch {
			handleAction(action, selected, provider, renderer, recorder, ac, whisperURL, lang, &appState, &micStopCh, &soundCheckOn, &llmBusy)
		}
	}

	go runVULoop(renderer, recorder, ac, &soundCheckOn)

	overlay := findOverlay(renderer)
	if overlay == nil {
		dispatch()
		return
	}

	overlay.SetProvider(provider)
	if overlayMonitor >= 0 && overlayMonitor < len(monitors) {
		m := monitors[overlayMonitor]
		overlay.MoveToMonitor(m.X, m.Y)
		if overlayFullscreen {
			overlay.Fullscreen(m.X, m.Y, m.Width, m.Height)
		}
	}
	overlay.SetToggleChunkHandler(func(id int, checked bool) {
		ac.ToggleSelection(id, checked)
	})
	overlay.SetActionHandler(func(a HotkeyAction) {
		select {
		case ch <- a:
		default:
		}
	})

	// Clean shutdown on signal so GTK releases X11 resources
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		renderer.Close()
	}()

	// Overlay mode: webview.Run() must be on the main thread
	go dispatch()
	overlay.Run()

	// Tear down MPX after overlay exits so Chromium-based apps regain keyboard input
	teardownMPX()
}

func applyConfig(cfg AppConfig, monitors []MonitorInfo) (int, int, bool, Provider, Renderer, string, CaptureMode, string, string, string, error) {
	if cfg.Monitor < 0 || cfg.Monitor >= len(monitors) {
		return 0, 0, false, nil, nil, "", 0, "", "", "", fmt.Errorf("saved monitor #%d no longer exists (%d available)", cfg.Monitor+1, len(monitors))
	}

	provider, err := providerFromChoice(cfg.Provider)
	if err != nil {
		return 0, 0, false, nil, nil, "", 0, "", "", "", err
	}

	renderer := rendererFromChoice(cfg.Renderer)
	lang := languageFromChoice(cfg.Language)

	captureMode, monSource, err := audioModeFromChoice(cfg.AudioMode, cfg.MonSource)
	if err != nil {
		return 0, 0, false, nil, nil, "", 0, "", "", "", err
	}

	whisper := cfg.WhisperModel
	if whisper == "" {
		whisper = "1"
	}

	overlayMon := cfg.OverlayMonitor
	if overlayMon < 0 || overlayMon >= len(monitors) {
		overlayMon = 0
	}

	return cfg.Monitor, overlayMon, cfg.OverlayFullscreen, provider, renderer, lang, captureMode, monSource, whisper, cfg.ContextDir, nil
}

func providerChoiceFromScanner(p Provider) string {
	name := p.ModelName()
	if strings.Contains(name, "claude") {
		return "1"
	}
	if strings.Contains(name, "codex") {
		return "2"
	}
	return "3"
}

func rendererChoiceFromScanner(r Renderer) string {
	if _, ok := r.(*MultiRenderer); ok {
		return "3"
	}
	if _, ok := r.(*OverlayRenderer); ok {
		return "2"
	}
	return "1"
}

func languageChoiceFromString(lang string) string {
	for k, v := range languages {
		if v == lang {
			return k
		}
	}
	return "1"
}

func audioModeChoiceFromMode(mode CaptureMode) string {
	if mode == CaptureModeSystem {
		return "2"
	}
	if mode == CaptureModeBoth {
		return "3"
	}
	return "1"
}

func selectProvider(scanner *bufio.Scanner) (Provider, error) {
	fmt.Println("\nSelect model:")
	fmt.Println("  1: Claude Opus 4.6 (Anthropic)")
	fmt.Println("  2: GPT-5.3 Codex (OpenAI)")
	fmt.Println("  3: GPT-5.4 (OpenAI)")
	fmt.Print("Choice [1]: ")
	scanner.Scan()
	return providerFromChoice(strings.TrimSpace(scanner.Text()))
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
	return rendererFromChoice(strings.TrimSpace(scanner.Text()))
}

type helpLine struct {
	action HotkeyAction
	text   string
}

const summarizePrompt = `Summarize this meeting transcript segment into concise bullet points.
Preserve: key decisions, action items, names, technical terms, questions raised.
Omit: filler words, repetition, small talk.
Keep the summary under 500 words.

Transcript segment:
`

var helpLines = []helpLine{
	{HotkeyCapture, "screen capture (stores for process)"},
	{HotkeyAudioCapture, "toggle audio capture (system audio)"},
	{HotkeyFollowUp, "toggle mic recording"},
	{HotkeyAudioSend, "process accumulated transcript via LLM"},
	{HotkeySoundCheck, "sound check (5s mic + audio test)"},
	{HotkeyClear, "clear history"},
}

var languages = map[string]string{
	"1": "Python",
	"2": "JavaScript (ECMAScript 6)",
	"3": "TypeScript",
	"4": "Go",
	"5": "Java",
	"6": "C++",
	"7": "Rust",
}

var fenceLangs = map[string]string{
	"Python":                    "python",
	"JavaScript (ECMAScript 6)": "javascript",
	"TypeScript":                "typescript",
	"Go":                        "go",
	"Java":                      "java",
	"C++":                       "cpp",
	"Rust":                      "rust",
}

func fenceLang(lang string) string {
	if f, ok := fenceLangs[lang]; ok {
		return f
	}
	return strings.ToLower(lang)
}

func selectLanguage(scanner *bufio.Scanner) string {
	fmt.Println("\nCode language:")
	fmt.Println("  1: Python")
	fmt.Println("  2: JavaScript (ECMAScript 6)")
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

func stopMic(recorder *Recorder, renderer Renderer, micStopCh *chan struct{}) {
	if *micStopCh == nil {
		return
	}
	close(*micStopCh)
	*micStopCh = nil
	recorder.Stop()
	renderer.SetMicRecording(false)
}

func llmGuardAsync(llmBusy *atomic.Bool, renderer Renderer, fn func()) {
	if !llmBusy.CompareAndSwap(false, true) {
		renderer.SetStatus("LLM busy")
		return
	}
	go func() {
		defer llmBusy.Store(false)
		fn()
	}()
}

func handleAction(action HotkeyAction, monitorIdx int, provider Provider, renderer Renderer, recorder *Recorder, ac *AudioCapture, whisperURL string, lang string, appState *AppState, micStopCh *chan struct{}, soundCheckOn *atomic.Bool, llmBusy *atomic.Bool) {
	handlers := map[HotkeyAction]func(){
		HotkeyCapture: func() {
			go handleCaptureStore(monitorIdx, renderer, appState)
		},
		HotkeyFollowUp: func() {
			if *micStopCh != nil {
				handleMicStop(recorder, renderer, ac, whisperURL, micStopCh)
				renderer.SetMicRecording(false)
				return
			}
			*micStopCh = handleMicStart(recorder, renderer, ac, whisperURL)
			renderer.SetMicRecording(true)
		},
		HotkeyExplain: func() {
			llmGuardAsync(llmBusy, renderer, func() {
				handleExplain(provider, renderer)
			})
		},
		HotkeyAudioCapture: func() {
			ac.Toggle()
			renderer.SetAudioRecording(ac.Active())
			if ac.Active() {
				go ac.RunChunkLoop()
			}
		},
		HotkeyAudioSend: func() {
			llmGuardAsync(llmBusy, renderer, func() {
				handleProcess(ac, provider, renderer, lang, appState)
			})
		},
		HotkeySoundCheck: func() {
			handleSoundCheckToggle(recorder, ac, renderer, soundCheckOn)
		},
		HotkeyImplement: func() {
			llmGuardAsync(llmBusy, renderer, func() {
				handleImplement(provider, renderer, lang)
			})
		},
		HotkeyClear: func() {
			stopMic(recorder, renderer, micStopCh)
			appState.Clear()
			ac.ClearAll()
			provider.ClearHistory()
			renderer.Clear()
		},
	}

	fn, ok := handlers[action]
	if !ok {
		return
	}
	fn()
}

func audioSendPrefix(lang string) string {
	return `The following is a raw audio transcript from the user. Start your response with a "**🎤 User said:** " line that summarizes what the user asked/said in 1-2 sentences, then a blank line, then respond naturally and conversationally. If code is requested, use ` + lang + `. Do NOT repeat or regenerate previous solutions — just answer the question or continue the discussion. Be concise.

Transcript:
`
}

func finishStream(renderer Renderer, ac *AudioCapture) {
	renderer.AppendStreamDone()
	renderer.SetStatus("")
	ac.ClearSelections()
	renderer.ClearTranscriptCheckboxes()
}

func handleProcess(ac *AudioCapture, provider Provider, renderer Renderer, lang string, appState *AppState) {
	screenshot, hasScreen := appState.ConsumeScreenshot()
	if hasScreen {
		renderer.SetScreenLoaded(false)
	}

	transcript := ac.BuildSelectedContext()
	if transcript == "" {
		transcript = ac.BuildContext()
	}

	hasContext := provider.ContextDir() != ""

	if !hasScreen && transcript == "" && !hasContext {
		renderer.SetStatus("nothing to process")
		return
	}

	renderer.AppendStreamStart()

	if hasScreen {
		renderer.SetStatus("solving...")
		_, err := provider.Solve(screenshot, transcript, func(delta string) {
			renderer.AppendStreamDelta(delta)
		})
		if err != nil {
			renderer.SetStatus("solve error: " + err.Error())
			return
		}
		finishStream(renderer, ac)
		return
	}

	fence := fenceLang(lang)
	prompt := "Based on the provided context files, first analyze the problem and requirements, then implement the complete working solution in " + lang + ". Use markdown fenced code blocks (```" + fence + ") for all code.\n\n" + codeRules
	if transcript != "" {
		prompt = audioSendPrefix(lang) + transcript + "\n\n" + codeRules
	}
	renderer.SetStatus("sending to LLM...")
	_, err := provider.FollowUp(prompt, func(delta string) {
		renderer.AppendStreamDelta(delta)
	})
	if err != nil {
		renderer.SetStatus("follow-up error: " + err.Error())
		return
	}
	finishStream(renderer, ac)
}

func handleMicStart(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string) chan struct{} {
	renderer.SetStatus("mic recording...")
	if err := recorder.Start(); err != nil {
		renderer.SetStatus("mic error: " + err.Error())
		return nil
	}

	stopCh := make(chan struct{})
	go runChunkLoop(micMinChunkDuration, micMaxChunkDuration, micSilenceWindow, silenceThreshold, micPollInterval, recorder, stopCh, func() {
		micTranscribeChunk(recorder, renderer, ac, whisperURL)
	})
	return stopCh
}

func transcribeAndAppend(samples []int16, renderer Renderer, ac *AudioCapture, whisperURL, statusPrefix string) bool {
	if len(samples) == 0 {
		return false
	}
	chunkRMS := rms(samples)
	if !hasSpeech(samples, silenceWindow, silenceThreshold) {
		fmt.Printf("[audio-capture] dropped chunk: %d samples, rms=%.0f (no speech window above %.0f)\n", len(samples), chunkRMS, silenceThreshold)
		return false
	}
	fmt.Printf("[audio-capture] sending chunk: %d samples, rms=%.0f\n", len(samples), chunkRMS)
	wavData := EncodeWAV(samples, asrSampleRate)
	text, err := Transcribe(wavData, whisperURL)
	if err != nil {
		renderer.SetStatus("asr error: " + err.Error())
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	ac.RecordMicText(trimmed)
	id := ac.AddEntry(trimmed)
	ac.AppendTranscript(trimmed)
	renderer.AppendTranscriptChunk("mic", trimmed, id)
	renderer.SetStatus(fmt.Sprintf("%s — %d chars accumulated", statusPrefix, ac.TranscriptLen()))
	return true
}

func handleMicStop(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string, micStopCh *chan struct{}) {
	close(*micStopCh)
	*micStopCh = nil

	renderer.SetStatus("transcribing final mic chunk...")
	samples := recorder.Stop()
	if !transcribeAndAppend(samples, renderer, ac, whisperURL, "mic stopped") {
		renderer.SetStatus("mic stopped")
	}
}

func micTranscribeChunk(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string) {
	transcribeAndAppend(recorder.DrainSamples(), renderer, ac, whisperURL, "mic")
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

func handleImplement(provider Provider, renderer Renderer, lang string) {
	fence := fenceLang(lang)
	prompt := "Based on our conversation so far, please implement the complete, working solution in " + lang + ". " +
		"Use markdown fenced code blocks (```" + fence + ") for all code. Keep explanations minimal.\n\n" + codeRules

	renderer.SetStatus("implementing...")
	renderer.AppendStreamStart()
	_, err := provider.FollowUp(prompt, func(delta string) {
		renderer.AppendStreamDelta(delta)
	})
	if err != nil {
		renderer.SetStatus("follow-up error: " + err.Error())
		return
	}
	renderer.AppendStreamDone()
	renderer.SetStatus("")
}

func selectWhisperModel(scanner *bufio.Scanner) string {
	fmt.Println("\nWhisper model:")
	fmt.Println("  1: large-v3-turbo (faster)")
	fmt.Println("  2: large-v3 (more accurate)")
	fmt.Print("Choice [1]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "2" {
		return "2"
	}
	return "1"
}

func selectOverlayMonitor(scanner *bufio.Scanner, monitors []MonitorInfo, renderer Renderer) int {
	if findOverlay(renderer) == nil {
		return 0
	}
	if len(monitors) < 2 {
		return 0
	}
	fmt.Println("\nOverlay monitor:")
	for _, m := range monitors {
		fmt.Printf("  %d: %s — %s (%s)\n", m.Index+1, m.Model, m.Output, m.Geom)
	}
	fmt.Print("Choice [1]: ")
	scanner.Scan()
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return 0
	}
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(monitors) {
		return 0
	}
	return idx - 1
}

func selectOverlayFullscreen(scanner *bufio.Scanner, renderer Renderer) bool {
	if findOverlay(renderer) == nil {
		return false
	}
	fmt.Print("\nOverlay fullscreen? (y/N): ")
	scanner.Scan()
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return input == "y" || input == "yes"
}

func selectContextDir(scanner *bufio.Scanner) string {
	fmt.Print("\nContext file or directory (Enter to skip): ")
	scanner.Scan()
	path := strings.TrimSpace(scanner.Text())
	if path == "" {
		return ""
	}
	_, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %q does not exist, skipping\n", path)
		return ""
	}
	return path
}

func whisperModel(choice string) struct{ label, file, url string } {
	m := whisperModels[choice]
	if m.file == "" {
		m = whisperModels["1"]
	}
	return m
}

func whisperModelPath(choice string) string {
	return os.ExpandEnv("$HOME/.local/share/whisper/" + whisperModel(choice).file)
}

func whisperModelURL(choice string) string {
	return whisperModel(choice).url
}

func ensureWhisperModel(path, url string) {
	if _, err := os.Stat(path); err == nil {
		return
	}

	fmt.Printf("downloading whisper model to %s ...\n", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: mkdir: %v\n", err)
		return
	}

	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: download failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "warning: download returned %d\n", resp.StatusCode)
		return
	}

	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: create file: %v\n", err)
		return
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write model: %v\n", err)
		os.Remove(path)
		return
	}

	fmt.Println("whisper model downloaded")
}

// ensureWhisperServer starts whisper-server if not already running.
// Returns the process handle (caller should defer Kill) or nil if already running.
func ensureWhisperServer(whisperURL, model string) *exec.Cmd {
	if whisperHealthy(whisperURL) {
		fmt.Println("whisper-server already running")
		return nil
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


func runVULoop(renderer Renderer, recorder *Recorder, ac *AudioCapture, soundCheck *atomic.Bool) {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		updateVU(renderer, recorder, ac, soundCheck)
	}
}

func updateVU(renderer Renderer, recorder *Recorder, ac *AudioCapture, soundCheck *atomic.Bool) {
	if !soundCheck.Load() {
		return
	}
	micLevel := rmsToVU(recorder.PeekTailRMS(512) / 32768.0)
	audioLevel := rmsToVU(ac.PeekTailRMS(512))
	renderer.UpdateVU(micLevel, audioLevel)
}

// rmsToVU converts a linear 0–1 RMS value to a 0–1 dB-scaled VU level.
// Maps -60 dB → 0.0 and 0 dB → 1.0.
func rmsToVU(linear float64) float64 {
	if linear < 1e-6 {
		return 0
	}
	db := 20 * math.Log10(linear)
	vu := (db + 60) / 60
	return max(0, min(vu, 1.0))
}

func handleSoundCheckToggle(recorder *Recorder, ac *AudioCapture, renderer Renderer, on *atomic.Bool) {
	if on.Load() {
		recorder.Stop()
		if ac.Active() {
			ac.Toggle()
		}
		on.Store(false)
		renderer.SetSoundCheck(false)
		renderer.UpdateVU(0, 0)
		renderer.SetStatus("sound check off")
		return
	}
	if err := recorder.Start(); err != nil {
		renderer.SetStatus("sound check mic error: " + err.Error())
		return
	}
	if !ac.Active() {
		ac.StartSoundCheck()
	}
	on.Store(true)
	renderer.SetSoundCheck(true)
	renderer.SetStatus("sound check — speak or play audio...")
}

func handleCaptureStore(monitorIdx int, renderer Renderer, appState *AppState) {
	renderer.SetStatus("capturing...")
	imgData, err := captureMonitor(monitorIdx)
	if err != nil {
		renderer.SetStatus("capture error: " + err.Error())
		return
	}
	appState.SetScreenshot(imgData)
	renderer.SetScreenLoaded(true)
	renderer.SetStatus("screen captured — ready to process")
}
