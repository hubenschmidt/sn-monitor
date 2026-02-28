package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

type AnthropicProvider struct {
	client  anthropic.Client
	history []anthropic.MessageParam
}

func NewAnthropicProvider() *AnthropicProvider {
	client := anthropic.NewClient()
	return &AnthropicProvider{client: client}
}

func (p *AnthropicProvider) ModelName() string {
	return string(anthropic.ModelClaudeOpus4_6)
}

func (p *AnthropicProvider) Solve(pngData []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(pngData)

	p.history = append(p.history, anthropic.NewUserMessage(
		anthropic.NewImageBlockBase64("image/png", b64),
		anthropic.NewTextBlock(solvePrompt),
	))

	resp, err := p.client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeOpus4_6,
		MaxTokens: 4096,
		Messages:  p.history,
	})
	if err != nil {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("api call failed: %w", err)
	}

	text := extractText(resp.Content)
	if text == "" {
		return "", fmt.Errorf("no text in response")
	}

	p.history = append(p.history, anthropic.NewAssistantMessage(
		anthropic.NewTextBlock(text),
	))
	return text, nil
}

func extractText(blocks []anthropic.ContentBlockUnion) string {
	for _, block := range blocks {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}
