package domain

import "slices"

// Protocol 描述公开入口或上游协议；账号能力与分组控制共用同一目录。
// @project-doc docs/interfaces/protocol_capabilities.md#protocol_catalog
type Protocol struct {
	ID           GroupClientProtocol `json:"id"`
	Name         string              `json:"name"`
	Endpoint     string              `json:"endpoint"`
	UpstreamOnly bool                `json:"upstream_only"`
	Platforms    []string            `json:"platforms"`
}

const (
	ProtocolAnthropicMessages     GroupClientProtocol = "anthropic_messages"
	ProtocolOpenAIResponses       GroupClientProtocol = "openai_responses"
	ProtocolOpenAIChatCompletions GroupClientProtocol = "openai_chat_completions"
	ProtocolGeminiGenerateContent GroupClientProtocol = "gemini_generate_content"
	ProtocolEmbeddings            GroupClientProtocol = "openai_embeddings"
	ProtocolImagesGenerations     GroupClientProtocol = "openai_images_generations"
	ProtocolImagesEdits           GroupClientProtocol = "openai_images_edits"
	ProtocolImageBatches          GroupClientProtocol = "image_batches"
	ProtocolVideosGenerations     GroupClientProtocol = "grok_videos_generations"
	ProtocolVideosEdits           GroupClientProtocol = "grok_videos_edits"
	ProtocolVideosExtensions      GroupClientProtocol = "grok_videos_extensions"
	ProtocolTTS                   GroupClientProtocol = "grok_tts"
	ProtocolSTT                   GroupClientProtocol = "grok_stt"
	ProtocolCustomVoices          GroupClientProtocol = "grok_custom_voices"
	ProtocolVoiceRealtime         GroupClientProtocol = "grok_voice_realtime"
	ProtocolResponsesWebSocket    GroupClientProtocol = "openai_responses_websocket"
	ProtocolLive                  GroupClientProtocol = "openai_live"
	ProtocolResponsesCompact      GroupClientProtocol = "openai_responses_compact"
	ProtocolAlphaSearch           GroupClientProtocol = "openai_alpha_search"
	ProtocolWebSearch             GroupClientProtocol = "grok_web_search"
	ProtocolXSearch               GroupClientProtocol = "grok_x_search"
	ProtocolQoderChat             GroupClientProtocol = "qoder_chat"
	ProtocolGeminiBatch           GroupClientProtocol = "gemini_batch_generate_content"
	ProtocolVertexBatch           GroupClientProtocol = "vertex_batch_prediction"
)

// 协议定义只初始化一次，候选过滤不反复分配整个目录。
var protocolCatalog = buildProtocolCatalog()

