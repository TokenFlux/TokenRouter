package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// TestHTTPTransportConfigurationReadBoundary 保留同一实例按请求读取安全配置的边界。
func TestHTTPTransportConfigurationReadBoundary(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	client := provideHTTPUpstream(cfg)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := client.Do(request, "", 1, 1)
	require.Error(t, err)
	require.Nil(t, response)
	require.Zero(t, calls.Load())

	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	response, err = client.Do(request, "", 1, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.Equal(t, int64(1), calls.Load())

	cfg.Security.URLAllowlist.AllowPrivateHosts = false
	response, err = client.Do(request, "", 1, 1)
	require.Error(t, err)
	require.Nil(t, response)
	require.Equal(t, int64(1), calls.Load())
}
