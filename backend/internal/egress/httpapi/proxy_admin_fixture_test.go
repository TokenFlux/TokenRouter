package httpapi

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/gin-gonic/gin"
)

// proxyAdminFixture 只记录代理管理端口的输入和探测，不持有其他管理领域。
type proxyAdminFixture struct {
	proxies                         []egress.Proxy
	proxyCounts                     []egress.ProxyWithAccountCount
	createdProxies                  []*egress.CreateProxyInput
	updatedProxies                  []*egress.UpdateProxyInput
	updatedProxyIDs, testedProxyIDs []int64
	mu                              sync.Mutex
	lastListProxies                 struct {
		protocol, status, search, sortBy, sortOrder string
		calls                                       int
	}
}

func newProxyAdminFixture() *proxyAdminFixture {
	now := time.Now().UTC()
	p := egress.Proxy{ID: 4, Name: "proxy", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: billing.StatusActive, CreatedAt: now, UpdatedAt: now}
	return &proxyAdminFixture{proxies: []egress.Proxy{p}, proxyCounts: []egress.ProxyWithAccountCount{{Proxy: p, AccountCount: 1}}}
}

// proxyTaskFixture 保留导入探测异步执行，测试退出前等待原回调结束。
type proxyTaskFixture struct{ wg sync.WaitGroup }

func (f *proxyTaskFixture) Go(_ string, run func()) bool {
	f.wg.Add(1)
	go func() { defer f.wg.Done(); run() }()
	return true
}

func setupProxyAdminContractRouter() (*gin.Engine, *proxyAdminFixture) {
	router := gin.New()
	source := newProxyAdminFixture()
	handler := NewProxyHandler(source)
	router.GET("/api/v1/admin/proxies", handler.List)
	router.GET("/api/v1/admin/proxies/all", handler.GetAll)
	router.GET("/api/v1/admin/proxies/:id", handler.GetByID)
	router.POST("/api/v1/admin/proxies", handler.Create)
	router.PUT("/api/v1/admin/proxies/:id", handler.Update)
	router.DELETE("/api/v1/admin/proxies/:id", handler.Delete)
	router.POST("/api/v1/admin/proxies/batch-delete", handler.BatchDelete)
	router.POST("/api/v1/admin/proxies/:id/test", handler.Test)
	router.POST("/api/v1/admin/proxies/:id/quality-check", handler.CheckQuality)
	router.GET("/api/v1/admin/proxies/:id/stats", handler.GetStats)
	router.GET("/api/v1/admin/proxies/:id/accounts", handler.GetProxyAccounts)
	return router, source
}
func (s *proxyAdminFixture) ListProxies(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]egress.Proxy, int64, error) {
	s.lastListProxies.protocol = protocol
	s.lastListProxies.status = status
	s.lastListProxies.search = search
	s.lastListProxies.sortBy = sortBy
	s.lastListProxies.sortOrder = sortOrder
	s.lastListProxies.calls++
	search = strings.TrimSpace(strings.ToLower(search))
	filtered := make([]egress.Proxy, 0, len(s.proxies))
	for _, proxy := range s.proxies {
		if protocol != "" && proxy.Protocol != protocol {
			continue
		}
		if status != "" && proxy.Status != status {
			continue
		}
		if search != "" {
			name := strings.ToLower(proxy.Name)
			host := strings.ToLower(proxy.Host)
			if !strings.Contains(name, search) && !strings.Contains(host, search) {
				continue
			}
		}
		filtered = append(filtered, proxy)
	}
	return filtered, int64(len(filtered)), nil
}

func (s *proxyAdminFixture) ListProxiesWithAccountCount(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]egress.ProxyWithAccountCount, int64, error) {
	return s.proxyCounts, int64(len(s.proxyCounts)), nil
}

func (s *proxyAdminFixture) GetAllProxies(ctx context.Context) ([]egress.Proxy, error) {
	return s.proxies, nil
}

