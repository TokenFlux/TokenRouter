package httpclient

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	"github.com/stretchr/testify/require"
)

func TestTLSFingerprintHTTPSProxyFallsBackWithoutBypassingProxy(t *testing.T) {
	proxyURL, err := url.Parse("https://user:pass@proxy.example:8443")
	require.NoError(t, err)
	roundTripper, err := buildUpstreamTransportWithTLSFingerprint(
		UpstreamSettings{},
		proxyURL,
		&tlsfingerprint.Profile{Name: "test"},
		TransportProtocol{CacheVariant: "default"},
	)
	require.NoError(t, err)
	transport, ok := roundTripper.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.Proxy)
	require.Nil(t, transport.DialTLSContext)
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "upstream.example"}}
	resolved, err := transport.Proxy(req)
	require.NoError(t, err)
	require.Equal(t, "https://user:pass@proxy.example:8443", resolved.String())
}

func TestResponseHeaderTimeoutRoundTripperTimesOut(t *testing.T) {
	transport := &responseHeaderTimeoutRoundTripper{
		base:    blockingHeaderRoundTripper{},
		timeout: 10 * time.Millisecond,
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com/v1/responses", nil)
	require.NoError(t, err)

	startedAt := time.Now()
	resp, err := transport.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout awaiting response headers")
	require.Less(t, time.Since(startedAt), time.Second)
}

type blockingHeaderRoundTripper struct{}

func (blockingHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 模拟上游迟迟不返回响应头，直到请求上下文被取消。
	<-req.Context().Done()
	return nil, req.Context().Err()
}
