package capability

import "github.com/TokenFlux/TokenRouter/internal/protocol"

// 协议值由 protocol 根包唯一声明，能力表复用相同类型。
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
