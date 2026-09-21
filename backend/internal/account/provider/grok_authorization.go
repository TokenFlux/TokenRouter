package provider

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokAuthorizationOptions 只绑定供应商原语与代理读取，授权状态由 account 唯一持有。
func GrokAuthorizationOptions(proxies egress.ProxyRepository, passwordEnabled func() bool) account.GrokAuthorizationOptions {
	if passwordEnabled == nil {
		passwordEnabled = func() bool { return false }
	}
	options := account.GrokAuthorizationOptions{
		PasswordAuthEnabled:     passwordEnabled,
		GenerateState:           grok.GenerateState,
		GenerateNonce:           grok.GenerateNonce,
		GenerateCodeVerifier:    grok.GenerateCodeVerifier,
		GenerateSessionID:       grok.GenerateSessionID,
		EffectiveRedirectURI:    grok.EffectiveRedirectURI,
		GenerateCodeChallenge:   grok.GenerateCodeChallenge,
		BuildAuthorizationURL:   grok.BuildAuthorizationURL,
		EffectiveClientID:       grok.EffectiveClientID,
		EffectiveScope:          grok.EffectiveScope,
		ParseAuthorizationInput: grok.ParseAuthorizationInput,
		DecodeJWTClaims:         grok.DecodeJWTClaims,
		JWTClaimString:          grok.JWTClaimString,
		SubscriptionTierFromJWT: grok.SubscriptionTierFromJWT,
		DefaultClientID:         grok.DefaultClientID,
		DefaultCLIBaseURL:       grok.DefaultCLIBaseURL,
	}
	if proxies != nil {
		options.LookupProxy = func(ctx context.Context, id int64) (string, bool, error) {
			proxy, err := proxies.GetByID(ctx, id)
			if errors.Is(err, egress.ErrProxyNotFound) {
				return "", false, nil
			}
			if err != nil {
				return "", false, err
			}
			if proxy == nil {
				return "", false, nil
			}
			return proxy.URL(), true, nil
		}
	}
	return options
}
