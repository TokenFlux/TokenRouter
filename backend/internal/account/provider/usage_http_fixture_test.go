package provider

import (
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// 原消费者契约只装配一个原生查询实例，HTTP 替身保留请求与取消断言。
func newUsageContractService(reader account.UpstreamUsageReader, transport interface {
	DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}, policy egress.UsageURLPolicy) *account.UpstreamUsageService {
	options := UsageHTTPOptions{Available: reader != nil && transport != nil, Policy: policy}
	if transport != nil {
		options.Do = transport.DoWithTLS
	}
	return account.NewUpstreamUsageService(reader, NewUpstreamUsageHTTPExecution(options), account.UpstreamUsageOptions{Now: time.Now})
}
