package openai

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	proxyurl "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/wsrelay"
	coderws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const WSMessageReadLimitBytes int64 = 16 * 1024 * 1024
const (
	openAIWSProxyTransportMaxIdleConns        = 128
	openAIWSProxyTransportMaxIdleConnsPerHost = 64
	openAIWSProxyTransportIdleConnTimeout     = 90 * time.Second
	openAIWSProxyClientCacheMaxEntries        = 256
	openAIWSProxyClientCacheIdleTTL           = 15 * time.Minute
)

type WSTransportMetricsSnapshot struct {
	ProxyClientCacheHits   int64   `json:"proxy_client_cache_hits"`
	ProxyClientCacheMisses int64   `json:"proxy_client_cache_misses"`
	TransportReuseRatio    float64 `json:"transport_reuse_ratio"`
}

// WSClientConn 抽象 WS 客户端连接，便于替换底层实现。
type WSClientConn interface {
	WriteJSON(ctx context.Context, value any) error
	ReadMessage(ctx context.Context) ([]byte, error)
	Ping(ctx context.Context) error
	Close() error
}

// WSIdlePingCapable 与 WSClientConn 分离，显式标记实现能否在无人读取时探测空闲连接。
type WSIdlePingCapable interface {
	SupportsIdlePingWithoutReader() bool
}

// WSClientDialer 抽象 WS 建连器。
type WSClientDialer interface {
	Dial(ctx context.Context, wsURL string, headers http.Header, proxyURL string, profile *tlsfingerprint.Profile) (WSClientConn, int, http.Header, error)
}

type WSTransportMetricsDialer interface {
	SnapshotTransportMetrics() WSTransportMetricsSnapshot
}

func NewDefaultWSClientDialer() WSClientDialer {
	return &CoderWSClientDialer{
		proxyClients: make(map[string]*openAIWSProxyClientEntry),
	}
}

type CoderWSClientDialer struct {
	proxyMu      sync.Mutex
	proxyClients map[string]*openAIWSProxyClientEntry
	proxyHits    atomic.Int64
	proxyMisses  atomic.Int64
}

// WSHandshakeError 保留有界且不写日志的握手错误体，用于区分 task 失效与其它 401。
type WSHandshakeError struct {
	Body []byte
	Err  error
}

func (e *WSHandshakeError) Error() string {
	if e == nil || e.Err == nil {
		return "openai ws handshake failed"
	}
	return e.Err.Error()
}

