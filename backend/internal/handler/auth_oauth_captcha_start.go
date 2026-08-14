package handler

import (
	"net/http"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type oauthStartCaptchaRequest struct {
	// TurnstileToken 承载阿里云验证码的 captchaVerifyParam（复用既有请求字段名）
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

type oauthStartResponse struct {
	AuthorizeURL string `json:"authorize_url"`
}

func (h *AuthHandler) requireActionCaptchaForOAuthLoginStart(c *gin.Context) bool {
	// 当前用户绑定已有独立认证态，不重复消费匿名登录验证码票据。
	if strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/bind/start") {
		return true
	}

	var req oauthStartCaptchaRequest
	if c.Request.Method == http.MethodPost {
		_ = c.ShouldBindJSON(&req)
	}
	if err := h.authService.VerifyActionCaptchaIfEnabled(c.Request.Context(), service.CaptchaProof{
		TurnstileToken: req.TurnstileToken,
		TencentTicket:  req.TencentCaptchaTicket,
		TencentRandstr: req.TencentCaptchaRandstr,
	}, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	return true
}

func respondOAuthStart(c *gin.Context, authorizeURL string) {
	if c.Request.Method == http.MethodPost {
		response.Success(c, oauthStartResponse{AuthorizeURL: authorizeURL})
		return
	}
	c.Redirect(http.StatusFound, authorizeURL)
}
