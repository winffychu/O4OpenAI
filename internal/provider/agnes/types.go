package agnes

import "encoding/json"

// ============================================================
// Agnes AI specific types - internal to the Agnes provider
// These represent the native Agnes AI API request/response format
// ============================================================

// AgnesChatRequest is the request body for Agnes AI chat completions
type AgnesChatRequest struct {
	Model              string         `json:"model"`
	Messages           []AgnesMessage `json:"messages"`
	Temperature        *float64       `json:"temperature,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	TopK               *int           `json:"top_k,omitempty"`
	MaxTokens          *int           `json:"max_tokens,omitempty"`
	FrequencyPenalty   *float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty    *float64       `json:"presence_penalty,omitempty"`
	RepetitionPenalty  *float64       `json:"repetition_penalty,omitempty"`
	Stop               interface{}    `json:"stop,omitempty"` // string or []string
	Seed               *int64         `json:"seed,omitempty"`
	Stream             bool           `json:"stream,omitempty"`
	Tools              []AgnesTool    `json:"tools,omitempty"`
	ToolChoice         interface{}    `json:"tool_choice,omitempty"` // string or object
	Thinking           interface{}    `json:"thinking,omitempty"`            // Anthropic-compatible thinking: {"type":"enabled","budget_tokens":2048}
	ChatTemplateKwargs interface{}    `json:"chat_template_kwargs,omitempty"` // OpenAI-compatible thinking: {"enable_thinking":true}
}

// AgnesMessage represents a message in the Agnes chat format
type AgnesMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"`                  // string or []AgnesContentPart
	ToolCalls  []AgnesToolCall  `json:"tool_calls,omitempty"`     // for assistant messages with tool calls
	ToolCallID string           `json:"tool_call_id,omitempty"`   // for tool result messages
	Name       string           `json:"name,omitempty"`           // function name (for tool messages)
}

// AgnesContentPart represents a content part in Agnes multimodal messages
type AgnesContentPart struct {
	Type     string       `json:"type"` // "text" or "image_url"
	Text     string       `json:"text,omitempty"`
	ImageURL *AgnesImageURL `json:"image_url,omitempty"`
}

// AgnesImageURL represents an image URL in Agnes content
type AgnesImageURL struct {
	URL string `json:"url"`
}

// AgnesChatResponse is the response from Agnes AI chat completions
type AgnesChatResponse struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []AgnesChatChoice   `json:"choices"`
	Usage   *AgnesUsage         `json:"usage,omitempty"`
}

// AgnesChatChoice represents a choice in the Agnes response
type AgnesChatChoice struct {
	Index        int            `json:"index"`
	Message      AgnesMessageResp `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

// AgnesMessageResp represents the message in the Agnes response
type AgnesMessageResp struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []AgnesToolCall  `json:"tool_calls,omitempty"`
}

// AgnesUsage represents token usage from Agnes AI
type AgnesUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AgnesStreamChunk represents a streaming chunk from Agnes AI
type AgnesStreamChunk struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []AgnesStreamChoice     `json:"choices"`
}

// AgnesStreamChoice represents a choice in a streaming chunk
type AgnesStreamChoice struct {
	Index        int                  `json:"index"`
	Delta        AgnesStreamDelta     `json:"delta"`
	FinishReason *string              `json:"finish_reason"`
}

// AgnesStreamDelta represents the delta in a streaming chunk
type AgnesStreamDelta struct {
	Role    string  `json:"role,omitempty"`
	Content *string `json:"content,omitempty"`
}

// AgnesImageRequest is the request body for Agnes AI image generation
// Agnes AI may use different parameter names than OpenAI
type AgnesImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"` // url only for Agnes
}

// AgnesImageResponse is the response from Agnes AI image generation
type AgnesImageResponse struct {
	Created int64             `json:"created"`
	Data    []AgnesImageData  `json:"data"`
}

// AgnesImageData represents image data from Agnes AI
type AgnesImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// AgnesVideoExtra is the extra_body object for multi-image and keyframe modes
type AgnesVideoExtra struct {
	Image []string `json:"image,omitempty"`
	Mode  string   `json:"mode,omitempty"`
}

// AgnesVideoRequest is the request body for Agnes AI video generation.
// See: https://agnes-ai.com/doc/agnes-video-v20
type AgnesVideoRequest struct {
	Model          string           `json:"model"`
	Prompt         string           `json:"prompt"`
	Image          string           `json:"image,omitempty"`     // single image-to-video
	Mode           string           `json:"mode,omitempty"`      // e.g. "ti2vid", "keyframes"
	Height         int              `json:"height,omitempty"`    // default 768
	Width          int              `json:"width,omitempty"`     // default 1152
	NumFrames      int              `json:"num_frames,omitempty"` // must be 8n+1, <= 441
	NumInferenceSteps int           `json:"num_inference_steps,omitempty"`
	Seed           int64            `json:"seed,omitempty"`
	FrameRate      float64          `json:"frame_rate,omitempty"` // 1-60
	NegativePrompt string           `json:"negative_prompt,omitempty"`
	ExtraBody      *AgnesVideoExtra `json:"extra_body,omitempty"`
}

// AgnesVideoResponse is the response from Agnes AI video generation.
// Status field: queued | in_progress | completed | failed
// remixed_from_video_id is the actual video URL when status == completed.
type AgnesVideoResponse struct {
	ID                  string  `json:"id"`
	TaskID              string  `json:"task_id,omitempty"`
	VideoID             string  `json:"video_id,omitempty"`              // recommended query ID (new)
	Object              string  `json:"object"`
	Model               string  `json:"model"`
	Status              string  `json:"status"`
	Progress            int     `json:"progress,omitempty"`
	CreatedAt           int64   `json:"created_at"`
	StartedAt           *int64  `json:"started_at,omitempty"`
	CompletedAt         *int64  `json:"completed_at,omitempty"`
	ExpiresAt           *int64  `json:"expires_at,omitempty"`
	VideoURL            string  `json:"video_url,omitempty"`             // new: direct video URL when completed
	RemixedFromVideoID  string  `json:"remixed_from_video_id,omitempty"` // legacy: actual video URL
	Size                string  `json:"size,omitempty"`
	Seconds             string  `json:"seconds,omitempty"`
	Error               *AgnesVideoError `json:"error,omitempty"`
}

// AgnesVideoError represents a video generation error from Agnes
type AgnesVideoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ------------------- Tool Calling -------------------

// AgnesTool represents a tool definition in the Agnes format (OpenAI-compatible)
type AgnesTool struct {
	Type     string          `json:"type"`     // "function"
	Function AgnesToolFunc   `json:"function"`
}

// AgnesToolFunc represents a function definition for tools
type AgnesToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// AgnesToolCall represents a tool call in an Agnes response message
type AgnesToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function AgnesToolCallFunc `json:"function"`
}

// AgnesToolCallFunc represents the function details of a tool call
type AgnesToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}
