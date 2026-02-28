package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
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

	for range ch {
		handleCapture(selected, provider)
	}
}

func selectProvider(scanner *bufio.Scanner) (Provider, error) {
	fmt.Println("\nSelect model:")
	fmt.Println("  1: Claude Opus 4.6 (Anthropic)")
	fmt.Println("  2: GPT-5.3 Codex (OpenAI)")
	fmt.Print("Choice [1]: ")
	scanner.Scan()

	input := strings.TrimSpace(scanner.Text())
	if input == "" || input == "1" {
		return newAnthropicProvider()
	}
	if input == "2" {
		return newOpenAIProvider()
	}
	return nil, fmt.Errorf("invalid model selection: %s", input)
}

func newAnthropicProvider() (Provider, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	return NewAnthropicProvider(), nil
}

func newOpenAIProvider() (Provider, error) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	return NewOpenAIProvider(), nil
}

func handleCapture(monitorIdx int, provider Provider) {
	fmt.Println("capturing...")
	pngData, err := captureMonitor(monitorIdx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture error: %v\n", err)
		return
	}
	fmt.Println("solving...")
	answer, err := provider.Solve(pngData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "solve error: %v\n", err)
		return
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)
	rendered := answer
	if err == nil {
		rendered, _ = renderer.Render(answer)
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Print(rendered)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
}
