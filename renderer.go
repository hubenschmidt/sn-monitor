package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

type Renderer interface {
	Render(markdown string) error
	SetStatus(status string)
	Close()
}

type TerminalRenderer struct{}

func (t *TerminalRenderer) Render(markdown string) error {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)
	rendered := markdown
	if err == nil {
		rendered, _ = renderer.Render(markdown)
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Print(rendered)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
	return nil
}

func (t *TerminalRenderer) SetStatus(status string) {
	fmt.Println(status)
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

func (m *MultiRenderer) Close() {
	for _, r := range m.renderers {
		r.Close()
	}
}
