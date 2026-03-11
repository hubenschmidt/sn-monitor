package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

type AnthropicProvider struct {
	client     anthropic.Client
	model      anthropic.Model
	lang       string
	contextDir string
	history    []anthropic.MessageParam
}

func NewAnthropicProvider(model anthropic.Model) *AnthropicProvider {
	return &AnthropicProvider{client: anthropic.NewClient(), model: model, lang: "Python"}
}

func (p *AnthropicProvider) SetLanguage(lang string) {
	p.lang = lang
}

func (p *AnthropicProvider) SetContextDir(dir string) {
	p.contextDir = dir
}

func (p *AnthropicProvider) ClearHistory() {
	p.history = nil
}

func (p *AnthropicProvider) ModelName() string {
	return string(p.model)
}

func (p *AnthropicProvider) Solve(pngData []byte, transcript string, onDelta func(string)) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(pngData)

	p.history = append(p.history, anthropic.NewUserMessage(
		anthropic.NewImageBlockBase64("image/jpeg", b64),
		anthropic.NewTextBlock(buildSolvePrompt(p.lang, readContextPath(p.contextDir), transcript)),
	))

	stream := p.client.Messages.NewStreaming(context.Background(), anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		Messages:  p.history,
	})

	var buf strings.Builder
	for stream.Next() {
		evt := stream.Current()
		if evt.Type != "content_block_delta" {
			continue
		}
		if evt.Delta.Type != "text_delta" {
			continue
		}
		buf.WriteString(evt.Delta.Text)
		onDelta(evt.Delta.Text)
	}

	if err := stream.Err(); err != nil {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("api call failed: %w", err)
	}

	text := buf.String()
	if text == "" {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("no text in response")
	}

	p.history = append(p.history, anthropic.NewAssistantMessage(
		anthropic.NewTextBlock(text),
	))
	return text, nil
}

func (p *AnthropicProvider) Summarize(text string) (string, error) {
	stream := p.client.Messages.NewStreaming(context.Background(), anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 2048,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(text)),
		},
	})

	var buf strings.Builder
	for stream.Next() {
		evt := stream.Current()
		if evt.Type != "content_block_delta" {
			continue
		}
		if evt.Delta.Type != "text_delta" {
			continue
		}
		buf.WriteString(evt.Delta.Text)
	}

	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("summarize failed: %w", err)
	}
	return buf.String(), nil
}

func (p *AnthropicProvider) FollowUp(text string, onDelta func(string)) (string, error) {
	msg := readContextPath(p.contextDir) + text
	p.history = append(p.history, anthropic.NewUserMessage(
		anthropic.NewTextBlock(msg),
	))

	stream := p.client.Messages.NewStreaming(context.Background(), anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		Messages:  p.history,
	})

	var buf strings.Builder
	for stream.Next() {
		evt := stream.Current()
		if evt.Type != "content_block_delta" {
			continue
		}
		if evt.Delta.Type != "text_delta" {
			continue
		}
		buf.WriteString(evt.Delta.Text)
		onDelta(evt.Delta.Text)
	}

	if err := stream.Err(); err != nil {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("api call failed: %w", err)
	}

	reply := buf.String()
	if reply == "" {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("no text in response")
	}

	p.history = append(p.history, anthropic.NewAssistantMessage(
		anthropic.NewTextBlock(reply),
	))
	return reply, nil
}
