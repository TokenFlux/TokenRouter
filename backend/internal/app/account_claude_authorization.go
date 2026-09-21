// 本文件将唯一 Claude 授权实例绑定到原代理读取和平台客户端。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

func provideClaudeAuthorization(proxies egress.ProxyRepository, client account.ClaudeOAuthClient) *account.ClaudeAuthorization {
	options := accountprovider.ClaudeAuthorizationOptions(func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxies.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return account.NewClaudeAuthorization(client, options)
}
