package transport

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// roundTripFunc 让测试同步观察每请求传输，不建立外部连接。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// newLocalTestServer 保留原环境限制检测；无法监听时明确标记跳过。
func newLocalTestServer(tb testing.TB, handler http.Handler) *httptest.Server {
	tb.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Skipf("local listeners are not permitted in this environment: %v", err)
	}
	require.NoError(tb, listener.Close())
	return httptest.NewServer(handler)
}

// testUpstreamPool 仍执行每请求客户端适配与结果通知。
type testUpstreamPool struct {
	transport http.RoundTripper
}

func (p testUpstreamPool) Do(req *http.Request, opts httpclient.UpstreamRequestOptions) (*http.Response, error) {
	client := &http.Client{Transport: p.transport, CheckRedirect: opts.CheckRedirect}
	if opts.PrepareClient != nil {
		client = opts.PrepareClient(client)
	}
	resp, err := client.Do(req)
	if opts.ObserveResult != nil {
		opts.ObserveResult(err)
	}
	return resp, err
}

// upstreamClientView 只保存通过公开执行接口观察到的客户端与所选技术标识。
type upstreamClientView struct {
	tlsProfile   *tlsfingerprint.Profile
	client       *http.Client
	proxyKey     string
	protocolMode string
}

func (s *Client) getClientEntry(proxyURL string, accountID int64, concurrency int, profile upstream.HTTPUpstreamProfile, _, _ bool) (*upstreamClientView, error) {
	return s.inspectClient(proxyURL, accountID, concurrency, profile, nil)
}
func (s *Client) getClientEntryWithTLS(
	proxyURL string,
	accountID int64,
	concurrency int,
	tlsProfile *tlsfingerprint.Profile,
	profile upstream.HTTPUpstreamProfile,
	_, _ bool,
) (*upstreamClientView, error) {
	return s.inspectClient(proxyURL, accountID, concurrency, profile, tlsProfile)
}
func (s *Client) inspectClient(
	proxyURL string,
	accountID int64,
	concurrency int,
	profile upstream.HTTPUpstreamProfile,
	tlsProfile *tlsfingerprint.Profile,
) (*upstreamClientView, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), profile))
	opts, err := s.transportOptions(req, proxyURL, accountID, concurrency, tlsProfile)
	if err != nil {
		return nil, err
	}
	key, _, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	view := &upstreamClientView{proxyKey: key, protocolMode: opts.Protocol.CacheVariant, tlsProfile: opts.TLSProfile}
	opts.PrepareClient = func(client *http.Client) *http.Client {
		view.client = client
		clone := *client
		clone.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
		})
		return &clone
	}
	resp, err := s.pool.Do(req, opts)
	if err != nil {
		return nil, err
	}
	if err = resp.Body.Close(); err != nil {
		return nil, err
	}
	return view, nil
}
func mustGetOrCreateClient(t *testing.T, svc *Client, proxyURL string, accountID int64, concurrency int) *upstreamClientView {
	t.Helper()
	entry, err := svc.inspectClient(proxyURL, accountID, concurrency, upstream.HTTPUpstreamProfileDefault, nil)
	require.NoError(t, err)
	return entry
}
