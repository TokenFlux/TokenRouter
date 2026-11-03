package anthropic

import (
	"encoding/json"
)

// types.go defines Messages wire types and serializes required empty content
// fields.
// Messages conversions need to distinguish an unfinished response from a
// stopped response and an empty initial content block from an absent field.
// Response stop reasons use pointers so message_start can encode JSON null,
// while text and thinking block serialization keeps their empty initial strings
// available for clients that expect subsequent deltas.
//
// Request content, metadata, and tool arguments retain raw JSON where their
// shape can vary or include fields the converters do not interpret. Usage
// counters include both canonical Messages buckets and compatible-provider
// aliases; MergeAnthropicUsage and NormalizeAnthropicCompatiblePromptUsage
// resolve those overlapping quantities before billing. Stream events attach
// message, block, or delta payloads according to their event type, retaining
// explicit index zero through an optional integer pointer.

// AnthropicRequest is the request body sent to the Messages endpoint.
type AnthropicRequest struct {
	Model     string             `json:"model"`            // forwarded model identifier
	MaxTokens int                `json:"max_tokens"`       // maximum generated tokens
	System    json.RawMessage    `json:"system,omitempty"` // JSON string or block array
	Messages  []AnthropicMessage `json:"messages"`         // ordered conversation turns
	Tools     []AnthropicTool    `json:"tools,omitempty"`  // advertised tool declarations
	Stream    bool               `json:"stream,omitempty"` // streaming response selection
	// Speed requests fast processing with "fast". An empty value is omitted,
	// leaving speed selection to the downstream request policy and upstream.
	Speed string `json:"speed,omitempty"`
	// Temperature and TopP distinguish supplied zero settings from omitted
	// settings. Nil pointers omit their JSON fields, allowing the downstream
	// adapter or upstream to choose those sampling values.
	Temperature *float64           `json:"temperature,omitempty"`    // optional sampling temperature
	TopP        *float64           `json:"top_p,omitempty"`          // optional nucleus probability
	StopSeqs    []string           `json:"stop_sequences,omitempty"` // requested stopping strings
	Thinking    *AnthropicThinking `json:"thinking,omitempty"`       // optional reasoning configuration
	ToolChoice  json.RawMessage    `json:"tool_choice,omitempty"`    // JSON tool-selection policy
	// Metadata survives request reserialization so the OAuth/Claude Code path can
	// rewrite metadata.user_id before forwarding. Upstream client attribution
	// depends on that value; dropping metadata here removes the input needed by
	// ensureClaudeOAuthMetadataUserID and RewriteUserIDWithMasking.
	// Keep the raw object, including fields this type does not interpret.
	Metadata     json.RawMessage        `json:"metadata,omitempty"`
	OutputConfig *AnthropicOutputConfig `json:"output_config,omitempty"` // optional output configuration
}

// AnthropicOutputConfig holds output-generation settings attached to a Messages request.
type AnthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low", "medium", "high", "xhigh", or "max"
}

// AnthropicThinking configures extended thinking in the Anthropic API.
type AnthropicThinking struct {
	Type         string `json:"type"`                    // "enabled", "adaptive", or "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // maximum reasoning tokens
}

// AnthropicMessage is a single message in the Anthropic conversation.
type AnthropicMessage struct {
	Role    string          `json:"role"`    // "user" or "assistant"
	Content json.RawMessage `json:"content"` // JSON string or block array
}

// AnthropicContentBlock is one block inside a message's content array.
type AnthropicContentBlock struct {
	Type string `json:"type"` // wire block discriminator

	CacheControl *AnthropicCacheControl `json:"cache_control,omitempty"` // optional cache directive

	// Text supplies readable content for text blocks. MarshalJSON keeps an
	// explicit empty string when such a block opens a streamed response.
	Text string `json:"text,omitempty"`

	// Thinking supplies readable reasoning for thinking blocks. MarshalJSON
	// retains an explicit empty string so later deltas can fill the opened block.
	Thinking string `json:"thinking,omitempty"`
	// Signature carries opaque reasoning replay data, including xAI
	// encrypted_content. Clients retain it alongside thinking blocks for later
	// conversation requests; its meaning depends on the producing upstream.
	Signature string `json:"signature,omitempty"`

	// Source supplies encoded image data when Type is image. A nil source is
	// omitted by the JSON encoder; callers validate the usable image representation.
	Source *AnthropicImageSource `json:"source,omitempty"`

	// ID, Name, and Input describe a tool_use invocation. ID connects later
	// tool_result blocks to this call, Name selects the tool, and Input retains
	// its JSON arguments for conversion or upstream replay.
	ID    string          `json:"id,omitempty"`    // tool invocation identifier
	Name  string          `json:"name,omitempty"`  // invoked tool name
	Input json.RawMessage `json:"input,omitempty"` // JSON tool arguments

	// ToolUseID, Content, and IsError describe a tool_result block. ToolUseID
	// links to the originating call, Content retains its JSON output, and IsError
	// reports whether the tool marked that execution as failed.
	ToolUseID string          `json:"tool_use_id,omitempty"` // originating tool-call identifier
	Content   json.RawMessage `json:"content,omitempty"`     // JSON string or block array
	IsError   bool            `json:"is_error,omitempty"`    // tool execution failure
}

