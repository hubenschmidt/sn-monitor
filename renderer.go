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
	AppendStreamStart()
	AppendStreamDelta(delta string)
	AppendStreamDone()
	AppendTranscriptChunk(source, text string, id int)
	ClearTranscriptCheckboxes()
	SetMicRecording(recording bool)
	SetAudioRecording(recording bool)
	SetSoundCheck(active bool)
	UpdateVU(micLevel, audioLevel float64)
	Clear()
	Close()
}

func hotkeyFooter() string {
	parts := make([]string, len(keyOrder))
	for i, a := range keyOrder {
		parts[i] = keyLabels[a]
	}
	return "\033[2m " + strings.Join(parts, " · ") + " \033[0m"
}

type TerminalRenderer struct {
	streamBuf strings.Builder
	history   strings.Builder // accumulated rendered output
	status    string
}

func (t *TerminalRenderer) renderMarkdown(markdown string) string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return markdown
	}
	rendered, _ := renderer.Render(markdown)
	return rendered
}

func (t *TerminalRenderer) repaint() {
	fmt.Print("\033[2J\033[H")
	fmt.Print(t.history.String())
	t.printFooter()
}

func (t *TerminalRenderer) printFooter() {
	if t.status != "" {
		fmt.Printf("\033[33m %s \033[0m\n", t.status)
	}
	fmt.Println(hotkeyFooter())
}

func (t *TerminalRenderer) Render(markdown string) error {
	sep := strings.Repeat("─", 60)
	rendered := t.renderMarkdown(markdown)
	t.history.WriteString(sep + "\n")
	t.history.WriteString(rendered)
	t.history.WriteString(sep + "\n\n")
	t.repaint()
	return nil
}

func (t *TerminalRenderer) SetStatus(status string) {
	t.status = status
	fmt.Printf("\033[s\033[999B\033[2K\033[33m %s \033[0m\033[u", status)
}

func (t *TerminalRenderer) StreamStart() {
	t.streamBuf.Reset()
}

func (t *TerminalRenderer) StreamDelta(delta string) {
	t.streamBuf.WriteString(delta)
}

func (t *TerminalRenderer) StreamDone() {
	t.Render(t.streamBuf.String())
}

func (t *TerminalRenderer) AppendStreamStart() {
	t.streamBuf.Reset()
	sep := strings.Repeat("─", 60)
	t.history.WriteString("\n" + sep + "\n▼ follow-up\n" + sep + "\n")
}

func (t *TerminalRenderer) AppendStreamDelta(delta string) {
	t.streamBuf.WriteString(delta)
}

func (t *TerminalRenderer) AppendStreamDone() {
	t.Render(t.streamBuf.String())
}

func (t *TerminalRenderer) AppendTranscriptChunk(source, text string, id int) {
	fmt.Printf("\033[2m[%s] %s\033[0m\n", source, text)
}

func (t *TerminalRenderer) ClearTranscriptCheckboxes() {}

func (t *TerminalRenderer) SetMicRecording(recording bool) {}

func (t *TerminalRenderer) SetAudioRecording(recording bool) {}

func (t *TerminalRenderer) SetSoundCheck(active bool) {}

func (t *TerminalRenderer) UpdateVU(micLevel, audioLevel float64) {}

func (t *TerminalRenderer) Clear() {
	t.history.Reset()
	t.repaint()
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

func (m *MultiRenderer) AppendStreamStart() {
	for _, r := range m.renderers {
		r.AppendStreamStart()
	}
}

func (m *MultiRenderer) AppendStreamDelta(delta string) {
	for _, r := range m.renderers {
		r.AppendStreamDelta(delta)
	}
}

func (m *MultiRenderer) AppendStreamDone() {
	for _, r := range m.renderers {
		r.AppendStreamDone()
	}
}

func (m *MultiRenderer) AppendTranscriptChunk(source, text string, id int) {
	for _, r := range m.renderers {
		r.AppendTranscriptChunk(source, text, id)
	}
}

func (m *MultiRenderer) ClearTranscriptCheckboxes() {
	for _, r := range m.renderers {
		r.ClearTranscriptCheckboxes()
	}
}

func (m *MultiRenderer) SetMicRecording(recording bool) {
	for _, r := range m.renderers {
		r.SetMicRecording(recording)
	}
}

func (m *MultiRenderer) SetAudioRecording(recording bool) {
	for _, r := range m.renderers {
		r.SetAudioRecording(recording)
	}
}

func (m *MultiRenderer) SetSoundCheck(active bool) {
	for _, r := range m.renderers {
		r.SetSoundCheck(active)
	}
}

func (m *MultiRenderer) UpdateVU(micLevel, audioLevel float64) {
	for _, r := range m.renderers {
		r.UpdateVU(micLevel, audioLevel)
	}
}

func (m *MultiRenderer) Clear() {
	for _, r := range m.renderers {
		r.Clear()
	}
}

func (m *MultiRenderer) Close() {
	for _, r := range m.renderers {
		r.Close()
	}
}
