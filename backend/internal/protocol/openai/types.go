package openai

import (
	"bytes"
	"encoding/json"
)

// ResponsesRequest is the request body for POST /v1/responses.
type ResponsesRequest struct {
	Model              string              `json:"model"`
	Instructions       string              `json:"instructions,omitempty"`
	Input              json.RawMessage     `json:"input"` // string or []ResponsesInputItem
	MaxOutputTokens    *int                `json:"max_output_tokens,omitempty"`
	Temperature        *float64            `json:"temperature,omitempty"`
	TopP               *float64            `json:"top_p,omitempty"`
	Stream             bool                `json:"stream,omitempty"`
	Tools              []ResponsesTool     `json:"tools,omitempty"`
	Include            []string            `json:"include,omitempty"`
	Store              *bool               `json:"store,omitempty"`
	ParallelToolCalls  *bool               `json:"parallel_tool_calls,omitempty"`
	Reasoning          *ResponsesReasoning `json:"reasoning,omitempty"`
	Text               *ResponsesText      `json:"text,omitempty"`
	ToolChoice         json.RawMessage     `json:"tool_choice,omitempty"`
	ServiceTier        string              `json:"service_tier,omitempty"`
	PromptCacheKey     string              `json:"prompt_cache_key,omitempty"`
	PreviousResponseID string              `json:"previous_response_id,omitempty"`
}

// ResponsesReasoning configures reasoning effort in the Responses API.
type ResponsesReasoning struct {
	Effort  string `json:"effort"`            // "low" | "medium" | "high" | "xhigh" | "max"
	Summary string `json:"summary,omitempty"` // "auto" | "concise" | "detailed"
}

// ResponsesText configures text output options in the Responses API.
type ResponsesText struct {
	Format    json.RawMessage `json:"format,omitempty"`    // Responses API 的结构化输出格式。
	Verbosity string          `json:"verbosity,omitempty"` // "low" | "medium" | "high"
}

// ResponsesInputItem is one item in the Responses API input array.
// The Type field determines which other fields are populated.
type ResponsesInputItem struct {
	// Common
	Type string `json:"type,omitempty"` // "" for role-based messages

	// Role-based messages (developer/system/user/assistant)
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"` // string or []ResponsesContentPart

	// type=reasoning，用于多轮回放加密推理。
	EncryptedContent string `json:"encrypted_content,omitempty"`

	// type=function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	ID        string `json:"id,omitempty"`

	// type=function_call_output
	Output    string `json:"output,omitempty"`
	outputRaw json.RawMessage
}

// RawOutput 返回反序列化时保留的工具结果原文，转换器只读此值。
func (i *ResponsesInputItem) RawOutput() json.RawMessage {
	return i.outputRaw
}

// UnmarshalJSON 保留数组或对象形式的工具输出，供协议转换时还原多模态内容。
func (i *ResponsesInputItem) UnmarshalJSON(data []byte) error {
	type alias ResponsesInputItem
	var wire struct {
		*alias
		Output json.RawMessage `json:"output"`
	}

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

	i.outputRaw = append(i.outputRaw[:0], output...)
	i.Output = string(output)
	return nil
}

// ResponsesContentPart is a typed content part in a Responses message.
type ResponsesContentPart struct {
	Type     string `json:"type"` // input_text、output_text、input_image 或 input_file
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"` // data URI for input_image

	// input_file 字段。
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"` // data URI 格式的文件内容
	FileID   string `json:"file_id,omitempty"`
}

