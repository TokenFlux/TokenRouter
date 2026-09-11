package domain

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ProtocolID 为旧 schema 和消费者保留相同类型，S16 清理。
type ProtocolID = protocol.ProtocolID

const ProtocolAnthropicMessages = protocol.ProtocolAnthropicMessages
const ProtocolOpenAIResponses = protocol.ProtocolOpenAIResponses
const ProtocolOpenAIChatCompletions = protocol.ProtocolOpenAIChatCompletions
const ProtocolGeminiGenerateContent = protocol.ProtocolGeminiGenerateContent
const ProtocolEmbeddings = protocol.ProtocolEmbeddings
const ProtocolImagesGenerations = protocol.ProtocolImagesGenerations
const ProtocolImagesEdits = protocol.ProtocolImagesEdits
const ProtocolImageBatches = protocol.ProtocolImageBatches
const ProtocolVideosGenerations = protocol.ProtocolVideosGenerations
const ProtocolVideosEdits = protocol.ProtocolVideosEdits
const ProtocolVideosExtensions = protocol.ProtocolVideosExtensions
const ProtocolTTS = protocol.ProtocolTTS
const ProtocolSTT = protocol.ProtocolSTT
const ProtocolCustomVoices = protocol.ProtocolCustomVoices
const ProtocolVoiceRealtime = protocol.ProtocolVoiceRealtime
const ProtocolResponsesWebSocket = protocol.ProtocolResponsesWebSocket
const ProtocolLive = protocol.ProtocolLive
const ProtocolResponsesCompact = protocol.ProtocolResponsesCompact
const ProtocolAlphaSearch = protocol.ProtocolAlphaSearch
const ProtocolWebSearch = protocol.ProtocolWebSearch
const ProtocolXSearch = protocol.ProtocolXSearch
const ProtocolQoderChat = protocol.ProtocolQoderChat
const ProtocolGeminiBatch = protocol.ProtocolGeminiBatch
const ProtocolVertexBatch = protocol.ProtocolVertexBatch

// NativeProtocolOptions 委托新能力目录。
func NativeProtocolOptions(platform, accountType, authMode string) []ProtocolID {
	return capability.NativeProtocolOptions(platform, accountType, authMode)
}

// ProtocolFallbackTargets 委托新能力目录。
func ProtocolFallbackTargets(platform string, source ProtocolID) []ProtocolID {
	return capability.ProtocolFallbackTargets(platform, source)
}

// SupportsProtocolConversion 委托新能力目录。
func SupportsProtocolConversion(platform, accountType, authMode string, source, target ProtocolID) bool {
	return capability.SupportsProtocolConversion(platform, accountType, authMode, source, target)
}
