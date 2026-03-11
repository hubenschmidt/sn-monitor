package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

type OpenAIProvider struct {
	client             openai.Client
	model              shared.ResponsesModel
	lang               string
	contextDir         string
	previousResponseID string
}

func NewOpenAIProvider(model shared.ResponsesModel) *OpenAIProvider {
	return &OpenAIProvider{client: openai.NewClient(), model: model, lang: "Python"}
}

func (p *OpenAIProvider) SetLanguage(lang string) {
	p.lang = lang
}

func (p *OpenAIProvider) SetContextDir(dir string) {
	p.contextDir = dir
}

func (p *OpenAIProvider) ContextDir() string {
	return p.contextDir
}

func (p *OpenAIProvider) ClearHistory() {
	p.previousResponseID = ""
}

func (p *OpenAIProvider) ModelName() string {
	return string(p.model)
}

func streamResponses(stream *ssestream.Stream[responses.ResponseStreamEventUnion], onDelta func(string)) (string, string, error) {
	var buf strings.Builder
	var responseID string
	for stream.Next() {
		evt := stream.Current()
		if evt.Type == "response.output_text.delta" {
			buf.WriteString(evt.Delta.OfString)
			if onDelta != nil {
				onDelta(evt.Delta.OfString)
			}
		}
		if evt.Type == "response.completed" {
			responseID = evt.Response.ID
		}
	}
	if err := stream.Err(); err != nil {
		return "", "", fmt.Errorf("api call failed: %w", err)
	}
	return buf.String(), responseID, nil
}

func (p *OpenAIProvider) Solve(pngData []byte, transcript string, onDelta func(string)) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(pngData)
	dataURL := "data:image/jpeg;base64," + b64

	params := responses.ResponseNewParams{
		Model:           p.model,
		MaxOutputTokens: openai.Int(4096),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				{OfMessage: &responses.EasyInputMessageParam{
					Role: "user",
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: responses.ResponseInputMessageContentListParam{
							responses.ResponseInputContentParamOfInputText(buildSolvePrompt(p.lang, readContextPath(p.contextDir), transcript)),
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

	text, respID, err := streamResponses(stream, onDelta)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("no text in response")
	}
	p.previousResponseID = respID
	return text, nil
}

func (p *OpenAIProvider) Summarize(text string) (string, error) {
	params := responses.ResponseNewParams{
		Model:           p.model,
		MaxOutputTokens: openai.Int(2048),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				{OfMessage: &responses.EasyInputMessageParam{
					Role: "user",
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: responses.ResponseInputMessageContentListParam{
							responses.ResponseInputContentParamOfInputText(text),
						},
					},
				}},
			},
		},
	}

	stream := p.client.Responses.NewStreaming(context.Background(), params)
	result, _, err := streamResponses(stream, nil)
	if err != nil {
		return "", fmt.Errorf("summarize failed: %w", err)
	}
	return result, nil
}

func (p *OpenAIProvider) FollowUp(text string, onDelta func(string)) (string, error) {
	msg := readContextPath(p.contextDir) + text
	params := responses.ResponseNewParams{
		Model:           p.model,
		MaxOutputTokens: openai.Int(4096),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				{OfMessage: &responses.EasyInputMessageParam{
					Role: "user",
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: responses.ResponseInputMessageContentListParam{
							responses.ResponseInputContentParamOfInputText(msg),
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

	reply, respID, err := streamResponses(stream, onDelta)
	if err != nil {
		return "", err
	}
	if reply == "" {
		return "", fmt.Errorf("no text in response")
	}
	p.previousResponseID = respID
	return reply, nil
}
