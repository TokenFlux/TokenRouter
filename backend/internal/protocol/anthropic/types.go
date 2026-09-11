package anthropic

import (
	"encoding/json"
)

// AnthropicRequest is the request body for POST /v1/messages.
type AnthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    json.RawMessage    `json:"system,omitempty"` // string or []AnthropicContentBlock
	Messages  []AnthropicMessage `json:"messages"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
	// Speed 为 Claude Fast 模式字段，目前支持 "fast"。
	Speed       string             `json:"speed,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	StopSeqs    []string           `json:"stop_sequences,omitempty"`
	Thinking    *AnthropicThinking `json:"thinking,omitempty"`
	ToolChoice  json.RawMessage    `json:"tool_choice,omitempty"`
	// Metadata 会被原样透传给上游。OAuth/Claude-Code 路径依赖 metadata.user_id
	// 参与上游的"是否为官方 Claude Code 请求"判定；如果经由本结构体重新序列化
	// 时丢弃该字段，网关侧后续的 metadata 重写(ensureClaudeOAuthMetadataUserID/
	// RewriteUserIDWithMasking) 在 body 里拿不到起点，就无法重建一个合法的
	// user_id，进而导致请求被归类为第三方 app。
	Metadata     json.RawMessage        `json:"metadata,omitempty"`
	OutputConfig *AnthropicOutputConfig `json:"output_config,omitempty"`
}

// AnthropicOutputConfig controls output generation parameters.
type AnthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low" | "medium" | "high" | "max"
}

// AnthropicThinking configures extended thinking in the Anthropic API.
type AnthropicThinking struct {
	Type         string `json:"type"`                    // "enabled" | "adaptive" | "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // max thinking tokens
}

// AnthropicMessage is a single message in the Anthropic conversation.
type AnthropicMessage struct {
	Role    string          `json:"role"` // "user" | "assistant"
	Content json.RawMessage `json:"content"`
}

// AnthropicContentBlock is one block inside a message's content array.
type AnthropicContentBlock struct {
	Type string `json:"type"`

	CacheControl *AnthropicCacheControl `json:"cache_control,omitempty"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=thinking
	Thinking string `json:"thinking,omitempty"`
	// Signature 携带提供方的加密推理（例如 xAI encrypted_content），使 Claude 多轮
	// 客户端可以在后续轮次中原样回传。
	Signature string `json:"signature,omitempty"`

	// type=image
	Source *AnthropicImageSource `json:"source,omitempty"`

	// type=tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// type=tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or []AnthropicContentBlock
	IsError   bool            `json:"is_error,omitempty"`
}

// MarshalJSON 保留 text/thinking 块里的空字符串字段，避免流式 content_block_start 丢失协议要求的键。
func (b AnthropicContentBlock) MarshalJSON() ([]byte, error) {
	type anthropicContentBlock AnthropicContentBlock

	switch b.Type {
	case "text":
		return json.Marshal(struct {
			Text string `json:"text"`
			anthropicContentBlock
		}{
			Text:                  b.Text,
			anthropicContentBlock: anthropicContentBlock(b),
		})
	case "thinking":
		return json.Marshal(struct {
			Thinking string `json:"thinking"`
			anthropicContentBlock
		}{
			Thinking:              b.Thinking,
			anthropicContentBlock: anthropicContentBlock(b),
		})
	default:
		return json.Marshal(anthropicContentBlock(b))
	}
}

// AnthropicImageSource describes the source data for an image content block.
type AnthropicImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// AnthropicTool describes a tool available to the model.
type AnthropicTool struct {
	Type         string                 `json:"type,omitempty"` // e.g. "web_search_20250305" for server tools
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  json.RawMessage        `json:"input_schema,omitempty"` // JSON Schema object
	CacheControl *AnthropicCacheControl `json:"cache_control,omitempty"`
}

// AnthropicCacheControl 对应 Anthropic API 的 cache_control 字段。
// ttl 默认由调用方决定；本项目策略见 claude.DefaultCacheControlTTL。
type AnthropicCacheControl struct {
	Type string `json:"type"`          // "ephemeral"
	TTL  string `json:"ttl,omitempty"` // "5m" / "1h" / 省略=默认 5m（由 Anthropic 判定）
}

// AnthropicResponse 表示 POST /v1/messages 的非流式响应。
//
// StopReason 使用指针，以便流式 message_start 按 Anthropic 官方格式输出 JSON null。
// 普通字符串的零值会序列化为空字符串，严格客户端会将其视为无效的流中状态。
type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"` // "message"
	Role         string                  `json:"role"` // "assistant"
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   *string                 `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicStopReasonPtr 为最终停止原因返回非空字符串指针。
func AnthropicStopReasonPtr(s string) *string {
	return &s
}

// AnthropicStopReasonString 返回停止原因；未设置或为 null 时返回空字符串。
func AnthropicStopReasonString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// AnthropicPromptTokensDetails 保存兼容 Anthropic 的 provider 偶尔附带的
// OpenAI 风格 prompt token 明细。
type AnthropicPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// AnthropicUsage holds token counts in Anthropic format.
type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	// Speed 为 Claude 实际处理速度：fast 或 standard。
	Speed string `json:"speed,omitempty"`
	// 兼容 Anthropic 的 provider 可能附带 OpenAI 风格的总量/缓存字段，保留后由
	// 调用方归一化为互斥的计费桶。
	PromptTokens          int                           `json:"prompt_tokens,omitempty"`
	CachedTokens          int                           `json:"cached_tokens,omitempty"`
	PromptTokensDetails   *AnthropicPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
	PromptCacheHitTokens  *int                          `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens *int                          `json:"prompt_cache_miss_tokens,omitempty"`
}

// AnthropicStreamEvent is a single SSE event in the Anthropic streaming protocol.
type AnthropicStreamEvent struct {
	Type string `json:"type"`

	// message_start
	Message *AnthropicResponse `json:"message,omitempty"`

	// content_block_start
	Index        *int                   `json:"index,omitempty"`
	ContentBlock *AnthropicContentBlock `json:"content_block,omitempty"`

	// content_block_delta
	Delta *AnthropicDelta `json:"delta,omitempty"`

	// message_delta
	Usage *AnthropicUsage `json:"usage,omitempty"`
}

// AnthropicDelta carries incremental content in streaming events.
type AnthropicDelta struct {
	Type string `json:"type,omitempty"` // "text_delta" | "input_json_delta" | "thinking_delta" | "signature_delta"

	// text_delta
	Text string `json:"text,omitempty"`

	// input_json_delta
	PartialJSON string `json:"partial_json,omitempty"`

	// thinking_delta
	Thinking string `json:"thinking,omitempty"`

	// signature_delta
	Signature string `json:"signature,omitempty"`

	// message_delta fields
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}