// ResponsesTool describes a tool in the Responses API.
type ResponsesTool struct {
	Type        string          `json:"type"` // "function" | "custom" | "web_search" | "x_search" | "local_shell" etc.
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`

	// type=namespace 的子工具列表（tools 与 children 二选一，语义相同）。
	Tools    []ResponsesTool `json:"tools,omitempty"`
	Children []ResponsesTool `json:"children,omitempty"`

	// type=x_search
	AllowedXHandles          []string `json:"allowed_x_handles,omitempty"`
	ExcludedXHandles         []string `json:"excluded_x_handles,omitempty"`
	FromDate                 string   `json:"from_date,omitempty"`
	ToDate                   string   `json:"to_date,omitempty"`
	EnableImageUnderstanding *bool    `json:"enable_image_understanding,omitempty"`
	EnableVideoUnderstanding *bool    `json:"enable_video_understanding,omitempty"`
}

// UnmarshalJSON 容忍字符串形式的工具声明：codex 会以 "name" 简写声明 custom 工具，
func (t *ResponsesTool) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*t = ResponsesTool{Type: "custom", Name: name}
		return nil
	}
	type alias ResponsesTool
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*t = ResponsesTool(a)
	return nil
}

// ResponsesResponse is the non-streaming response from POST /v1/responses.
type ResponsesResponse struct {
	ID     string `json:"id"`
	Object string `json:"object"` // "response"
	// CreatedAt 是 Unix 创建时间戳。严格 Responses 客户端将其视为必填字段，
	// 缺失会以 missing field 'created_at' 终止反序列化，因此始终输出且不使用 omitempty。
	CreatedAt   int64             `json:"created_at"`
	Model       string            `json:"model"`
	Status      string            `json:"status"` // "completed" | "incomplete" | "failed"
	Output      []ResponsesOutput `json:"output"`
	Usage       *ResponsesUsage   `json:"usage,omitempty"`
	ServiceTier string            `json:"service_tier,omitempty"` // 原样回传上游 tier

	// incomplete_details is present when status="incomplete"
	IncompleteDetails *ResponsesIncompleteDetails `json:"incomplete_details,omitempty"`

	// Error is present when status="failed"
	Error *ResponsesError `json:"error,omitempty"`
}

// ResponsesError describes an error in a failed response.
type ResponsesError struct {
	Code       string `json:"code"`
	Type       string `json:"type,omitempty"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code,omitempty"` // 保留聚合上游附带的语义状态码，供账号错误策略判断。
}

// ResponsesIncompleteDetails explains why a response is incomplete.
type ResponsesIncompleteDetails struct {
	Reason string `json:"reason"` // "max_output_tokens" | "content_filter"
}

// ResponsesOutput is one output item in a Responses API response.
type ResponsesOutput struct {
	Type string `json:"type"` // "message" | "reasoning" | "function_call" | "web_search_call"

	// type=message
	ID      string                 `json:"id,omitempty"`
	Role    string                 `json:"role,omitempty"`
	Content []ResponsesContentPart `json:"content,omitempty"`
	Status  string                 `json:"status,omitempty"`

	// type=reasoning
	EncryptedContent string             `json:"encrypted_content,omitempty"`
	Summary          []ResponsesSummary `json:"summary,omitempty"`

	// type=function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// 来源为 namespace 子工具时的归属命名空间（codex 按 namespace+name 路由该调用）。
	Namespace string `json:"namespace,omitempty"`

	// type=custom_tool_call（custom/freeform 工具，input 为自由文本）
	Input string `json:"input,omitempty"`

	// type=web_search_call
	Action *WebSearchAction `json:"action,omitempty"`
}

