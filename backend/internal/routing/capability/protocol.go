package capability

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

const (
	ProtocolAnthropicMessages     = protocol.ProtocolAnthropicMessages
	ProtocolOpenAIResponses       = protocol.ProtocolOpenAIResponses
	ProtocolOpenAIChatCompletions = protocol.ProtocolOpenAIChatCompletions
	ProtocolGeminiGenerateContent = protocol.ProtocolGeminiGenerateContent
	ProtocolSystemOne             = protocol.ProtocolSystemOne
	ProtocolEmbeddings            = protocol.ProtocolEmbeddings
	ProtocolImagesGenerations     = protocol.ProtocolImagesGenerations
	ProtocolImagesEdits           = protocol.ProtocolImagesEdits
	ProtocolImageBatches          = protocol.ProtocolImageBatches
	ProtocolVideosGenerations     = protocol.ProtocolVideosGenerations
	ProtocolVideosEdits           = protocol.ProtocolVideosEdits
	ProtocolVideosExtensions      = protocol.ProtocolVideosExtensions
	ProtocolTTS                   = protocol.ProtocolTTS
	ProtocolSTT                   = protocol.ProtocolSTT
	ProtocolCustomVoices          = protocol.ProtocolCustomVoices
	ProtocolVoiceRealtime         = protocol.ProtocolVoiceRealtime
	ProtocolResponsesWebSocket    = protocol.ProtocolResponsesWebSocket
	ProtocolLive                  = protocol.ProtocolLive
	ProtocolResponsesCompact      = protocol.ProtocolResponsesCompact
	ProtocolAlphaSearch           = protocol.ProtocolAlphaSearch
	ProtocolWebSearch             = protocol.ProtocolWebSearch
	ProtocolXSearch               = protocol.ProtocolXSearch
	ProtocolQoderChat             = protocol.ProtocolQoderChat
	ProtocolGeminiBatch           = protocol.ProtocolGeminiBatch
	ProtocolVertexBatch           = protocol.ProtocolVertexBatch
)

var protocolCatalog = buildProtocolCatalog()

// Protocol 描述协议标识、名称、适用平台及是否专供上游使用。
// @project-doc docs/interfaces/protocol_capabilities.md#protocol_catalog
type Protocol struct {
	ID           ProtocolID `json:"id"`
	Name         string     `json:"name"`
	UpstreamOnly bool       `json:"upstream_only"`
	Platforms    []string   `json:"platforms"`
}

// ProtocolID 引用 protocol 包的协议标识类型。
type ProtocolID = protocol.ProtocolID

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
		{ID: ProtocolSystemOne, Name: "SystemOne", Platforms: []string{PlatformJev}},
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

// NativeProtocolOptions 返回该认证方式直接支持的协议。
func NativeProtocolOptions(platform, providerType, authMode string) []ProtocolID {
	var selected []ProtocolID
	switch platform {
	case PlatformJev:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolSystemOne}
		}
	case PlatformAnthropic:
		if slices.Contains([]string{ProviderTypeOAuth, ProviderTypeSetupToken, ProviderTypeAPIKey, ProviderTypeBedrock, ProviderTypeServiceAccount}, providerType) {
			selected = []ProtocolID{ProtocolAnthropicMessages}
		}
	case PlatformOpenAI:
		switch providerType {
		case ProviderTypeAPIKey:
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolAlphaSearch}
		case ProviderTypeOAuth:
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolResponsesWebSocket, ProtocolResponsesCompact}
			if authMode != "personalAccessToken" {
				selected = append(selected, ProtocolAlphaSearch)
			}
			if authMode != "personalAccessToken" && authMode != "agentIdentity" {
				selected = append(selected, ProtocolLive)
			}
		}
	case PlatformKimi, PlatformDeepseek, PlatformZhipu:
		if providerType == ProviderTypeAPIKey {
			selected = []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
			if platform != PlatformZhipu {
				selected = append(selected, ProtocolOpenAIResponses)
			}
		}
	case PlatformGemini:
		switch providerType {
		case ProviderTypeOAuth:
			selected = []ProtocolID{ProtocolGeminiGenerateContent}
		case ProviderTypeAPIKey:
			selected = []ProtocolID{ProtocolGeminiGenerateContent, ProtocolGeminiBatch}
		case ProviderTypeServiceAccount:
			selected = []ProtocolID{ProtocolGeminiGenerateContent, ProtocolVertexBatch}
		}
	case PlatformAntigravity:
		if providerType == ProviderTypeOAuth {
			selected = []ProtocolID{ProtocolGeminiGenerateContent}
		}
	case PlatformQoder:
		if providerType == ProviderTypeCosy {
			selected = []ProtocolID{ProtocolQoderChat}
		}
	case PlatformGrok:
		if providerType == ProviderTypeAPIKey || providerType == ProviderTypeOAuth {
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

// ProtocolFallbackTargets 返回适配器支持的单步转换目标，并跳过与来源相同的协议。
func ProtocolFallbackTargets(platform string, source ProtocolID) []ProtocolID {
	if !platformSupportsClientProtocol(platform, source) {
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

// SupportsProtocolConversion 根据提供商平台、认证类型和认证模式判断是否支持单步转换。
func SupportsProtocolConversion(platform, providerType, authMode string, source, target ProtocolID) bool {
	if !slices.Contains(ProtocolFallbackTargets(platform, source), target) {
		return false
	}
	if source == ProtocolImagesGenerations || source == ProtocolImagesEdits {
		return platform == PlatformOpenAI && providerType == ProviderTypeOAuth
	}
	if source == ProtocolAlphaSearch {
		return platform == PlatformOpenAI && providerType == ProviderTypeOAuth && authMode == "personalAccessToken"
	}
	return slices.Contains(NativeProtocolOptions(platform, providerType, authMode), target)
}

// ProtocolCatalog 返回协议目录的深复制副本，调用方修改副本时目录保持原值。
func ProtocolCatalog() []Protocol {
	out := slices.Clone(protocolCatalog)
	for i := range out {
		out[i].Platforms = slices.Clone(out[i].Platforms)
	}
	return out
}

// platformSupportsClientProtocol 校验具体上游已有的入口实现，独立于分组准入。
func platformSupportsClientProtocol(platform string, source ProtocolID) bool {
	for _, entry := range protocolCatalog {
		if entry.ID == source {
			return slices.Contains(entry.Platforms, platform)
		}
	}
	return false
}

// AutomaticProtocolFallbackTargets 是所有候选共用的稳定转换顺序，只包含已实现的单步路线。
func AutomaticProtocolFallbackTargets(source ProtocolID) []ProtocolID {
	targets := []ProtocolID{}
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		for _, target := range ProtocolFallbackTargets(platform, source) {
			if !slices.Contains(targets, target) {
				targets = append(targets, target)
			}
		}
	}
	return targets
}