func (s *proxyAdminFixture) GetAllProxiesWithAccountCount(ctx context.Context) ([]egress.ProxyWithAccountCount, error) {
	return s.proxyCounts, nil
}

func (s *proxyAdminFixture) GetProxy(ctx context.Context, id int64) (*egress.Proxy, error) {
	for i := range s.proxies {
		proxy := s.proxies[i]
		if proxy.ID == id {
			return &proxy, nil
		}
	}
	proxy := egress.Proxy{ID: id, Name: "proxy", Status: billing.StatusActive}
	return &proxy, nil
}

func (s *proxyAdminFixture) GetProxiesByIDs(ctx context.Context, ids []int64) ([]egress.Proxy, error) {
	if len(ids) == 0 {
		return []egress.Proxy{}, nil
	}
	out := make([]egress.Proxy, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	for i := range s.proxies {
		proxy := s.proxies[i]
		if _, ok := seen[proxy.ID]; ok {
			out = append(out, proxy)
		}
	}
	return out, nil
}

func (s *proxyAdminFixture) CreateProxy(ctx context.Context, input *egress.CreateProxyInput) (*egress.Proxy, error) {
	s.mu.Lock()
	s.createdProxies = append(s.createdProxies, input)
	s.mu.Unlock()
	proxy := egress.Proxy{ID: 400, Name: input.Name, Status: billing.StatusActive}
	return &proxy, nil
}

func (s *proxyAdminFixture) UpdateProxy(ctx context.Context, id int64, input *egress.UpdateProxyInput) (*egress.Proxy, error) {
	s.mu.Lock()
	s.updatedProxyIDs = append(s.updatedProxyIDs, id)
	s.updatedProxies = append(s.updatedProxies, input)
	s.mu.Unlock()
	proxy := egress.Proxy{ID: id, Name: input.Name, Status: billing.StatusActive}
	return &proxy, nil
}

func (s *proxyAdminFixture) DeleteProxy(ctx context.Context, id int64) error {
	return nil
}

func (s *proxyAdminFixture) BatchDeleteProxies(ctx context.Context, ids []int64) (*egress.ProxyBatchDeleteResult, error) {
	return &egress.ProxyBatchDeleteResult{DeletedIDs: ids}, nil
}

func (s *proxyAdminFixture) GetProxyAccounts(ctx context.Context, proxyID int64) ([]egress.ProxyAccountSummary, error) {
	return []egress.ProxyAccountSummary{{ID: 1, Name: "account"}}, nil
}

func (s *proxyAdminFixture) CheckProxyExists(ctx context.Context, host string, port int, username, password string) (bool, error) {
	return false, nil
}

func (s *proxyAdminFixture) TestProxy(ctx context.Context, id int64) (*egress.ProxyTestResult, error) {
	s.mu.Lock()
	s.testedProxyIDs = append(s.testedProxyIDs, id)
	s.mu.Unlock()
	return &egress.ProxyTestResult{Success: true, Message: "ok"}, nil
}

func (s *proxyAdminFixture) CheckProxyQuality(ctx context.Context, id int64) (*egress.ProxyQualityCheckResult, error) {
	return &egress.ProxyQualityCheckResult{
		ProxyID:        id,
		Score:          95,
		Grade:          "A",
		Summary:        "通过 5 项，告警 0 项，失败 0 项，挑战 0 项",
		PassedCount:    5,
		WarnCount:      0,
		FailedCount:    0,
		ChallengeCount: 0,
		CheckedAt:      time.Now().Unix(),
		Items: []egress.ProxyQualityCheckItem{
			{Target: "base_connectivity", Status: "pass", Message: "ok"},
			{Target: "openai", Status: "pass", HTTPStatus: 401},
			{Target: "anthropic", Status: "pass", HTTPStatus: 401},
			{Target: "gemini", Status: "pass", HTTPStatus: 200},
		},
	}, nil
}
