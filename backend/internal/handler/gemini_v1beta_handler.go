package handler

import (
	"github.com/gin-gonic/gin"
)

// writeGeminiModelsListWithAPIKeyAliases 返回 Gemini 列表并追加当前可请求目标的精确别名。

// GeminiV1BetaModels proxies Gemini native REST endpoints like:
// POST /v1beta/models/{model}:generateContent
// POST /v1beta/models/{model}:streamGenerateContent?alt=sse
// @project-doc docs/interfaces/gemini_upstream.md#gemini_protocol_dispatch
func (h *GatewayHandler) GeminiV1BetaModels(c *gin.Context) {
	h.NewGeminiNativeHTTPHandler().GeminiV1BetaModels(c)
}
