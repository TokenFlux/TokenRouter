// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	gin "github.com/gin-gonic/gin"
)

const (
	emailOAuthCookiePath        = identityhttp.EmailOAuthCookiePath
	emailOAuthStateCookieName   = identityhttp.EmailOAuthStateCookieName
	emailOAuthRedirectCookie    = identityhttp.EmailOAuthRedirectCookie
	emailOAuthProviderCookie    = identityhttp.EmailOAuthProviderCookie
	emailOAuthAffCookie         = identityhttp.EmailOAuthAffCookie
	emailOAuthCookieMaxAgeSec   = identityhttp.EmailOAuthCookieMaxAgeSec
	emailOAuthDefaultRedirect   = identityhttp.EmailOAuthDefaultRedirect
	emailOAuthDefaultFrontendCB = identityhttp.EmailOAuthDefaultFrontendCB
)

type emailOAuthProfile = identityprovider.EmailOAuthProfile

func (h *AuthHandler) GitHubOAuthStart(c *gin.Context) { h.emailOAuthHTTP().GitHubOAuthStart(c) }
func (h *AuthHandler) GoogleOAuthStart(c *gin.Context) { h.emailOAuthHTTP().GoogleOAuthStart(c) }

func (h *AuthHandler) GitHubOAuthCallback(c *gin.Context) { h.emailOAuthHTTP().GitHubOAuthCallback(c) }
func (h *AuthHandler) GoogleOAuthCallback(c *gin.Context) { h.emailOAuthHTTP().GoogleOAuthCallback(c) }
func (h *AuthHandler) CompleteGitHubOAuthRegistration(c *gin.Context) {
	h.emailOAuthHTTP().CompleteGitHubOAuthRegistration(c)
}
func (h *AuthHandler) CompleteGoogleOAuthRegistration(c *gin.Context) {
	h.emailOAuthHTTP().CompleteGoogleOAuthRegistration(c)
}

func (h *AuthHandler) emailOAuthCallbackWithProfile(
	c *gin.Context,
	provider string,
	cfg config.EmailOAuthProviderConfig,
	frontendCallback string,
	redirectTo string,
	profile *emailOAuthProfile,
) {
	h.emailOAuthHTTP().EmailOAuthCallbackWithProfile(c, provider, identitycore.EmailOAuthOptions(cfg), frontendCallback, redirectTo, profile)
}

func (h *AuthHandler) completeEmailOAuthRegistration(c *gin.Context, provider string) {
	h.emailOAuthHTTP().CompleteEmailOAuthRegistration(c, provider)
}

func (h *AuthHandler) getEmailOAuthConfig(ctx context.Context, provider string) (config.EmailOAuthProviderConfig, error) {
	if h != nil && h.settingSvc != nil {
		return h.settingSvc.GetEmailOAuthProviderConfig(ctx, provider)
	}
	return config.EmailOAuthProviderConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
}

// emailOAuthHTTP 注入 GitHub/Google 验证端口，旧 callback 与完成注册只保留调用转接。
func (h *AuthHandler) emailOAuthHTTP() *identityhttp.EmailOAuthHandler {
	return identityhttp.NewEmailOAuthHandler(h.pendingHTTP(), identityprovider.EmailOAuthClientAdapter{}, func(ctx context.Context, provider string) (identitycore.EmailOAuthOptions, error) {
		v, e := h.getEmailOAuthConfig(ctx, provider)
		return identitycore.EmailOAuthOptions(v), e
	})
}
