package capability

import "slices"

// Protocol 描述协议能力，不拥有 HTTP 路径或具体转换实现。
// @project-doc docs/interfaces/protocol_capabilities.md#protocol_catalog
type Protocol struct {
	ID           ProtocolID `json:"id"`
	Name         string     `json:"name"`
	UpstreamOnly bool       `json:"upstream_only"`
	Platforms    []string   `json:"platforms"`
}

var protocolCatalog = buildProtocolCatalog()

func buildProtocolCatalog() []Protocol {
	all := []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek}
	openai := []string{PlatformOpenAI}
	grok := []string{PlatformGrok}
	both := []string{PlatformOpenAI, PlatformGrok}
	return []Protocol{
		{ID: ProtocolAnthropicMessages, Name: "Anthropic Messages", Platforms: all},
		{ID: ProtocolOpenAIResponses, Name: "OpenAI Responses", Platforms: all},
		{ID: ProtocolOpenAIChatCompletions, Name: "Chat Completions", Platforms: all},
		{ID: ProtocolGeminiGenerateContent, Name: "Gemini GenerateContent", Platforms: []string{PlatformGemini, PlatformAntigravity}},
		{ID: ProtocolEmbeddings, Name: "Embeddings", Platforms: openai},
		{ID: ProtocolImagesGenerations, Name: "Images Generations", Platforms: both},
		{ID: ProtocolImagesEdits, Name: "Images Edits", Platforms: both},
		{ID: ProtocolImageBatches, Name: "Image Batches", Platforms: []string{PlatformGemini}},
		{ID: ProtocolVideosGenerations, Name: "Video Generations", Platforms: grok},
		{ID: ProtocolVideosEdits, Name: "Video Edits", Platforms: grok},
		{ID: ProtocolVideosExtensions, Name: "Video Extensions", Platforms: grok},
		{ID: ProtocolTTS, Name: "TTS", Platforms: grok},
		{ID: ProtocolSTT, Name: "STT", Platforms: grok},
		{ID: ProtocolCustomVoices, Name: "Custom Voices", Platforms: grok},
		{ID: ProtocolVoiceRealtime, Name: "Voice Realtime", Platforms: grok},
		{ID: ProtocolResponsesWebSocket, Name: "Responses WebSocket", Platforms: both},
		{ID: ProtocolLive, Name: "OpenAI Live", Platforms: openai},
		{ID: ProtocolResponsesCompact, Name: "Responses Compact", Platforms: both},
		{ID: ProtocolAlphaSearch, Name: "Alpha Search", Platforms: openai},
		{ID: ProtocolWebSearch, Name: "Web Search", Platforms: grok},
		{ID: ProtocolXSearch, Name: "X Search", Platforms: grok},
		{ID: ProtocolQoderChat, Name: "Qoder Chat", Platforms: []string{PlatformQoder}, UpstreamOnly: true},
		{ID: ProtocolGeminiBatch, Name: "Gemini Batch GenerateContent", Platforms: []string{PlatformGemini}, UpstreamOnly: true},
		{ID: ProtocolVertexBatch, Name: "Vertex Batch Prediction", Platforms: []string{PlatformGemini}, UpstreamOnly: true},
	}
}

// NativeProtocolOptions 只表达认证方式具备的原生协议，不包含兼容转换入口。
func NativeProtocolOptions(platform, accountType, authMode string) []ProtocolID {
	var selected []ProtocolID
	switch platform {
	case PlatformAnthropic:
		if slices.Contains([]string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, AccountTypeBedrock, AccountTypeServiceAccount}, accountType) {
			selected = []ProtocolID{ProtocolAnthropicMessages}
		}
	case PlatformOpenAI:
		switch accountType {
		case AccountTypeAPIKey:
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolAlphaSearch}
		case AccountTypeOAuth:
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolResponsesWebSocket, ProtocolResponsesCompact}
			if authMode != "personalAccessToken" {
				selected = append(selected, ProtocolAlphaSearch)
			}
			if authMode != "personalAccessToken" && authMode != "agentIdentity" {
				selected = append(selected, ProtocolLive)
			}
		}
	case PlatformKimi, PlatformDeepseek, PlatformZhipu:
		if accountType == AccountTypeAPIKey {
			selected = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
			if platform != PlatformZhipu {
				selected = append(selected, ProtocolOpenAIResponses)
			}
		}
	case PlatformGemini:
		switch accountType {
		case AccountTypeOAuth:
			selected = []ProtocolID{ProtocolGeminiGenerateContent}
		case AccountTypeAPIKey:
			selected = []ProtocolID{ProtocolGeminiGenerateContent, ProtocolGeminiBatch}
		case AccountTypeServiceAccount:
			selected = []ProtocolID{ProtocolGeminiGenerateContent, ProtocolVertexBatch}
		}
	case PlatformAntigravity:
		if accountType == AccountTypeUpstream {
			selected = []ProtocolID{ProtocolAnthropicMessages}
		}
		if accountType == AccountTypeOAuth {
			selected = []ProtocolID{ProtocolGeminiGenerateContent}
		}
	case PlatformQoder:
		if accountType == AccountTypeCosy {
			selected = []ProtocolID{ProtocolQoderChat}
		}
	case PlatformGrok:
		if accountType == AccountTypeAPIKey || accountType == AccountTypeOAuth {
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolVideosGenerations, ProtocolVideosEdits, ProtocolVideosExtensions, ProtocolTTS, ProtocolSTT, ProtocolCustomVoices, ProtocolVoiceRealtime}
		}
	}
	out := []ProtocolID{}
	for _, protocol := range protocolCatalog {
		if slices.Contains(selected, protocol.ID) {
			out = append(out, protocol.ID)
		}
	}
	return out
}

// ProtocolFallbackTargets 仅列出已有适配器支持的单步目标；原生直通不作为转换项。
func ProtocolFallbackTargets(platform string, source ProtocolID) []ProtocolID {
	if !slices.Contains(SupportedGroupClientProtocols(platform), source) {
		return []ProtocolID{}
	}
	var targets []ProtocolID
	switch source {
	case ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions:
		switch platform {
		case PlatformAnthropic:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolGeminiGenerateContent}
		case PlatformOpenAI, PlatformGrok:
			targets = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformKimi, PlatformDeepseek:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
		case PlatformZhipu:
			targets = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
		case PlatformGemini, PlatformAntigravity:
			targets = []ProtocolID{ProtocolGeminiGenerateContent}
		case PlatformQoder:
			targets = []ProtocolID{ProtocolQoderChat}
		}
	case ProtocolImagesGenerations, ProtocolImagesEdits:
		if platform == PlatformOpenAI {
			targets = []ProtocolID{ProtocolOpenAIResponses}
		}
	case ProtocolResponsesWebSocket, ProtocolAlphaSearch, ProtocolWebSearch, ProtocolXSearch:
		targets = []ProtocolID{ProtocolOpenAIResponses}
	case ProtocolResponsesCompact:
		if platform == PlatformGrok {
			targets = []ProtocolID{ProtocolOpenAIResponses}
		}
	}
	out := []ProtocolID{}
	for _, target := range targets {
		if target != source {
			out = append(out, target)
		}
	}
	return out
}

// SupportsProtocolConversion 在候选账号层收窄转换边，禁止把 OAuth 专属适配套用到 API Key。
func SupportsProtocolConversion(platform, accountType, authMode string, source, target ProtocolID) bool {
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