// MarshalJSON 处理 tool_search_call 项的线上形态（复用 CallID/Arguments 字段）：
// execution 固定为 "client"（codex 的必填字段，非 client 的调用会被静默忽略），
// arguments 是 JSON 对象而非 function_call 语义下的字符串。其余类型走默认结构体
// 序列化，输出逐字节不变。
func (o ResponsesOutput) MarshalJSON() ([]byte, error) {
	type responsesOutputAlias ResponsesOutput
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

// UnmarshalJSON 同时接受普通 function call 的字符串参数和 tool_search_call 的
// 对象参数。桥接层内部统一存储字符串，因此对象参数会保留为原始 JSON 文本。
func (o *ResponsesOutput) UnmarshalJSON(data []byte) error {
	type responsesOutputAlias ResponsesOutput

	var kind struct {
		Type string `json:"type"`
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
		o.Arguments = string(arguments)
	}
	return nil
}

// WebSearchAction describes the search action in a web_search_call output item.
type WebSearchAction struct {
	Type  string `json:"type,omitempty"`  // "search"
	Query string `json:"query,omitempty"` // primary search query
}

// ResponsesSummary is a summary text block inside a reasoning output.
type ResponsesSummary struct {
	Type string `json:"type"` // "summary_text"
	Text string `json:"text"`
}

// ResponsesUsage holds token counts in Responses API format.
type ResponsesUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	TotalTokens              int `json:"total_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`

	// Optional detailed breakdown
	InputTokensDetails  *ResponsesInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *ResponsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// UnmarshalJSON 兼容 OpenAI Responses 与 Chat Completions 两种 usage 字段命名。
func (u *ResponsesUsage) UnmarshalJSON(data []byte) error {
	type responsesUsageAlias ResponsesUsage
	type cacheTokenPresence struct {
		CacheCreationTokens *int `json:"cache_creation_tokens"`
		CacheWriteTokens    *int `json:"cache_write_tokens"`
	}
	var aux struct {
		responsesUsageAlias
		PromptTokens            int                           `json:"prompt_tokens"`
		CompletionTokens        int                           `json:"completion_tokens"`
		CacheCreationTokens     int                           `json:"cache_creation_tokens"`
		CacheWriteInputTokens   int                           `json:"cache_write_input_tokens"`
		CacheWriteTokens        int                           `json:"cache_write_tokens"`
		PromptTokensDetails     *ResponsesInputTokensDetails  `json:"prompt_tokens_details,omitempty"`
		CompletionTokensDetails *ResponsesOutputTokensDetails `json:"completion_tokens_details,omitempty"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var nestedPresence struct {
		InputTokensDetails  *cacheTokenPresence `json:"input_tokens_details"`
		PromptTokensDetails *cacheTokenPresence `json:"prompt_tokens_details"`
	}
	if err := json.Unmarshal(data, &nestedPresence); err != nil {
		return err
	}
	*u = ResponsesUsage(aux.responsesUsageAlias)
	if u.InputTokens == 0 && aux.PromptTokens != 0 {
		u.InputTokens = aux.PromptTokens
	}
	if u.OutputTokens == 0 && aux.CompletionTokens != 0 {
		u.OutputTokens = aux.CompletionTokens
	}
	if u.CacheCreationInputTokens == 0 {
		switch {
		case aux.CacheWriteInputTokens > 0:
			u.CacheCreationInputTokens = aux.CacheWriteInputTokens
		case aux.CacheCreationTokens > 0:
			u.CacheCreationInputTokens = aux.CacheCreationTokens
		case aux.CacheWriteTokens > 0:
			u.CacheCreationInputTokens = aux.CacheWriteTokens
		}
	}
	if u.InputTokensDetails == nil && aux.PromptTokensDetails != nil {
		u.InputTokensDetails = aux.PromptTokensDetails
	}
	if u.OutputTokensDetails == nil && aux.CompletionTokensDetails != nil {
		u.OutputTokensDetails = aux.CompletionTokensDetails
	}
	var canonicalCacheCreationTokens *int
	switch {
	case nestedPresence.InputTokensDetails != nil && nestedPresence.InputTokensDetails.CacheWriteTokens != nil:
		canonicalCacheCreationTokens = nestedPresence.InputTokensDetails.CacheWriteTokens
	case nestedPresence.PromptTokensDetails != nil && nestedPresence.PromptTokensDetails.CacheWriteTokens != nil:
		canonicalCacheCreationTokens = nestedPresence.PromptTokensDetails.CacheWriteTokens
	case nestedPresence.InputTokensDetails != nil && nestedPresence.InputTokensDetails.CacheCreationTokens != nil:
		canonicalCacheCreationTokens = nestedPresence.InputTokensDetails.CacheCreationTokens
	case nestedPresence.PromptTokensDetails != nil && nestedPresence.PromptTokensDetails.CacheCreationTokens != nil:
		canonicalCacheCreationTokens = nestedPresence.PromptTokensDetails.CacheCreationTokens
	}
	if canonicalCacheCreationTokens != nil {
		u.CacheCreationInputTokens = max(*canonicalCacheCreationTokens, 0)
	}
	if u.TotalTokens == 0 && (u.InputTokens != 0 || u.OutputTokens != 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return nil
}

// ResponsesInputTokensDetails breaks down input token usage.
type ResponsesInputTokensDetails struct {
	CachedTokens        int `json:"cached_tokens,omitempty"`
	AudioTokens         int `json:"audio_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheWriteTokens    int `json:"cache_write_tokens,omitempty"`
}

// ResponsesOutputTokensDetails breaks down output token usage.
type ResponsesOutputTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	AudioTokens              int `json:"audio_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// ResponsesStreamEvent is a single SSE event in the Responses streaming protocol.
// The Type field corresponds to the "type" in the JSON payload.
type ResponsesStreamEvent struct {
	Type string `json:"type"`

	// response.created / response.completed / response.done / response.failed / response.incomplete
	Response *ResponsesResponse `json:"response,omitempty"`
	// 部分 OpenAI 兼容上游会把 usage 放在终止事件顶层，而不是 response.usage。
	Usage *ResponsesUsage `json:"usage,omitempty"`

	// response.output_item.added / response.output_item.done
	Item *ResponsesOutput `json:"item,omitempty"`

	// response.output_text.delta / response.output_text.done
	OutputIndex  int    `json:"output_index,omitempty"`
	ContentIndex int    `json:"content_index,omitempty"`
	Delta        string `json:"delta,omitempty"`
	Text         string `json:"text,omitempty"`
	ItemID       string `json:"item_id,omitempty"`

	// response.function_call_arguments.delta / done
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// response.custom_tool_call_input.done
	Input string `json:"input,omitempty"`

	// response.reasoning_summary_text.delta / done
	// 复用上面的 Text/Delta 字段，SummaryIndex 标识 summary part。
	SummaryIndex int `json:"summary_index,omitempty"`

	// response.content_part.added / done 以及
	// response.reasoning_summary_part.added / done。
	Part *ResponsesContentPart `json:"part,omitempty"`

	// error event fields
	Code  string `json:"code,omitempty"`
	Param string `json:"param,omitempty"`

	// Sequence number for ordering events
	SequenceNumber int `json:"sequence_number,omitempty"`
}

// ChatCompletionsRequest is the request body for POST /v1/chat/completions.
type ChatCompletionsRequest struct {
	Model               string             `json:"model"`
	Messages            []ChatMessage      `json:"messages"`
	Instructions        string             `json:"instructions,omitempty"` // OpenAI Responses API compat
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	Stream              bool               `json:"stream,omitempty"`
	StreamOptions       *ChatStreamOptions `json:"stream_options,omitempty"`
	Tools               []ChatTool         `json:"tools,omitempty"`
	ParallelToolCalls   *bool              `json:"parallel_tool_calls,omitempty"`
	ToolChoice          json.RawMessage    `json:"tool_choice,omitempty"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"` // "low" | "medium" | "high" | "xhigh" | "max"
	ServiceTier         string             `json:"service_tier,omitempty"`
	Stop                json.RawMessage    `json:"stop,omitempty"`            // string or []string
	ResponseFormat      json.RawMessage    `json:"response_format,omitempty"` // Chat Completions 的结构化输出格式。

	// Legacy function calling (deprecated but still supported)
	Functions    []ChatFunction  `json:"functions,omitempty"`
	FunctionCall json.RawMessage `json:"function_call,omitempty"`
}

// ChatStreamOptions configures streaming behavior.
type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatMessage is a single message in the Chat Completions conversation.
type ChatMessage struct {
	Role             string          `json:"role"` // "system" | "user" | "assistant" | "tool" | "function"
	Content          json.RawMessage `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	Name             string          `json:"name,omitempty"`
	ToolCalls        []ChatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`

	// Legacy function calling
	FunctionCall *ChatFunctionCall `json:"function_call,omitempty"`
}

// ChatContentPart is a typed content part in a multi-modal message.
type ChatContentPart struct {
	Type     string        `json:"type"` // text、image_url 或 file
	Text     string        `json:"text,omitempty"`
	ImageURL *ChatImageURL `json:"image_url,omitempty"`
	File     *ChatFile     `json:"file,omitempty"`
}

// ChatImageURL contains the URL for an image content part.
type ChatImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto" | "low" | "high"
}

