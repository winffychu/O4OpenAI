package handler

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// ============================================================
// Anthropic Messages API handler
//
// Receives Anthropic-format requests, converts to OpenAI format,
// calls the existing provider chain, and converts responses back.
// Supports both non-streaming and streaming (SSE with Anthropic
// event types: message_start, content_block_delta, message_stop).
// ============================================================

// AnthropicHandler handles Anthropic Messages API requests
type AnthropicHandler struct {
	registry       *provider.Registry
	logger         *zap.Logger
	base64Handler  *utils.Base64Handler
	forcedProvider string // if non-empty, force this provider
}

// NewAnthropicHandler creates a new Anthropic handler
func NewAnthropicHandler(registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) *AnthropicHandler {
	return &AnthropicHandler{
		registry:       registry,
		logger:         logger,
		base64Handler:  base64Handler,
		forcedProvider: forcedProvider,
	}
}

// HandleMessages handles POST /v1/messages (Anthropic Messages API)
func (h *AnthropicHandler) HandleMessages(c *gin.Context) {
	var req model.AnthropicMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	h.logger.Info("Anthropic messages request",
		zap.String("model", req.Model),
		zap.Bool("stream", req.Stream),
		zap.Int("messages", len(req.Messages)),
	)

	// Validate required fields
	if req.Model == "" {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			"model: Field required")
		return
	}
	if len(req.Messages) == 0 {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			"messages: Field required")
		return
	}
	if req.MaxTokens <= 0 {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			"max_tokens: Field required")
		return
	}

	// Resolve provider
	var p model.Provider
	var err error
	if h.forcedProvider != "" {
		p, err = h.registry.GetProvider(h.forcedProvider)
	} else {
		p, err = h.registry.GetProviderForModel(req.Model)
	}
	if err != nil {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			fmt.Sprintf("Model %q not found. Available models can be listed via GET /v1/models", req.Model))
		return
	}

	if !p.SupportsChat() {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			fmt.Sprintf("Provider %q does not support chat completions", p.Name()))
		return
	}

	// Handle streaming vs non-streaming
	if req.Stream {
		h.handleStream(c, p, &req)
	} else {
		h.handleNonStream(c, p, &req)
	}
}

// handleNonStream handles a non-streaming Anthropic messages request
func (h *AnthropicHandler) handleNonStream(c *gin.Context, p model.Provider, req *model.AnthropicMessagesRequest) {
	reqCtx := utils.NewRequestContext()
	ctx := ctxWithKeyAnthropic(c)
	ctx = utils.WithRequestContext(ctx, reqCtx)

	// Convert Anthropic → OpenAI
	openaiReq, err := anthropicToOpenAI(req)
	if err != nil {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			fmt.Sprintf("Failed to convert request: %v", err))
		return
	}

	// Call provider
	resp, err := p.ChatCompletion(ctx, openaiReq)

	if h.base64Handler != nil {
		h.base64Handler.CleanupRequest(reqCtx)
	}

	if err != nil {
		h.logger.Error("Anthropic chat completion failed",
			zap.String("model", req.Model),
			zap.Error(err))
		respondAnthropicProviderError(c, "Chat completion", err)
		return
	}

	// Convert OpenAI → Anthropic
	anthropicResp := openAIToAnthropic(resp, req.Model)
	c.JSON(http.StatusOK, anthropicResp)
}

