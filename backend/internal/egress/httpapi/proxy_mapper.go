// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	proxydto "github.com/TokenFlux/TokenRouter/internal/egress/httpapi/dto"
)

func ProxyFromService(p *egress.Proxy) *Proxy { return proxydto.ProxyFromEgress(p) }

func ProxyWithAccountCountFromService(p *egress.ProxyWithAccountCount) *ProxyWithAccountCount {
	return proxydto.ProxyWithAccountCountFromEgress(p)
}

func ProxyFromServiceAdmin(p *egress.Proxy) *AdminProxy { return proxydto.ProxyFromEgressAdmin(p) }

func ProxyWithAccountCountFromServiceAdmin(p *egress.ProxyWithAccountCount) *AdminProxyWithAccountCount {
	return proxydto.ProxyWithAccountCountFromEgressAdmin(p)
}

func ProxyAccountSummaryFromService(a *egress.ProxyAccountSummary) *ProxyAccountSummary {
	return proxydto.ProxyAccountSummaryFromEgress(a)
}
