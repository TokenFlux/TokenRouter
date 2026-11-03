package openai

import (
	"bytes"
	"encoding/json"

	"github.com/tidwall/gjson"
)

// types.go defines Responses and Chat wire values and adapts tool arguments,
// result content, and usage aliases during JSON conversion.
// Responses and Chat share conversation concepts but encode tool output,
// arguments, usage aliases, and streamed empty values differently. Input
// decoding retains nonstring tool-result JSON beside the public string form,
// and discovery-call decoding keeps object arguments as text until its
// specialized encoder can restore a JSON value. The typed shapes cover
// recognized fields; unknown extensions require a raw helper when callers need
// to retain them.
//
// Usage decoding resolves canonical counts, Chat aliases, and nested cache
// presence in a defined order. An explicit nested zero can defeat a positive
// alias, while a canonical numeric zero can still use a nonzero Chat count.
// Stream event encoding adds required indexes and arrays separately from
// ordinary struct tags, so the same values can have different wire presence
// according to the selected event kind.

// Text protocol values select the wire format expected by text exchange adapters
// and keep endpoint labels consistent across their shared protocol request types.
const (
	TextProtocolChatCompletions TextProtocol = "chat_completions" // Chat-compatible text endpoint
	TextProtocolResponses       TextProtocol = "responses"        // Responses text endpoint
)

