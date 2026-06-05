package handler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/o4openai/internal/model"
)

// ============================================================
// Anthropic ↔ OpenAI format conversion functions
//
// These functions convert between Anthropic Messages API format
// and the internal OpenAI-compatible format. They are used by
// AnthropicHandler to bridge Anthropic SDK clients to the
// existing provider infrastructure.
// ============================================================

// ---------- Stop reason mappings ----------

// openAIToAnthropicStop maps OpenAI finish_reason to Anthropic stop_reason
var openAIToAnthropicStop = map[string]string{
	"stop":       "end_turn",
	"length":     "max_tokens",
	"tool_calls": "tool_use",
}

// anthropicToOpenAIStop maps Anthropic stop_reason to OpenAI finish_reason
var anthropicToOpenAIStop = map[string]string{
	"end_turn":      "stop",
	"max_tokens":    "length",
	"stop_sequence": "stop",
	"tool_use":      "tool_calls",
}

// ---------- Request conversion: Anthropic → OpenAI ----------

// anthropicToOpenAI converts an Anthropic Messages request to an OpenAI ChatCompletion request.
func anthropicToOpenAI(req *model.AnthropicMessagesRequest) (*model.ChatCompletionRequest, error) {
	maxTokens := req.MaxTokens
	openaiReq := &model.ChatCompletionRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		MaxTokens:   &maxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Extra:       make(map[string]interface{}),
	}

	// top_k has no OpenAI equivalent — pass through via Extra
	if req.TopK != nil {
		openaiReq.Extra["top_k"] = *req.TopK
	}

	// thinking mode — pass through via Extra for Agnes native support
	// Agnes supports Anthropic-compatible thinking: {"type":"enabled","budget_tokens":2048}
	if req.Thinking != nil {
		var thinkingVal interface{}
		if err := json.Unmarshal(req.Thinking, &thinkingVal); err == nil {
			openaiReq.Extra["thinking"] = thinkingVal
		}
	}

	// user from metadata
	if req.Metadata != nil && req.Metadata.UserID != "" {
		openaiReq.User = req.Metadata.UserID
	}

	// stop_sequences → stop
	if len(req.StopSequences) > 0 {
		stopJSON, _ := json.Marshal(req.StopSequences)
		openaiReq.Stop = stopJSON
	}

	// Convert tools
	if len(req.Tools) > 0 {
		openaiReq.Tools = anthropicToolsToOpenAI(req.Tools)
	}

	// Convert tool_choice
	if req.ToolChoice != nil {
		openaiReq.ToolChoice = anthropicToolChoiceToOpenAI(req.ToolChoice)
	}

	// Convert system prompt → prepend a system message
	messages := make([]model.ChatCompletionMessageParam, 0)
	if req.System != nil {
		sysContent, err := parseAnthropicSystem(req.System)
		if err != nil {
			return nil, fmt.Errorf("invalid system field: %w", err)
		}
		if sysContent != "" {
			sysContentJSON, _ := json.Marshal(sysContent)
			messages = append(messages, model.ChatCompletionMessageParam{
				Role:    "system",
				Content: sysContentJSON,
			})
		}
	}

	// Convert messages
	for _, msg := range req.Messages {
		converted, err := anthropicMessageToOpenAI(msg)
		if err != nil {
			return nil, fmt.Errorf("invalid message: %w", err)
		}
		messages = append(messages, converted...)
	}

	openaiReq.Messages = messages
	return openaiReq, nil
}

// parseAnthropicSystem parses the system field which can be a string or an array of blocks.
func parseAnthropicSystem(raw json.RawMessage) (string, error) {
	if raw == nil {
		return "", nil
	}

	// Try string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	// Try array of blocks
	var blocks []model.AnthropicSystemBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" || b.Type == "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n"), nil
	}

	return "", fmt.Errorf("system must be a string or an array of text blocks")
}

