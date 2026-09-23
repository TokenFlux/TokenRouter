package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 实际组合根构造三种文本入口；缺失依赖和关闭拒绝均不能提前读取报文。
func TestOpenAITextAssemblyReadAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("openai-text-contract")}
	h := provideOpenAITextHTTP(nil, nil, nil, nil, nil, nil, nil, nil, nil, &handler.OpenAIGatewayHandler{}, activity, GatewayCompletionRecorders{})
	for _, stopped := range []bool{false, true} {
		if stopped {
			require.NoError(t, activity.StopContext(context.Background()))
		}
		for name, entry := range map[string]gin.HandlerFunc{"responses": h.Responses, "chat": h.ChatCompletions, "messages": h.Messages} {
			t.Run(name+map[bool]string{false: "-running", true: "-stopped"}[stopped], func(t *testing.T) {
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				body := &protocolGateTrackingReader{}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+name, body)
				c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7})
				c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
				entry(c)
				require.Equal(t, http.StatusServiceUnavailable, writer.Code)
				require.False(t, body.read)
				if stopped {
					require.Contains(t, writer.Body.String(), "Service is shutting down")
				} else {
					require.Contains(t, writer.Body.String(), "Service temporarily unavailable")
				}
			})
		}
	}
}
