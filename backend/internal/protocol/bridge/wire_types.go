package bridge

import (
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// 协议类型由各 wire 包唯一拥有；桥接内部复用别名，不复制编解码实现。
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

func AnthropicStopReasonPtr(s string) *string {
	return anthropic.AnthropicStopReasonPtr(s)
}

func AnthropicStopReasonString(p *string) string {
	return anthropic.AnthropicStopReasonString(p)
}

const minMaxOutputTokens = 128

func toolSearchCallArgumentsJSON(arguments string) json.RawMessage {
	return openai.ToolSearchCallArgumentsJSON(arguments)
}
