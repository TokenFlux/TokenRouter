package app

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

type idempotencyHTTPReady struct{}

// provideIdempotencyHTTP 在开放 HTTP 前把全部幂等入口绑定到应用唯一协调器。
// 无全局发布，构造失败无需恢复其他应用实例的依赖。
func provideIdempotencyHTTP(
	coordinator *idempotency.IdempotencyCoordinator,
	accounts *accounthttp.ManagementHandler,
	archive *accounthttp.ArchiveHandler,
	codex *accounthttp.CodexImportHandler,
	keys *keyhttp.APIKeyHandler[routingdto.Group],
	redeem *billinghttp.AdminRedeemHandler,
	subscriptions *billinghttp.AdminSubscriptionHandler,
	proxies *egresshttp.ProxyHandler,
	users *identityhttp.AdminUserHandler[keydto.APIKey[routingdto.Group]],
	groups *routinghttp.GroupHandler,
	system *opshttp.SystemHandler,
	usage *usagehttp.UsageHandler,
) *idempotencyHTTPReady {
	for _, consumer := range []interface {
		BindIdempotency(*idempotency.IdempotencyCoordinator)
	}{accounts, archive, codex, keys, redeem, subscriptions, proxies, users, groups, system, usage} {
		consumer.BindIdempotency(coordinator)
	}
	return &idempotencyHTTPReady{}
}
