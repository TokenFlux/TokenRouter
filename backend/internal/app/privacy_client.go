package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/imroc/req/v3"
)

// providePrivacyClientFactory 保留隐私请求原有超时、Chrome 指纹与共享池。
func providePrivacyClientFactory() openai.PrivacyClientFactory {
	return func(proxyURL string) (*req.Client, error) {
		return httpclient.GetSharedReqClient(httpclient.ReqClientOptions{
			ProxyURL:    proxyURL,
			Timeout:     30 * time.Second,
			Impersonate: true,
		})
	}
}
