package apicompat

import (
	"crypto/rand"
	"encoding/json"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// 本文件保留旧协议入口；类型、算法和流状态由新协议包唯一拥有，S11/S16 清理。
type AnthropicEventToResponsesState = bridge.AnthropicEventToResponsesState
type ChatCompletionsToAnthropicStreamState = bridge.ChatCompletionsToAnthropicStreamState
type ResponsesToChatOptions = bridge.ResponsesToChatOptions
type NamespacedToolName = bridge.NamespacedToolName
type ChatCompletionsToResponsesStreamState = bridge.ChatCompletionsToResponsesStreamState
type ResponsesClientToolMapping = bridge.ResponsesClientToolMapping
type ResponsesClientToolStreamRestorer = bridge.ResponsesClientToolStreamRestorer
type ResponsesNamespaceName = bridge.ResponsesNamespaceName
type ResponsesEventToAnthropicState = bridge.ResponsesEventToAnthropicState
type ResponsesEventToChatState = bridge.ResponsesEventToChatState
type BufferedResponseAccumulator = bridge.BufferedResponseAccumulator
type AnthropicRequest = anthropic.AnthropicRequest
type AnthropicOutputConfig = anthropic.AnthropicOutputConfig
type AnthropicThinking = anthropic.AnthropicThinking
type AnthropicMessage = anthropic.AnthropicMessage
type AnthropicContentBlock = anthropic.AnthropicContentBlock
type AnthropicImageSource = anthropic.AnthropicImageSource
type AnthropicTool = anthropic.AnthropicTool
type AnthropicCacheControl = anthropic.AnthropicCacheControl
type AnthropicResponse = anthropic.AnthropicResponse
type AnthropicPromptTokensDetails = anthropic.AnthropicPromptTokensDetails
type AnthropicUsage = anthropic.AnthropicUsage
type AnthropicStreamEvent = anthropic.AnthropicStreamEvent
type AnthropicDelta = anthropic.AnthropicDelta
type ResponsesRequest = openai.ResponsesRequest
type ResponsesReasoning = openai.ResponsesReasoning
type ResponsesText = openai.ResponsesText
type ResponsesInputItem = openai.ResponsesInputItem
type ResponsesContentPart = openai.ResponsesContentPart
type ResponsesTool = openai.ResponsesTool
type ResponsesResponse = openai.ResponsesResponse
type ResponsesError = openai.ResponsesError
type ResponsesIncompleteDetails = openai.ResponsesIncompleteDetails
type ResponsesOutput = openai.ResponsesOutput
type WebSearchAction = openai.WebSearchAction
type ResponsesSummary = openai.ResponsesSummary
type ResponsesUsage = openai.ResponsesUsage
type ResponsesInputTokensDetails = openai.ResponsesInputTokensDetails
type ResponsesOutputTokensDetails = openai.ResponsesOutputTokensDetails
type ResponsesStreamEvent = openai.ResponsesStreamEvent
type ChatCompletionsRequest = openai.ChatCompletionsRequest
type ChatStreamOptions = openai.ChatStreamOptions
type ChatMessage = openai.ChatMessage
type ChatContentPart = openai.ChatContentPart
type ChatImageURL = openai.ChatImageURL
type ChatFile = openai.ChatFile
type ChatTool = openai.ChatTool
type ChatFunction = openai.ChatFunction
type ChatToolCall = openai.ChatToolCall
type ChatFunctionCall = openai.ChatFunctionCall
type ChatCompletionsResponse = openai.ChatCompletionsResponse
type ChatChoice = openai.ChatChoice
type ChatUsage = openai.ChatUsage
type ChatTokenDetails = openai.ChatTokenDetails
type ChatCompletionsChunk = openai.ChatCompletionsChunk
type ChatChunkChoice = openai.ChatChunkChoice
type ChatDelta = openai.ChatDelta

// AnthropicToResponses 委托唯一协议实现。
func AnthropicToResponses(req *AnthropicRequest) (*ResponsesRequest, error) {
	return bridge.AnthropicToResponses(req, requestOptions(req.Model))
}

// AnthropicToResponsesResponse 委托唯一协议实现。
func AnthropicToResponsesResponse(resp *AnthropicResponse) *ResponsesResponse {
	return bridge.AnthropicToResponsesResponse(conversionRuntime(), resp)
}

// NewAnthropicEventToResponsesState 委托唯一协议实现。
func NewAnthropicEventToResponsesState() *AnthropicEventToResponsesState {
	return bridge.NewAnthropicEventToResponsesState(conversionRuntime())
}

// AnthropicEventToResponsesEvents 委托唯一协议实现。
func AnthropicEventToResponsesEvents(
	evt *AnthropicStreamEvent,
	state *AnthropicEventToResponsesState,
) []ResponsesStreamEvent {
	return bridge.AnthropicEventToResponsesEvents(conversionRuntime(), evt, state)
}

// FinalizeAnthropicResponsesStream 委托唯一协议实现。
func FinalizeAnthropicResponsesStream(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	return bridge.FinalizeAnthropicResponsesStream(state)
}

// ResponsesEventToSSE 委托唯一协议实现。
func ResponsesEventToSSE(evt ResponsesStreamEvent) (string, error) {
	return bridge.ResponsesEventToSSE(evt)
}

// AnthropicToChatCompletionsRequest 委托唯一协议实现。
func AnthropicToChatCompletionsRequest(req *AnthropicRequest) (*ChatCompletionsRequest, error) {
	return bridge.AnthropicToChatCompletionsRequest(req, requestOptions(req.Model))
}

// ChatCompletionsResponseToAnthropic 委托唯一协议实现。
func ChatCompletionsResponseToAnthropic(resp *ChatCompletionsResponse, model string) *AnthropicResponse {
	return bridge.ChatCompletionsResponseToAnthropic(conversionRuntime(), resp, model)
}

// NewChatCompletionsToAnthropicStreamState 委托唯一协议实现。
func NewChatCompletionsToAnthropicStreamState(model string) *ChatCompletionsToAnthropicStreamState {
	return bridge.NewChatCompletionsToAnthropicStreamState(conversionRuntime(), model)
}

// ChatCompletionsChunkToAnthropicEvents 委托唯一协议实现。
func ChatCompletionsChunkToAnthropicEvents(
	chunk *ChatCompletionsChunk,
	state *ChatCompletionsToAnthropicStreamState,
) []AnthropicStreamEvent {
	return bridge.ChatCompletionsChunkToAnthropicEvents(conversionRuntime(), chunk, state)
}

// FinalizeChatCompletionsAnthropicStream 委托唯一协议实现。
func FinalizeChatCompletionsAnthropicStream(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	return bridge.FinalizeChatCompletionsAnthropicStream(conversionRuntime(), state)
}

// ResponsesToChatCompletionsRequest 委托唯一协议实现。
func ResponsesToChatCompletionsRequest(req *ResponsesRequest) (*ChatCompletionsRequest, error) {
	return bridge.ResponsesToChatCompletionsRequest(req)
}

// ResponsesToChatCompletionsRequestWithOptions 委托唯一协议实现。
func ResponsesToChatCompletionsRequestWithOptions(req *ResponsesRequest, opts *ResponsesToChatOptions) (*ChatCompletionsRequest, error) {
	return bridge.ResponsesToChatCompletionsRequestWithOptions(req, opts)
}

// EffectiveResponsesTools 委托唯一协议实现。
func EffectiveResponsesTools(req *ResponsesRequest) ([]ResponsesTool, error) {
	return bridge.EffectiveResponsesTools(req)
}

// CustomToolNames 委托唯一协议实现。
func CustomToolNames(tools []ResponsesTool) map[string]bool {
	return bridge.CustomToolNames(tools)
}

// FunctionToolNames 委托唯一协议实现。
func FunctionToolNames(tools []ResponsesTool) map[string]bool {
	return bridge.FunctionToolNames(tools)
}

// NamespaceToolNames 委托唯一协议实现。
func NamespaceToolNames(tools []ResponsesTool) map[string]NamespacedToolName {
	return bridge.NamespaceToolNames(tools)
}

// HasToolSearchTool 委托唯一协议实现。
func HasToolSearchTool(tools []ResponsesTool) bool {
	return bridge.HasToolSearchTool(tools)
}

// ExtractResponsesReasoningItem 委托唯一协议实现。
func ExtractResponsesReasoningItem(raw json.RawMessage) (id string, text string, ok bool) {
	return bridge.ExtractResponsesReasoningItem(raw)
}

// ChatCompletionsResponseToResponses 委托唯一协议实现。
func ChatCompletionsResponseToResponses(resp *ChatCompletionsResponse, model string, customTools, functionTools map[string]bool, toolSearch bool, namespaceTools map[string]NamespacedToolName) *ResponsesResponse {
	return bridge.ChatCompletionsResponseToResponses(conversionRuntime(), resp, model, customTools, functionTools, toolSearch, namespaceTools)
}

// ChatUsageToResponsesUsage 委托唯一协议实现。
func ChatUsageToResponsesUsage(usage *ChatUsage) *ResponsesUsage {
	return bridge.ChatUsageToResponsesUsage(usage)
}

// NewChatCompletionsToResponsesStreamState 委托唯一协议实现。
func NewChatCompletionsToResponsesStreamState(model string) *ChatCompletionsToResponsesStreamState {
	return bridge.NewChatCompletionsToResponsesStreamState(conversionRuntime(), model)
}

// ChatCompletionsChunkToResponsesEvents 委托唯一协议实现。
func ChatCompletionsChunkToResponsesEvents(
	chunk *ChatCompletionsChunk,
	state *ChatCompletionsToResponsesStreamState,
) []ResponsesStreamEvent {
	return bridge.ChatCompletionsChunkToResponsesEvents(conversionRuntime(), chunk, state)
}

// FinalizeChatCompletionsResponsesStream 委托唯一协议实现。
func FinalizeChatCompletionsResponsesStream(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	return bridge.FinalizeChatCompletionsResponsesStream(conversionRuntime(), state)
}

// ChatCompletionsToResponses 委托唯一协议实现。
func ChatCompletionsToResponses(req *ChatCompletionsRequest) (*ResponsesRequest, error) {
	return bridge.ChatCompletionsToResponses(req, requestOptions(req.Model))
}

// AdaptResponsesClientTools 委托唯一协议实现。
func AdaptResponsesClientTools(req map[string]any) (ResponsesClientToolMapping, bool, error) {
	return bridge.AdaptResponsesClientTools(req)
}

// AdaptResponsesClientToolsWithInheritedMapping 委托唯一协议实现。
func AdaptResponsesClientToolsWithInheritedMapping(
	req map[string]any,
	inherited ResponsesClientToolMapping,
	inheritedLoweredTools ...[]any,
) (ResponsesClientToolMapping, bool, error) {
	return bridge.AdaptResponsesClientToolsWithInheritedMapping(req, inherited, inheritedLoweredTools...)
}

// RestoreResponsesClientToolPayload 委托唯一协议实现。
func RestoreResponsesClientToolPayload(payload []byte, mapping ResponsesClientToolMapping) ([]byte, bool, error) {
	return bridge.RestoreResponsesClientToolPayload(payload, mapping)
}

// NewResponsesClientToolStreamRestorer 委托唯一协议实现。
func NewResponsesClientToolStreamRestorer(mapping ResponsesClientToolMapping) *ResponsesClientToolStreamRestorer {
	return bridge.NewResponsesClientToolStreamRestorer(mapping)
}

// FlattenResponsesNamespaces 委托唯一协议实现。
func FlattenResponsesNamespaces(req map[string]any) (map[string]ResponsesNamespaceName, bool, error) {
	return bridge.FlattenResponsesNamespaces(req)
}

// FlattenResponsesNamespacesExcept 委托唯一协议实现。
func FlattenResponsesNamespacesExcept(req map[string]any, preserved map[string]bool) (map[string]ResponsesNamespaceName, bool, error) {
	return bridge.FlattenResponsesNamespacesExcept(req, preserved)
}

// RestoreResponsesNamespaceCalls 委托唯一协议实现。
func RestoreResponsesNamespaceCalls(payload []byte, names map[string]ResponsesNamespaceName) ([]byte, bool, error) {
	return bridge.RestoreResponsesNamespaceCalls(payload, names)
}

// ResponsesToAnthropic 委托唯一协议实现。
func ResponsesToAnthropic(resp *ResponsesResponse, model string) *AnthropicResponse {
	return bridge.ResponsesToAnthropic(resp, model)
}

// NewResponsesEventToAnthropicState 委托唯一协议实现。
func NewResponsesEventToAnthropicState() *ResponsesEventToAnthropicState {
	return bridge.NewResponsesEventToAnthropicState(conversionRuntime())
}

// ResponsesEventToAnthropicEvents 委托唯一协议实现。
func ResponsesEventToAnthropicEvents(
	evt *ResponsesStreamEvent,
	state *ResponsesEventToAnthropicState,
) []AnthropicStreamEvent {
	return bridge.ResponsesEventToAnthropicEvents(evt, state)
}

// FinalizeResponsesAnthropicStream 委托唯一协议实现。
func FinalizeResponsesAnthropicStream(state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	return bridge.FinalizeResponsesAnthropicStream(state)
}

// ResponsesAnthropicEventToSSE 委托唯一协议实现。
func ResponsesAnthropicEventToSSE(evt AnthropicStreamEvent) (string, error) {
	return bridge.ResponsesAnthropicEventToSSE(evt)
}

// ResponsesToAnthropicRequest 委托唯一协议实现。
func ResponsesToAnthropicRequest(req *ResponsesRequest) (*AnthropicRequest, error) {
	return bridge.ResponsesToAnthropicRequest(req)
}

// ResponsesToChatCompletions 委托唯一协议实现。
func ResponsesToChatCompletions(resp *ResponsesResponse, model string) *ChatCompletionsResponse {
	return bridge.ResponsesToChatCompletions(conversionRuntime(), resp, model)
}

// NewResponsesEventToChatState 委托唯一协议实现。
func NewResponsesEventToChatState() *ResponsesEventToChatState {
	return bridge.NewResponsesEventToChatState(conversionRuntime())
}

// ResponsesEventToChatChunks 委托唯一协议实现。
func ResponsesEventToChatChunks(evt *ResponsesStreamEvent, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	return bridge.ResponsesEventToChatChunks(evt, state)
}

// FinalizeResponsesChatStream 委托唯一协议实现。
func FinalizeResponsesChatStream(state *ResponsesEventToChatState) []ChatCompletionsChunk {
	return bridge.FinalizeResponsesChatStream(state)
}

// ChatChunkToSSE 委托唯一协议实现。
func ChatChunkToSSE(chunk ChatCompletionsChunk) (string, error) {
	return bridge.ChatChunkToSSE(chunk)
}

// NewBufferedResponseAccumulator 委托唯一协议实现。
func NewBufferedResponseAccumulator() *BufferedResponseAccumulator {
	return bridge.NewBufferedResponseAccumulator()
}

// AnthropicStopReasonPtr 委托唯一协议实现。
func AnthropicStopReasonPtr(s string) *string {
	return anthropic.AnthropicStopReasonPtr(s)
}

// AnthropicStopReasonString 委托唯一协议实现。
func AnthropicStopReasonString(p *string) string {
	return anthropic.AnthropicStopReasonString(p)
}

// conversionRuntime 保持旧入口的时间和随机数来源，不在纯转换器安装全局状态。
func conversionRuntime() bridge.Runtime {
	return bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}
}

// requestOptions 在兼容边界选择旧型号策略，纯转换只使用两个明确的行为开关。
func requestOptions(model string) bridge.RequestOptions {
	return bridge.RequestOptions{
		DropSampling:      capability.ResponsesBridgeDropsSampling(model),
		SupportsMaxEffort: capability.ResponsesBridgeSupportsMaxEffort(model),
	}
}