// ResponsesRequest represents the typed Responses request subset consumed by
// protocol converters.
type ResponsesRequest struct {
	Model string `json:"model"` // requested Responses model
	// leading request instruction text
	Instructions string          `json:"instructions,omitempty"`
	Input        json.RawMessage `json:"input"` // raw string or item-array input
	// MaxOutputTokens, Temperature, and TopP use nil to omit their controls. A
	// nonnil pointer retains the supplied numeric value, including zero, for the
	// upstream or converter to interpret.
	// optional output token limit
	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`
	// optional sampling temperature value
	Temperature *float64 `json:"temperature,omitempty"`
	// optional nucleus sampling probability
	TopP   *float64 `json:"top_p,omitempty"`
	Stream bool     `json:"stream,omitempty"` // requested SSE response mode
	// available Responses tool declarations
	Tools []ResponsesTool `json:"tools,omitempty"`
	// requested supplemental output fields
	Include []string `json:"include,omitempty"`
	// Store distinguishes an omitted storage preference from a supplied false
	// value; JSON encoding keeps either pointed-to boolean.
	// optional response storage selection
	Store *bool `json:"store,omitempty"`
	// ParallelToolCalls distinguishes an omitted concurrency preference from a
	// supplied false value; converters can therefore disable parallel execution.
	// optional parallel call selection
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
	// optional reasoning control object
	Reasoning  *ResponsesReasoning `json:"reasoning,omitempty"`
	Text       *ResponsesText      `json:"text,omitempty"`        // optional text output preferences
	ToolChoice json.RawMessage     `json:"tool_choice,omitempty"` // raw tool selection value
	// requested upstream service tier
	ServiceTier string `json:"service_tier,omitempty"`
	// caller prompt-cache routing hint
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
	// earlier response continuation identity
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

// ResponsesReasoning carries effort and summary preferences for converted
// Responses requests.
type ResponsesReasoning struct {
	Effort  string `json:"effort"`            // reasoning effort label, such as low, medium, high, xhigh, or max
	Summary string `json:"summary,omitempty"` // reasoning summary style: auto, concise, or detailed
}

// ResponsesText carries structured format and verbosity preferences for
// Responses output.
type ResponsesText struct {
	Format    json.RawMessage `json:"format,omitempty"`    // raw structured output format
	Verbosity string          `json:"verbosity,omitempty"` // output verbosity label: low, medium, or high
}

// ResponsesInputItem represents a supported conversation item and retains
// nonstring tool output for conversion. Its ordinary encoding uses the public
// string Output; RawOutput supplies retained JSON to multimodal readers.
type ResponsesInputItem struct {
	Type string `json:"type,omitempty"` // input kind; blank denotes a role-based message

	// Role and Content carry role-based messages when Type is blank or message.
	// Content retains raw string or typed-part JSON for the request converter
	// that selects the target format.
	Role    string          `json:"role,omitempty"`    // message speaker role
	Content json.RawMessage `json:"content,omitempty"` // raw message content value

	EncryptedContent string `json:"encrypted_content,omitempty"` // opaque reasoning replay data

	// CallID correlates a call with its result, while ID identifies the upstream
	// input item. Name and Arguments describe the called function independently
	// of either identity.
	CallID    string `json:"call_id,omitempty"`   // tool result correlation identity
	Name      string `json:"name,omitempty"`      // called function tool name
	Arguments string `json:"arguments,omitempty"` // serialized function argument text
	ID        string `json:"id,omitempty"`        // upstream input item identity

	// Output contains decoded string output, or the raw JSON text of nonstring
	// output. outputRaw retains the latter bytes for RawOutput consumers;
	// ordinary struct serialization uses Output as a string.
	Output string `json:"output,omitempty"` // decoded tool output text
	// outputRaw owns copied nonstring JSON from UnmarshalJSON. RawOutput exposes
	// this storage, so its consumers treat the returned bytes as read-only.
	outputRaw json.RawMessage // retained nonstring output JSON
}

// ResponsesContentPart represents supported text, image, and file parts in
// Responses messages.
type ResponsesContentPart struct {
	Type     string `json:"type"`                // typed content part discriminator
	Text     string `json:"text,omitempty"`      // input or output text
	ImageURL string `json:"image_url,omitempty"` // image URL or data URI

	Filename string `json:"filename,omitempty"`  // attached file display name
	FileData string `json:"file_data,omitempty"` // attached file data URI
	FileID   string `json:"file_id,omitempty"`   // upstream uploaded file identity
}

// ResponsesTool represents supported function, namespace, search, and client
// tool declaration fields.
type ResponsesTool struct {
	Type        string          `json:"type"`                  // Responses tool kind discriminator
	Name        string          `json:"name,omitempty"`        // declared client tool name
	Description string          `json:"description,omitempty"` // model-visible tool usage description
	Parameters  json.RawMessage `json:"parameters,omitempty"`  // raw function parameter schema
	// Strict uses pointer presence so an explicit false selection remains
	// serialized; nil omits the setting from the declaration.
	Strict *bool `json:"strict,omitempty"` // optional strict-schema selection

	// Tools and Children are alternate child arrays. Bridge namespace conversion
	// prefers a nonempty Tools array and otherwise reads Children; encoding
	// retains whichever collections the caller populated.
	Tools    []ResponsesTool `json:"tools,omitempty"`    // primary namespace child declarations
	Children []ResponsesTool `json:"children,omitempty"` // alternate namespace child declarations

	// permitted X account handles
	// AllowedXHandles and ExcludedXHandles filter x_search accounts, while
	// FromDate and ToDate bound its dates. EnableImageUnderstanding and
	// EnableVideoUnderstanding select supported media interpretation on that tool.
	AllowedXHandles []string `json:"allowed_x_handles,omitempty"`
	// excluded X account handles
	ExcludedXHandles []string `json:"excluded_x_handles,omitempty"`
	FromDate         string   `json:"from_date,omitempty"` // search lower date bound
	ToDate           string   `json:"to_date,omitempty"`   // search upper date bound
	// optional image analysis selection
	// EnableImageUnderstanding and EnableVideoUnderstanding keep explicit false
	// when their pointers are supplied. Nil omits the corresponding media setting
	// from the tool, leaving its upstream default unspecified here.
	EnableImageUnderstanding *bool `json:"enable_image_understanding,omitempty"`
	// optional video analysis selection
	EnableVideoUnderstanding *bool `json:"enable_video_understanding,omitempty"`
}

// ResponsesResponse represents the typed buffered response and lifecycle
// snapshot consumed by converters.
type ResponsesResponse struct {
	ID     string `json:"id"`     // upstream response object identity
	Object string `json:"object"` // response object kind label
	// CreatedAt remains present even when zero. The Responses event fixtures
	// check this required timestamp key during struct serialization and event
	// round trips.
	CreatedAt   int64             `json:"created_at"`             // Unix creation time in seconds
	Model       string            `json:"model"`                  // response model identity string
	Status      string            `json:"status"`                 // reported response lifecycle status
	Output      []ResponsesOutput `json:"output"`                 // ordered Responses output items
	Usage       *ResponsesUsage   `json:"usage,omitempty"`        // optional reported token usage
	ServiceTier string            `json:"service_tier,omitempty"` // reported upstream service tier

	// optional incomplete response explanation
	IncompleteDetails *ResponsesIncompleteDetails `json:"incomplete_details,omitempty"`

	Error *ResponsesError `json:"error,omitempty"` // optional failed response explanation
}

// ResponsesError carries a failed response's code, message, and optional
// diagnostic fields.
type ResponsesError struct {
	Code       string `json:"code"`                  // upstream error category code
	Type       string `json:"type,omitempty"`        // optional error type label
	Message    string `json:"message"`               // upstream error message text
	StatusCode int    `json:"status_code,omitempty"` // upstream HTTP status observation
}

// ResponsesIncompleteDetails carries the reported reason for an incomplete
// Responses result.
type ResponsesIncompleteDetails struct {
	Reason string `json:"reason"` // incomplete response reason label
}

// ResponsesOutput represents the known message, reasoning, search, and
// tool-call output fields.
type ResponsesOutput struct {
	// Type selects the tool-search-specific MarshalJSON shape and the event
	// serializer's message, reasoning, function, and custom-call field rules.
	Type string `json:"type"` // output item kind discriminator

	// ID identifies the upstream output item. Lifecycle item encoding includes
	// the key even when empty; ordinary item encoding can omit it.
	ID string `json:"id,omitempty"` // upstream output item identity
	// Role, Content, and Status describe message output. Event item encoding
	// defaults a blank role to assistant and emits an array for empty Content;
	// ordinary item tags can omit these fields.
	Role    string                 `json:"role,omitempty"`    // message output speaker role
	Content []ResponsesContentPart `json:"content,omitempty"` // typed message output parts
	Status  string                 `json:"status,omitempty"`  // output item lifecycle status

	// EncryptedContent and Summary describe reasoning output. Event item encoding
	// emits an array for empty Summary and includes encrypted replay data when
	// its string is nonempty.
	// opaque reasoning replay data
	EncryptedContent string             `json:"encrypted_content,omitempty"`
	Summary          []ResponsesSummary `json:"summary,omitempty"` // reasoning summary text blocks

	// CallID links client results to the call. Name and Namespace together
	// identify a namespaced function, while Arguments carries its serialized
	// parameters; the event codec selects fields by item type.
	// CallID remains present, including empty text, in function, custom, and
	// discovery lifecycle item shapes so clients receive the result correlation field.
	CallID string `json:"call_id,omitempty"` // tool result correlation identity
	// Name remains present, including empty text, for function and custom
	// lifecycle items. Discovery lifecycle items omit this modeled field.
	Name string `json:"name,omitempty"` // called client tool name
	// Arguments stores serialized parameter text. Function lifecycle items keep
	// empty text; discovery encoding applies ToolSearchCallArgumentsJSON's
	// parse-or-quote rule to the stored text.
	Arguments string `json:"arguments,omitempty"` // serialized tool argument text
	// Namespace qualifies function names. Function lifecycle encoding emits
	// nonempty namespace values; custom and discovery lifecycle shapes omit this
	// modeled field.
	Namespace string `json:"namespace,omitempty"` // called tool namespace identity

	// Input carries freeform custom-tool text. Custom call lifecycle encoding
	// includes an empty string so newly opened calls retain their input field.
	Input string `json:"input,omitempty"` // freeform custom tool input

	Action *WebSearchAction `json:"action,omitempty"` // optional hosted search action
}

// WebSearchAction carries the kind and query of a hosted web-search action.
type WebSearchAction struct {
	Type  string `json:"type,omitempty"`  // hosted search action kind
	Query string `json:"query,omitempty"` // primary hosted search query
}

// ResponsesSummary represents one typed plaintext block in a reasoning
// summary.
type ResponsesSummary struct {
	Type string `json:"type"` // reasoning summary block kind
	Text string `json:"text"` // reasoning summary text value
}

// ResponsesUsage represents token counts after Responses and Chat usage alias
// decoding.
type ResponsesUsage struct {
	// InputTokens and OutputTokens prefer canonical counts. UnmarshalJSON reads
	// nonzero prompt/completion aliases when the corresponding canonical count
	// is zero, including an explicitly supplied zero.
	InputTokens  int `json:"input_tokens"`  // total input token count
	OutputTokens int `json:"output_tokens"` // total output token count
	// TotalTokens retains a nonzero supplied aggregate. UnmarshalJSON recomputes
	// zero from the selected input and output counts after resolving their aliases.
	TotalTokens int `json:"total_tokens"` // reported aggregate token count
	// CacheCreationInputTokens is normalized by UnmarshalJSON. Present nested
	// cache-write or creation counts take precedence over top-level aliases,
	// including explicit zero.
	// selected cache creation token count
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`

	// optional input usage breakdown
	InputTokensDetails *ResponsesInputTokensDetails `json:"input_tokens_details,omitempty"`
	// optional output usage breakdown
	OutputTokensDetails *ResponsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// ResponsesInputTokensDetails represents cached, audio, and cache-write input
// token observations.
type ResponsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"` // cache-read input token count
	AudioTokens  int `json:"audio_tokens,omitempty"`  // audio input token count
	// cache-creation input token count
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheWriteTokens    int `json:"cache_write_tokens,omitempty"` // cache-write input token count
}