func (e *WSHandshakeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type openAIWSProxyClientEntry struct {
	client           *http.Client
	lastUsedUnixNano int64
}

func (d *CoderWSClientDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (WSClientConn, int, http.Header, error) {
	targetURL := strings.TrimSpace(wsURL)
	if targetURL == "" {
		return nil, 0, nil, errors.New("ws url is empty")
	}

	opts := &coderws.DialOptions{
		HTTPHeader:      upstream.CloneHeader(headers),
		CompressionMode: coderws.CompressionContextTakeover,
	}
	if profile != nil || strings.TrimSpace(proxyURL) != "" {
		proxyClient, err := d.ProxyHTTPClient(proxyURL, profile)
		if err != nil {
			return nil, 0, nil, err
		}
		opts.HTTPClient = proxyClient
	}

	conn, resp, err := coderws.Dial(ctx, targetURL, opts)
	if err != nil {
		status := 0
		respHeaders := http.Header(nil)
		if resp != nil {
			status = resp.StatusCode
			respHeaders = upstream.CloneHeader(resp.Header)
		}
		var body []byte
		if resp != nil && resp.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
		}
		return nil, status, respHeaders, &WSHandshakeError{Body: body, Err: err}
	}
	// coder/websocket 默认单消息读取上限为 32KB，Codex WS 事件（如 rate_limits/大 delta）
	// 可能超过该阈值，需显式提高上限，避免本地 read_fail(message too big)。
	conn.SetReadLimit(WSMessageReadLimitBytes)
	respHeaders := http.Header(nil)
	if resp != nil {
		respHeaders = upstream.CloneHeader(resp.Header)
	}
	return &coderOpenAIWSClientConn{conn: conn}, 0, respHeaders, nil
}

func (d *CoderWSClientDialer) ProxyHTTPClient(proxy string, profile *tlsfingerprint.Profile) (*http.Client, error) {
	if d == nil {
		return nil, errors.New("openai ws dialer is nil")
	}
	normalizedProxy, parsedProxyURL, err := proxyurl.Parse(proxy)
	if err != nil {
		return nil, err
	}
	if normalizedProxy == "" && profile == nil {
		return nil, errors.New("proxy url is empty")
	}
	// WebSocket 使用 HTTP/1.1 Upgrade，TLS ALPN 不能声明 h2，避免 TLS 协商和后续请求协议不一致。
	profile = tlsfingerprint.HTTP1OnlyProfile(profile)
	profileKey := tlsfingerprint.CacheKey(profile)
	cacheKey := normalizedProxy + "|tls:" + profileKey
	now := time.Now().UnixNano()

	d.proxyMu.Lock()
	defer d.proxyMu.Unlock()
	if entry, ok := d.proxyClients[cacheKey]; ok && entry != nil && entry.client != nil {
		entry.lastUsedUnixNano = now
		d.proxyHits.Add(1)
		return entry.client, nil
	}
	d.cleanupProxyClientsLocked(now)
	transport, err := buildOpenAIWSHTTPTransport(normalizedProxy, parsedProxyURL, profile)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport}
	d.proxyClients[cacheKey] = &openAIWSProxyClientEntry{
		client:           client,
		lastUsedUnixNano: now,
	}
	d.ensureProxyClientCapacityLocked()
	d.proxyMisses.Add(1)
	return client, nil
}

func buildOpenAIWSHTTPTransport(normalizedProxy string, parsedProxyURL *url.URL, profile *tlsfingerprint.Profile) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:        openAIWSProxyTransportMaxIdleConns,
		MaxIdleConnsPerHost: openAIWSProxyTransportMaxIdleConnsPerHost,
		IdleConnTimeout:     openAIWSProxyTransportIdleConnTimeout,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   false,
		// WebSocket 建连固定是 HTTP/1.1 Upgrade，显式关闭自动 HTTP/2 协商。
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	profile = tlsfingerprint.HTTP1OnlyProfile(profile)
	if profile == nil {
		if parsedProxyURL != nil {
			transport.Proxy = http.ProxyURL(parsedProxyURL)
		}
		return transport, nil
	}

	// 使用自定义 DialTLSContext，让 coder/websocket 的 wss 握手复用 TLS 指纹伪装。
	switch {
	case parsedProxyURL == nil:
		dialer := tlsfingerprint.NewDialer(profile, nil)
		transport.DialTLSContext = dialer.DialTLSContext
	case parsedProxyURL.Scheme == "socks5" || parsedProxyURL.Scheme == "socks5h":
		dialer := tlsfingerprint.NewSOCKS5ProxyDialer(profile, parsedProxyURL)
		transport.DialTLSContext = dialer.DialTLSContext
	case parsedProxyURL.Scheme == "http" || parsedProxyURL.Scheme == "https":
		dialer := tlsfingerprint.NewHTTPProxyDialer(profile, parsedProxyURL)
		transport.DialTLSContext = dialer.DialTLSContext
	default:
		return nil, fmt.Errorf("unsupported proxy URL for OpenAI WS TLS fingerprint: %s", normalizedProxy)
	}
	return transport, nil
}

func (d *CoderWSClientDialer) cleanupProxyClientsLocked(nowUnixNano int64) {
	if d == nil || len(d.proxyClients) == 0 {
		return
	}
	idleTTL := openAIWSProxyClientCacheIdleTTL
	if idleTTL <= 0 {
		return
	}
	now := time.Unix(0, nowUnixNano)
	for key, entry := range d.proxyClients {
		if entry == nil || entry.client == nil {
			delete(d.proxyClients, key)
			continue
		}
		lastUsed := time.Unix(0, entry.lastUsedUnixNano)
		if now.Sub(lastUsed) > idleTTL {
			closeOpenAIWSProxyClient(entry.client)
			delete(d.proxyClients, key)
		}
	}
}

