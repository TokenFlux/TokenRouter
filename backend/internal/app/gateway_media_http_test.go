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
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 媒体与辅助入口直接复用原生运行时，依赖拒绝和关闭都不能提前读取正文。
func TestMediaAssemblyKeepsReadAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("media-entry-contract")}
	common := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil)
	runtime := provideMediaRuntime(nil, nil, nil, common, nil, nil, nil)
	media := provideMediaHTTP(runtime, activity)
	auxiliary := provideAuxiliaryHTTP(runtime, activity)
	for _, stopped := range []bool{false, true} {
		if stopped {
			require.NoError(t, activity.StopContext(context.Background()))
		}
		for _, entry := range []struct {
			name string
			run  gin.HandlerFunc
		}{
			{"images", media.Images},
			{"embeddings", auxiliary.Embeddings},
			{"alpha-search", auxiliary.AlphaSearch},
		} {
			t.Run(entry.name+map[bool]string{true: "-stopped", false: "-running"}[stopped], func(t *testing.T) {
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				body := &protocolGateTrackingReader{}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+entry.name, body)
				c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7, Group: &routing.Group{Platform: capability.PlatformOpenAI, AllowImageGeneration: true}})
				c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
				entry.run(c)
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
