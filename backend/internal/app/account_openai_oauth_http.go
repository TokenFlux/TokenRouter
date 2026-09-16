// OpenAI 管理路由直接绑定账号用例，静态平台客户端规则由组合根投影。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func provideOpenAIAccountOAuth(auth *service.OpenAIOAuthService, admin *account.Admin, quota *service.OpenAIQuotaService, recovery *account.RecoveryService, proxies *egress.ProxyAdmin, manager *lifecycle.Manager) *accounthttp.OpenAIOAuthHandler {
	handler := accounthttp.NewOpenAIOAuthHandler(auth.Core(), admin, quota.Core(), recovery, accounthttp.OpenAIHTTPOptions{
		ClientID: openai.OAuthClientConfigByPlatform,
		ProxyURL: func(ctx context.Context, id int64) (string, bool, error) {
			proxy, err := proxies.GetProxy(ctx, id)
			if err != nil || proxy == nil {
				return "", false, err
			}
			return proxy.URL(), true, nil
		},
	})
	// 后置流程在所属额度和账号依赖停止前完成；超时由统一生命周期报告。
	manager.Register(lifecycle.Hook{Name: "OpenAIQuotaActions", StopOrder: 14, Stop: handler.QuotaActions.StopContext})
	return handler
}