// AnthropicImageSource describes the source data for an image content block.
type AnthropicImageSource struct {
	Type      string `json:"type"`       // base64 source encoding
	MediaType string `json:"media_type"` // image MIME type
	Data      string `json:"data"`       // base64
}

// AnthropicTool describes a tool available to the model.
type AnthropicTool struct {
	Type         string                 `json:"type,omitempty"`          // e.g. "web_search_20250305" for server tools
	Name         string                 `json:"name"`                    // advertised tool name
	Description  string                 `json:"description,omitempty"`   // advertised tool description
	InputSchema  json.RawMessage        `json:"input_schema,omitempty"`  // JSON Schema object
	CacheControl *AnthropicCacheControl `json:"cache_control,omitempty"` // optional cache directive
}

// AnthropicCacheControl represents the API's cache_control field.
// The caller chooses a TTL; see claude.DefaultCacheControlTTL for the project default.
type AnthropicCacheControl struct {
	Type string `json:"type"` // ephemeral cache directive
	// TTL selects "5m" or "1h" when supplied. Omission leaves lifetime selection
	// to the upstream; gateway-generated directives set their TTL when constructed.
	TTL string `json:"ttl,omitempty"` // "5m" or "1h"
}

// AnthropicResponse represents complete Messages output or the initial
// message_start payload supplied before streamed content and final usage arrive.
type AnthropicResponse struct {
	ID      string                  `json:"id"`      // response message identifier
	Type    string                  `json:"type"`    // message response type
	Role    string                  `json:"role"`    // assistant response role
	Content []AnthropicContentBlock `json:"content"` // ordered output blocks
	Model   string                  `json:"model"`   // client-visible model identifier
	// StopReason encodes nil as JSON null while the message is unfinished.
	// A nonnil pointer supplies the completed reason; even an empty pointed-to
	// string is serialized, so callers choose the appropriate protocol value.
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence,omitempty"` // matched stopping sequence
	Usage        AnthropicUsage `json:"usage"`                   // reported token buckets
}

// AnthropicPromptTokensDetails contains OpenAI-style prompt details returned
// by some Anthropic-compatible providers.
type AnthropicPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"` // cached prompt tokens
}