// handleStream handles a streaming Anthropic messages request
func (h *AnthropicHandler) handleStream(c *gin.Context, p model.Provider, req *model.AnthropicMessagesRequest) {
	reqCtx := utils.NewRequestContext()
	ctx := ctxWithKeyAnthropic(c)
	ctx = utils.WithRequestContext(ctx, reqCtx)

	// Convert Anthropic → OpenAI
	openaiReq, err := anthropicToOpenAI(req)
	if err != nil {
		respondAnthropicError(c, http.StatusBadRequest,
			"invalid_request_error",
			fmt.Sprintf("Failed to convert request: %v", err))
		return
	}
	openaiReq.Stream = true

	// Call provider to start stream
	body, err := p.ChatCompletionStream(ctx, openaiReq)
	if err != nil {
		if h.base64Handler != nil {
			h.base64Handler.CleanupRequest(reqCtx)
		}
		h.logger.Error("Anthropic stream failed",
			zap.String("model", req.Model),
			zap.Error(err))
		respondAnthropicProviderError(c, "Stream", err)
		return
	}
	defer body.Close()

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Convert OpenAI SSE → Anthropic SSE
	h.streamAnthropicSSE(c, body, req.Model)

	if h.base64Handler != nil {
		h.base64Handler.CleanupRequest(reqCtx)
	}
}

