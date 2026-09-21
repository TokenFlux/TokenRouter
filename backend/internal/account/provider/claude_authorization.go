// 本文件提供 Claude 授权协议参数，授权会话和刷新规则仍由账号核心拥有。
package provider

import (
	"context"
	"log"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/oauth"
)

func ClaudeAuthorizationOptions(resolveProxy func(context.Context, int64) (string, bool)) account.ClaudeAuthorizationOptions {
	return account.ClaudeAuthorizationOptions{
		ScopeOAuth:            oauth.ScopeOAuth,
		ScopeAPI:              oauth.ScopeAPI,
		ScopeInference:        oauth.ScopeInference,
		GenerateState:         oauth.GenerateState,
		GenerateCodeVerifier:  oauth.GenerateCodeVerifier,
		GenerateCodeChallenge: oauth.GenerateCodeChallenge,
		GenerateSessionID:     oauth.GenerateSessionID,
		BuildAuthorizationURL: oauth.BuildAuthorizationURL,
		Logf:                  log.Printf,
		ResolveProxy:          resolveProxy,
	}
}