// ChatFile 保存 "file" 内容 part 的载荷（例如 PDF 输入）。
type ChatFile struct {
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"` // data URI 格式的文件内容
	FileID   string `json:"file_id,omitempty"`
}

// ChatTool describes a tool available to the model.
type ChatTool struct {
	Type     string        `json:"type"` // function、web_search、code_execution 或 x_search
	Function *ChatFunction `json:"function,omitempty"`

	// type=x_search
	AllowedXHandles          []string `json:"allowed_x_handles,omitempty"`
	ExcludedXHandles         []string `json:"excluded_x_handles,omitempty"`
	FromDate                 string   `json:"from_date,omitempty"`
	ToDate                   string   `json:"to_date,omitempty"`
	EnableImageUnderstanding *bool    `json:"enable_image_understanding,omitempty"`
	EnableVideoUnderstanding *bool    `json:"enable_video_understanding,omitempty"`
}

// ChatFunction describes a function tool definition.
type ChatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ChatToolCall represents a tool call made by the assistant.
// Index is only populated in streaming chunks (omitted in non-streaming responses).
type ChatToolCall struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"` // "function"
	Function ChatFunctionCall `json:"function"`
}

// ChatFunctionCall contains the function name and arguments.
type ChatFunctionCall struct {
	// Empty name is omitted so streamed arguments-only deltas never overwrite
	// the tool name a client accumulated from the first delta.
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// UnmarshalJSON 同时兼容官方字符串参数，以及部分 OpenAI 兼容历史回放里的对象参数。
func (c *ChatFunctionCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
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
	c.Arguments = string(arguments)
	return nil
}

// ChatCompletionsResponse is the non-streaming response from POST /v1/chat/completions.
type ChatCompletionsResponse struct {
	ID                string       `json:"id"`
	Object            string       `json:"object"` // "chat.completion"
	Created           int64        `json:"created"`
	Model             string       `json:"model"`
	Choices           []ChatChoice `json:"choices"`
	Usage             *ChatUsage   `json:"usage,omitempty"`
	SystemFingerprint string       `json:"system_fingerprint,omitempty"`
	ServiceTier       string       `json:"service_tier,omitempty"`
}

// ChatChoice is a single completion choice.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"` // "stop" | "length" | "tool_calls" | "content_filter"
}

