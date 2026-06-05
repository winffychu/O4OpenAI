package model

import "encoding/json"

// ============================================================
// Anthropic Messages API types
// Complete types covering Messages requests, responses, streaming events
// See: https://docs.anthropic.com/en/api/messages
// ============================================================

// ------------------- Messages Request -------------------

// AnthropicMessagesRequest represents an Anthropic Messages API request
type AnthropicMessagesRequest struct {
	Model         string               `json:"model"`
	Messages      []AnthropicMessage   `json:"messages"`
	System        json.RawMessage      `json:"system,omitempty"` // string or []AnthropicSystemBlock
	MaxTokens     int                  `json:"max_tokens"`
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	TopK          *int                 `json:"top_k,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Tools         []AnthropicTool      `json:"tools,omitempty"`
	ToolChoice    json.RawMessage      `json:"tool_choice,omitempty"` // object or string ("auto", "any", "none")
	Thinking      json.RawMessage      `json:"thinking,omitempty"`    // {"type":"enabled","budget_tokens":2048}
	Metadata      *AnthropicMetadata   `json:"metadata,omitempty"`
}

// AnthropicSystemBlock represents a block in the system field
type AnthropicSystemBlock struct {
	Type         string                `json:"type"` // "text"
	Text         string                `json:"text"`
	CacheControl *AnthropicCacheControl `json:"cache_control,omitempty"`
}

// AnthropicCacheControl represents cache control hints
type AnthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// AnthropicMessage represents a message in the Anthropic format
type AnthropicMessage struct {
	Role    string          `json:"role"`              // "user" or "assistant"
	Content json.RawMessage `json:"content"`           // string or []AnthropicContentBlock
}

// AnthropicContentBlock represents a content block in a message
type AnthropicContentBlock struct {
	Type  string                `json:"type"`  // "text", "image", "tool_use", "tool_result"
	Text  string                `json:"text,omitempty"`

	// For image blocks
	Source *AnthropicImageSource `json:"source,omitempty"`

	// For tool_use blocks
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool_result blocks
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or []AnthropicContentBlock (nested)
	IsError   bool            `json:"is_error,omitempty"`
}

// AnthropicImageSource represents an image source
type AnthropicImageSource struct {
	Type      string `json:"type"`       // "base64" or "url"
	MediaType string `json:"media_type"` // "image/png", "image/jpeg", "image/gif", "image/webp"
	Data      string `json:"data,omitempty"` // base64 data (when type="base64")
	URL       string `json:"url,omitempty"`  // URL (when type="url")
}

// AnthropicTool represents a tool definition
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicMetadata represents request metadata
type AnthropicMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// ------------------- Messages Response -------------------

// AnthropicMessagesResponse represents an Anthropic Messages API response
type AnthropicMessagesResponse struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"`  // "message"
	Role         string                   `json:"role"`  // "assistant"
	Content      []AnthropicResponseBlock `json:"content"`
	Model        string                   `json:"model"`
	StopReason   string                   `json:"stop_reason"`   // "end_turn", "max_tokens", "stop_sequence", "tool_use"
	StopSequence *string                  `json:"stop_sequence"`
	Usage        AnthropicUsage           `json:"usage"`
}

// AnthropicResponseBlock represents a content block in the response
type AnthropicResponseBlock struct {
	Type  string          `json:"type"` // "text", "tool_use"
	Text  string          `json:"text,omitempty"`

	// For tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// AnthropicUsage represents token usage in Anthropic format
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ------------------- Streaming Event Types -------------------

// AnthropicMessageStartEvent is emitted once at the start of a streamed response
type AnthropicMessageStartEvent struct {
	Type    string                    `json:"type"` // "message_start"
	Message AnthropicMessagesResponse `json:"message"`
}

// AnthropicContentBlockStartEvent is emitted when a new content block begins
type AnthropicContentBlockStartEvent struct {
	Type         string                 `json:"type"` // "content_block_start"
	Index        int                    `json:"index"`
	ContentBlock AnthropicResponseBlock `json:"content_block"`
}

// AnthropicContentBlockDeltaEvent is emitted for incremental content updates
type AnthropicContentBlockDeltaEvent struct {
	Type  string               `json:"type"` // "content_block_delta"
	Index int                  `json:"index"`
	Delta AnthropicDeltaBlock  `json:"delta"`
}

// AnthropicDeltaBlock represents a delta in streaming
type AnthropicDeltaBlock struct {
	Type        string `json:"type"`              // "text_delta", "input_json_delta"
	Text        string `json:"text,omitempty"`    // for text_delta
	PartialJSON string `json:"partial_json,omitempty"` // for input_json_delta
}

// AnthropicContentBlockStopEvent is emitted when a content block ends
type AnthropicContentBlockStopEvent struct {
	Type  string `json:"type"` // "content_block_stop"
	Index int    `json:"index"`
}

// AnthropicMessageDeltaEvent is emitted when the overall message state changes (e.g. stop_reason)
type AnthropicMessageDeltaEvent struct {
	Type  string                    `json:"type"` // "message_delta"
	Delta AnthropicMessageDeltaData `json:"delta"`
	Usage AnthropicDeltaUsage       `json:"usage"`
}

// AnthropicMessageDeltaData represents the delta in message_delta events
type AnthropicMessageDeltaData struct {
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence"`
}

// AnthropicDeltaUsage represents output token usage in delta events
type AnthropicDeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

// AnthropicMessageStopEvent is emitted once at the end of a streamed response
type AnthropicMessageStopEvent struct {
	Type string `json:"type"` // "message_stop"
}

// ------------------- Ping Event -------------------

// AnthropicPingEvent is a keep-alive event
type AnthropicPingEvent struct {
	Type string `json:"type"` // "ping"
}

// ------------------- Error -------------------

// AnthropicErrorResponse represents an Anthropic error response
type AnthropicErrorResponse struct {
	Type  string               `json:"type"` // "error"
	Error AnthropicErrorDetail `json:"error"`
}

// AnthropicErrorDetail represents error details
type AnthropicErrorDetail struct {
	Type    string `json:"type"`    // "invalid_request_error", "authentication_error", etc.
	Message string `json:"message"`
}
