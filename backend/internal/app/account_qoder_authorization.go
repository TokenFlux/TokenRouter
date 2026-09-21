package app

import (
	"context"
	"errors"
	"fmt"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// provideQoderAuthorization 只投影代理读取，授权会话和供应商执行各由原生拥有者管理。
func provideQoderAuthorization(proxies egress.ProxyRepository) *accountprovider.QoderAuthorization {
	return accountprovider.NewQoderAuthorization(func(ctx context.Context, id *int64) (string, error) {
		if id == nil {
			return "", nil
		}
		if proxies == nil {
			return "", errors.New("proxy repository is not configured")
		}
		proxy, err := proxies.GetByID(ctx, *id)
		if err != nil {
			return "", fmt.Errorf("get proxy: %w", err)
		}
		if proxy == nil {
			return "", errors.New("proxy not found")
		}
		return proxy.URL(), nil
	}, nil)
}
