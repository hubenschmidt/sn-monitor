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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/joho/godotenv"
	"github.com/openai/openai-go/shared"
)

const configPath = "config.json"

type AppConfig struct {
	Name         string `json:"name"`
	Monitor      int    `json:"monitor"`
	Provider     string `json:"provider"`
	Renderer     string `json:"renderer"`
	Language     string `json:"language"`
	AudioMode    string `json:"audio_mode"`
	MonSource    string `json:"mon_source,omitempty"`
	WhisperModel string `json:"whisper_model,omitempty"`
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
	if len(configs) == 0 {
		return nil
	}

	fmt.Println("\nSaved configurations:")
	for i, c := range configs {
		monLabel := fmt.Sprintf("#%d", c.Monitor+1)
		if c.Monitor >= 0 && c.Monitor < len(monitors) {
			m := monitors[c.Monitor]
			monLabel = fmt.Sprintf("%s (%s)", m.Model, m.Output)
		}
		prov := providerLabels[c.Provider]
		if prov == "" {
			prov = "?"
		}
		rend := rendererLabels[c.Renderer]
		if rend == "" {
			rend = "?"
		}
		lang := languages[c.Language]
		if lang == "" {
			lang = "?"
		}
		audio := audioModeLabels[c.AudioMode]
		if audio == "" {
			audio = "?"
		}
		if c.MonSource != "" {
			audio += " (" + c.MonSource + ")"
		}
		wm := whisperModels[c.WhisperModel]
		wmLabel := wm.label
		if wmLabel == "" {
			wmLabel = whisperModels["1"].label
		}
		fmt.Printf("  %d: %q\n", i+1, c.Name)
		fmt.Printf("     Monitor: %s | Model: %s | Output: %s\n", monLabel, prov, rend)
		fmt.Printf("     Language: %s | Audio: %s | Whisper: %s\n", lang, audio, wmLabel)
	}
	fmt.Printf("  %d: New configuration\n", len(configs)+1)
	fmt.Print("Choice [new]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return nil
	}
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(configs) {
		return nil
	}
	return &configs[idx-1]
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
	var provider Provider
	var renderer Renderer
	var lang string
	var captureMode CaptureMode
	var monSource string
	var whisperChoice string

	saved := promptSavedConfig(scanner, loadConfigs(), monitors)
	if saved != nil {
		selected, provider, renderer, lang, captureMode, monSource, whisperChoice, err = applyConfig(*saved, monitors)
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

		lang = selectLanguage(scanner)

		captureMode, monSource, err = SelectAudioMode(scanner)
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio mode error: %v\n", err)
			os.Exit(1)
		}

		whisperChoice = selectWhisperModel(scanner)

		promptSaveConfig(scanner, AppConfig{
			Monitor:      selected,
			Provider:     providerChoiceFromScanner(provider),
			Renderer:     rendererChoiceFromScanner(renderer),
			Language:     languageChoiceFromString(lang),
			AudioMode:    audioModeChoiceFromMode(captureMode),
			MonSource:    monSource,
			WhisperModel: whisperChoice,
		})
	}

	provider.SetLanguage(lang)

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
	var micStopCh chan struct{}
	var soundCheckOn atomic.Bool
	var llmBusy atomic.Bool
	dispatch := func() {
		for action := range ch {
			handleAction(action, selected, provider, renderer, recorder, ac, whisperURL, lang, &micStopCh, &soundCheckOn, &llmBusy)
		}
	}

	go runVULoop(renderer, recorder, ac, &soundCheckOn)

	overlay := findOverlay(renderer)
	if overlay == nil {
		dispatch()
		return
	}

	overlay.SetProvider(provider)
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
}

