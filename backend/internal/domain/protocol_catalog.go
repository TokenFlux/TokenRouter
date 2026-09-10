package domain

import (
	"net/http"
	"slices"
	"strings"
)

// ProtocolID 统一标识账号原生能力和分组客户端入口。
type ProtocolID string

// Protocol 描述公开入口或上游协议；账号能力与分组控制共用同一目录。
// @project-doc docs/interfaces/protocol_capabilities.md#protocol_catalog
type Protocol struct {
	ID           ProtocolID      `json:"id"`
	Name         string          `json:"name"`
	Endpoint     string          `json:"endpoint"`
	UpstreamOnly bool            `json:"upstream_only"`
	Platforms    []string        `json:"platforms"`
	Routes       []ProtocolRoute `json:"-"`
}

// ProtocolRoute 描述需要分组准入的标准化入口；别名在路由层统一去除前缀。
type ProtocolRoute struct {
	// 空方法表示所有方法；子资源匹配以完整路径段为边界。
	Method    string
	Path      string
	Prefix    bool
	WebSocket bool
}

const (
	ProtocolAnthropicMessages     ProtocolID = "anthropic_messages"
	ProtocolOpenAIResponses       ProtocolID = "openai_responses"
	ProtocolOpenAIChatCompletions ProtocolID = "openai_chat_completions"
	ProtocolGeminiGenerateContent ProtocolID = "gemini_generate_content"
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

// 协议定义只初始化一次，候选过滤不反复分配整个目录。
var protocolCatalog = buildProtocolCatalog()

func buildProtocolCatalog() []Protocol {
	all := []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek}
	openai := []string{PlatformOpenAI}
	grok := []string{PlatformGrok}
	both := []string{PlatformOpenAI, PlatformGrok}
	return []Protocol{
		{ID: ProtocolAnthropicMessages, Name: "Anthropic Messages", Endpoint: "POST /v1/messages", Platforms: all},
		{ID: ProtocolOpenAIResponses, Name: "OpenAI Responses", Endpoint: "POST /v1/responses", Platforms: all},
		{ID: ProtocolOpenAIChatCompletions, Name: "Chat Completions", Endpoint: "POST /v1/chat/completions", Platforms: all},
		{ID: ProtocolGeminiGenerateContent, Name: "Gemini GenerateContent", Endpoint: "POST /v1beta/models/{model}:generateContent / :streamGenerateContent", Platforms: []string{PlatformGemini, PlatformAntigravity}},
		httpProtocol(ProtocolEmbeddings, "Embeddings", openai, ProtocolRoute{Method: http.MethodPost, Path: "/embeddings"}),
		httpProtocol(ProtocolImagesGenerations, "Images Generations", both, ProtocolRoute{Method: http.MethodPost, Path: "/images/generations"}),
		httpProtocol(ProtocolImagesEdits, "Images Edits", both, ProtocolRoute{Method: http.MethodPost, Path: "/images/edits"}),
		httpProtocol(ProtocolImageBatches, "Image Batches", []string{PlatformGemini}, ProtocolRoute{Method: http.MethodPost, Path: "/images/batches"}),
		httpProtocol(ProtocolVideosGenerations, "Video Generations", grok, ProtocolRoute{Method: http.MethodPost, Path: "/videos/generations"}, ProtocolRoute{Method: http.MethodPost, Path: "/videos"}),
		httpProtocol(ProtocolVideosEdits, "Video Edits", grok, ProtocolRoute{Method: http.MethodPost, Path: "/videos/edits"}),
		httpProtocol(ProtocolVideosExtensions, "Video Extensions", grok, ProtocolRoute{Method: http.MethodPost, Path: "/videos/extensions"}),
		httpProtocol(ProtocolTTS, "TTS", grok, ProtocolRoute{Method: http.MethodPost, Path: "/tts"}),
		httpProtocol(ProtocolSTT, "STT", grok, ProtocolRoute{Method: http.MethodPost, Path: "/stt"}),
		httpProtocol(ProtocolCustomVoices, "Custom Voices", grok, ProtocolRoute{Path: "/custom-voices", Prefix: true}),
		httpProtocol(ProtocolVoiceRealtime, "Voice Realtime", grok, ProtocolRoute{Method: http.MethodGet, Path: "/realtime", WebSocket: true}),
		httpProtocol(ProtocolResponsesWebSocket, "Responses WebSocket", both, ProtocolRoute{Method: http.MethodGet, Path: "/responses", WebSocket: true}),
		httpProtocol(ProtocolLive, "OpenAI Live", openai, ProtocolRoute{Method: http.MethodPost, Path: "/live"}, ProtocolRoute{Method: http.MethodPost, Path: "/realtime/calls"}),
		httpProtocol(ProtocolResponsesCompact, "Responses Compact", both, ProtocolRoute{Method: http.MethodPost, Path: "/responses/compact"}),
		httpProtocol(ProtocolAlphaSearch, "Alpha Search", openai, ProtocolRoute{Method: http.MethodPost, Path: "/alpha/search"}),
		httpProtocol(ProtocolWebSearch, "Web Search", grok, ProtocolRoute{Method: http.MethodPost, Path: "/web_search"}),
		httpProtocol(ProtocolXSearch, "X Search", grok, ProtocolRoute{Method: http.MethodPost, Path: "/x_search"}),
		{ID: ProtocolQoderChat, Name: "Qoder Chat", Endpoint: "agent_chat_generation (SSE)", Platforms: []string{PlatformQoder}, UpstreamOnly: true},
		{ID: ProtocolGeminiBatch, Name: "Gemini Batch GenerateContent", Endpoint: "POST /v1beta/models/{model}:batchGenerateContent", Platforms: []string{PlatformGemini}, UpstreamOnly: true},
		{ID: ProtocolVertexBatch, Name: "Vertex Batch Prediction", Endpoint: "POST /v1/projects/{project}/locations/{location}/batchPredictionJobs", Platforms: []string{PlatformGemini}, UpstreamOnly: true},
	}
}

// httpProtocol 从同一份路径元数据派生目录展示，避免入口描述与门禁映射漂移。
func httpProtocol(id ProtocolID, name string, platforms []string, primary ProtocolRoute, aliases ...ProtocolRoute) Protocol {
	endpoint := "/v1" + primary.Path
	if primary.Method != "" {
		endpoint = primary.Method + " " + endpoint
	}
	if primary.WebSocket {
		endpoint += " (WebSocket)"
	}
	return Protocol{ID: id, Name: name, Endpoint: endpoint, Platforms: platforms, Routes: append([]ProtocolRoute{primary}, aliases...)}
}

// ProtocolForRoute 根据目录中的入口元数据解析扩展路由归属。
func ProtocolForRoute(method, path string) (ProtocolID, bool) {
	for _, protocol := range protocolCatalog {
		for _, route := range protocol.Routes {
			if route.Method != "" && route.Method != method {
				continue
			}
			if path == route.Path || (route.Prefix && strings.HasPrefix(path, route.Path+"/")) {
				return protocol.ID, true
			}
		}
	}
	return "", false
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
		if accountType == AccountTypeAPIKey {
			selected = []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolAlphaSearch}
		} else if accountType == AccountTypeOAuth {
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
		out[i].Routes = slices.Clone(out[i].Routes)
	}
	return out
}

// AuxiliaryOperation 登记主协议的辅助操作；资源生命周期不会生成新的协议复选框。
type AuxiliaryOperation struct {
	Operation     string     `json:"operation"`
	Protocol      ProtocolID `json:"protocol,omitempty"`
	Authorization string     `json:"authorization"`
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
