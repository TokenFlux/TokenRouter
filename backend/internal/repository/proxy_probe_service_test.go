// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	require "github.com/stretchr/testify/require"
	http "net/http"
	httptest "net/http/httptest"
	testing "testing"
)

// 原配置必须控制实际请求目标，不能在装配迁移中回退内置地址。
func TestNewProxyExitInfoProberUsesConfiguredTargets(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"ip":"203.0.113.42"}`))
	}))
	defer server.Close()
	prober := NewProxyExitInfoProber(&config.Config{Security: config.SecurityConfig{ProxyProbe: config.ProxyProbeConfig{URLs: []config.ProbeURLConfig{{URL: server.URL, Parser: "ipify"}}}}})
	result, _, err := prober.ProbeProxy(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "203.0.113.42", result.IP)
	require.Equal(t, 1, calls)
}
