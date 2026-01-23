package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

var history []anthropic.MessageParam

func solve(pngData []byte) (string, error) {
	client := anthropic.NewClient()

	b64 := base64.StdEncoding.EncodeToString(pngData)

	history = append(history, anthropic.NewUserMessage(
		anthropic.NewImageBlockBase64("image/png", b64),
		anthropic.NewTextBlock("Look at this screen capture. If there's a code problem, solve it concisely. If it's a continuation of a previous problem, build on your prior answer."),
	))

	resp, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeOpus4_5_20251101,
		MaxTokens: 2048,
		Messages:  history,
	})
	if err != nil {
		// Remove the failed user message
		history = history[:len(history)-1]
		return "", fmt.Errorf("api call failed: %w", err)
	}

	for _, block := range resp.Content {
		if block.Type == "text" {
			history = append(history, anthropic.NewAssistantMessage(
				anthropic.NewTextBlock(block.Text),
			))
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("no text in response")
}
