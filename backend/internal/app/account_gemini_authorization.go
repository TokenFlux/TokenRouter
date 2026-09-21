// 本文件在原读取时点投影 Gemini 配置，HTTP、刷新和层级查询共享同一授权实例。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

func provideGeminiAuthorization(proxies egress.ProxyRepository, client account.GeminiOAuthClient, discovery account.GeminiCodeAssistClient, drive codeassist.DriveClient, cfg *config.Config) *account.GeminiAuthorization {
	options := accountprovider.GeminiAuthorizationOptions(func() google.OAuthConfig {
		return google.OAuthConfig{ClientID: cfg.Gemini.OAuth.ClientID, ClientSecret: cfg.Gemini.OAuth.ClientSecret, Scopes: cfg.Gemini.OAuth.Scopes}
	}, func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxies.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return account.NewGeminiAuthorization(client, discovery, drive, options)
}
