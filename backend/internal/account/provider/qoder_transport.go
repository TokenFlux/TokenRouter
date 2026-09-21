package provider

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// QoderTransport 只接收单次技术请求，账号缓存和授权状态仍由原生账号核心管理。
type QoderTransport interface {
	DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}

// QoderRequestDoer 固化原代理及 TLS 选择，不复制客户端池或改变缺失代理的原行为。
func QoderRequestDoer(value *account.Record, transport QoderTransport, profiles *egressprovider.TLSProfiles) qoder.RequestDoer {
	if transport == nil || value == nil {
		return nil
	}
	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	var profile *tlsfingerprint.Profile
	if profiles != nil {
		profile = profiles.ResolveRequestTLS(egress.TLSSelection{
			Enabled:         value.IsTLSFingerprintEnabled(),
			DirectProfileID: value.GetTLSFingerprintProfileID(),
		})
	}
	return func(req *http.Request) (*http.Response, error) {
		return transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, profile)
	}
}
