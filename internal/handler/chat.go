package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// ============================================================
// Chat Completions handler
// Handles both regular and streaming chat completion requests
// ============================================================

// ChatHandler handles chat completion requests
type ChatHandler struct {
	registry        *provider.Registry
	logger          *zap.Logger
	base64Handler   *utils.Base64Handler
	forcedProvider  string // if non-empty, force this provider
}

// NewChatHandler creates a new chat handler
func NewChatHandler(registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) *ChatHandler {
	return &ChatHandler{
		registry:       registry,
		logger:         logger,
		base64Handler:  base64Handler,
		forcedProvider: forcedProvider,
	}
}

// HandleCreate handles POST /v1/chat/completions
func (h *ChatHandler) HandleCreate(c *gin.Context) {
	var req model.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	h.logger.Info("Chat completion request",
		zap.String("model", req.Model),
		zap.Bool("stream", req.Stream),
		zap.Int("messages", len(req.Messages)),
	)

	// Find the provider: use forced provider from URL path, or auto-detect by model name
	var p model.Provider
	var err error
	if h.forcedProvider != "" {
		p, err = h.registry.GetProvider(h.forcedProvider)
	} else {
		p, err = h.registry.GetProviderForModel(req.Model)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found. Available models can be listed via GET /v1/models", req.Model),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
				Param:   "model",
			},
		})
		return
	}

	// Check if provider supports chat
	if !p.SupportsChat() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support chat completions", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	// Handle streaming vs non-streaming
	if req.Stream {
		h.handleStream(c, p, &req)
	} else {
		h.handleNonStream(c, p, &req)
	}
}

// handleNonStream handles a non-streaming chat completion request
func (h *ChatHandler) handleNonStream(c *gin.Context, p model.Provider, req *model.ChatCompletionRequest) {
	// Create request context for tracking base64 temp images
	reqCtx := utils.NewRequestContext()
	// ★ 把客户端的 API Key 透传给 Provider
	ctx := ctxWithKey(c)
	ctx = utils.WithRequestContext(ctx, reqCtx)

	// Call provider
	resp, err := p.ChatCompletion(ctx, req)

	// ★ Cleanup temp images immediately after provider responds
	if h.base64Handler != nil {
		h.base64Handler.CleanupRequest(reqCtx)
	}

	if err != nil {
		h.logger.Error("Chat completion failed",
			zap.String("model", req.Model),
			zap.Error(err),
		)
		respondProviderError(c, "Chat completion", err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// handleStream handles a streaming chat completion request
func (h *ChatHandler) handleStream(c *gin.Context, p model.Provider, req *model.ChatCompletionRequest) {
	// Create request context for tracking base64 temp images
	reqCtx := utils.NewRequestContext()
	// ★ 把客户端的 API Key 透传给 Provider
	ctx := ctxWithKey(c)
	ctx = utils.WithRequestContext(ctx, reqCtx)

	// Call provider to start stream
	body, err := p.ChatCompletionStream(ctx, req)
	if err != nil {
		// Cleanup on error
		if h.base64Handler != nil {
			h.base64Handler.CleanupRequest(reqCtx)
		}
		h.logger.Error("Chat completion stream failed",
			zap.String("model", req.Model),
			zap.Error(err),
		)
		respondProviderError(c, "Stream", err)
		return
	}
	defer body.Close()

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Stream the response, rewriting the model name back to the original
	h.streamSSE(c, body, req.Model)

	// ★ Cleanup temp images after stream finishes
	if h.base64Handler != nil {
		h.base64Handler.CleanupRequest(reqCtx)
	}
}

// streamSSE reads SSE events from the provider and forwards them to the client
func (h *ChatHandler) streamSSE(c *gin.Context, body io.ReadCloser, originalModel string) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	clientClosed := false
	for scanner.Scan() {
		line := scanner.Text()

		select {
		case <-c.Request.Context().Done():
			clientClosed = true
			break
		default:
		}
		if clientClosed {
			break
		}

		if len(line) > 6 && line[:6] == "data: " {
			data := line[6:]
			if data == "[DONE]" {
				fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
				c.Writer.Flush()
				break
			}
			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &chunk); err == nil {
				if _, ok := chunk["model"]; ok {
					chunk["model"] = originalModel
				}
				if rewritten, err := json.Marshal(chunk); err == nil {
					data = string(rewritten)
				}
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			c.Writer.Flush()
		} else if line == "" {
			continue
		} else if len(line) > 2 && line[:2] == ": " {
			fmt.Fprintf(c.Writer, "%s\n\n", line)
			c.Writer.Flush()
		}
	}

	if err := scanner.Err(); err != nil && !clientClosed {
		h.logger.Error("Stream scanner error", zap.Error(err))
	}
}