func buildProtocolCatalog() []Protocol {
	all := []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek}
	openai := []string{PlatformOpenAI}
	grok := []string{PlatformGrok}
	both := []string{PlatformOpenAI, PlatformGrok}
	return []Protocol{
		{ProtocolAnthropicMessages, "Anthropic Messages", "POST /v1/messages", false, all},
		{ProtocolOpenAIResponses, "OpenAI Responses", "POST /v1/responses", false, all},
		{ProtocolOpenAIChatCompletions, "Chat Completions", "POST /v1/chat/completions", false, all},
		{ProtocolGeminiGenerateContent, "Gemini GenerateContent", "POST /v1beta/models/{model}:generateContent / :streamGenerateContent", false, []string{PlatformGemini, PlatformAntigravity}},
		{ProtocolEmbeddings, "Embeddings", "POST /v1/embeddings", false, openai},
		{ProtocolImagesGenerations, "Images Generations", "POST /v1/images/generations", false, both},
		{ProtocolImagesEdits, "Images Edits", "POST /v1/images/edits", false, both},
		{ProtocolImageBatches, "Image Batches", "POST /v1/images/batches", false, []string{PlatformGemini}},
		{ProtocolVideosGenerations, "Video Generations", "POST /v1/videos/generations", false, grok},
		{ProtocolVideosEdits, "Video Edits", "POST /v1/videos/edits", false, grok},
		{ProtocolVideosExtensions, "Video Extensions", "POST /v1/videos/extensions", false, grok},
		{ProtocolTTS, "TTS", "POST /v1/tts", false, grok},
		{ProtocolSTT, "STT", "POST /v1/stt", false, grok},
		{ProtocolCustomVoices, "Custom Voices", "/v1/custom-voices", false, grok},
		{ProtocolVoiceRealtime, "Voice Realtime", "GET /v1/realtime (WebSocket)", false, grok},
		{ProtocolResponsesWebSocket, "Responses WebSocket", "GET /v1/responses (WebSocket)", false, both},
		{ProtocolLive, "OpenAI Live", "POST /v1/live", false, openai},
		{ProtocolResponsesCompact, "Responses Compact", "POST /v1/responses/compact", false, both},
		{ProtocolAlphaSearch, "Alpha Search", "POST /v1/alpha/search", false, openai},
		{ProtocolWebSearch, "Web Search", "POST /v1/web_search", false, grok},
		{ProtocolXSearch, "X Search", "POST /v1/x_search", false, grok},
		{ProtocolQoderChat, "Qoder Chat", "agent_chat_generation (SSE)", true, []string{PlatformQoder}},
		{ProtocolGeminiBatch, "Gemini Batch GenerateContent", "POST /v1beta/models/{model}:batchGenerateContent", true, []string{PlatformGemini}},
		{ProtocolVertexBatch, "Vertex Batch Prediction", "POST /v1/projects/{project}/locations/{location}/batchPredictionJobs", true, []string{PlatformGemini}},
	}
}

// NativeProtocolOptions 只表达认证方式具备的原生协议，不包含兼容转换入口。
func NativeProtocolOptions(platform, accountType, authMode string) []GroupClientProtocol {
	var selected []GroupClientProtocol
	switch platform {
	case PlatformAnthropic:
		if slices.Contains([]string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, AccountTypeBedrock, AccountTypeServiceAccount}, accountType) {
			selected = []GroupClientProtocol{ProtocolAnthropicMessages}
		}
	case PlatformOpenAI:
		if accountType == AccountTypeAPIKey {
			selected = []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolAlphaSearch}
		} else if accountType == AccountTypeOAuth {
			selected = []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolResponsesWebSocket, ProtocolResponsesCompact}
			if authMode != "personalAccessToken" {
				selected = append(selected, ProtocolAlphaSearch)
			}
			if authMode != "personalAccessToken" && authMode != "agentIdentity" {
				selected = append(selected, ProtocolLive)
			}
		}
	case PlatformKimi, PlatformDeepseek, PlatformZhipu:
		if accountType == AccountTypeAPIKey {
			selected = []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
			if platform != PlatformZhipu {
				selected = append(selected, ProtocolOpenAIResponses)
			}
		}
	case PlatformGemini:
		switch accountType {
		case AccountTypeOAuth:
			selected = []GroupClientProtocol{ProtocolGeminiGenerateContent}
		case AccountTypeAPIKey:
			selected = []GroupClientProtocol{ProtocolGeminiGenerateContent, ProtocolGeminiBatch}
		case AccountTypeServiceAccount:
			selected = []GroupClientProtocol{ProtocolGeminiGenerateContent, ProtocolVertexBatch}
		}
	case PlatformAntigravity:
		if accountType == AccountTypeUpstream {
			selected = []GroupClientProtocol{ProtocolAnthropicMessages}
		}
		if accountType == AccountTypeOAuth {
			selected = []GroupClientProtocol{ProtocolGeminiGenerateContent}
		}
	case PlatformQoder:
		if accountType == AccountTypeCosy {
			selected = []GroupClientProtocol{ProtocolQoderChat}
		}
	case PlatformGrok:
		if accountType == AccountTypeAPIKey || accountType == AccountTypeOAuth {
			selected = []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolVideosGenerations, ProtocolVideosEdits, ProtocolVideosExtensions, ProtocolTTS, ProtocolSTT, ProtocolCustomVoices, ProtocolVoiceRealtime}
		}
	}
	out := []GroupClientProtocol{}
	for _, protocol := range protocolCatalog {
		if slices.Contains(selected, protocol.ID) {
			out = append(out, protocol.ID)
		}
	}
	return out
}

