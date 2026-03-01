package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/joho/godotenv"
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

	fmt.Printf("\nModel: %s\n", provider.ModelName())
	fmt.Printf("Watching %s (%s). Press Left+Right arrows to capture, Ctrl+C to quit.\n\n",
		monitors[selected].Model, monitors[selected].Output)

	ch := make(chan struct{}, 1)
	go func() {
		if err := listenHotkey(ch); err != nil {
			fmt.Fprintf(os.Stderr, "hotkey error: %v\n", err)
			os.Exit(1)
		}
	}()

	overlay := findOverlay(renderer)
	if overlay == nil {
		for range ch {
			handleCapture(selected, provider, renderer)
		}
		return
	}

	// Overlay mode: webview.Run() must be on the main thread
	go func() {
		for range ch {
			handleCapture(selected, provider, renderer)
		}
	}()
	overlay.Run()
}

func selectProvider(scanner *bufio.Scanner) (Provider, error) {
	fmt.Println("\nSelect model:")
	fmt.Println("  1: Claude Opus 4.6 (Anthropic)")
	fmt.Println("  2: GPT-5.3 Codex (OpenAI)")
	fmt.Print("Choice [1]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "" || input == "1" {
		return newAnthropicProvider("claude-opus-4-6")
	}
	if input == "2" {
		return newOpenAIProvider()
	}
	return nil, fmt.Errorf("invalid model selection: %s", input)
}

func newAnthropicProvider(model string) (Provider, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	return NewAnthropicProvider(anthropic.Model(model)), nil
}

func newOpenAIProvider() (Provider, error) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	return NewOpenAIProvider(), nil
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

func handleCapture(monitorIdx int, provider Provider, renderer Renderer) {
	renderer.SetStatus("capturing...")
	imgData, err := captureMonitor(monitorIdx)
	if err != nil {
		renderer.SetStatus("capture error: " + err.Error())
		return
	}
	renderer.SetStatus("solving...")
	renderer.StreamStart()
	_, err = provider.Solve(imgData, func(delta string) {
		renderer.StreamDelta(delta)
	})
	if err != nil {
		renderer.SetStatus("solve error: " + err.Error())
		return
	}
	renderer.StreamDone()
}
