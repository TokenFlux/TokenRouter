//go:build unit

package handler

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestAppendOpenAIAccountProxyLogFields 验证代理定位字段完整且不会泄露凭据。
func TestAppendOpenAIAccountProxyLogFields(t *testing.T) {
	proxyID := int64(17)
	account := &service.Account{
		ProxyID: &proxyID,
		Proxy: &service.Proxy{
			ID:       proxyID,
			Name:     "openai-egress",
			Host:     "proxy.example.com",
			Port:     8443,
			Username: "proxy-user-secret",
			Password: "proxy-password-secret",
		},
	}
	core, logs := observer.New(zap.WarnLevel)
	log := zap.New(core)

	log.Warn("openai.websocket_proxy_failed", appendOpenAIAccountProxyLogFields(nil, account)...)

	entries := logs.All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.EqualValues(t, proxyID, fields["proxy_id"])
	require.Equal(t, "openai-egress", fields["proxy_name"])
	require.Equal(t, "proxy.example.com", fields["proxy_host"])
	require.EqualValues(t, 8443, fields["proxy_port"])
	require.NotContains(t, fields, "proxy_username")
	require.NotContains(t, fields, "proxy_password")
	require.NotContains(t, fields, "proxy_url")
}

// TestAppendOpenAIAccountProxyLogFields_FallsBackToProxyID 验证代理未预加载时仍保留可查询的 ID。
func TestAppendOpenAIAccountProxyLogFields_FallsBackToProxyID(t *testing.T) {
	proxyID := int64(23)
	core, logs := observer.New(zap.WarnLevel)
	log := zap.New(core)

	log.Warn("openai.websocket_proxy_failed", appendOpenAIAccountProxyLogFields(nil, &service.Account{ProxyID: &proxyID})...)

	entries := logs.All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.EqualValues(t, proxyID, fields["proxy_id"])
	require.Len(t, fields, 1)
}