// ResponsesOutputTokensDetails represents reasoning, audio, and prediction
// output token observations.
type ResponsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"` // reasoning output token count
	AudioTokens     int `json:"audio_tokens,omitempty"`     // audio output token count
	// accepted prediction token count
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	// rejected prediction token count
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// ResponsesStreamEvent represents known lifecycle, delta, tool, reasoning, and
// error event fields. Its event-specific MarshalJSON supplies required zero
// values and arrays that ordinary omitempty tags would omit.
type ResponsesStreamEvent struct {
	Type string `json:"type"` // Responses stream event discriminator

	// Response and Usage can both carry terminal counts. Bridge response-to-Chat
	// and response-to-Messages completion adapters prefer a present
	// Response.Usage over the top-level Usage alias.
	Response *ResponsesResponse `json:"response,omitempty"` // optional response lifecycle snapshot
	Usage    *ResponsesUsage    `json:"usage,omitempty"`    // optional terminal usage alias

	// Item becomes an empty object when nil in output-item lifecycle encoding.
	// Ordinary event encoding follows the tag and omits a nil item.
	Item *ResponsesOutput `json:"item,omitempty"` // optional output lifecycle item

	// OutputIndex and ContentIndex may identify position zero. Event-specific
	// MarshalJSON emits those indexes for text/content events even when omitempty
	// would remove them; other event kinds select their own fields.
	OutputIndex  int `json:"output_index,omitempty"`  // output item position index
	ContentIndex int `json:"content_index,omitempty"` // message content part index
	// Delta and Text separate fragments from completed text. Selected delta maps
	// emit Delta even when empty, while text-done maps emit Text even when empty.
	// Those maps carry one of these fields.
	Delta  string `json:"delta,omitempty"`   // incremental text or argument fragment
	Text   string `json:"text,omitempty"`    // completed text event value
	ItemID string `json:"item_id,omitempty"` // associated output item identity

	CallID string `json:"call_id,omitempty"` // associated tool call identity
	Name   string `json:"name,omitempty"`    // associated tool function name
	// Arguments and Input carry final function and freeform parameters.
	// Their recognized done-event maps keep empty strings so completion retains
	// the matching parameter field. Delta events emit Delta.
	Arguments string `json:"arguments,omitempty"` // completed function argument text

	Input string `json:"input,omitempty"` // completed custom tool input

	// SummaryIndex identifies a reasoning summary block. Reasoning summary
	// text/part events serialize zero as the first block position through MarshalJSON.
	SummaryIndex int `json:"summary_index,omitempty"` // reasoning summary part index

	// Part contributes Text to content/summary events. Nil yields empty text in
	// an output_text or summary_text part; ordinary event encoding omits nil.
	Part *ResponsesContentPart `json:"part,omitempty"` // optional content or summary part

	Code  string `json:"code,omitempty"`  // stream error category code
	Param string `json:"param,omitempty"` // stream error parameter name

	// SequenceNumber is present even at zero in selected event maps. Lifecycle
	// and unlisted events use ordinary tags and can omit zero.
	SequenceNumber int `json:"sequence_number,omitempty"` // stream event sequence index
}

