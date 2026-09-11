package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// 路由声明复用唯一的协议 ID。
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

// ProtocolRoute 描述需要分组准入的标准化入口；别名在路由层统一去除前缀。
type ProtocolRoute struct {
	// 空方法表示所有方法；子资源匹配以完整路径段为边界。
	Method    string
	Path      string
	Prefix    bool
	WebSocket bool
}

// ProtocolEndpoint 保存 HTTP 展示和准入的共同声明。
type ProtocolEndpoint struct {
	ID       ProtocolID
	Endpoint string
	Routes   []ProtocolRoute
}

var protocolEndpoints = []ProtocolEndpoint{
	{ID: ProtocolAnthropicMessages, Endpoint: "POST /v1/messages"},
	{ID: ProtocolOpenAIResponses, Endpoint: "POST /v1/responses"},
	{ID: ProtocolOpenAIChatCompletions, Endpoint: "POST /v1/chat/completions"},
	{ID: ProtocolGeminiGenerateContent, Endpoint: "POST /v1beta/models/{model}:generateContent / :streamGenerateContent"},
	httpProtocol(ProtocolEmbeddings, ProtocolRoute{Method: http.MethodPost, Path: "/embeddings"}),
	httpProtocol(ProtocolImagesGenerations, ProtocolRoute{Method: http.MethodPost, Path: "/images/generations"}),
	httpProtocol(ProtocolImagesEdits, ProtocolRoute{Method: http.MethodPost, Path: "/images/edits"}),
	httpProtocol(ProtocolImageBatches, ProtocolRoute{Method: http.MethodPost, Path: "/images/batches"}),
	httpProtocol(ProtocolVideosGenerations, ProtocolRoute{Method: http.MethodPost, Path: "/videos/generations"}, ProtocolRoute{Method: http.MethodPost, Path: "/videos"}),
	httpProtocol(ProtocolVideosEdits, ProtocolRoute{Method: http.MethodPost, Path: "/videos/edits"}),
	httpProtocol(ProtocolVideosExtensions, ProtocolRoute{Method: http.MethodPost, Path: "/videos/extensions"}),
	httpProtocol(ProtocolTTS, ProtocolRoute{Method: http.MethodPost, Path: "/tts"}),
	httpProtocol(ProtocolSTT, ProtocolRoute{Method: http.MethodPost, Path: "/stt"}),
	httpProtocol(ProtocolCustomVoices, ProtocolRoute{Path: "/custom-voices", Prefix: true}),
	httpProtocol(ProtocolVoiceRealtime, ProtocolRoute{Method: http.MethodGet, Path: "/realtime", WebSocket: true}),
	httpProtocol(ProtocolResponsesWebSocket, ProtocolRoute{Method: http.MethodGet, Path: "/responses", WebSocket: true}),
	httpProtocol(ProtocolLive, ProtocolRoute{Method: http.MethodPost, Path: "/live"}, ProtocolRoute{Method: http.MethodPost, Path: "/realtime/calls"}),
	httpProtocol(ProtocolResponsesCompact, ProtocolRoute{Method: http.MethodPost, Path: "/responses/compact"}),
	httpProtocol(ProtocolAlphaSearch, ProtocolRoute{Method: http.MethodPost, Path: "/alpha/search"}),
	httpProtocol(ProtocolWebSearch, ProtocolRoute{Method: http.MethodPost, Path: "/web_search"}),
	httpProtocol(ProtocolXSearch, ProtocolRoute{Method: http.MethodPost, Path: "/x_search"}),
	{ID: ProtocolQoderChat, Endpoint: "agent_chat_generation (SSE)"},
	{ID: ProtocolGeminiBatch, Endpoint: "POST /v1beta/models/{model}:batchGenerateContent"},
	{ID: ProtocolVertexBatch, Endpoint: "POST /v1/projects/{project}/locations/{location}/batchPredictionJobs"},
}

// httpProtocol 从同一份路径元数据派生目录展示，避免入口描述与门禁映射漂移。
func httpProtocol(id ProtocolID, primary ProtocolRoute, aliases ...ProtocolRoute) ProtocolEndpoint {
	endpoint := "/v1" + primary.Path
	if primary.Method != "" {
		endpoint = primary.Method + " " + endpoint
	}
	if primary.WebSocket {
		endpoint += " (WebSocket)"
	}
	return ProtocolEndpoint{ID: id, Endpoint: endpoint, Routes: append([]ProtocolRoute{primary}, aliases...)}
}

// ProtocolForRoute 根据目录中的入口元数据解析扩展路由归属。
func ProtocolForRoute(method, path string) (ProtocolID, bool) {
	for _, protocol := range protocolEndpoints {
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

// ProtocolEndpoints 返回独立副本，调用方不能修改路由准入。
func ProtocolEndpoints() []ProtocolEndpoint {
	out := slices.Clone(protocolEndpoints)
	for i := range out {
		out[i].Routes = slices.Clone(out[i].Routes)
	}
	return out
}
