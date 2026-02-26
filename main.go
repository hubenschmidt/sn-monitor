package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

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

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Fprintf(os.Stderr, "error: ANTHROPIC_API_KEY not set\n")
		os.Exit(1)
	}

	fmt.Printf("\nModel: %s\n", solveModel)
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
		handleCapture(selected)
	}
}

func handleCapture(monitorIdx int) {
	fmt.Println("capturing...")
	pngData, err := captureMonitor(monitorIdx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture error: %v\n", err)
		return
	}
	fmt.Println("solving...")
	answer, err := solve(pngData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "solve error: %v\n", err)
		return
	}
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println(answer)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
}
