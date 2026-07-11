package handler

import (
	"net/http"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// CodexModels 为 Codex CLI 和桌面客户端透传实时模型清单。
// 自定义 provider 使用 /v1/models，chatgpt_base_url 模式使用
// /backend-api/codex/models，两条路由都进入此处理器。
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI groups")
		return
	}

	account, err := h.gatewayService.SelectAccountForModel(c.Request.Context(), apiKey.GroupID, "", "")
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
		return
	}

	manifest, err := h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), c.GetHeader("If-None-Match"))
	if err != nil {
		h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
		return
	}

	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}
