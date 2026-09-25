package app

import (
	accountauth "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// providePricingRemoteClient 沿用更新代理及显式直连回退配置。
func providePricingRemoteClient(cfg *config.Config) provider.PricingRemoteClient {
	return provider.NewPricingRemoteClient(cfg.Update.ProxyURL, cfg.Security.ProxyFallback.AllowDirectOnError)
}

// provideClaudeUsageFetcher 适配既有共享 HTTP 池，不创建独立传输状态。
func provideClaudeUsageFetcher(upstream httpclient.UpstreamTransport) accountprovider.ClaudeUsageClient {
	if upstream == nil {
		return anthropic.NewUsageClient(nil)
	}
	return anthropic.NewUsageClient(upstream.DoWithTLS)
}

// provideOpenAIOAuthClient 将原传输端口绑定到唯一平台实现。
func provideOpenAIOAuthClient(upstream httpclient.UpstreamTransport) accountprovider.OpenAIOAuthClient {
	return openai.NewOAuthClient(upstream)
}

// provideGeminiOAuthClient 保留调用时读取启动配置的原时点。
func provideGeminiOAuthClient(cfg *config.Config) accountauth.GeminiOAuthClient {
	return codeassist.NewOAuthClient(func() codeassist.OAuthConfig {
		return codeassist.OAuthConfig{
			ClientID:     cfg.Gemini.OAuth.ClientID,
			ClientSecret: cfg.Gemini.OAuth.ClientSecret,
			Scopes:       cfg.Gemini.OAuth.Scopes,
		}
	})
}