// streamAnthropicSSE reads OpenAI SSE events from the provider and converts
// them to Anthropic SSE events using a state machine.
//
// Anthropic SSE format uses typed events:
//
//	event: message_start
//	data: {"type":"message_start","message":{...}}
//
//	event: content_block_start
//	data: {"type":"content_block_start","index":0,"content_block":{...}}
//
//	event: content_block_delta
//	data: {"type":"content_block_delta","index":0,"delta":{...}}
//
//	event: content_block_stop
//	data: {"type":"content_block_stop","index":0}
//
//	event: message_delta
//	data: {"type":"message_delta","delta":{...},"usage":{...}}
//
//	event: message_stop
//	data: {"type":"message_stop"}
func (h *AnthropicHandler) streamAnthropicSSE(c *gin.Context, body io.ReadCloser, originalModel string) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// State machine
	msgID := generateMessageID()
	messageStarted := false
	blockIndex := -1
	blockStarted := false
	inputTokens := 0
	outputTokens := 0

	writeEvent := func(eventType string, payload interface{}) {
		data, err := json.Marshal(payload)
		if err != nil {
			h.logger.Error("Failed to marshal Anthropic SSE event", zap.Error(err))
			return
		}
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, string(data))
		c.Writer.Flush()
	}

	for scanner.Scan() {
		line := scanner.Text()

		select {
		case <-c.Request.Context().Done():
			break
		default:
		}

		if len(line) <= 6 || line[:6] != "data: " {
			if line == "" {
				continue
			}
			// Skip comment lines
			if len(line) > 2 && line[:2] == ": " {
				continue
			}
			continue
		}

		data := line[6:]
		if data == "[DONE]" {
			// Ensure we close any open blocks and send final events
			if messageStarted {
				if blockStarted {
					writeEvent("content_block_stop", model.AnthropicContentBlockStopEvent{
						Type:  "content_block_stop",
						Index: blockIndex,
					})
				}
				writeEvent("message_delta", model.AnthropicMessageDeltaEvent{
					Type: "message_delta",
					Delta: model.AnthropicMessageDeltaData{
						StopReason: "end_turn",
					},
					Usage: model.AnthropicDeltaUsage{
						OutputTokens: outputTokens,
					},
				})
				writeEvent("message_stop", model.AnthropicMessageStopEvent{
					Type: "message_stop",
				})
			}
			break
		}

		// Parse OpenAI chunk
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			h.logger.Debug("Failed to parse SSE chunk", zap.Error(err))
			continue
		}

		choices, _ := chunk["choices"].([]interface{})

		if !messageStarted {
			// Emit message_start with full message skeleton
			inputTokens = extractInputTokens(chunk)
			writeEvent("message_start", model.AnthropicMessageStartEvent{
				Type: "message_start",
				Message: model.AnthropicMessagesResponse{
					ID:      msgID,
					Type:    "message",
					Role:    "assistant",
					Content: []model.AnthropicResponseBlock{},
					Model:   originalModel,
					Usage: model.AnthropicUsage{
						InputTokens:  inputTokens,
						OutputTokens: 0,
					},
					StopReason: "",
				},
			})
			messageStarted = true
		}

		if len(choices) == 0 {
			continue
		}

		choice, _ := choices[0].(map[string]interface{})
		if choice == nil {
			continue
		}

		delta, _ := choice["delta"].(map[string]interface{})
		finishReason, _ := choice["finish_reason"].(string)

		if delta != nil {
			// Handle text content delta
			if content, ok := delta["content"].(string); ok && content != "" {
				if !blockStarted {
					blockIndex++
					blockStarted = true
					writeEvent("content_block_start", model.AnthropicContentBlockStartEvent{
						Type:  "content_block_start",
						Index: blockIndex,
						ContentBlock: model.AnthropicResponseBlock{
							Type: "text",
							Text: "",
						},
					})
				}
				writeEvent("content_block_delta", model.AnthropicContentBlockDeltaEvent{
					Type:  "content_block_delta",
					Index: blockIndex,
					Delta: model.AnthropicDeltaBlock{
						Type: "text_delta",
						Text: content,
					},
				})
				outputTokens++
			}

			// Handle tool_calls delta
			if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
				for _, tcRaw := range toolCalls {
					tc, _ := tcRaw.(map[string]interface{})
					if tc == nil {
						continue
					}

					tcID, _ := tc["id"].(string)
					fn, _ := tc["function"].(map[string]interface{})
					fnName, _ := fn["name"].(string)
					fnArgs, _ := fn["arguments"].(string)

					// If this tool call has an ID, it's a NEW tool_use block
					if tcID != "" {
						// Close previous block (text or tool_use) only when starting a new one
						if blockStarted {
							writeEvent("content_block_stop", model.AnthropicContentBlockStopEvent{
								Type:  "content_block_stop",
								Index: blockIndex,
							})
						}
						blockIndex++
						blockStarted = true
						writeEvent("content_block_start", model.AnthropicContentBlockStartEvent{
							Type:  "content_block_start",
							Index: blockIndex,
							ContentBlock: model.AnthropicResponseBlock{
								Type: "tool_use",
								ID:   tcID,
								Name: fnName,
							},
						})
					}

					// Emit arguments delta (accumulates into the current tool_use block)
					if fnArgs != "" {
						writeEvent("content_block_delta", model.AnthropicContentBlockDeltaEvent{
							Type:  "content_block_delta",
							Index: blockIndex,
							Delta: model.AnthropicDeltaBlock{
								Type:        "input_json_delta",
								PartialJSON: fnArgs,
							},
						})
					}
				}
			}
		}

		// Handle finish_reason
		if finishReason != "" {
			// Close current block
			if blockStarted {
				writeEvent("content_block_stop", model.AnthropicContentBlockStopEvent{
					Type:  "content_block_stop",
					Index: blockIndex,
				})
				blockStarted = false
			}

			// Map stop reason
			anthropicStopReason := "end_turn"
			if mapped, ok := openAIToAnthropicStop[finishReason]; ok {
				anthropicStopReason = mapped
			}

			writeEvent("message_delta", model.AnthropicMessageDeltaEvent{
				Type: "message_delta",
				Delta: model.AnthropicMessageDeltaData{
					StopReason: anthropicStopReason,
				},
				Usage: model.AnthropicDeltaUsage{
					OutputTokens: outputTokens,
				},
			})

			writeEvent("message_stop", model.AnthropicMessageStopEvent{
				Type: "message_stop",
			})

			break
		}
	}

	if err := scanner.Err(); err != nil {
		h.logger.Error("Anthropic stream scanner error", zap.Error(err))
	}
}

// generateMessageID generates a random Anthropic-style message ID (msg_<hex>).
func generateMessageID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	return "msg_" + hex.EncodeToString(b)
}

// extractInputTokens extracts input_tokens from an OpenAI chunk's usage field.
func extractInputTokens(chunk map[string]interface{}) int {
	usage, _ := chunk["usage"].(map[string]interface{})
	if usage == nil {
		return 0
	}
	promptTokens, _ := usage["prompt_tokens"].(float64)
	return int(promptTokens)
}
