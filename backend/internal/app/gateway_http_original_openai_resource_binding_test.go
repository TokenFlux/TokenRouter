package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 原生资源与仍在迁移的 HTTP 入口必须看到同一个已占用图片槽，不能各建一份 limiter。
func TestOpenAIHTTPResourceBindingSharesImageCapacity(t *testing.T) {
	resources := &gatewayhttp.OpenAIHTTPResources{
		Concurrency:  gatewayhttp.NewConcurrencyHelper(nil, gatewayhttp.SSEPingFormatNone, 0),
		Images:       &scheduler.ImageConcurrencyLimiter{},
		ImageOptions: &gatewayhttp.OpenAIImageAdmissionOptions{Enabled: true, Limit: 1},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{ImageConcurrency: config.ImageConcurrencyConfig{Enabled: true, MaxConcurrentRequests: 1}}}
	h := newGatewayHTTPEndpointsFromDeps(nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil, resources)
	require.Same(t, resources.Images, h.Input.Images)
	require.Same(t, resources.Concurrency, h.Input.Concurrency)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	release, ok := resources.AcquireImage(c, false)
	require.True(t, ok)
	require.NotNil(t, release)
	blocked, ok := h.httpResources().AcquireImage(c, false)
	require.False(t, ok)
	require.Nil(t, blocked)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	release()
	release()
	acquired, ok := h.httpResources().AcquireImage(c, false)
	require.True(t, ok)
	require.NotNil(t, acquired)
	acquired()
}
