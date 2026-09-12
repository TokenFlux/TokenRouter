// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

const (
	oidcOAuthCookiePath         = identityhttp.OidcOAuthCookiePath
	oidcOAuthStateCookieName    = identityhttp.OidcOAuthStateCookieName
	oidcOAuthVerifierCookie     = identityhttp.OidcOAuthVerifierCookie
	oidcOAuthRedirectCookie     = identityhttp.OidcOAuthRedirectCookie
	oidcOAuthNonceCookie        = identityhttp.OidcOAuthNonceCookie
	oidcOAuthIntentCookieName   = identityhttp.OidcOAuthIntentCookieName
	oidcOAuthBindUserCookieName = identityhttp.OidcOAuthBindUserCookieName
	oidcOAuthCookieMaxAgeSec    = identityhttp.OidcOAuthCookieMaxAgeSec
	oidcOAuthDefaultRedirectTo  = identityhttp.OidcOAuthDefaultRedirectTo
	oidcOAuthDefaultFrontendCB  = identityhttp.OidcOAuthDefaultFrontendCB
)

type oidcTokenResponse = provider.OidcTokenResponse

type oidcIDTokenClaims = provider.OidcIDTokenClaims

type oidcUserInfoClaims = provider.OidcUserInfoClaims

type oidcJWKSet = provider.OidcJWKSet

type oidcJWK = provider.OidcJWK

func (h *AuthHandler) OIDCOAuthStart(c *gin.Context) { h.oidcHTTP().OIDCOAuthStart(c) }

func (h *AuthHandler) OIDCOAuthCallback(c *gin.Context) { h.oidcHTTP().OIDCOAuthCallback(c) }

func (h *AuthHandler) CompleteOIDCOAuthRegistration(c *gin.Context) {
	h.oidcHTTP().CompleteOIDCOAuthRegistration(c)
}

func (h *AuthHandler) getOIDCOAuthConfig(ctx context.Context) (config.OIDCConnectConfig, error) {
	if h != nil && h.settingSvc != nil {
		return h.settingSvc.GetOIDCConnectOAuthConfig(ctx)
	}
	if h == nil || h.cfg == nil {
		return config.OIDCConnectConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}
	if !h.cfg.OIDC.Enabled {
		return config.OIDCConnectConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}
	return h.cfg.OIDC, nil
}

func oidcParseUserInfo(body string, cfg config.OIDCConnectConfig) *oidcUserInfoClaims {
	return provider.OidcParseUserInfo(body, oidcProviderOptions(cfg))
}

func buildOIDCAuthorizeURL(cfg config.OIDCConnectConfig, state, nonce, codeChallenge, redirectURI string) (string, error) {
	return identityhttp.BuildOIDCAuthorizeURL(identitycore.OIDCOAuthOptions(cfg), state, nonce, codeChallenge, redirectURI)
}

func oidcParseAndValidateIDToken(ctx context.Context, cfg config.OIDCConnectConfig, idToken string, expectedNonce string) (*oidcIDTokenClaims, error) {
	return provider.OidcParseAndValidateIDToken(ctx, oidcProviderOptions(cfg), idToken, expectedNonce)
}

func oidcIdentityKey(issuer, subject string) string {
	return identitycore.OIDCIdentityKey(issuer, subject)
}

func oidcSyntheticEmailFromIdentityKey(identityKey string) string {
	return identitycore.OIDCSyntheticEmailFromIdentityKey(identityKey)
}

func (h *AuthHandler) tryOIDCVerifiedEmailFastPath(
	c *gin.Context,
	frontendCallback string,
	redirectTo string,
	identity service.PendingAuthIdentityKey,
	compatEmail string,
	username string,
	upstreamClaims map[string]any,
) bool {
	return h.oidcHTTP().TryOIDCVerifiedEmailFastPath(c, frontendCallback, redirectTo, identity, compatEmail, username, upstreamClaims)
}

// oidcHTTP 通过端口执行 ID Token/JWK 校验，回调仅消费验证后的声明投影。
func (h *AuthHandler) oidcHTTP() *identityhttp.OIDCHandler {
	return identityhttp.NewOIDCHandler(h.pendingHTTP(), h.oauthBindHTTP(), provider.OIDCClient{}, func(ctx context.Context) (identitycore.OIDCOAuthOptions, error) {
		v, e := h.getOIDCOAuthConfig(ctx)
		return identitycore.OIDCOAuthOptions(v), e
	})
}
