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

// 缺少依赖和停止后的拒绝仍先于报文读取，构造不需要旧生成 Handler。
func TestOpenAITokenAssemblyKeepsReadAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("token-contract")}
	first := provideOpenAITokensHTTP(nil, nil, nil, nil, nil, nil, activity, nil, nil, nil)
	second := provideOpenAITokensHTTP(nil, nil, nil, nil, nil, nil, activity, nil, nil, nil)
	for _, stopped := range []bool{false, true} {
		if stopped {
			require.NoError(t, activity.StopContext(context.Background()))
		}
		writer := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(writer)
		body := &protocolGateTrackingReader{}
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7})
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
		if stopped {
			second.ResponsesInputTokens(c)
			require.Contains(t, writer.Body.String(), "Service is shutting down")
		} else {
			first.CountTokens(c)
			require.Contains(t, writer.Body.String(), "Service temporarily unavailable")
		}
		require.Equal(t, http.StatusServiceUnavailable, writer.Code)
		require.False(t, body.read)
	}
}
