package service

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestCoderOpenAIWSClientDialer_ProxyHTTPClientReuse(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	c1, err := impl.proxyHTTPClient("http://127.0.0.1:8080", nil)
	require.NoError(t, err)
	c2, err := impl.proxyHTTPClient("http://127.0.0.1:8080", nil)
	require.NoError(t, err)
	require.Same(t, c1, c2, "同一代理地址应复用同一个 HTTP 客户端")

	c3, err := impl.proxyHTTPClient("http://127.0.0.1:8081", nil)
	require.NoError(t, err)
	require.NotSame(t, c1, c3, "不同代理地址应分离客户端")
}

func TestCoderOpenAIWSClientDialer_ProxyHTTPClientInvalidURL(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	_, err := impl.proxyHTTPClient("://bad", nil)
	require.Error(t, err)
}

func TestCoderOpenAIWSClientDialer_TransportMetricsSnapshot(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	_, err := impl.proxyHTTPClient("http://127.0.0.1:18080", nil)
	require.NoError(t, err)
	_, err = impl.proxyHTTPClient("http://127.0.0.1:18080", nil)
	require.NoError(t, err)
	_, err = impl.proxyHTTPClient("http://127.0.0.1:18081", nil)
	require.NoError(t, err)

	snapshot := impl.SnapshotTransportMetrics()
	require.Equal(t, int64(1), snapshot.ProxyClientCacheHits)
	require.Equal(t, int64(2), snapshot.ProxyClientCacheMisses)
	require.InDelta(t, 1.0/3.0, snapshot.TransportReuseRatio, 0.0001)
}

func TestCoderOpenAIWSClientDialer_ProxyClientCacheCapacity(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	total := openAIWSProxyClientCacheMaxEntries + 32
	for i := 0; i < total; i++ {
		_, err := impl.proxyHTTPClient(fmt.Sprintf("http://127.0.0.1:%d", 20000+i), nil)
		require.NoError(t, err)
	}

	impl.proxyMu.Lock()
	cacheSize := len(impl.proxyClients)
	impl.proxyMu.Unlock()

	require.LessOrEqual(t, cacheSize, openAIWSProxyClientCacheMaxEntries, "代理客户端缓存应受容量上限约束")
}

func TestCoderOpenAIWSClientDialer_ProxyClientCacheIdleTTL(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	oldProxy := "http://127.0.0.1:28080"
	_, err := impl.proxyHTTPClient(oldProxy, nil)
	require.NoError(t, err)
	oldCacheKey := oldProxy + "|tls:none"

	impl.proxyMu.Lock()
	oldEntry := impl.proxyClients[oldCacheKey]
	require.NotNil(t, oldEntry)
	oldEntry.lastUsedUnixNano = time.Now().Add(-openAIWSProxyClientCacheIdleTTL - time.Minute).UnixNano()
	impl.proxyMu.Unlock()

	// 触发一次新的代理获取，驱动 TTL 清理。
	_, err = impl.proxyHTTPClient("http://127.0.0.1:28081", nil)
	require.NoError(t, err)

	impl.proxyMu.Lock()
	_, exists := impl.proxyClients[oldCacheKey]
	impl.proxyMu.Unlock()

	require.False(t, exists, "超过空闲 TTL 的代理客户端应被回收")
}

func TestCoderOpenAIWSClientDialer_ProxyTransportTLSHandshakeTimeout(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	client, err := impl.proxyHTTPClient("http://127.0.0.1:38080", nil)
	require.NoError(t, err)
	require.NotNil(t, client)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport)
	require.Equal(t, 10*time.Second, transport.TLSHandshakeTimeout)
}

func TestBuildOpenAIWSHTTPTransport_TLSProfileForcesHTTP1(t *testing.T) {
	t.Run("without h2", func(t *testing.T) {
		transport, err := buildOpenAIWSHTTPTransport("", nil, &tlsfingerprint.Profile{Name: "h1"})
		require.NoError(t, err)
		require.False(t, transport.ForceAttemptHTTP2)
		require.NotNil(t, transport.TLSNextProto)
	})

	t.Run("with h2", func(t *testing.T) {
		profile := &tlsfingerprint.Profile{
			Name:          "h2",
			ALPNProtocols: []string{"h2", "http/1.1"},
		}
		transport, err := buildOpenAIWSHTTPTransport("", nil, profile)
		require.NoError(t, err)
		require.False(t, transport.ForceAttemptHTTP2)
		require.NotNil(t, transport.TLSNextProto)
		require.False(t, tlsfingerprint.SupportsHTTP2(tlsfingerprint.HTTP1OnlyProfile(profile)))
	})
}

func TestCoderOpenAIWSClientDialer_ProxyHTTPClientCacheUsesHTTP1TLSProfile(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer()
	impl, ok := dialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)

	profile := &tlsfingerprint.Profile{Name: "h2", ALPNProtocols: []string{"h2", "http/1.1"}}
	client, err := impl.proxyHTTPClient("http://127.0.0.1:48080", profile)
	require.NoError(t, err)
	require.NotNil(t, client)

	wsProfileKey := tlsfingerprint.CacheKey(tlsfingerprint.HTTP1OnlyProfile(profile))
	rawProfileKey := tlsfingerprint.CacheKey(profile)
	require.NotEqual(t, rawProfileKey, wsProfileKey)

	impl.proxyMu.Lock()
	_, hasWSKey := impl.proxyClients["http://127.0.0.1:48080|tls:"+wsProfileKey]
	_, hasRawKey := impl.proxyClients["http://127.0.0.1:48080|tls:"+rawProfileKey]
	impl.proxyMu.Unlock()

	require.True(t, hasWSKey, "WS 缓存键应使用剥离 h2 后的 TLS 模板")
	require.False(t, hasRawKey, "WS 缓存键不应使用仍声明 h2 的 TLS 模板")
}

func TestCoderOpenAIWSClientConn_DoesNotSupportIdlePingWithoutReader(t *testing.T) {
	require.False(t, (&coderOpenAIWSClientConn{}).SupportsIdlePingWithoutReader())
}
