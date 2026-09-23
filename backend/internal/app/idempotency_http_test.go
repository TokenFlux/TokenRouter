package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencytest "github.com/TokenFlux/TokenRouter/internal/idempotency/testkit"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 所有写入口共享同一协调器，独立装配不能覆盖既有处理器的期限。
func TestIdempotencyHTTPUsesExplicitApplicationCoordinator(t *testing.T) {
	options := idempotency.DefaultIdempotencyConfig()
	options.DefaultTTL = 2 * time.Hour
	options.SystemOperationTTL = 17 * time.Minute
	coordinator := idempotency.NewIdempotencyCoordinator(idempotencytest.NewMemoryStore(), options)
	accounts := &accounthttp.ManagementHandler{}
	archive := &accounthttp.ArchiveHandler{}
	codex := &accounthttp.CodexImportHandler{}
	keys := &keyhttp.APIKeyHandler[routingdto.Group]{}
	redeem := &billinghttp.AdminRedeemHandler{}
	subscriptions := &billinghttp.AdminSubscriptionHandler{}
	proxies := &egresshttp.ProxyHandler{}
	users := &identityhttp.AdminUserHandler[keydto.APIKey[routingdto.Group]]{}
	groups := &routinghttp.GroupHandler{}
	system := &opshttp.SystemHandler{}
	usage := &usagehttp.UsageHandler{}
	provideIdempotencyHTTP(coordinator, accounts, archive, codex, keys, redeem, subscriptions, proxies, users, groups, system, usage)
	for _, handler := range []interface {
		DefaultWriteIdempotencyTTL() time.Duration
		DefaultSystemOperationIdempotencyTTL() time.Duration
	}{accounts, archive, codex, keys, redeem, subscriptions, proxies, users, groups, system, usage} {
		require.Equal(t, 2*time.Hour, handler.DefaultWriteIdempotencyTTL())
		require.Equal(t, 17*time.Minute, handler.DefaultSystemOperationIdempotencyTTL())
	}
	// 同路由的两个已绑定入口验证真实共享认领与重放，不仅比较指针。
	calls := 0
	router := gin.New()
	request := 0
	router.POST("/operation", func(c *gin.Context) {
		execute := accounts.ExecuteAdminIdempotentJSON
		if request > 0 {
			execute = groups.ExecuteAdminIdempotentJSON
		}
		request++
		execute(c, "shared-binding", map[string]string{"action": "copy"}, time.Hour, func(context.Context) (any, error) { calls++; return gin.H{"ok": true}, nil })
	})
	for i := 0; i < 2; i++ {
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
