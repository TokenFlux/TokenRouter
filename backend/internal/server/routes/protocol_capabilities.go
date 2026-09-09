package routes

import (
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"net/http"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/gin-gonic/gin"
)

// extendedRouteProtocol 为非文本入口及别名统一命名；已有任务操作不受新建开关影响。
func extendedRouteProtocol(method, path string) domain.GroupClientProtocol {
	path = strings.TrimPrefix(path, "/backend-api/codex")
	path = strings.TrimPrefix(path, "/v1")
	if method == http.MethodGet {
		switch path {
		case "/responses":
			return domain.ProtocolResponsesWebSocket
		case "/realtime":
			return domain.ProtocolVoiceRealtime
		}
	}
	if path == "/custom-voices" || strings.HasPrefix(path, "/custom-voices/") {
		return domain.ProtocolCustomVoices
	}
	if method != http.MethodPost {
		return ""
	}
	switch path {
	case "/embeddings":
		return domain.ProtocolEmbeddings
	case "/images/generations":
		return domain.ProtocolImagesGenerations
	case "/images/edits":
		return domain.ProtocolImagesEdits
	case "/images/batches":
		return domain.ProtocolImageBatches
	case "/videos", "/videos/generations":
		return domain.ProtocolVideosGenerations
	case "/videos/edits":
		return domain.ProtocolVideosEdits
	case "/videos/extensions":
		return domain.ProtocolVideosExtensions
	case "/tts":
		return domain.ProtocolTTS
	case "/stt":
		return domain.ProtocolSTT
	case "/live", "/realtime/calls":
		return domain.ProtocolLive
	case "/alpha/search":
		return domain.ProtocolAlphaSearch
	case "/web_search":
		return domain.ProtocolWebSearch
	case "/x_search":
		return domain.ProtocolXSearch
	}
	return ""
}

func requireExtendedProtocol(c *gin.Context) {
	protocol := extendedRouteProtocol(c.Request.Method, c.Request.URL.Path)
	if key, ok := middleware.GetAPIKeyFromContext(c); ok && key != nil && key.Group != nil && !slices.Contains(domain.SupportedGroupClientProtocols(key.Group.Platform), protocol) {
		c.Next()
		return
	}
	if protocol == "" || enforceGroupClientProtocol(c, protocol, groupClientProtocolErrorOpenAI) {
		c.Next()
	}
}
