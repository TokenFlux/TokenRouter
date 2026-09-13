// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// egressAdmin 返回生产注入的唯一管理用例；旧独立构造仅适配已有依赖。
func (s *adminServiceImpl) egressAdmin() *egress.ProxyAdmin {
	if s.proxyAdmin != nil {
		return s.proxyAdmin
	}
	return egress.NewProxyAdmin(s.proxyRepo, s.proxyProber, s.proxyLatencyCache, egressprovider.ProxyQualityHTTP{}, egress.ProxyAdminOptions{Tasks: legacyProxyTasks{}, Diagnostics: egress.Diagnostics{Logf: logger.LegacyPrintf}})
}

type legacyProxyTasks struct{}

func (legacyProxyTasks) Go(name string, fn func()) bool { return RunBackgroundTask(name, fn) }
func (s *adminServiceImpl) ListProxies(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]Proxy, int64, error) {
	return s.egressAdmin().ListProxies(ctx, page, pageSize, protocol, status, search, sortBy, sortOrder)
}

func (s *adminServiceImpl) ListProxiesWithAccountCount(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]ProxyWithAccountCount, int64, error) {
	return s.egressAdmin().ListProxiesWithAccountCount(ctx, page, pageSize, protocol, status, search, sortBy, sortOrder)
}

func (s *adminServiceImpl) GetAllProxies(ctx context.Context) ([]Proxy, error) {
	return s.egressAdmin().GetAllProxies(ctx)
}

func (s *adminServiceImpl) GetAllProxiesWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error) {
	return s.egressAdmin().GetAllProxiesWithAccountCount(ctx)
}

func (s *adminServiceImpl) GetProxy(ctx context.Context, id int64) (*Proxy, error) {
	return s.egressAdmin().GetProxy(ctx, id)
}

func (s *adminServiceImpl) GetProxiesByIDs(ctx context.Context, ids []int64) ([]Proxy, error) {
	return s.egressAdmin().GetProxiesByIDs(ctx, ids)
}

func (s *adminServiceImpl) CreateProxy(ctx context.Context, input *CreateProxyInput) (*Proxy, error) {
	return s.egressAdmin().CreateProxy(ctx, input)
}

func (s *adminServiceImpl) UpdateProxy(ctx context.Context, id int64, input *UpdateProxyInput) (*Proxy, error) {
	return s.egressAdmin().UpdateProxy(ctx, id, input)
}

func (s *adminServiceImpl) DeleteProxy(ctx context.Context, id int64) error {
	return s.egressAdmin().DeleteProxy(ctx, id)
}

func (s *adminServiceImpl) BatchDeleteProxies(ctx context.Context, ids []int64) (*ProxyBatchDeleteResult, error) {
	return s.egressAdmin().BatchDeleteProxies(ctx, ids)
}

func (s *adminServiceImpl) GetProxyAccounts(ctx context.Context, proxyID int64) ([]ProxyAccountSummary, error) {
	return s.egressAdmin().GetProxyAccounts(ctx, proxyID)
}

func (s *adminServiceImpl) CheckProxyExists(ctx context.Context, host string, port int, username, password string) (bool, error) {
	return s.egressAdmin().CheckProxyExists(ctx, host, port, username, password)
}

func (s *adminServiceImpl) TestProxy(ctx context.Context, id int64) (*ProxyTestResult, error) {
	return s.egressAdmin().TestProxy(ctx, id)
}

func (s *adminServiceImpl) CheckProxyQuality(ctx context.Context, id int64) (*ProxyQualityCheckResult, error) {
	return s.egressAdmin().CheckProxyQuality(ctx, id)
}
