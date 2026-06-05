package agnes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/o4openai/internal/model"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// ============================================================
// Chat Completions adapter - converts between OpenAI and Agnes formats
//
// Key differences handled:
// - Agnes has extra `repetition_penalty` parameter
// - Agnes doesn't support all OpenAI params (tools, logprobs, etc.)
// - Content format: OpenAI supports base64 in image_url, Agnes only URL
//   → base64 data URIs are converted to temp URLs via Base64Handler
// - Multi-image input: multiple image_url parts with base64 all converted
// - Streaming format is compatible (both use SSE)
// ============================================================

// ChatCompletion converts an OpenAI chat request to Agnes format and back
func (p *Provider) ChatCompletion(ctx context.Context, req *model.ChatCompletionRequest) (*model.ChatCompletionResponse, error) {
	agnesReq, err := p.convertChatRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	resp, err := p.doRequest(ctx, "POST", "/chat/completions", agnesReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var agnesResp AgnesChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&agnesResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return p.convertChatResponse(&agnesResp, req.Model), nil
}

// ChatCompletionStream opens a streaming connection for chat completions
func (p *Provider) ChatCompletionStream(ctx context.Context, req *model.ChatCompletionRequest) (io.ReadCloser, error) {
	agnesReq, err := p.convertChatRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}
	agnesReq.Stream = true

	resp, err := p.doRequest(ctx, "POST", "/chat/completions", agnesReq)
	if err != nil {
		return nil, err
	}

	// Return the raw response body for SSE streaming
	// The Agnes SSE format is the same as OpenAI's:
	// data: {json}\n\n
	// data: [DONE]\n\n
	return resp.Body, nil
}

