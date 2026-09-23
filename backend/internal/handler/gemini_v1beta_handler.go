package handler

import (
	"errors"
	"net/http"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
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

func googleError(c *gin.Context, status int, message string) {
	gatewayhttp.WriteGoogleError(c, status, message)
}

// handleGeminiGroupModelUnsupportedError 将分组模型限制转换为 Google API 风格错误。
func handleGeminiGroupModelUnsupportedError(c *gin.Context, err error) bool {
	var modelErr *routing.GroupModelUnsupportedError
	if !errors.As(err, &modelErr) {
		return false
	}
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
	googleError(c, http.StatusForbidden, modelErr.Error())
	return true
}

// derefGroupID 安全解引用 *int64，nil 返回 0
func derefGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}
