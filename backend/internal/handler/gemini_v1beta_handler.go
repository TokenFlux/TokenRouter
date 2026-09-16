package handler

import (
	"errors"
	"net/http"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// GeminiV1BetaListModels 返回 Gemini 原生模型列表及当前 Key 可用的精确别名。
// @project-doc docs/domains/api_key_model_redirects.md#model_list_projection
func (h *GatewayHandler) GeminiV1BetaListModels(c *gin.Context) {
	h.NewModelsHTTPHandler().GeminiV1BetaListModels(c)
}

// writeGeminiModelsListWithAPIKeyAliases 返回 Gemini 列表并追加当前可请求目标的精确别名。

// appendAPIKeyAliasesToGeminiModelsJSON 克隆目标元数据，仅改写 Gemini 协议的模型字段。
func appendAPIKeyAliasesToGeminiModelsJSON(body []byte, mapping map[string]string) []byte {
	return newModelDisplayHandler().AppendAPIKeyAliasesToGeminiModelsJSON(body, mapping)
}

// GeminiV1BetaGetModel proxies:
// GET /v1beta/models/{model}
func (h *GatewayHandler) GeminiV1BetaGetModel(c *gin.Context) {
	h.NewModelsHTTPHandler().GeminiV1BetaGetModel(c)
}

// GeminiV1BetaModels proxies Gemini native REST endpoints like:
// POST /v1beta/models/{model}:generateContent
// POST /v1beta/models/{model}:streamGenerateContent?alt=sse
// @project-doc docs/interfaces/gemini_upstream.md#gemini_protocol_dispatch
func (h *GatewayHandler) GeminiV1BetaModels(c *gin.Context) {
	h.NewGeminiNativeHTTPHandler().GeminiV1BetaModels(c)
}

func (h *GatewayHandler) handleGeminiFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError) {
	if failoverErr == nil {
		googleError(c, http.StatusBadGateway, "Upstream request failed")
		return
	}

	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody

	// 先检查透传规则
	if h.errorPassthroughService != nil && len(responseBody) > 0 {
		if rule := h.errorPassthroughService.MatchRule(service.PlatformGemini, statusCode, responseBody); rule != nil {
			// 确定响应状态码
			respCode := statusCode
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				respCode = *rule.ResponseCode
			}

			// 确定响应消息
			msg := service.ExtractUpstreamErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				c.Set(service.OpsSkipPassthroughKey, true)
			}

			googleError(c, respCode, msg)
			return
		}
	}

	// 记录原始上游状态码，以便 ops 错误日志捕获真实的上游错误
	upstreamMsg := service.ExtractUpstreamErrorMessage(responseBody)
	service.SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	// 使用默认的错误映射
	status, message := mapGeminiUpstreamError(statusCode)
	googleError(c, status, message)
}

func mapGeminiUpstreamError(statusCode int) (int, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "Upstream request failed"
	}
}

func googleError(c *gin.Context, status int, message string) {
	gatewayhttp.WriteGoogleError(c, status, message)
}

// handleGeminiGroupModelUnsupportedError 将分组模型限制转换为 Google API 风格错误。
func handleGeminiGroupModelUnsupportedError(c *gin.Context, err error) bool {
	var modelErr *service.GroupModelUnsupportedError
	if !errors.As(err, &modelErr) {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
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