// convertChatRequest converts OpenAI format to Agnes format
func (p *Provider) convertChatRequest(ctx context.Context, req *model.ChatCompletionRequest) (*AgnesChatRequest, error) {
	agnesReq := &AgnesChatRequest{
		Model:            p.resolveModel(req.Model),
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Seed:             req.Seed,
		Stream:           req.Stream,
	}

	// Handle max_tokens / max_completion_tokens
	if req.MaxCompletionTokens != nil {
		agnesReq.MaxTokens = req.MaxCompletionTokens
	} else if req.MaxTokens != nil {
		agnesReq.MaxTokens = req.MaxTokens
	}

	// Handle stop - can be string or []string
	if req.Stop != nil {
		var stopVal interface{}
		if err := json.Unmarshal(req.Stop, &stopVal); err == nil {
			agnesReq.Stop = stopVal
		}
	}

	// Convert messages - this is where base64 → URL conversion happens
	agnesMessages := make([]AgnesMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		agnesMsg, err := p.convertMessage(ctx, msg)
		if err != nil {
			return nil, err
		}
		agnesMessages = append(agnesMessages, *agnesMsg)
	}
	agnesReq.Messages = agnesMessages

	// Handle extra provider-specific params from the Extra map
	if req.Extra != nil {
		if rp, ok := req.Extra["repetition_penalty"]; ok {
			if val, ok := rp.(float64); ok {
				agnesReq.RepetitionPenalty = &val
			}
		}
		// top_k passed through from Anthropic compatibility layer
		if tk, ok := req.Extra["top_k"]; ok {
			if val, ok := tk.(int); ok {
				agnesReq.TopK = &val
			} else if val, ok := tk.(float64); ok {
				intVal := int(val)
				agnesReq.TopK = &intVal
			}
		}
		// thinking mode passed through from Anthropic compatibility layer
		// Agnes natively supports: {"type":"enabled","budget_tokens":2048}
		if thinking, ok := req.Extra["thinking"]; ok {
			agnesReq.Thinking = thinking
		}
		// chat_template_kwargs for OpenAI-compatible thinking mode
		if ctk, ok := req.Extra["chat_template_kwargs"]; ok {
			agnesReq.ChatTemplateKwargs = ctk
		}
	}

	// Convert tools (OpenAI function calling format → Agnes format)
	if len(req.Tools) > 0 {
		agnesReq.Tools = make([]AgnesTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			agnesReq.Tools = append(agnesReq.Tools, AgnesTool{
				Type: t.Type,
				Function: AgnesToolFunc{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
	}

	// Pass through tool_choice
	if req.ToolChoice != nil {
		var choiceVal interface{}
		if err := json.Unmarshal(req.ToolChoice, &choiceVal); err == nil {
			agnesReq.ToolChoice = choiceVal
		}
	}

	// Pass through chat_template_kwargs (OpenAI-compatible thinking: {"enable_thinking":true})
	if req.ChatTemplateKwargs != nil {
		var ctkVal interface{}
		if err := json.Unmarshal(req.ChatTemplateKwargs, &ctkVal); err == nil {
			agnesReq.ChatTemplateKwargs = ctkVal
		}
	}

	// Pass through thinking (Anthropic-compatible thinking: {"type":"enabled","budget_tokens":2048})
	// Priority: direct field > Extra map (from Anthropic handler)
	if req.Thinking != nil {
		var thinkingVal interface{}
		if err := json.Unmarshal(req.Thinking, &thinkingVal); err == nil {
			agnesReq.Thinking = thinkingVal
		}
	}

	return agnesReq, nil
}

// convertMessage converts an OpenAI message to Agnes format.
// Handles base64 image URIs by converting them to temp URLs.
// Also passes through tool_calls and tool result messages.
func (p *Provider) convertMessage(ctx context.Context, msg model.ChatCompletionMessageParam) (*AgnesMessage, error) {
	agnesMsg := &AgnesMessage{
		Role: msg.Role,
	}

	// Handle tool result messages: role="tool", tool_call_id=...
	if msg.Role == "tool" && msg.ToolCallID != "" {
		agnesMsg.ToolCallID = msg.ToolCallID
		agnesMsg.Name = msg.Name
		// Content is the tool result
		if msg.Content != nil {
			var contentStr string
			if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
				agnesMsg.Content = contentStr
			} else {
				agnesMsg.Content = string(msg.Content)
			}
		}
		return agnesMsg, nil
	}

	// Handle tool_calls on assistant messages
	if len(msg.ToolCalls) > 0 {
		agnesMsg.ToolCalls = make([]AgnesToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			agnesMsg.ToolCalls = append(agnesMsg.ToolCalls, AgnesToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: AgnesToolCallFunc{
					Name:      tc.Function.Name,
					Arguments: string(tc.Function.Parameters),
				},
			})
		}
	}

	// Content can be a string or an array of content parts
	if msg.Content == nil {
		agnesMsg.Content = ""
		return agnesMsg, nil
	}

	// Try to parse as string first
	var contentStr string
	if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
		agnesMsg.Content = contentStr
		return agnesMsg, nil
	}

	// Parse as array of content parts (multimodal)
	var parts []model.ChatCompletionContentPart
	if err := json.Unmarshal(msg.Content, &parts); err != nil {
		// Fallback: return raw content
		agnesMsg.Content = string(msg.Content)
		return agnesMsg, nil
	}

	agnesParts := make([]AgnesContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			agnesParts = append(agnesParts, AgnesContentPart{
				Type: "text",
				Text: part.Text,
			})

		case "image_url":
			// ============================================
			// Critical conversion: base64 → URL
			// ============================================
			// OpenAI clients send images as:
			//   {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}}
			//   {"type": "image_url", "image_url": {"url": "https://example.com/img.png"}}
			//
			// Agnes AI only accepts URLs, not base64 data URIs.
			// So we must convert base64 → temp URL using Base64Handler.
			url := ""
			if part.ImageURL != nil {
				url = part.ImageURL.URL
			}

			if utils.IsDataURL(url) && p.base64Handler != nil {
				// Convert base64 data URI → temp URL
				reqCtx := utils.RequestContextFromCtx(ctx)
				convertedURL, _, err := p.base64Handler.ConvertDataURL(url, reqCtx)
				if err != nil {
					p.logger.Error("Failed to convert base64 image to temp URL",
						zap.Error(err))
					return nil, fmt.Errorf("failed to process image: %w", err)
				}
				p.logger.Debug("Converted base64 image to temp URL",
					zap.Int("original_size", len(url)),
					zap.String("temp_url", convertedURL))
				url = convertedURL
			} else if utils.IsDataURL(url) {
				// No base64 handler available - can't convert
				p.logger.Warn("Base64 image received but no Base64Handler configured. " +
					"Agnes AI may reject this request. Set up Base64Handler in provider config.")
			}

			agnesParts = append(agnesParts, AgnesContentPart{
				Type: "image_url",
				ImageURL: &AgnesImageURL{
					URL: url,
				},
			})

		case "input_audio":
			p.logger.Warn("Agnes AI does not support audio input, skipping",
				zap.String("type", part.Type))

		case "file":
			// File content could contain base64 image data
			if part.File != nil && part.File.FileData != "" {
				dataURI := part.File.FileData
				// If it's raw base64 without data: prefix, add one
				if !utils.IsDataURL(dataURI) {
					dataURI = "data:application/octet-stream;base64," + dataURI
				}
				if p.base64Handler != nil {
					reqCtx := utils.RequestContextFromCtx(ctx)
					convertedURL, _, err := p.base64Handler.ConvertDataURL(dataURI, reqCtx)
					if err == nil {
						// Convert file content to an image_url for Agnes
						agnesParts = append(agnesParts, AgnesContentPart{
							Type: "image_url",
							ImageURL: &AgnesImageURL{
								URL: convertedURL,
							},
						})
						continue
					}
				}
			}
			p.logger.Warn("Agnes AI does not support file input, skipping",
				zap.String("type", part.Type))

		default:
			p.logger.Warn("Unknown content part type, skipping",
				zap.String("type", part.Type))
		}
	}

	agnesMsg.Content = agnesParts
	return agnesMsg, nil
}

// convertChatResponse converts Agnes response to OpenAI format
func (p *Provider) convertChatResponse(agnesResp *AgnesChatResponse, originalModel string) *model.ChatCompletionResponse {
	choices := make([]model.ChatCompletionChoice, 0, len(agnesResp.Choices))
	for _, c := range agnesResp.Choices {
		content, _ := json.Marshal(c.Message.Content)

		msg := model.ChatCompletionMessage{
			Role:    c.Message.Role,
			Content: content,
		}

		// Preserve tool_calls from Agnes response
		if len(c.Message.ToolCalls) > 0 {
			msg.ToolCalls = make([]model.ToolCall, 0, len(c.Message.ToolCalls))
			for _, tc := range c.Message.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: model.ToolFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}

		choices = append(choices, model.ChatCompletionChoice{
			Index:        c.Index,
			Message:      msg,
			FinishReason: c.FinishReason,
		})
	}

	resp := &model.ChatCompletionResponse{
		ID:      agnesResp.ID,
		Object:  "chat.completion",
		Created: agnesResp.Created,
		Model:   originalModel, // Return the original model name the client requested
		Choices: choices,
	}

	if agnesResp.Usage != nil {
		resp.Usage = &model.CompletionUsage{
			PromptTokens:     agnesResp.Usage.PromptTokens,
			CompletionTokens: agnesResp.Usage.CompletionTokens,
			TotalTokens:      agnesResp.Usage.TotalTokens,
		}
	}

	return resp
}
