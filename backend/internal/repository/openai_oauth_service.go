// 旧构造器返回原生唯一 OAuth 交换实现；传输依赖仍使用应用同一实例。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/imroc/req/v3"
)

func NewOpenAIOAuthClient(httpUpstream service.HTTPUpstream) service.OpenAIOAuthClient {
	return native.NewOAuthClient(httpUpstream)
}
func createOpenAIReqClient(proxyURL string) (*req.Client, error) {
	return native.CreateOAuthReqClient(proxyURL)
}