// ChatUsage holds token counts in Chat Completions format.
type ChatUsage struct {
	PromptTokens            int               `json:"prompt_tokens"`
	CompletionTokens        int               `json:"completion_tokens"`
	TotalTokens             int               `json:"total_tokens"`
	PromptTokensDetails     *ChatTokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *ChatTokenDetails `json:"completion_tokens_details,omitempty"`
}

// ChatTokenDetails 提供 token 使用明细。prompt_tokens_details 与
// completion_tokens_details 复用同一结构；未设置字段会被省略，因此两侧只输出各自适用的字段。
//
// 字段集合对齐 OpenAI 官方 CompletionUsage schema：
//   - prompt_tokens_details: cached_tokens, audio_tokens
//   - completion_tokens_details: reasoning_tokens, audio_tokens,
//     accepted_prediction_tokens, rejected_prediction_tokens
type ChatTokenDetails struct {
	CachedTokens             int `json:"cached_tokens,omitempty"`
	AudioTokens              int `json:"audio_tokens,omitempty"`
	CacheCreationTokens      int `json:"cache_creation_tokens,omitempty"`
	CacheWriteTokens         int `json:"cache_write_tokens,omitempty"`
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// ChatCompletionsChunk is a single streaming chunk from POST /v1/chat/completions.
type ChatCompletionsChunk struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"` // "chat.completion.chunk"
	Created           int64             `json:"created"`
	Model             string            `json:"model"`
	Choices           []ChatChunkChoice `json:"choices"`
	Usage             *ChatUsage        `json:"usage,omitempty"`
	SystemFingerprint string            `json:"system_fingerprint,omitempty"`
	ServiceTier       string            `json:"service_tier,omitempty"`
}

// ChatChunkChoice is a single choice in a streaming chunk.
type ChatChunkChoice struct {
	Index        int       `json:"index"`
	Delta        ChatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"` // pointer: null when not final
}

// ChatDelta carries incremental content in a streaming chunk.
type ChatDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          *string        `json:"content,omitempty"` // pointer: omit when not present, null vs "" matters
	ReasoningContent *string        `json:"reasoning_content,omitempty"`
	Reasoning        *string        `json:"reasoning,omitempty"`
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"`
}

// ReasoningText 返回消息中的推理文本；正式字段有内容时优先于兼容别名。
func (m ChatMessage) ReasoningText() string {
	if m.ReasoningContent != "" {
		return m.ReasoningContent
	}
	return m.Reasoning
}

// ReasoningText 返回增量中的推理文本；正式字段即使显式为空也优先于兼容别名。
func (d ChatDelta) ReasoningText() *string {
	if d.ReasoningContent != nil {
		return d.ReasoningContent
	}
	return d.Reasoning
}