// ChatCompletionsRequest represents modern and legacy Chat request fields
// consumed by converters.
type ChatCompletionsRequest struct {
	Model    string        `json:"model"`    // requested Chat model identity
	Messages []ChatMessage `json:"messages"` // ordered Chat conversation messages
	// Responses-compatible instruction text alias
	Instructions string `json:"instructions,omitempty"`
	// MaxTokens and MaxCompletionTokens are alternate output limits.
	// Chat-to-Responses request conversion uses MaxCompletionTokens when present;
	// otherwise it uses MaxTokens, and only a positive selected limit is
	// forwarded.
	MaxTokens *int `json:"max_tokens,omitempty"` // legacy output token limit
	// completion output token limit
	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"`
	// optional sampling temperature value
	// Temperature and TopP retain pointer presence, so supplied numeric zero
	// remains available for conversion or encoding. Nil omits the selected control
	// and lets the receiving request path choose its ordinary sampling default.
	Temperature *float64 `json:"temperature,omitempty"`
	// optional nucleus sampling probability
	TopP   *float64 `json:"top_p,omitempty"`
	Stream bool     `json:"stream,omitempty"` // requested SSE response mode
	// optional stream output settings
	StreamOptions *ChatStreamOptions `json:"stream_options,omitempty"`
	Tools         []ChatTool         `json:"tools,omitempty"` // available Chat tool declarations
	// ParallelToolCalls uses pointer presence to retain explicit false during
	// conversion and serialization. Nil leaves the upstream preference
	// unspecified here.
	// optional parallel call selection
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"` // raw tool selection value
	// reasoning effort label, such as low, medium, high, xhigh, or max
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// requested upstream service tier
	ServiceTier string          `json:"service_tier,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"` // raw stop string or list
	// raw Chat structured-output format
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`

	Functions    []ChatFunction  `json:"functions,omitempty"`     // legacy function tool declarations
	FunctionCall json.RawMessage `json:"function_call,omitempty"` // legacy function selection value
}

// ChatStreamOptions carries the request flag that asks for a final usage
// chunk.
type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"` // request final usage chunk
}

