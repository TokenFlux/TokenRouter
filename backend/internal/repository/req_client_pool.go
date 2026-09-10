// 本文件保留旧平台工厂入口；共享 req 客户端状态由 infra/httpclient 唯一持有。
package repository

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"github.com/imroc/req/v3"
)

type reqClientOptions = httpclient.ReqClientOptions

// instrumentReqClient 保留 Claude OAuth 工厂的耗时装配入口。
func instrumentReqClient(client *req.Client) *req.Client {
	return httpclient.InstrumentReqClient(client)
}

func getSharedReqClient(opts reqClientOptions) (*req.Client, error) {
	return httpclient.GetSharedReqClient(opts)
}

// CreatePrivacyReqClient 保留隐私设置请求原有的超时和 Chrome 指纹。
func CreatePrivacyReqClient(proxyURL string) (*req.Client, error) {
	return getSharedReqClient(reqClientOptions{ProxyURL: proxyURL, Timeout: 30 * time.Second, Impersonate: true})
}
