// 代理探测配置直接验证生产装配，避免只覆盖已删除的仓储构造器。
package app

import (
	context "context"
	http "net/http"
	httptest "net/http/httptest"
	testing "testing"

	config "github.com/TokenFlux/TokenRouter/internal/config"
	require "github.com/stretchr/testify/require"
)

// 配置必须控制实际请求目标，装配不能忽略配置而使用内置地址。
func TestNewProxyExitInfoProberUsesConfiguredTargets(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"ip":"203.0.113.42"}`))
	}))
	defer server.Close()
	prober := provideEgressProbe(&config.Config{Security: config.SecurityConfig{ProxyProbe: config.ProxyProbeConfig{URLs: []config.ProbeURLConfig{{URL: server.URL, Parser: "ipify"}}}}})
	result, _, err := prober.ProbeProxy(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "203.0.113.42", result.IP)
	require.Equal(t, 1, calls)
}