// ChatMessage represents a conversation message with modern or legacy tool
// calling fields.
type ChatMessage struct {
	Role    string          `json:"role"`              // conversation message speaker role
	Content json.RawMessage `json:"content,omitempty"` // raw text or multimodal content
	// ReasoningContent and Reasoning are alternate plaintext fields.
	// ReasoningText chooses nonempty ReasoningContent first, then Reasoning, so
	// an empty primary string permits the alternate value.
	// primary reasoning text alias
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Reasoning        string         `json:"reasoning,omitempty"`  // alternate reasoning text value
	Name             string         `json:"name,omitempty"`       // optional message function name
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"` // assistant tool call records
	// tool-result correlation call identity
	ToolCallID string `json:"tool_call_id,omitempty"`

	FunctionCall *ChatFunctionCall `json:"function_call,omitempty"` // optional legacy function call
}

// ChatContentPart represents a supported text, image, or file message part.
type ChatContentPart struct {
	Type     string        `json:"type"`                // multimodal content kind discriminator
	Text     string        `json:"text,omitempty"`      // message text part value
	ImageURL *ChatImageURL `json:"image_url,omitempty"` // optional image reference object
	File     *ChatFile     `json:"file,omitempty"`      // optional attached file object
}

// ChatImageURL carries an image reference and optional model detail
// preference.
type ChatImageURL struct {
	URL    string `json:"url"`              // image URL or data URI
	Detail string `json:"detail,omitempty"` // requested image detail label
}

// ChatFile carries supported uploaded-file identities or data-URI file
// attachments.
type ChatFile struct {
	Filename string `json:"filename,omitempty"`  // attached file display name
	FileData string `json:"file_data,omitempty"` // attached file data URI
	FileID   string `json:"file_id,omitempty"`   // upstream uploaded file identity
}

// ChatTool represents function, search, and code-execution declarations
// supported by request converters.
type ChatTool struct {
	Type     string        `json:"type"`               // Chat tool kind discriminator
	Function *ChatFunction `json:"function,omitempty"` // optional function declaration object

	// permitted X account handles
	// AllowedXHandles and ExcludedXHandles filter x_search accounts, while
	// FromDate and ToDate bound its dates. EnableImageUnderstanding and
	// EnableVideoUnderstanding select supported media interpretation on that tool.
	AllowedXHandles []string `json:"allowed_x_handles,omitempty"`
	// excluded X account handles
	ExcludedXHandles []string `json:"excluded_x_handles,omitempty"`
	FromDate         string   `json:"from_date,omitempty"` // search lower date bound
	ToDate           string   `json:"to_date,omitempty"`   // search upper date bound
	// optional image analysis selection
	// EnableImageUnderstanding and EnableVideoUnderstanding keep explicit false
	// when their pointers are supplied. Nil omits the corresponding media setting
	// from the tool, leaving its upstream default unspecified here.
	EnableImageUnderstanding *bool `json:"enable_image_understanding,omitempty"`
	// optional video analysis selection
	EnableVideoUnderstanding *bool `json:"enable_video_understanding,omitempty"`
}

// ChatFunction carries a named function's description, parameter schema, and
// strictness preference.
type ChatFunction struct {
	Name string `json:"name"` // declared function tool name
	// model-visible function usage description
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // raw function parameter schema
	// Strict uses pointer presence so an explicit false selection remains
	// serialized; nil omits the setting from the declaration.
	Strict *bool `json:"strict,omitempty"` // optional strict-schema selection
}

// ChatToolCall represents an assistant call and an optional streaming position
// index.
type ChatToolCall struct {
	// Index identifies a tool within streaming chunks. Nil omits the index, while
	// a pointer to zero retains the first tool position.
	Index    *int             `json:"index,omitempty"` // stream tool call position
	ID       string           `json:"id,omitempty"`    // tool result correlation identity
	Type     string           `json:"type,omitempty"`  // assistant tool call kind
	Function ChatFunctionCall `json:"function"`        // function identity and arguments
}

// ChatFunctionCall represents function identity and argument text with
// compatible raw-JSON decoding.
type ChatFunctionCall struct {
	// Name omits an empty string during encoding, allowing argument-only deltas
	// to leave a previously accumulated client function name intact.
	Name string `json:"name,omitempty"` // called function tool name
	// Arguments stores decoded strings or raw JSON text from another argument
	// value. Ordinary encoding emits a JSON string for either internal text form.
	Arguments string `json:"arguments"` // serialized function argument text
}

