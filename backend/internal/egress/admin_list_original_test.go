//go:build unit

package egress_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type proxyRepoStubForAdminList struct {
	egress.ProxyRepository

	listWithFiltersCalls    int
	listWithFiltersParams   pagination.PaginationParams
	listWithFiltersProtocol string
	listWithFiltersStatus   string
	listWithFiltersSearch   string
	listWithFiltersProxies  []egress.Proxy
	listWithFiltersResult   *pagination.PaginationResult
	listWithFiltersErr      error

	listWithFiltersAndAccountCountCalls    int
	listWithFiltersAndAccountCountParams   pagination.PaginationParams
	listWithFiltersAndAccountCountProtocol string
	listWithFiltersAndAccountCountStatus   string
	listWithFiltersAndAccountCountSearch   string
	listWithFiltersAndAccountCountProxies  []egress.ProxyWithAccountCount
	listWithFiltersAndAccountCountResult   *pagination.PaginationResult
	listWithFiltersAndAccountCountErr      error
}

func (s *proxyRepoStubForAdminList) ListWithFilters(_ context.Context, params pagination.PaginationParams, protocol, status, search string) ([]egress.Proxy, *pagination.PaginationResult, error) {
	s.listWithFiltersCalls++
	s.listWithFiltersParams = params
	s.listWithFiltersProtocol = protocol
	s.listWithFiltersStatus = status
	s.listWithFiltersSearch = search

	if s.listWithFiltersErr != nil {
		return nil, nil, s.listWithFiltersErr
	}

	result := s.listWithFiltersResult
	if result == nil {
		result = &pagination.PaginationResult{
			Total:    int64(len(s.listWithFiltersProxies)),
			Page:     params.Page,
			PageSize: params.PageSize,
		}
	}

	return s.listWithFiltersProxies, result, nil
}

func (s *proxyRepoStubForAdminList) ListWithFiltersAndAccountCount(_ context.Context, params pagination.PaginationParams, protocol, status, search string) ([]egress.ProxyWithAccountCount, *pagination.PaginationResult, error) {
	s.listWithFiltersAndAccountCountCalls++
	s.listWithFiltersAndAccountCountParams = params
	s.listWithFiltersAndAccountCountProtocol = protocol
	s.listWithFiltersAndAccountCountStatus = status
	s.listWithFiltersAndAccountCountSearch = search

	if s.listWithFiltersAndAccountCountErr != nil {
		return nil, nil, s.listWithFiltersAndAccountCountErr
	}

	result := s.listWithFiltersAndAccountCountResult
	if result == nil {
		result = &pagination.PaginationResult{
			Total:    int64(len(s.listWithFiltersAndAccountCountProxies)),
			Page:     params.Page,
			PageSize: params.PageSize,
		}
	}

	return s.listWithFiltersAndAccountCountProxies, result, nil
}

func TestAdminService_ListProxies_WithSearch(t *testing.T) {
	t.Run("search 参数正常传递到 repository 层", func(t *testing.T) {
		repo := &proxyRepoStubForAdminList{
			listWithFiltersProxies: []egress.Proxy{{ID: 2, Name: "p1"}},
			listWithFiltersResult:  &pagination.PaginationResult{Total: 7},
		}
		svc := egress.NewProxyAdmin(repo, nil, nil, nil, egress.ProxyAdminOptions{})

		proxies, total, err := svc.ListProxies(context.Background(), 3, 50, "http", billing.StatusActive, "p1", "name", "ASC")
		require.NoError(t, err)
		require.Equal(t, int64(7), total)
		require.Equal(t, []egress.Proxy{{ID: 2, Name: "p1"}}, proxies)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 3, PageSize: 50, SortBy: "name", SortOrder: "ASC"}, repo.listWithFiltersParams)
		require.Equal(t, "http", repo.listWithFiltersProtocol)
		require.Equal(t, billing.StatusActive, repo.listWithFiltersStatus)
		require.Equal(t, "p1", repo.listWithFiltersSearch)
	})
}

func TestAdminService_ListProxiesWithAccountCount_WithSearch(t *testing.T) {
	t.Run("search 参数正常传递到 repository 层", func(t *testing.T) {
		repo := &proxyRepoStubForAdminList{
			listWithFiltersAndAccountCountProxies: []egress.ProxyWithAccountCount{{Proxy: egress.Proxy{ID: 3, Name: "p2"}, AccountCount: 5}},
			listWithFiltersAndAccountCountResult:  &pagination.PaginationResult{Total: 9},
		}
		svc := egress.NewProxyAdmin(repo, nil, nil, nil, egress.ProxyAdminOptions{})

		proxies, total, err := svc.ListProxiesWithAccountCount(context.Background(), 2, 10, "socks5", billing.StatusDisabled, "p2", "account_count", "DESC")
		require.NoError(t, err)
		require.Equal(t, int64(9), total)
		require.Equal(t, []egress.ProxyWithAccountCount{{Proxy: egress.Proxy{ID: 3, Name: "p2"}, AccountCount: 5}}, proxies)

		require.Equal(t, 1, repo.listWithFiltersAndAccountCountCalls)
		require.Equal(t, pagination.PaginationParams{Page: 2, PageSize: 10, SortBy: "account_count", SortOrder: "DESC"}, repo.listWithFiltersAndAccountCountParams)
		require.Equal(t, "socks5", repo.listWithFiltersAndAccountCountProtocol)
		require.Equal(t, billing.StatusDisabled, repo.listWithFiltersAndAccountCountStatus)
		require.Equal(t, "p2", repo.listWithFiltersAndAccountCountSearch)
	})
}