// ProtocolFallbackTargets 仅列出已有适配器支持的单步目标；原生直通不作为转换项。
func ProtocolFallbackTargets(platform string, source GroupClientProtocol) []GroupClientProtocol {
	if !slices.Contains(SupportedGroupClientProtocols(platform), source) {
		return []GroupClientProtocol{}
	}
	var targets []GroupClientProtocol
	switch source {
	case ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions:
		switch platform {
		case PlatformAnthropic:
			targets = []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolGeminiGenerateContent}
		case PlatformOpenAI, PlatformGrok:
			targets = []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformKimi, PlatformDeepseek:
			targets = []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformZhipu:
			targets = []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
		case PlatformGemini, PlatformAntigravity:
			targets = []GroupClientProtocol{ProtocolGeminiGenerateContent}
		case PlatformQoder:
			targets = []GroupClientProtocol{ProtocolQoderChat}
		}
	case ProtocolImagesGenerations, ProtocolImagesEdits:
		if platform == PlatformOpenAI {
			targets = []GroupClientProtocol{ProtocolOpenAIResponses}
		}
	case ProtocolResponsesWebSocket, ProtocolAlphaSearch, ProtocolWebSearch, ProtocolXSearch:
		targets = []GroupClientProtocol{ProtocolOpenAIResponses}
	case ProtocolResponsesCompact:
		if platform == PlatformGrok {
			targets = []GroupClientProtocol{ProtocolOpenAIResponses}
		}
	}
	out := []GroupClientProtocol{}
	for _, target := range targets {
		if target != source {
			out = append(out, target)
		}
	}
	return out
}

// SupportsProtocolConversion 在候选账号层收窄转换边，禁止把 OAuth 专属适配套用到 API Key。
func SupportsProtocolConversion(platform, accountType, authMode string, source, target GroupClientProtocol) bool {
	if !slices.Contains(ProtocolFallbackTargets(platform, source), target) {
		return false
	}
	if source == ProtocolImagesGenerations || source == ProtocolImagesEdits {
		return platform == PlatformOpenAI && accountType == AccountTypeOAuth
	}
	if source == ProtocolAlphaSearch {
		return platform == PlatformOpenAI && accountType == AccountTypeOAuth && authMode == "personalAccessToken"
	}
	return slices.Contains(NativeProtocolOptions(platform, accountType, authMode), target)
}

// ProtocolCatalog 返回深复制的只读投影，调用方不能修改进程能力定义。
func ProtocolCatalog() []Protocol {
	out := slices.Clone(protocolCatalog)
	for i := range out {
		out[i].Platforms = slices.Clone(out[i].Platforms)
	}
	return out
}

// AuxiliaryOperation 登记主协议的辅助操作；资源生命周期不会生成新的协议复选框。
type AuxiliaryOperation struct {
	Operation     string              `json:"operation"`
	Protocol      GroupClientProtocol `json:"protocol,omitempty"`
	Authorization string              `json:"authorization"`
}

func AuxiliaryOperations() []AuxiliaryOperation {
	return []AuxiliaryOperation{
		{"/messages/count_tokens", ProtocolAnthropicMessages, "protocol"},
		{"/responses/input_tokens", ProtocolOpenAIResponses, "protocol"},
		{"Gemini :countTokens", ProtocolGeminiGenerateContent, "protocol"},
		{"native_compaction_v2", ProtocolOpenAIResponses, "capability"},
		{"http_continuation", ProtocolOpenAIResponses, "capability"},
		{"responses_image_tools", ProtocolOpenAIResponses, "image_policy"},
		{"/models, /usage", "", "local"},
		{"video status/content", ProtocolVideosGenerations, "resource"},
		{"batch status/download/cancel/delete", ProtocolImageBatches, "resource"},
		{"Live sideband", ProtocolLive, "session"},
		{"custom voice read/update/delete/audio", ProtocolCustomVoices, "protocol_and_resource"},
	}
}