func (d *CoderWSClientDialer) ensureProxyClientCapacityLocked() {
	if d == nil {
		return
	}
	maxEntries := openAIWSProxyClientCacheMaxEntries
	if maxEntries <= 0 {
		return
	}
	for len(d.proxyClients) > maxEntries {
		var oldestKey string
		var oldestLastUsed int64
		hasOldest := false
		for key, entry := range d.proxyClients {
			lastUsed := int64(0)
			if entry != nil {
				lastUsed = entry.lastUsedUnixNano
			}
			if !hasOldest || lastUsed < oldestLastUsed {
				hasOldest = true
				oldestKey = key
				oldestLastUsed = lastUsed
			}
		}
		if !hasOldest {
			return
		}
		if entry := d.proxyClients[oldestKey]; entry != nil {
			closeOpenAIWSProxyClient(entry.client)
		}
		delete(d.proxyClients, oldestKey)
	}
}

func closeOpenAIWSProxyClient(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
		transport.CloseIdleConnections()
	}
}

func (d *CoderWSClientDialer) SnapshotTransportMetrics() WSTransportMetricsSnapshot {
	if d == nil {
		return WSTransportMetricsSnapshot{}
	}
	hits := d.proxyHits.Load()
	misses := d.proxyMisses.Load()
	total := hits + misses
	reuseRatio := 0.0
	if total > 0 {
		reuseRatio = float64(hits) / float64(total)
	}
	return WSTransportMetricsSnapshot{
		ProxyClientCacheHits:   hits,
		ProxyClientCacheMisses: misses,
		TransportReuseRatio:    reuseRatio,
	}
}

type coderOpenAIWSClientConn struct {
	conn *coderws.Conn
}

var _ openaiwsv2.FrameConn = (*coderOpenAIWSClientConn)(nil)

func (c *coderOpenAIWSClientConn) WriteJSON(ctx context.Context, value any) error {
	if c == nil || c.conn == nil {
		return ErrWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return wsjson.Write(ctx, c.conn, value)
}

func (c *coderOpenAIWSClientConn) ReadMessage(ctx context.Context) ([]byte, error) {
	if c == nil || c.conn == nil {
		return nil, ErrWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	msgType, payload, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	switch msgType {
	case coderws.MessageText, coderws.MessageBinary:
		return payload, nil
	default:
		return nil, ErrWSConnClosed
	}
}

func (c *coderOpenAIWSClientConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.conn == nil {
		return coderws.MessageText, nil, ErrWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	msgType, payload, err := c.conn.Read(ctx)
	if err != nil {
		return coderws.MessageText, nil, err
	}
	return msgType, payload, nil
}

func (c *coderOpenAIWSClientConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.conn == nil {
		return ErrWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.conn.Write(ctx, msgType, payload)
}

func (c *coderOpenAIWSClientConn) Ping(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return ErrWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.conn.Ping(ctx)
}

// SupportsIdlePingWithoutReader 反映 coder/websocket 的实际契约：Conn.Ping 会等待 pong，
// 而控制帧只能由 Read 消费。连接池不会读取空闲连接，因此 Ping 会让健康连接必然超时。
func (*coderOpenAIWSClientConn) SupportsIdlePingWithoutReader() bool {
	return false
}

func (c *coderOpenAIWSClientConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	// Close 为幂等，忽略重复关闭错误。
	_ = c.conn.Close(coderws.StatusNormalClosure, "")
	_ = c.conn.CloseNow()
	return nil
}

// ErrWSConnClosed 与连接池共用同一错误身份。
var ErrWSConnClosed = errors.New("openai ws connection closed")
