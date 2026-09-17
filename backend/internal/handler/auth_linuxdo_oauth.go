// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	strings "strings"

	config "github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	gin "github.com/gin-gonic/gin"
)

const (
	linuxDoOAuthCookiePath         = identityhttp.LinuxDoOAuthCookiePath
	oauthBindAccessTokenCookiePath = identityhttp.OauthBindAccessTokenCookiePath
	linuxDoOAuthStateCookieName    = identityhttp.LinuxDoOAuthStateCookieName
	linuxDoOAuthVerifierCookie     = identityhttp.LinuxDoOAuthVerifierCookie
	linuxDoOAuthRedirectCookie     = identityhttp.LinuxDoOAuthRedirectCookie
	linuxDoOAuthIntentCookieName   = identityhttp.LinuxDoOAuthIntentCookieName
	linuxDoOAuthBindUserCookieName = identityhttp.LinuxDoOAuthBindUserCookieName
	oauthBindAccessTokenCookieName = identityhttp.OauthBindAccessTokenCookieName
	linuxDoOAuthCookieMaxAgeSec    = identityhttp.LinuxDoOAuthCookieMaxAgeSec
	linuxDoOAuthDefaultRedirectTo  = identityhttp.LinuxDoOAuthDefaultRedirectTo
	linuxDoOAuthDefaultFrontendCB  = identityhttp.LinuxDoOAuthDefaultFrontendCB

	linuxDoOAuthMaxRedirectLen      = identityhttp.LinuxDoOAuthMaxRedirectLen
	linuxDoOAuthMaxFragmentValueLen = identityhttp.LinuxDoOAuthMaxFragmentValueLen
	linuxDoOAuthMaxSubjectLen       = identityhttp.LinuxDoOAuthMaxSubjectLen

	oauthIntentLogin           = identityhttp.OauthIntentLogin
	oauthIntentBindCurrentUser = identityhttp.OauthIntentBindCurrentUser
)

type linuxDoTokenResponse = provider.LinuxDoTokenResponse

func (h *AuthHandler) LinuxDoOAuthStart(c *gin.Context) { h.linuxDoHTTP().LinuxDoOAuthStart(c) }

func (h *AuthHandler) LinuxDoOAuthCallback(c *gin.Context) { h.linuxDoHTTP().LinuxDoOAuthCallback(c) }

func (h *AuthHandler) CompleteLinuxDoOAuthRegistration(c *gin.Context) {
	h.linuxDoHTTP().CompleteLinuxDoOAuthRegistration(c)
}

func (h *AuthHandler) getLinuxDoOAuthConfig(ctx context.Context) (config.LinuxDoConnectConfig, error) {
	if h != nil && h.settingSvc != nil {
		return h.settingSvc.GetLinuxDoConnectOAuthConfig(ctx)
	}
	if h == nil || h.cfg == nil {
		return config.LinuxDoConnectConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}
	if !h.cfg.LinuxDo.Enabled {
		return config.LinuxDoConnectConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}
	return h.cfg.LinuxDo, nil
}

func linuxDoParseUserInfo(body string, cfg config.LinuxDoConnectConfig) (email string, username string, subject string, displayName string, avatarURL string, err error) {
	return provider.LinuxDoParseUserInfo(body, linuxDoProviderOptions(cfg))
}

func firstNonEmpty(values ...string) string { return identity.OAuthFirstNonEmpty(values...) }

func parseOAuthProviderError(body string) (providerErr string, providerDesc string) {
	return provider.ParseOAuthProviderError(body)
}

func parseLinuxDoTokenResponse(body string) (*linuxDoTokenResponse, bool) {
	return provider.ParseLinuxDoTokenResponse(body)
}

func singleLine(value string) string { return identityhttp.OAuthSingleLine(value) }

func sanitizeFrontendRedirectPath(path string) string {
	return identityhttp.SanitizeFrontendRedirectPath(path)
}

func isRequestHTTPS(c *gin.Context) bool { return identityhttp.IsRequestHTTPS(c) }

func encodeCookieValue(value string) string { return identityhttp.EncodeCookieValue(value) }

func decodeCookieValue(value string) (string, error) { return identityhttp.DecodeCookieValue(value) }

func buildBearerAuthorization(tokenType, accessToken string) (string, error) {
	return provider.BuildBearerAuthorization(tokenType, accessToken)
}

func linuxDoSyntheticEmail(subject string) string {
	return identity.OAuthLinuxDoSyntheticEmail(subject)
}

func (h *AuthHandler) PrepareOAuthBindAccessTokenCookie(c *gin.Context) {
	h.oauthBindHTTP().PrepareOAuthBindAccessTokenCookie(c)
}

func (h *AuthHandler) oauthBindCookieSecret() string {
	if h == nil || h.cfg == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.JWT.Secret)
}

func buildOAuthBindUserCookieValue(userID int64, secret string) (string, error) {
	return identity.BuildOAuthBindUserCookieValue(userID, secret)
}

func parseOAuthBindUserCookieValue(value string, secret string) (int64, error) {
	return identity.ParseOAuthBindUserCookieValue(value, secret)
}

// oauthBindHTTP 仅投影签名密钥；旧构造器不持有第二份签名或验证算法。
func (h *AuthHandler) oauthBindHTTP() *identityhttp.OAuthBindHandler {
	return identityhttp.NewOAuthBindHandler(h.sessionHTTP(), identity.NewOAuthBindingSigner(h.oauthBindCookieSecret()))
}

// linuxDoHTTP 注入提供方端口及动态配置投影，旧入口不持有第二份回调流程。
func (h *AuthHandler) linuxDoHTTP() *identityhttp.LinuxDoHandler {
	return identityhttp.NewLinuxDoHandler(h.pendingHTTP(), h.oauthBindHTTP(), provider.LinuxDoClient{}, func(ctx context.Context) (identity.LinuxDoOAuthOptions, error) {
		v, e := h.getLinuxDoOAuthConfig(ctx)
		return identity.LinuxDoOAuthOptions(v), e
	})
}
