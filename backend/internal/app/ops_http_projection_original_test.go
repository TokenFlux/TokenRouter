package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsErrorLoggerMiddleware_DoesNotBreakOuterMiddlewares(t *testing.T) {

	r := gin.New()
	r.Use(middleware2.Recovery())
	r.Use(middleware2.RequestLogger())
	r.Use(middleware2.Logger())
	r.GET("/v1/messages", gatewayhttp.OpsErrorLoggerMiddleware(nil, nil, provideOpsObservationAccess()), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)

	require.NotPanics(t, func() {
		r.ServeHTTP(rec, req)
	})
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestGetOpsAPIKeyFallsBackToOpsFallbackKey(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// 主 key 缺席（鉴权早退场景）：返回 nil。
	require.Nil(t, provideOpsObservationAccess().APIKey(c))

	// 写入 ops 专用 fallback key 后应能取到，且带齐 user/group。
	groupID := int64(55)
	apiKey := &apikey.APIKey{
		ID:      100,
		GroupID: &groupID,
		User:    &identity.User{ID: 7},
		Group:   &routing.Group{ID: groupID, Platform: capability.PlatformAnthropic},
	}
	c.Set(string(keyhttp.ContextKeyOpsFallbackAPIKey), apiKey)

	got := provideOpsObservationAccess().APIKey(c)
	require.NotNil(t, got)
	require.Equal(t, int64(100), got.ID)
	require.NotNil(t, got.User)
	require.Equal(t, int64(7), got.User.ID)
	require.NotNil(t, got.Group)
	require.Equal(t, capability.PlatformAnthropic, got.Group.Platform)
	_, authenticated := authctx.GetPrincipal(c)
	require.False(t, authenticated, "读取失败 Key 的观测投影不能创建认证主体")
}

func TestGetOpsAPIKeyPrefersPrimaryContextKey(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	primary := &apikey.APIKey{ID: 1}
	fallback := &apikey.APIKey{ID: 2}
	c.Set("gateway_effective_key", primary)
	c.Set(string(keyhttp.ContextKeyOpsFallbackAPIKey), fallback)

	got := provideOpsObservationAccess().APIKey(c)
	require.NotNil(t, got)
	require.Equal(t, int64(1), got.ID, "已鉴权请求应优先使用正式 api key")
}

// 实际装配继续读取原拒绝标记类型，不能把任意字符串误判为已经执行的准入拒绝。
func TestOpsObservationAccessUsesTypedIngressMarker(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	access := provideOpsObservationAccess()
	require.False(t, access.Rejected(c))
	c.Set("ingress_reject_reason", "invalid_api_key")
	require.False(t, access.Rejected(c))
	middleware2.MarkIngressRejected(c, middleware2.IngressRejectInvalidAPIKey)
	require.True(t, access.Rejected(c))
}
