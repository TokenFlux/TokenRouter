package admin

import (
	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type QoderOAuthHandler struct {
	qoderOAuthService *service.QoderOAuthService
}

func NewQoderOAuthHandler(qoderOAuthService *service.QoderOAuthService) *QoderOAuthHandler {
	return &QoderOAuthHandler{qoderOAuthService: qoderOAuthService}
}

type QoderGenerateAuthURLRequest struct {
	ProxyID *int64 `json:"proxy_id"`
	Site    string `json:"site"`
}

// GenerateAuthURL 生成 Qoder 浏览器授权 URL。
// 路由：POST /api/v1/admin/qoder/oauth/auth-url
func (h *QoderOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req QoderGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	site, err := qoder.ParseSite(req.Site)
	if err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}
	result, err := h.qoderOAuthService.GenerateAuthURLForSite(c.Request.Context(), site, req.ProxyID)
	if err != nil {
		response.InternalError(c, "生成授权链接失败: "+err.Error())
		return
	}

	response.Success(c, result)
}

type QoderExchangeCodeRequest struct {
	SessionID   string `json:"session_id" binding:"required"`
	State       string `json:"state"`
	Code        string `json:"code"`
	CallbackURL string `json:"callback_url"`
	ProxyID     *int64 `json:"proxy_id"`
}

// ExchangeCode 完成 Qoder 设备授权并返回账号凭据。
// 路由：POST /api/v1/admin/qoder/oauth/exchange-code
func (h *QoderOAuthHandler) ExchangeCode(c *gin.Context) {
	var req QoderExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	tokenInfo, err := h.qoderOAuthService.ExchangeCode(c.Request.Context(), &service.QoderExchangeCodeInput{
		SessionID:   req.SessionID,
		State:       req.State,
		Code:        req.Code,
		CallbackURL: req.CallbackURL,
		ProxyID:     req.ProxyID,
	})
	if err != nil {
		response.BadRequest(c, "Token 交换失败: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

type QoderPollRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	State     string `json:"state" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

// Poll 检查 Qoder 浏览器授权是否已完成。
// 路由：POST /api/v1/admin/qoder/oauth/poll
func (h *QoderOAuthHandler) Poll(c *gin.Context) {
	var req QoderPollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	result, err := h.qoderOAuthService.Poll(c.Request.Context(), req.SessionID, req.State, req.ProxyID)
	if err != nil {
		response.BadRequest(c, "授权状态检查失败: "+err.Error())
		return
	}

	response.Success(c, result)
}
