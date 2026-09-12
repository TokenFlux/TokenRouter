//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package handler

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	gin "github.com/gin-gonic/gin"
)

type oauthStartCaptchaRequest = identityhttp.OAuthStartCaptchaRequest

func (h *AuthHandler) requireActionCaptchaForOAuthLoginStart(c *gin.Context) bool {
	return h.sessionHTTP().RequireActionCaptchaForOAuthLoginStart(c)
}

func respondOAuthStart(c *gin.Context, url string) { identityhttp.RespondOAuthStart(c, url) }
