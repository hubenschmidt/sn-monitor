package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

type Renderer interface {
	Render(markdown string) error
	SetStatus(status string)
	StreamStart()
	StreamDelta(delta string)
	StreamDone()
	Close()
}

type TerminalRenderer struct {
	streamBuf strings.Builder
}

func (t *TerminalRenderer) Render(markdown string) error {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		fmt.Println(strings.Repeat("─", 60))
		fmt.Print(markdown)
		fmt.Println(strings.Repeat("─", 60))
		fmt.Println()
		return nil
	}

	rendered, _ := renderer.Render(markdown)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Print(rendered)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
	return nil
}

func (t *TerminalRenderer) SetStatus(status string) {
	fmt.Println(status)
}

func (t *TerminalRenderer) StreamStart() {
	t.streamBuf.Reset()
}

func (t *TerminalRenderer) StreamDelta(delta string) {
	t.streamBuf.WriteString(delta)
	fmt.Printf("\rstreaming... %d chars", t.streamBuf.Len())
}

func (t *TerminalRenderer) StreamDone() {
	fmt.Print("\r\033[K")
	t.Render(t.streamBuf.String())
}

func (t *TerminalRenderer) Close() {}

type MultiRenderer struct {
	renderers []Renderer
}

func (m *MultiRenderer) Render(markdown string) error {
	for _, r := range m.renderers {
		r.Render(markdown)
	}
	return nil
}

func (m *MultiRenderer) SetStatus(status string) {
	for _, r := range m.renderers {
		r.SetStatus(status)
	}
}

func (m *MultiRenderer) StreamStart() {
	for _, r := range m.renderers {
		r.StreamStart()
	}
}

func (m *MultiRenderer) StreamDelta(delta string) {
	for _, r := range m.renderers {
		r.StreamDelta(delta)
	}
}

func (m *MultiRenderer) StreamDone() {
	for _, r := range m.renderers {
		r.StreamDone()
	}
}

func (m *MultiRenderer) Close() {
	for _, r := range m.renderers {
		r.Close()
	}
}
