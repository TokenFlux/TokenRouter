// 旧构造入口仅投影配置，客户端与共享池由原生平台拥有。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	codeassist "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/imroc/req/v3"
)

func NewGeminiOAuthClient(cfg *config.Config) service.GeminiOAuthClient {
	return codeassist.NewOAuthClient(func() codeassist.OAuthConfig {
		return codeassist.OAuthConfig{ClientID: cfg.Gemini.OAuth.ClientID, ClientSecret: cfg.Gemini.OAuth.ClientSecret, Scopes: cfg.Gemini.OAuth.Scopes}
	})
}
func createGeminiReqClient(proxy string) (*req.Client, error) {
	return codeassist.CreateOAuthReqClient(proxy)
}