// ChatCompletionsResponse represents a buffered completion with choices and
// optional usage observations.
type ChatCompletionsResponse struct {
	ID      string       `json:"id"`              // upstream Chat completion identity
	Object  string       `json:"object"`          // Chat response object kind
	Created int64        `json:"created"`         // Unix creation time in seconds
	Model   string       `json:"model"`           // response model identity string
	Choices []ChatChoice `json:"choices"`         // reported completion choice records
	Usage   *ChatUsage   `json:"usage,omitempty"` // optional reported token usage
	// upstream system fingerprint observation
	SystemFingerprint string `json:"system_fingerprint,omitempty"`
	ServiceTier       string `json:"service_tier,omitempty"` // reported upstream service tier
}

// ChatChoice represents one completed message and its reported stopping
// reason.
type ChatChoice struct {
	Index        int         `json:"index"`         // completion choice position index
	Message      ChatMessage `json:"message"`       // completed assistant message value
	FinishReason string      `json:"finish_reason"` // reported completion stopping reason
}

// ChatUsage represents prompt/completion counts and their optional detailed
// usage observations.
type ChatUsage struct {
	PromptTokens int `json:"prompt_tokens"` // total prompt token count
	// total completion token count
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"` // reported aggregate token count
	// optional prompt usage breakdown
	PromptTokensDetails *ChatTokenDetails `json:"prompt_tokens_details,omitempty"`
	// optional completion usage breakdown
	CompletionTokensDetails *ChatTokenDetails `json:"completion_tokens_details,omitempty"`
}

// ChatTokenDetails carries the token categories shared by prompt and
// completion usage details. Callers populate the categories applicable to
// their side, and zero-valued fields are omitted during encoding.
type ChatTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"` // cache-read input token count
	AudioTokens  int `json:"audio_tokens,omitempty"`  // audio input or output count
	// cache-creation input token count
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheWriteTokens    int `json:"cache_write_tokens,omitempty"` // cache-write input token count
	ReasoningTokens     int `json:"reasoning_tokens,omitempty"`   // reasoning output token count
	// accepted prediction token count
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	// rejected prediction token count
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// ChatCompletionsChunk represents incremental choices and optional final usage
// for Chat streams.
type ChatCompletionsChunk struct {
	ID      string            `json:"id"`              // upstream Chat completion identity
	Object  string            `json:"object"`          // Chat stream object kind
	Created int64             `json:"created"`         // Unix creation time in seconds
	Model   string            `json:"model"`           // response model identity string
	Choices []ChatChunkChoice `json:"choices"`         // incremental completion choice records
	Usage   *ChatUsage        `json:"usage,omitempty"` // optional reported token usage
	// upstream system fingerprint observation
	SystemFingerprint string `json:"system_fingerprint,omitempty"`
	// reported upstream service tier
	ServiceTier string `json:"service_tier,omitempty"`
}

// ChatChunkChoice represents one incremental message delta and optional
// stopping reason.
type ChatChunkChoice struct {
	Index int       `json:"index"` // completion choice position index
	Delta ChatDelta `json:"delta"` // incremental assistant message fields
	// FinishReason serializes null when nil because its tag has no omitempty. A
	// nonnil pointer carries the supplied stopping reason.
	FinishReason *string `json:"finish_reason"` // optional completion stopping reason
}

// ChatDelta represents incremental content and tool fields while retaining
// text pointer presence.
type ChatDelta struct {
	Role string `json:"role,omitempty"` // stream message speaker role
	// Content, ReasoningContent, and Reasoning retain pointer presence, including
	// empty strings. ReasoningText prefers a present ReasoningContent before
	// Reasoning; nil Content omits text from the stream chunk.
	Content *string `json:"content,omitempty"` // optional incremental text value
	// primary reasoning fragment alias
	ReasoningContent *string        `json:"reasoning_content,omitempty"`
	Reasoning        *string        `json:"reasoning,omitempty"`  // alternate reasoning fragment value
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"` // incremental assistant tool calls
}

// TextProtocol identifies the Chat or Responses endpoint used for a text
// exchange.
type TextProtocol string

// RawOutput returns the retained nonstring tool-output JSON, or nil when no
// such value was decoded. The returned slice shares the item's storage;
// callers treat it as read-only and copy it before modifying bytes.
func (i *ResponsesInputItem) RawOutput() json.RawMessage {
	return i.outputRaw
}

