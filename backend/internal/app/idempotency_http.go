package app

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	usageuserhttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

// idempotency_http.go binds the application idempotency coordinator to panel
// command handlers before serving starts. Handler executors store the shared
// coordinator pointer without synchronization, so binding finishes before HTTP
// serving; the readiness marker makes Wire resolve these bindings before
// application startup.

// idempotencyHTTPReady marks assignment of the application coordinator to handler executors.
type idempotencyHTTPReady struct{}

// provideIdempotencyHTTP assigns the supplied coordinator to each assembled
// command handler and returns a Wire readiness marker. Supply all handler
// instances and call before serving HTTP: executor binding is unsynchronized
// with requests, and each consumer stores the same coordinator pointer. A nil
// coordinator selects the executors' direct-callback fixture behavior.
func provideIdempotencyHTTP(
	coordinator *idempotency.IdempotencyCoordinator,
	providers *providerhttp.ManagementHandler,
	archive *providerhttp.ArchiveHandler,
	codex *providerhttp.CodexImportHandler,
	keys *keyhttp.APIKeyHandler[routingdto.Group],
	redeem *billinghttp.AdminRedeemHandler,
	subscriptions *billinghttp.AdminSubscriptionHandler,
	proxies *egresshttp.ProxyHandler,
	users *identityhttp.AdminUserHandler[keydto.APIKey[routingdto.Group]],
	groups *routinghttp.GroupHandler,
	system *opshttp.SystemHandler,
	usage *usagehttp.UsageHandler,
	userUsage *usageuserhttp.UsageHandler,
) *idempotencyHTTPReady {
	// Bind every consumer before endpoints read its unsynchronized executor
	// pointer. A configured coordinator supplies shared storage, while command
	// scopes still determine which requests can replay the same record.
	for _, consumer := range []interface {
		// BindIdempotency assigns the coordinator for subsequent command execution.
		// Assignment is performed during construction because it is unsynchronized
		// with requests, and another binding replaces the executor's current pointer.
		// Nil clears the binding for standalone direct-callback fixtures.
		BindIdempotency(*idempotency.IdempotencyCoordinator)
	}{providers, archive, codex, keys, redeem, subscriptions, proxies, users, groups, system, usage, userUsage} {
		consumer.BindIdempotency(coordinator)
	}
	return &idempotencyHTTPReady{}
}
