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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 真实 WS 组合根在升级和依赖拒绝前不读报文，关闭仍先于新连接处理。
func TestOpenAIWSAssemblyUpgradeAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("ws-entry-contract")}
	common := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil)
	h := provideResponsesWSHTTP(nil, nil, nil, nil, common, nil, nil, nil, activity)
	for _, step := range []struct {
		name    string
		upgrade bool
		stop    bool
		status  int
		message string
	}{
		{"upgrade", false, false, http.StatusUpgradeRequired, "WebSocket upgrade required"},
		{"dependencies", true, false, http.StatusServiceUnavailable, "Service temporarily unavailable"},
		{"stop", true, true, http.StatusServiceUnavailable, "Service is shutting down"},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.stop {
				require.NoError(t, activity.StopContext(context.Background()))
			}
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			body := &protocolGateTrackingReader{}
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", body)
			if step.upgrade {
				c.Request.Header.Set("Upgrade", "websocket")
				c.Request.Header.Set("Connection", "Upgrade")
			}
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7})
			c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
			h.ResponsesWebSocket(c)
			require.Equal(t, step.status, writer.Code)
			require.Contains(t, writer.Body.String(), step.message)
			require.False(t, body.read)
		})
	}
}
