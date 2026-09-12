// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	gin "github.com/gin-gonic/gin"
	idtoken "google.golang.org/api/idtoken"
	time "time"
)

const (
	googleOneTapCredentialMaxBytes  = identityhttp.GoogleOneTapCredentialMaxBytes
	googleOneTapContextMaxBytes     = identityhttp.GoogleOneTapContextMaxBytes
	googleOneTapRequestMaxBytes     = identityhttp.GoogleOneTapRequestMaxBytes
	googleOneTapStatusAuthenticated = identityhttp.GoogleOneTapStatusAuthenticated
	googleOneTapStatusRegistration  = identityhttp.GoogleOneTapStatusRegistration
)

type googleIDTokenClaims = provider.GoogleIDTokenClaims

type googleIDTokenVerifier = provider.GoogleIDTokenVerifier

type googleAPIIDTokenVerifier = provider.GoogleAPIIDTokenVerifier

func validateGoogleIDTokenPayload(payload *idtoken.Payload, audience string, now time.Time) (*googleIDTokenClaims, error) {
	return provider.ValidateGoogleIDTokenPayload(payload, audience, now)
}

func (h *AuthHandler) GoogleOneTap(c *gin.Context) { h.googleOneTapHTTP().GoogleOneTap(c) }

var _ googleIDTokenVerifier = googleAPIIDTokenVerifier{}

// googleOneTapHTTP 投影动态设置，默认仍使用 Google 官方验证器。
func (h *AuthHandler) googleOneTapHTTP() *identityhttp.GoogleOneTapHandler {
	if h == nil {
		return identityhttp.NewGoogleOneTapHandler(nil, nil, identityhttp.GoogleOneTapHTTPOptions{})
	}
	var load func(context.Context) (identityhttp.GoogleOneTapOptions, error)
	var registration func(context.Context) bool
	if h != nil && h.settingSvc != nil {
		load = func(ctx context.Context) (identityhttp.GoogleOneTapOptions, error) {
			v, e := h.settingSvc.GetGoogleOneTapConfig(ctx)
			return identityhttp.GoogleOneTapOptions{ClientID: v.ClientID, FrontendRedirectURL: v.FrontendRedirectURL}, e
		}
		registration = h.settingSvc.IsRegistrationEnabled
	}
	verifier := h.googleIDTokenVerifier
	if verifier == nil {
		verifier = provider.GoogleAPIIDTokenVerifier{}
	}
	return identityhttp.NewGoogleOneTapHandler(h.pendingHTTP(), verifier, identityhttp.GoogleOneTapHTTPOptions{LoadConfig: load, RegistrationEnabled: registration})
}
