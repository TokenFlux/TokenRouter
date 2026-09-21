// 本文件直接装配唯一 Antigravity 授权状态与原代理读取。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

func provideAntigravityAuthorization(proxies egress.ProxyRepository) *account.AntigravityAuthorization {
	options := accountprovider.AntigravityAuthorizationOptions(func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxies.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return account.NewAntigravityAuthorization(options)
}
