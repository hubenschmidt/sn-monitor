package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

const openAIModel shared.ResponsesModel = "gpt-5.2-codex"

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

func (p *OpenAIProvider) Solve(pngData []byte) (string, error) {
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

	resp, err := p.client.Responses.New(context.Background(), params)
	if err != nil {
		return "", fmt.Errorf("api call failed: %w", err)
	}

	p.previousResponseID = resp.ID

	text := extractOpenAIText(resp.Output)
	if text == "" {
		return "", fmt.Errorf("no text in response")
	}
	return text, nil
}

func extractOpenAIText(output []responses.ResponseOutputItemUnion) string {
	for _, item := range output {
		if item.Type == "message" {
			return extractOutputText(item.Content)
		}
	}
	return ""
}

func extractOutputText(content []responses.ResponseOutputMessageContentUnion) string {
	for _, c := range content {
		if c.Type == "output_text" {
			return c.Text
		}
	}
	return ""
}
