package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencytest "github.com/TokenFlux/TokenRouter/internal/idempotency/testkit"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	usageuserhttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
)

// idempotency_http_test.go verifies that participating panel command handlers
// receive one shared idempotency coordinator during application construction.
// The fixture checks each consumer's retention settings, replays a command when
// execution switches from provider to group handlers, and compares a second
// coordinator's write TTL.

// TestIdempotencyHTTPUsesExplicitApplicationCoordinator binds multiple handler
// types to one coordinator. Their write and maintenance TTLs must match that
// instance, provider/group requests must replay one command, and another
// binding must retain its own default TTL.
func TestIdempotencyHTTPUsesExplicitApplicationCoordinator(t *testing.T) {
	options := idempotency.DefaultIdempotencyConfig()
	options.DefaultTTL = 2 * time.Hour
	options.SystemOperationTTL = 17 * time.Minute
	coordinator := idempotency.NewIdempotencyCoordinator(idempotencytest.NewMemoryStore(), options)
	providers := &providerhttp.ManagementHandler{}
	archive := &providerhttp.ArchiveHandler{}
	codex := &providerhttp.CodexImportHandler{}
	keys := &keyhttp.APIKeyHandler[routingdto.Group]{}
	redeem := &billinghttp.AdminRedeemHandler{}
	subscriptions := &billinghttp.AdminSubscriptionHandler{}
	proxies := &egresshttp.ProxyHandler{}
	users := &identityhttp.AdminUserHandler[keydto.APIKey[routingdto.Group]]{}
	groups := &routinghttp.GroupHandler{}
	system := &opshttp.SystemHandler{}
	usage := &usagehttp.UsageHandler{}
	userUsage := &usageuserhttp.UsageHandler{}
	provideIdempotencyHTTP(coordinator, providers, archive, codex, keys, redeem, subscriptions, proxies, users, groups, system, usage, userUsage)
	for _, handler := range []interface {
		// DefaultWriteIdempotencyTTL returns write-record retention from the bound
		// coordinator. Nil coordinators and nonpositive configured periods use that
		// accessor's default, letting standalone handlers report a usable duration.
		DefaultWriteIdempotencyTTL() time.Duration
		// DefaultSystemOperationIdempotencyTTL returns maintenance retention from
		// this handler's coordinator. Nil coordinators and nonpositive maintenance
		// periods use that accessor's default, keeping this retention setting
		// separate from the normal write period.
		DefaultSystemOperationIdempotencyTTL() time.Duration
	}{providers, archive, codex, keys, redeem, subscriptions, proxies, users, groups, system, usage, userUsage} {
		require.Equal(t, 2*time.Hour, handler.DefaultWriteIdempotencyTTL())
		require.Equal(t, 17*time.Minute, handler.DefaultSystemOperationIdempotencyTTL())
	}
	// Unauthenticated fixture requests use actor admin:0. Their method, route,
	// scope, key, and payload stay fixed while the executor changes, so the shared
	// coordinator replays the stored command and executes its callback once.
	calls := 0
	router := gin.New()
	request := 0
	router.POST("/operation", func(c *gin.Context) {
		execute := providers.ExecuteAdminIdempotentJSON
		if request > 0 {
			execute = groups.ExecuteAdminIdempotentJSON
		}
		request++
		execute(c, "shared-binding", map[string]string{"action": "copy"}, time.Hour, func(context.Context) (any, error) { calls++; return gin.H{"ok": true}, nil })
	})
	for i := range 2 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/operation", nil)
		req.Header.Set("Idempotency-Key", "shared-key")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		if i == 1 {
			require.Equal(t, "true", rec.Header().Get("X-Idempotency-Replayed"))
		}
	}
	require.Equal(t, 1, calls)
	other := &routinghttp.GroupHandler{}
	other.BindIdempotency(idempotency.NewIdempotencyCoordinator(nil, idempotency.DefaultIdempotencyConfig()))
	require.Equal(t, 24*time.Hour, other.DefaultWriteIdempotencyTTL())
	require.Equal(t, 2*time.Hour, groups.DefaultWriteIdempotencyTTL())
}