func applyConfig(cfg AppConfig, monitors []MonitorInfo) (int, Provider, Renderer, string, CaptureMode, string, string, error) {
	if cfg.Monitor < 0 || cfg.Monitor >= len(monitors) {
		return 0, nil, nil, "", 0, "", "", fmt.Errorf("saved monitor #%d no longer exists (%d available)", cfg.Monitor+1, len(monitors))
	}

	provider, err := providerFromChoice(cfg.Provider)
	if err != nil {
		return 0, nil, nil, "", 0, "", "", err
	}

	renderer := rendererFromChoice(cfg.Renderer)
	lang := languageFromChoice(cfg.Language)

	captureMode, monSource, err := audioModeFromChoice(cfg.AudioMode, cfg.MonSource)
	if err != nil {
		return 0, nil, nil, "", 0, "", "", err
	}

	whisper := cfg.WhisperModel
	if whisper == "" {
		whisper = "1"
	}

	return cfg.Monitor, provider, renderer, lang, captureMode, monSource, whisper, nil
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

const summarizePrompt = `Summarize this meeting transcript segment into concise bullet points.
Preserve: key decisions, action items, names, technical terms, questions raised.
Omit: filler words, repetition, small talk.
Keep the summary under 500 words.

Transcript segment:
`

var helpLines = []helpLine{
	{HotkeyCapture, "screen capture → solve"},
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

func handleAction(action HotkeyAction, monitorIdx int, provider Provider, renderer Renderer, recorder *Recorder, ac *AudioCapture, whisperURL string, lang string, micStopCh *chan struct{}, soundCheckOn *atomic.Bool, llmBusy *atomic.Bool) {
	if action == HotkeyCapture {
		if !llmBusy.CompareAndSwap(false, true) {
			renderer.SetStatus("LLM busy")
			return
		}
		go func() {
			defer llmBusy.Store(false)
			handleCapture(monitorIdx, provider, renderer)
		}()
		return
	}
	if action == HotkeyFollowUp && *micStopCh != nil {
		handleMicStop(recorder, renderer, ac, whisperURL, micStopCh)
		renderer.SetMicRecording(false)
		return
	}
	if action == HotkeyFollowUp {
		*micStopCh = handleMicStart(recorder, renderer, ac, whisperURL)
		renderer.SetMicRecording(true)
		return
	}
	if action == HotkeyExplain {
		if !llmBusy.CompareAndSwap(false, true) {
			renderer.SetStatus("LLM busy")
			return
		}
		go func() {
			defer llmBusy.Store(false)
			handleExplain(provider, renderer)
		}()
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
		if !llmBusy.CompareAndSwap(false, true) {
			renderer.SetStatus("LLM busy")
			return
		}
		go func() {
			defer llmBusy.Store(false)
			handleAudioSend(ac, provider, renderer, lang)
		}()
		return
	}
	if action == HotkeySoundCheck {
		handleSoundCheckToggle(recorder, ac, renderer, soundCheckOn)
		return
	}
	if action == HotkeyImplement {
		if !llmBusy.CompareAndSwap(false, true) {
			renderer.SetStatus("LLM busy")
			return
		}
		go func() {
			defer llmBusy.Store(false)
			handleImplement(provider, renderer, ac, lang)
		}()
		return
	}
	if action == HotkeyClear {
		if *micStopCh != nil {
			close(*micStopCh)
			*micStopCh = nil
			recorder.Stop()
			renderer.SetMicRecording(false)
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
	transcript := ac.BuildSelectedContext()
	if transcript == "" {
		transcript = ac.BuildContext()
	}
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

	ac.ClearSelections()
	renderer.ClearTranscriptCheckboxes()
}

func handleMicStart(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string) chan struct{} {
	renderer.SetStatus("mic recording...")
	if err := recorder.Start(); err != nil {
		renderer.SetStatus("mic error: " + err.Error())
		return nil
	}

	stopCh := make(chan struct{})
	go runChunkLoop(micMinChunkDuration, micMaxChunkDuration, recorder, stopCh, func() {
		micTranscribeChunk(recorder, renderer, ac, whisperURL)
	})
	return stopCh
}

func handleMicStop(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string, micStopCh *chan struct{}) {
	close(*micStopCh)
	*micStopCh = nil

	// Final drain of remaining samples
	samples := recorder.Stop()
	if len(samples) == 0 {
		renderer.SetStatus("mic stopped")
		return
	}

	if rms(samples) < silenceThreshold {
		renderer.SetStatus("mic stopped")
		return
	}

	renderer.SetStatus("transcribing final mic chunk...")
	wavData := EncodeWAV(samples, asrSampleRate)
	transcript, err := Transcribe(wavData, whisperURL)
	if err != nil {
		renderer.SetStatus("asr error: " + err.Error())
		return
	}

	trimmed := strings.TrimSpace(transcript)
	if trimmed == "" {
		renderer.SetStatus("mic stopped")
		return
	}

	id := ac.AddEntry(trimmed)
	ac.AppendTranscript(trimmed)
	renderer.AppendTranscriptChunk("mic", trimmed, id)
	renderer.SetStatus(fmt.Sprintf("mic stopped — %d chars accumulated", ac.TranscriptLen()))
}

func micTranscribeChunk(recorder *Recorder, renderer Renderer, ac *AudioCapture, whisperURL string) {
	samples := recorder.DrainSamples()
	if len(samples) == 0 {
		return
	}
	if rms(samples) < silenceThreshold {
		return
	}

	wavData := EncodeWAV(samples, asrSampleRate)
	text, err := Transcribe(wavData, whisperURL)
	if err != nil {
		fmt.Printf("[mic] transcribe error: %v\n", err)
		return
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}

	id := ac.AddEntry(trimmed)
	ac.AppendTranscript(trimmed)
	renderer.AppendTranscriptChunk("mic", trimmed, id)
	renderer.SetStatus(fmt.Sprintf("mic — %d chars accumulated", ac.TranscriptLen()))
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

func handleImplement(provider Provider, renderer Renderer, ac *AudioCapture, lang string) {
	transcript := ac.BuildContext()
	fence := fenceLang(lang)
	prompt := "Based on our conversation so far, please implement the complete, working solution in " + lang + ". " +
		"Use markdown fenced code blocks (```" + fence + ") for all code. Keep explanations minimal."
	if transcript != "" {
		prompt += "\n\nAdditional context from audio transcript:\n" + transcript
	}

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

func whisperModelPath(choice string) string {
	m := whisperModels[choice]
	if m.file == "" {
		m = whisperModels["1"]
	}
	return os.ExpandEnv("$HOME/.local/share/whisper/" + m.file)
}

func whisperModelURL(choice string) string {
	m := whisperModels[choice]
	if m.url == "" {
		m = whisperModels["1"]
	}
	return m.url
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
		ac.Toggle()
	}
	on.Store(true)
	renderer.SetSoundCheck(true)
	renderer.SetStatus("sound check — speak or play audio...")
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