// AnthropicUsage holds Messages token counts and compatible-provider prompt aliases.
type AnthropicUsage struct {
	// InputTokens, CacheCreationInputTokens, and CacheReadInputTokens describe
	// the canonical prompt buckets. Compatible providers can also report totals
	// below; MergeAnthropicUsage normalizes their overlapping quantities before billing.
	InputTokens              int `json:"input_tokens"`                // reported input tokens
	OutputTokens             int `json:"output_tokens"`               // generated response tokens
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"` // cache-written prompt tokens
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`     // cache-read prompt tokens
	// CacheCreation preserves reported lifetime buckets. Nil means no lifetime
	// details were supplied; individual pointers retain explicit zero observations.
	CacheCreation *AnthropicCacheCreation `json:"cache_creation,omitempty"`
	// Speed reports the upstream processing mode, such as fast or standard.
	// MergeAnthropicUsage retains a previous observation when this string is blank.
	Speed string `json:"speed,omitempty"`
	// PromptTokens reports total prompt work, while CachedTokens,
	// PromptTokensDetails, and PromptCacheHitTokens can identify cache reads.
	// PromptCacheMissTokens supplies an uncached bucket directly. The merge
	// helpers resolve these aliases before subtracting cached work from a total.
	PromptTokens        int                           `json:"prompt_tokens,omitempty"`         // total prompt tokens
	CachedTokens        int                           `json:"cached_tokens,omitempty"`         // cached prompt tokens
	PromptTokensDetails *AnthropicPromptTokensDetails `json:"prompt_tokens_details,omitempty"` // nested prompt details
	// PromptCacheHitTokens distinguishes an omitted observation from an
	// explicit zero count when MergeAnthropicUsage selects compatible-provider normalization.
	PromptCacheHitTokens *int `json:"prompt_cache_hit_tokens,omitempty"` // observed cache-hit tokens
	// PromptCacheMissTokens is authoritative when present, including zero.
	// MergeAnthropicUsage uses that uncached bucket directly rather than subtracting cache reads again.
	PromptCacheMissTokens *int `json:"prompt_cache_miss_tokens,omitempty"` // observed uncached tokens
}

// AnthropicCacheCreation retains the independently reported cache-write lifetimes
// used by Messages responses and their streaming usage updates.
type AnthropicCacheCreation struct {
	// Ephemeral5mInputTokens replaces the five-minute bucket when present, including
	// zero when an upstream moves the same writes to another lifetime.
	Ephemeral5mInputTokens *int `json:"ephemeral_5m_input_tokens,omitempty"`
	// Ephemeral1hInputTokens replaces the one-hour bucket when present, including
	// zero when an upstream moves the same writes to another lifetime.
	Ephemeral1hInputTokens *int `json:"ephemeral_1h_input_tokens,omitempty"`
}

// AnthropicStreamEvent is a single SSE event in the Anthropic streaming protocol.
type AnthropicStreamEvent struct {
	Type string `json:"type"` // wire event discriminator

	// Message supplies the initial response in message_start events. A nil
	// message is omitted when this event type carries a block or delta instead.
	Message *AnthropicResponse `json:"message,omitempty"`

	// Index and ContentBlock describe a content_block_start event together.
	// Index also accompanies later block events, while ContentBlock supplies
	// the opening block shape before subsequent deltas fill its content.
	// Index uses a pointer so index zero is serialized while nil omits the
	// field for events that have no associated content-block position.
	Index        *int                   `json:"index,omitempty"`         // content block position
	ContentBlock *AnthropicContentBlock `json:"content_block,omitempty"` // initial block shape

	// Delta supplies incremental block or message information. The payload
	// fields depend on its type, and nil omits it from events without a delta.
	Delta *AnthropicDelta `json:"delta,omitempty"`

	// Usage supplies message_delta observations for stream accounting. A nil
	// pointer omits usage when an event carries no new token-count observation.
	Usage *AnthropicUsage `json:"usage,omitempty"`
}

// AnthropicDelta carries incremental content changes in Messages streaming events.
type AnthropicDelta struct {
	Type string `json:"type,omitempty"` // "text_delta" | "input_json_delta" | "thinking_delta" | "signature_delta"

	// Text supplies a text_delta fragment. Empty strings are omitted when
	// this delta instead carries reasoning, arguments, a signature, or completion information.
	Text string `json:"text,omitempty"`

	// PartialJSON supplies an input_json_delta argument fragment. Consumers
	// concatenate fragments for one tool call before decoding its complete argument object.
	PartialJSON string `json:"partial_json,omitempty"`

	// Thinking supplies a thinking_delta fragment. Consumers accumulate it
	// with reasoning fragments for the same content block in their stream state.
	Thinking string `json:"thinking,omitempty"`

	// Signature supplies a signature_delta fragment. Clients retain the
	// accumulated opaque signature separately from readable reasoning for later conversation replay.
	Signature string `json:"signature,omitempty"`

	// StopReason and StopSequence describe message completion in message_delta.
	// An empty reason is omitted; a nil stopping-sequence pointer is omitted,
	// while a supplied pointer retains even an empty matched sequence.
	StopReason   string  `json:"stop_reason,omitempty"`   // generation stop reason
	StopSequence *string `json:"stop_sequence,omitempty"` // matched stopping sequence
}

// MarshalJSON encodes a content block while keeping required empty text or
// thinking strings for their respective block types. Clients can receive the
// block's initial shape before a later content_block_delta supplies its text.
//
// The method uses an alias to apply ordinary JSON rules to the remaining
// fields without invoking this method recursively. For text and thinking
// blocks, a wrapper adds the required string field without omitempty; other
// block types use the alias directly. Encoding errors are returned to the
// caller before any usable serialized block is available.
func (b AnthropicContentBlock) MarshalJSON() ([]byte, error) {
	// anthropicContentBlock retains the block's fields while disabling its custom marshaler.
	type anthropicContentBlock AnthropicContentBlock

	switch b.Type {
	case "text":
		// A stream can open a text block before its first fragment arrives.
		// Override omitempty so strict clients still receive the required empty text field.
		return json.Marshal(struct {
			Text                  string `json:"text"` // required initial text
			anthropicContentBlock        // remaining block fields
		}{
			Text:                  b.Text,
			anthropicContentBlock: anthropicContentBlock(b),
		})
	case "thinking":
		// Reasoning streams likewise open their block before a thinking delta.
		// Keep the empty thinking field present so the opening event supplies its expected shape.
		return json.Marshal(struct {
			Thinking              string `json:"thinking"` // required initial reasoning
			anthropicContentBlock        // remaining block fields
		}{
			Thinking:              b.Thinking,
			anthropicContentBlock: anthropicContentBlock(b),
		})
	default:
		return json.Marshal(anthropicContentBlock(b))
	}
}

// AnthropicStopReasonPtr returns a fresh pointer containing s, including an
// empty string. A nonnil result serializes its value; callers pass nil separately
// when a Messages response needs an unfinished null stop reason.
func AnthropicStopReasonPtr(s string) *string {
	return &s
}

// AnthropicStopReasonString reads a pointed-to stop reason or returns an empty
// string for nil. Callers needing to distinguish an unfinished response from an
// explicitly empty reason inspect the pointer before using this string value.
func AnthropicStopReasonString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