// anthropicMessageToOpenAI converts a single Anthropic message to one or more OpenAI messages.
// Anthropic tool_result blocks need to become separate role:"tool" messages in OpenAI format.
func anthropicMessageToOpenAI(msg model.AnthropicMessage) ([]model.ChatCompletionMessageParam, error) {
	if msg.Content == nil {
		contentJSON, _ := json.Marshal("")
		return []model.ChatCompletionMessageParam{{Role: msg.Role, Content: contentJSON}}, nil
	}

	// Try to parse content as a simple string
	var contentStr string
	if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
		contentJSON, _ := json.Marshal(contentStr)
		return []model.ChatCompletionMessageParam{{Role: msg.Role, Content: contentJSON}}, nil
	}

	// Parse as array of content blocks
	var blocks []model.AnthropicContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		// Fallback: return raw content
		return []model.ChatCompletionMessageParam{{Role: msg.Role, Content: msg.Content}}, nil
	}

	// Separate tool_use and tool_result blocks for correct OpenAI message structure
	var openaiParts []model.ChatCompletionContentPart
	var toolCalls []model.ToolCall
	var toolResultMsgs []model.ChatCompletionMessageParam

	for _, block := range blocks {
		switch block.Type {
		case "text":
			openaiParts = append(openaiParts, model.ChatCompletionContentPart{
				Type: "text",
				Text: block.Text,
			})

		case "image":
			part, err := anthropicImageToOpenAI(block)
			if err != nil {
				return nil, err
			}
			openaiParts = append(openaiParts, *part)

		case "tool_use":
			// For assistant messages: collect as tool_calls
			tc := model.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: model.ToolFunction{
					Name:      block.Name,
					Parameters: block.Input,
				},
			}
			toolCalls = append(toolCalls, tc)

		case "tool_result":
			// Must become a separate role:"tool" message
			resultContent := ""
			if block.Content != nil {
				// Content can be string or array of text blocks
				var s string
				if err := json.Unmarshal(block.Content, &s); err == nil {
					resultContent = s
				} else {
					var textBlocks []model.AnthropicContentBlock
					if err := json.Unmarshal(block.Content, &textBlocks); err == nil {
						var parts []string
						for _, tb := range textBlocks {
							if tb.Type == "text" {
								parts = append(parts, tb.Text)
							}
						}
						resultContent = strings.Join(parts, "\n")
					} else {
						resultContent = string(block.Content)
					}
				}
			}
			// If the tool_result indicates an error, include that in the content
			if block.IsError {
				resultContent = "Error: " + resultContent
			}
			resultJSON, _ := json.Marshal(resultContent)
			toolResultMsgs = append(toolResultMsgs, model.ChatCompletionMessageParam{
				Role:       "tool",
				Content:    resultJSON,
				ToolCallID: block.ToolUseID,
			})

		default:
			// Unknown block type — skip gracefully
		}
	}

	// Build the main message for this role
	var result []model.ChatCompletionMessageParam
	mainMsg := model.ChatCompletionMessageParam{Role: msg.Role}

	if len(toolCalls) > 0 {
		// Assistant message with tool calls: content may be empty or have text parts
		if len(openaiParts) > 0 {
			partsJSON, _ := json.Marshal(openaiParts)
			mainMsg.Content = partsJSON
		} else {
			emptyJSON, _ := json.Marshal("")
			mainMsg.Content = emptyJSON
		}
		mainMsg.ToolCalls = toolCalls
	} else if len(openaiParts) > 0 {
		partsJSON, _ := json.Marshal(openaiParts)
		mainMsg.Content = partsJSON
	} else {
		emptyJSON, _ := json.Marshal("")
		mainMsg.Content = emptyJSON
	}

	result = append(result, mainMsg)
	result = append(result, toolResultMsgs...)

	return result, nil
}

// anthropicImageToOpenAI converts an Anthropic image block to an OpenAI image_url content part.
func anthropicImageToOpenAI(block model.AnthropicContentBlock) (*model.ChatCompletionContentPart, error) {
	if block.Source == nil {
		return nil, fmt.Errorf("image block missing source")
	}

	var url string
	switch block.Source.Type {
	case "base64":
		// Convert to data URI: data:{media_type};base64,{data}
		mediaType := block.Source.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		url = fmt.Sprintf("data:%s;base64,%s", mediaType, block.Source.Data)
	case "url":
		url = block.Source.URL
	default:
		return nil, fmt.Errorf("unsupported image source type: %s", block.Source.Type)
	}

	return &model.ChatCompletionContentPart{
		Type: "image_url",
		ImageURL: &model.ImageURL{
			URL: url,
		},
	}, nil
}

// anthropicToolsToOpenAI converts Anthropic tool definitions to OpenAI format.
func anthropicToolsToOpenAI(tools []model.AnthropicTool) []model.Tool {
	result := make([]model.Tool, 0, len(tools))
	for _, t := range tools {
		result = append(result, model.Tool{
			Type: "function",
			Function: model.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema, // input_schema → parameters (both are JSON Schema)
			},
		})
	}
	return result
}