// UnmarshalJSON decodes a Responses input item and retains nonstring output
// for multimodal conversion. It resets the receiver before decoding, so an
// error can leave a zero or partially populated item. Absent or null output
// leaves both output forms empty, while string output is stored as decoded
// text.
//
// For any other valid output value, the trimmed raw JSON is copied into
// outputRaw and also stored as Output text. RawOutput consumers can inspect
// its original JSON shape; ordinary struct marshaling later encodes Output as
// a string and does not reproduce outputRaw automatically.
func (i *ResponsesInputItem) UnmarshalJSON(data []byte) error {
	// alias exposes input fields while preventing recursive ResponsesInputItem
	// JSON decoding.
	type alias ResponsesInputItem
	var wire struct {
		// alias carries the ordinary fields, while Output intercepts its JSON value
		// before typed string decoding so nonstring tool results remain readable.
		*alias                 // ordinary input item fields
		Output json.RawMessage `json:"output"` // raw tool output value
	}

	// Reset retained output before decoding another item, so absent output cannot
	// accidentally reuse bytes from a previous tool result on this receiver.
	*i = ResponsesInputItem{}
	wire.alias = (*alias)(i)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	output := bytes.TrimSpace(wire.Output)
	if len(output) == 0 || bytes.Equal(output, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(output, &i.Output); err == nil {
		return nil
	}

	// String decoding failed for a valid JSON value. Copy its raw shape for
	// multimodal conversion and retain text for consumers using the public field.
	i.outputRaw = append(i.outputRaw[:0], output...)
	i.Output = string(output)
	return nil
}

// UnmarshalJSON accepts a JSON string as a custom-tool name and otherwise
// decodes the supported object fields. JSON null also succeeds in the string
// path and produces an empty-name custom tool. Object decoding publishes
// temporary state after success; errors retain the previous receiver.
func (t *ResponsesTool) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*t = ResponsesTool{Type: "custom", Name: name}
		return nil
	}
	// alias exposes tool fields while preventing recursive ResponsesTool JSON
	// decoding.
	type alias ResponsesTool
	// A failed string decode can still be a valid object declaration. Decode
	// that alternate shape into temporary fields before replacing the receiver.
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*t = ResponsesTool(a)
	return nil
}

// MarshalJSON encodes tool_search_call as a client-executed discovery call and
// otherwise uses the ordinary item fields. The specialized object carries
// type, ID, call ID, arguments, and optional status; other modeled fields are
// outside that shape. Argument text is interpreted by
// ToolSearchCallArgumentsJSON.
//
// That interpretation keeps valid trimmed JSON as a value, uses an object for
// blank text, and quotes invalid text. Function calls retain string arguments
// through ordinary encoding, so the two tool kinds remain distinguishable
// across the bridge's shared argument-string representation.
func (o ResponsesOutput) MarshalJSON() ([]byte, error) {
	// responsesOutputAlias exposes output fields while bypassing the custom item
	// JSON codec.
	type responsesOutputAlias ResponsesOutput
	// Only discovery calls convert argument text to a JSON value. Ordinary
	// function items keep string arguments and the default typed output fields.
	if o.Type != "tool_search_call" {
		return json.Marshal(responsesOutputAlias(o))
	}
	m := map[string]any{
		"type":      o.Type,
		"id":        o.ID,
		"call_id":   o.CallID,
		"execution": "client",
		"arguments": ToolSearchCallArgumentsJSON(o.Arguments),
	}
	if o.Status != "" {
		m["status"] = o.Status
	}
	return json.Marshal(m)
}

// UnmarshalJSON decodes an output item and accepts object-valued arguments for
// tool_search_call. Other item types use the ordinary string field decoding.
// Successful decoding replaces the receiver; failures before publication
// retain its prior value, and missing or null discovery arguments remain
// empty.
//
// The discovery path temporarily removes arguments before decoding the
// remaining typed fields. It stores a supplied JSON string as decoded text and
// stores any other argument value as raw JSON text. Numeric token spelling
// stays in that text until specialized marshaling; later encoding can compact
// whitespace and escape strings. A decoded string that itself spells valid
// JSON is subsequently emitted as that inner value, because Arguments retains
// text rather than the source token kind. Unknown output-item fields remain
// outside the typed representation.
func (o *ResponsesOutput) UnmarshalJSON(data []byte) error {
	// responsesOutputAlias exposes output fields while bypassing the custom item
	// JSON codec.
	type responsesOutputAlias ResponsesOutput

	var kind struct {
		Type string `json:"type"` // output item discriminator
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return err
	}
	if kind.Type != "tool_search_call" {
		var decoded responsesOutputAlias
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		*o = ResponsesOutput(decoded)
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// Decode the surrounding typed fields separately because discovery arguments
	// can be objects that would fail ordinary string-field decoding for functions.
	arguments, hasArguments := fields["arguments"]
	delete(fields, "arguments")
	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}

	var decoded responsesOutputAlias
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return err
	}
	*o = ResponsesOutput(decoded)
	if !hasArguments || string(arguments) == "null" {
		return nil
	}

	var argumentString string
	if err := json.Unmarshal(arguments, &argumentString); err == nil {
		o.Arguments = argumentString
	} else {
		// The parsed value is valid JSON of another type. Retain its bytes as
		// text so specialized discovery encoding can recover its value shape.
		o.Arguments = string(arguments)
	}
	return nil
}

