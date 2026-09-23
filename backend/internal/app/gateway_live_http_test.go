package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 原生 Live 装配保持平台门禁与共享停止屏障先于请求体读取，不构造旧 Handler。
func TestLiveAssemblyKeepsPermissionAndStopBeforeRead(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("live-contract")}
	first := provideLiveHTTP(nil, nil, nil, nil, activity)
	second := provideLiveHTTP(nil, nil, nil, nil, activity)
	for _, stopped := range []bool{false, true} {
		if stopped {
			require.NoError(t, activity.StopContext(context.Background()))
		}
		writer := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(writer)
		body := &protocolGateTrackingReader{}
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/live", body)
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 2, UserID: 1, Group: &routing.Group{Platform: "openai"}})
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 1})
		if stopped {
			second.Live(c)
			require.Equal(t, http.StatusServiceUnavailable, writer.Code)
			require.Contains(t, writer.Body.String(), "Service is shutting down")
		} else {
			first.Live(c)
			require.Equal(t, http.StatusForbidden, writer.Code)
			require.Contains(t, writer.Body.String(), "Live is not enabled for this group")
		}
		require.False(t, body.read)
	}
}
