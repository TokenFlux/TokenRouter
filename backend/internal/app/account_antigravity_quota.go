package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// provideAntigravityQuota 保留模型读取边界与代理读取失败语义，直接绑定原生额度用例。
func provideAntigravityQuota(cfg *config.Config, proxies egress.ProxyRepository) *account.AntigravityQuota {
	limit := resolveModelsListReadLimit(cfg)
	options := provider.AntigravityQuotaOptions(limit, func(ctx context.Context, id int64) (string, bool) {
		if proxies == nil {
			return "", false
		}
		proxy, err := proxies.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return &account.AntigravityQuota{Options: options}
}
