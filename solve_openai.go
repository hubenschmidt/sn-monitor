package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

const openAIModel shared.ResponsesModel = "gpt-5.3-codex"

type OpenAIProvider struct {
	client             openai.Client
	previousResponseID string
}

func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{client: openai.NewClient()}
}

func (p *OpenAIProvider) ModelName() string {
	return string(openAIModel)
}

func (p *OpenAIProvider) Solve(pngData []byte, onDelta func(string)) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(pngData)
	dataURL := "data:image/jpeg;base64," + b64

	params := responses.ResponseNewParams{
		Model:           openAIModel,
		MaxOutputTokens: openai.Int(4096),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				{OfMessage: &responses.EasyInputMessageParam{
					Role: "user",
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: responses.ResponseInputMessageContentListParam{
							responses.ResponseInputContentParamOfInputText(solvePrompt),
							{OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String(dataURL),
								Detail:   "high",
							}},
						},
					},
				}},
			},
		},
	}

	if p.previousResponseID != "" {
		params.PreviousResponseID = openai.String(p.previousResponseID)
	}

	stream := p.client.Responses.NewStreaming(context.Background(), params)

	var buf strings.Builder
	for stream.Next() {
		evt := stream.Current()
		if evt.Type == "response.output_text.delta" {
			buf.WriteString(evt.Delta.OfString)
			onDelta(evt.Delta.OfString)
		}
		if evt.Type == "response.completed" {
			p.previousResponseID = evt.Response.ID
		}
	}

	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("api call failed: %w", err)
	}

	text := buf.String()
	if text == "" {
		return "", fmt.Errorf("no text in response")
	}
	return text, nil
}
