//go:build unit

package egress_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

type updatingProxyRepoStub struct {
	*proxyRepoStub
	proxy       *egress.Proxy
	updateCalls int
}

func (s *updatingProxyRepoStub) GetByID(context.Context, int64) (*egress.Proxy, error) {
	copy := *s.proxy
	return &copy, nil
}

func (s *updatingProxyRepoStub) Update(_ context.Context, proxy *egress.Proxy) error {
	s.updateCalls++
	copy := *proxy
	s.proxy = &copy
	return nil
}

func TestBothProxyUpdateServicesUseRepositoryUpdateBoundary(t *testing.T) {
	t.Run("ProxyService", func(t *testing.T) {
		repo := &updatingProxyRepoStub{
			proxyRepoStub: &proxyRepoStub{},
			proxy:         &egress.Proxy{ID: 9, Protocol: "http", Host: "old.example", Port: 8080, Status: billing.StatusActive},
		}
		svc := egress.NewProxyService(repo)
		host := "new.example"

		_, err := svc.Update(context.Background(), 9, egress.UpdateProxyRequest{Host: &host})

		require.NoError(t, err)
		require.Equal(t, 1, repo.updateCalls)
		require.Equal(t, host, repo.proxy.Host)
	})

	t.Run("adminService", func(t *testing.T) {
		repo := &updatingProxyRepoStub{
			proxyRepoStub: &proxyRepoStub{},
			proxy: &egress.Proxy{
				ID:             9,
				Protocol:       "http",
				Host:           "old.example",
				Port:           8080,
				Status:         billing.StatusActive,
				FallbackMode:   egress.FallbackModeNone,
				ExpiryWarnDays: 7,
			},
		}
		svc := egress.NewProxyAdmin(repo, nil, nil, nil, egress.ProxyAdminOptions{})

		_, err := svc.UpdateProxy(context.Background(), 9, &egress.UpdateProxyInput{
			Host:           "new.example",
			FallbackMode:   egress.FallbackModeNone,
			ExpiryWarnDays: 7,
		})

		require.NoError(t, err)
		require.Equal(t, 1, repo.updateCalls)
		require.Equal(t, "new.example", repo.proxy.Host)
	})
}
