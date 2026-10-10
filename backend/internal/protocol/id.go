package protocol

import (
	"slices"
)

const (
	ProtocolAnthropicMessages     ProtocolID = "anthropic_messages"
	ProtocolOpenAIResponses       ProtocolID = "openai_responses"
	ProtocolOpenAIChatCompletions ProtocolID = "openai_chat_completions"
	ProtocolGeminiGenerateContent ProtocolID = "gemini_generate_content"
	ProtocolSystemOne             ProtocolID = "systemone"
	ProtocolEmbeddings            ProtocolID = "openai_embeddings"
	ProtocolImagesGenerations     ProtocolID = "openai_images_generations"
	ProtocolImagesEdits           ProtocolID = "openai_images_edits"
	ProtocolImageBatches          ProtocolID = "image_batches"
	ProtocolVideosGenerations     ProtocolID = "grok_videos_generations"
	ProtocolVideosEdits           ProtocolID = "grok_videos_edits"
	ProtocolVideosExtensions      ProtocolID = "grok_videos_extensions"
	ProtocolTTS                   ProtocolID = "grok_tts"
	ProtocolSTT                   ProtocolID = "grok_stt"
	ProtocolCustomVoices          ProtocolID = "grok_custom_voices"
	ProtocolVoiceRealtime         ProtocolID = "grok_voice_realtime"
	ProtocolResponsesWebSocket    ProtocolID = "openai_responses_websocket"
	ProtocolLive                  ProtocolID = "openai_live"
	ProtocolResponsesCompact      ProtocolID = "openai_responses_compact"
	ProtocolAlphaSearch           ProtocolID = "openai_alpha_search"
	ProtocolWebSearch             ProtocolID = "grok_web_search"
	ProtocolXSearch               ProtocolID = "grok_x_search"
	ProtocolQoderChat             ProtocolID = "qoder_chat"
	ProtocolGeminiBatch           ProtocolID = "gemini_batch_generate_content"
	ProtocolVertexBatch           ProtocolID = "vertex_batch_prediction"
)

// ProtocolID 标识提供商支持的协议和客户端协议，值用于持久化。
type ProtocolID string

// CloneFallbacks 隔离每个入口的目标列表，并保留省略、空列表和有序列表的区别。
func CloneFallbacks(source map[ProtocolID][]ProtocolID) map[ProtocolID][]ProtocolID {
	if source == nil {
		return nil
	}
	result := make(map[ProtocolID][]ProtocolID, len(source))
	for key, targets := range source {
		result[key] = slices.Clone(targets)
	}
	return result
}
