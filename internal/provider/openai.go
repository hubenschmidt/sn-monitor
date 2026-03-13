package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/openai/openai-go/shared/constant"

	appctx "second-nature/internal/context"
)

type OpenAIProvider struct {
	client     openai.Client
	model      shared.ResponsesModel
	lang       string
	contextDir string
	history    responses.ResponseInputParam
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
	p.history = nil
}

func (p *OpenAIProvider) ModelName() string {
	return string(p.model)
}

func (p *OpenAIProvider) HistoryLen() int { return len(p.history) }

func (p *OpenAIProvider) RemoveHistoryPair(userIndex int) {
	if userIndex < 0 || userIndex+2 > len(p.history) {
		return
	}
	p.history = append(p.history[:userIndex], p.history[userIndex+2:]...)
}

func firstOutputMessageID(output []responses.ResponseOutputItemUnion) string {
	for _, item := range output {
		if item.Type == "message" {
			return item.ID
		}
	}
	return ""
}

func streamResponses(stream *ssestream.Stream[responses.ResponseStreamEventUnion], onDelta func(string)) (string, string, error) {
	var buf strings.Builder
	var messageID string
	for stream.Next() {
		evt := stream.Current()
		if evt.Type == "response.output_text.delta" {
			buf.WriteString(evt.Delta.OfString)
			if onDelta != nil {
				onDelta(evt.Delta.OfString)
			}
		}
		if evt.Type == "response.completed" {
			messageID = firstOutputMessageID(evt.Response.Output)
		}
	}
	if err := stream.Err(); err != nil {
		return "", "", fmt.Errorf("api call failed: %w", err)
	}
	return buf.String(), messageID, nil
}

func (p *OpenAIProvider) buildUserItem(contentList responses.ResponseInputMessageContentListParam) responses.ResponseInputItemUnionParam {
	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role: "user",
			Content: responses.EasyInputMessageContentUnionParam{
				OfInputItemContentList: contentList,
			},
		},
	}
}

func (p *OpenAIProvider) buildAssistantItem(text, id string) responses.ResponseInputItemUnionParam {
	return responses.ResponseInputItemUnionParam{
		OfOutputMessage: &responses.ResponseOutputMessageParam{
			ID: id,
			Content: []responses.ResponseOutputMessageContentUnionParam{{
				OfOutputText: &responses.ResponseOutputTextParam{
					Text: text,
					Type: constant.ValueOf[constant.OutputText](),
				},
			}},
			Status: responses.ResponseOutputMessageStatusCompleted,
			Role:   constant.ValueOf[constant.Assistant](),
			Type:   constant.ValueOf[constant.Message](),
		},
	}
}

func (p *OpenAIProvider) sendHistory(maxTokens int64, onDelta func(string)) (string, string, error) {
	params := responses.ResponseNewParams{
		Model:           p.model,
		MaxOutputTokens: openai.Int(maxTokens),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: p.history,
		},
	}
	stream := p.client.Responses.NewStreaming(context.Background(), params)
	return streamResponses(stream, onDelta)
}

func (p *OpenAIProvider) Solve(images [][]byte, transcript string, onDelta func(string)) (string, error) {
	contentList := responses.ResponseInputMessageContentListParam{
		responses.ResponseInputContentParamOfInputText(BuildSolvePrompt(p.lang, appctx.ReadContextPath(p.contextDir), transcript, len(images))),
	}
	for _, img := range images {
		dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img)
		contentList = append(contentList, responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: openai.String(dataURL),
				Detail:   "high",
			},
		})
	}

	userItem := p.buildUserItem(contentList)
	p.history = append(p.history, userItem)

	text, respID, err := p.sendHistory(4096, onDelta)
	if err != nil {
		p.history = p.history[:len(p.history)-1]
		return "", err
	}
	if text == "" {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("no text in response")
	}

	p.history = append(p.history, p.buildAssistantItem(text, respID))
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
	msg := appctx.ReadContextPath(p.contextDir) + text
	contentList := responses.ResponseInputMessageContentListParam{
		responses.ResponseInputContentParamOfInputText(msg),
	}

	userItem := p.buildUserItem(contentList)
	p.history = append(p.history, userItem)

	reply, respID, err := p.sendHistory(4096, onDelta)
	if err != nil {
		p.history = p.history[:len(p.history)-1]
		return "", err
	}
	if reply == "" {
		p.history = p.history[:len(p.history)-1]
		return "", fmt.Errorf("no text in response")
	}

	p.history = append(p.history, p.buildAssistantItem(reply, respID))
	return reply, nil
}