// anthropicToolChoiceToOpenAI converts Anthropic tool_choice to OpenAI format.
func anthropicToolChoiceToOpenAI(raw json.RawMessage) json.RawMessage {
	// Try string first: "auto", "any", "none"
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "any":
			// Anthropic "any" ≈ OpenAI "required"
			return json.RawMessage(`{"type":"required"}`)
		case "none":
			return json.RawMessage(`{"type":"none"}`)
		case "auto":
			return json.RawMessage(`{"type":"auto"}`)
		}
		return json.RawMessage(`{"type":"auto"}`)
	}

	// Try object: {"type":"tool","name":"X"}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if t, ok := obj["type"].(string); ok && t == "tool" {
			if name, ok := obj["name"].(string); ok {
				result, _ := json.Marshal(map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": name,
					},
				})
				return result
			}
		}
	}

	return raw
}

// ---------- Response conversion: OpenAI → Anthropic ----------

// openAIToAnthropic converts an OpenAI ChatCompletion response to an Anthropic Messages response.
func openAIToAnthropic(resp *model.ChatCompletionResponse, originalModel string) *model.AnthropicMessagesResponse {
	content := make([]model.AnthropicResponseBlock, 0)

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]

		// Convert message content
		content = append(content, openAIContentToAnthropic(choice.Message)...)

		// Convert tool calls to content blocks
		for _, tc := range choice.Message.ToolCalls {
			// OpenAI response uses "arguments" (JSON string), request uses "parameters" (JSON Schema)
			// Priority: Arguments (from response) > Parameters (from request)
			var inputJSON json.RawMessage
			if tc.Function.Arguments != "" {
				// Arguments is a JSON string containing the actual call parameters
				inputJSON = json.RawMessage(tc.Function.Arguments)
			} else if tc.Function.Parameters != nil {
				inputJSON = tc.Function.Parameters
			} else {
				inputJSON = json.RawMessage("{}")
			}
			content = append(content, model.AnthropicResponseBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: inputJSON,
			})
		}
	}

	// Ensure at least one content block
	if len(content) == 0 {
		content = append(content, model.AnthropicResponseBlock{
			Type: "text",
			Text: "",
		})
	}

	// Build response
	msgID := resp.ID
	if msgID == "" || !strings.HasPrefix(msgID, "msg_") {
		msgID = "msg_" + msgID
	}

	stopReason := "end_turn"
	var stopSequence *string
	if len(resp.Choices) > 0 {
		stopReason = mapStopReason(choiceFinishReason(resp), nil)
	}

	result := &model.AnthropicMessagesResponse{
		ID:           msgID,
		Type:         "message",
		Role:         "assistant",
		Content:      content,
		Model:        originalModel,
		StopReason:   stopReason,
		StopSequence: stopSequence,
		Usage: model.AnthropicUsage{
			InputTokens:  0,
			OutputTokens: 0,
		},
	}

	// Map usage
	if resp.Usage != nil {
		result.Usage.InputTokens = resp.Usage.PromptTokens
		result.Usage.OutputTokens = resp.Usage.CompletionTokens
	}

	return result
}

// openAIContentToAnthropic converts OpenAI message content to Anthropic response blocks.
func openAIContentToAnthropic(msg model.ChatCompletionMessage) []model.AnthropicResponseBlock {
	var blocks []model.AnthropicResponseBlock

	if msg.Content == nil {
		return blocks
	}

	// Try string
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		if s != "" {
			blocks = append(blocks, model.AnthropicResponseBlock{
				Type: "text",
				Text: s,
			})
		}
		return blocks
	}

	// Try array of content parts
	var parts []model.ChatCompletionContentPart
	if err := json.Unmarshal(msg.Content, &parts); err == nil {
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				blocks = append(blocks, model.AnthropicResponseBlock{
					Type: "text",
					Text: p.Text,
				})
			}
		}
		return blocks
	}

	// Fallback: treat as raw string
	text := string(msg.Content)
	if text != "" {
		blocks = append(blocks, model.AnthropicResponseBlock{
			Type: "text",
			Text: text,
		})
	}

	return blocks
}

// mapStopReason maps OpenAI finish_reason to Anthropic stop_reason.
// If a stopSequence is matched, returns "stop_sequence" instead.
func mapStopReason(finishReason string, stopSequence *string) string {
	if stopSequence != nil && *stopSequence != "" {
		return "stop_sequence"
	}
	if mapped, ok := openAIToAnthropicStop[finishReason]; ok {
		return mapped
	}
	return "end_turn"
}

// choiceFinishReason extracts the finish_reason from the first choice safely.
func choiceFinishReason(resp *model.ChatCompletionResponse) string {
	if len(resp.Choices) > 0 {
		return resp.Choices[0].FinishReason
	}
	return ""
}
