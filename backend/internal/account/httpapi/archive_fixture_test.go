package httpapi

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	"strings"
)

func (s *archiveHTTPFixture) ListProxies(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]egress.Proxy, int64, error) {
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

func (s *archiveHTTPFixture) GetProxy(ctx context.Context, id int64) (*egress.Proxy, error) {
	for i := range s.proxies {
		proxy := s.proxies[i]
		if proxy.ID == id {
			return &proxy, nil
		}
	}
	proxy := egress.Proxy{ID: id, Name: "proxy", Status: billing.StatusActive}
	return &proxy, nil
}

func (s *archiveHTTPFixture) GetProxiesByIDs(ctx context.Context, ids []int64) ([]egress.Proxy, error) {
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

func (s *archiveHTTPFixture) CreateProxy(ctx context.Context, input *egress.CreateProxyInput) (*egress.Proxy, error) {
	s.mu.Lock()
	s.createdProxies = append(s.createdProxies, input)
	s.mu.Unlock()
	proxy := egress.Proxy{ID: 400, Name: input.Name, Status: billing.StatusActive}
	return &proxy, nil
}

func (s *archiveHTTPFixture) UpdateProxy(ctx context.Context, id int64, input *egress.UpdateProxyInput) (*egress.Proxy, error) {
	s.mu.Lock()
	s.updatedProxyIDs = append(s.updatedProxyIDs, id)
	s.updatedProxies = append(s.updatedProxies, input)
	s.mu.Unlock()
	proxy := egress.Proxy{ID: id, Name: input.Name, Status: billing.StatusActive}
	return &proxy, nil
}

// archiveHTTPFixture 组合账号与代理的窄测试端口，保留导入参数和列表查询观察。
type archiveHTTPFixture struct {
	*managementMutationFixture
	egress.ProxyAdministrator
	proxies         []egress.Proxy
	createdProxies  []*egress.CreateProxyInput
	updatedProxies  []*egress.UpdateProxyInput
	updatedProxyIDs []int64
	list            managementListFixture
	lastListProxies struct {
		protocol, status, search, sortBy, sortOrder string
		calls                                       int
	}
}

func newArchiveHTTPFixture() *archiveHTTPFixture {
	source := &archiveHTTPFixture{managementMutationFixture: newManagementMutationFixture()}
	source.accounts = newManagementListFixture().accounts
	now := time.Now().UTC()
	source.proxies = []egress.Proxy{{ID: 4, Name: "proxy", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: egress.StatusActive, CreatedAt: now, UpdatedAt: now}}
	return source
}
func (s *archiveHTTPFixture) ListAccounts(ctx context.Context, page, size int, platform, kind, status, search string, gid int64, privacy, sortBy, order string) ([]account.Record, int64, error) {
	s.list.accounts = s.accounts
	return s.list.ListAccounts(ctx, page, size, platform, kind, status, search, gid, privacy, sortBy, order)
}

// archiveSettingFixture 只提供原模板读取，保留缺少键时的空值和意外写入拒绝。
type archiveSettingFixture struct{ values map[string]string }

func (s *archiveSettingFixture) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (*archiveSettingFixture) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}