// UnmarshalJSON decodes inclusive Responses usage and compatible Chat names.
// Canonical nonzero input/output values win; zero values permit their existing
// Chat aliases. Cache selectors share raw-accounting precedence, including
// authoritative nested zero values and supported top-level cache aliases.
//
// Decode into a temporary value before replacing the receiver so malformed
// typed fields cannot leave partially changed usage. Detail objects retain
// their non-cache categories, while overlapping cache-write aliases converge
// on one selected count. A zero total is derived after normalization, including
// prompt totals recovered from explicit DeepSeek hit/miss observations.
func (u *ResponsesUsage) UnmarshalJSON(data []byte) error {
	// responsesUsageAlias exposes wire fields without recursively invoking this
	// compatibility decoder while validating the supplied typed JSON values.
	type responsesUsageAlias ResponsesUsage
	var decoded struct {
		responsesUsageAlias                                   // canonical Responses usage fields
		PromptTokens            int                           `json:"prompt_tokens"`             // compatible inclusive prompt count
		CompletionTokens        int                           `json:"completion_tokens"`         // compatible generated token count
		PromptTokensDetails     *ResponsesInputTokensDetails  `json:"prompt_tokens_details"`     // compatible input detail object
		CompletionTokensDetails *ResponsesOutputTokensDetails `json:"completion_tokens_details"` // compatible output detail object
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value := ResponsesUsage(decoded.responsesUsageAlias)
	if counters, ok := OpenAIUsageFromGJSON(gjson.ParseBytes(data)); ok {
		// Keep typed client totals aligned with raw accounting, including independent
		// reasoning reported through the total gap rather than the output bucket.
		value.InputTokens = counters.InputTokens
		value.OutputTokens = counters.OutputTokens
	}
	if value.InputTokensDetails == nil {
		value.InputTokensDetails = decoded.PromptTokensDetails
	}
	if value.OutputTokensDetails == nil {
		value.OutputTokensDetails = decoded.CompletionTokensDetails
	}
	normalizeResponsesCacheUsage(gjson.ParseBytes(data), &value)
	if value.TotalTokens == 0 {
		value.TotalTokens = value.InputTokens + value.OutputTokens
	}
	*u = value
	return nil
}

// UnmarshalJSON decodes function identity and accepts either argument strings
// or raw JSON argument values. A successful object decode updates the
// receiver, while decoding failures leave its earlier values available. Absent
// or null arguments become empty text.
//
// A supplied string is unquoted into Arguments; other valid values keep their
// trimmed raw JSON spelling as text. Reencoding this struct uses the string
// field, so callers converting another protocol can read raw object text but
// cannot assume a general JSON-type round trip through ordinary Chat
// marshaling.
func (c *ChatFunctionCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string          `json:"name"`      // called function tool name
		Arguments json.RawMessage `json:"arguments"` // raw function argument value
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Name = raw.Name
	arguments := bytes.TrimSpace(raw.Arguments)
	if len(arguments) == 0 || string(arguments) == "null" {
		c.Arguments = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(arguments, &text); err == nil {
		c.Arguments = text
		return nil
	}
	// String decoding failed for another valid JSON type. Keep its raw text
	// so request converters can recover the object or scalar argument value.
	c.Arguments = string(arguments)
	return nil
}

// ReasoningText selects a message's nonempty reasoning_content before the
// reasoning alias. A blank primary value permits the alternate string, and
// both blank values produce empty text for the consuming converter.
func (m ChatMessage) ReasoningText() string {
	if m.ReasoningContent != "" {
		return m.ReasoningContent
	}
	return m.Reasoning
}

// ReasoningText selects a present reasoning_content pointer before the
// reasoning alias, including a pointer to empty text. It returns the supplied
// shared pointer, or nil when both fields are absent; mutation affects that
// pointed-to value.
func (d ChatDelta) ReasoningText() *string {
	if d.ReasoningContent != nil {
		return d.ReasoningContent
	}
	return d.Reasoning
}
