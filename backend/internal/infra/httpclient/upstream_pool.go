// 本文件拥有上游连接池及响应释放机制；平台选择和回退由调用方提供技术快照。
package httpclient

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/http2"
)

const (
	defaultUpstreamDialTimeout         = 10 * time.Second
	defaultUpstreamDialKeepAlive       = 30 * time.Second
	defaultUpstreamTLSHandshakeTimeout = 10 * time.Second
	http2ReadIdleTimeout               = 15 * time.Second
	http2PingTimeout                   = 15 * time.Second
)

// ErrUpstreamClientLimitReached 表示所有缓存条目仍被在途请求占用。
var ErrUpstreamClientLimitReached = errors.New("upstream client cache limit reached")

// TransportProtocol 只描述传输能力；CacheVariant 保留调用方已有的隔离标识。
type TransportProtocol struct {
	CacheVariant string
	HTTP2        bool
	DisableHTTP2 bool
}

// UpstreamRequestOptions 是一次执行使用的技术参数快照，不持有全局配置或业务实体。
type UpstreamRequestOptions struct {
	ProxyURL      string
	AccountID     int64
	Isolation     string
	MaxClients    int
	IdleTTL       time.Duration
	Settings      UpstreamSettings
	Protocol      TransportProtocol
	TLSProfile    *tlsfingerprint.Profile
	CheckRedirect func(*http.Request, []*http.Request) error
	// PrepareClient 仅派生当前请求使用的客户端，不修改缓存客户端。
	PrepareClient func(*http.Client) *http.Client
	// ObserveResult 在拿到响应头或执行失败时通知外层，早于释放或响应体处理。
	ObserveResult func(error)
}

// UpstreamPool 唯一持有普通和指纹上游客户端的缓存与在途计数。
type UpstreamPool struct {
	mu      sync.RWMutex
	clients map[string]*upstreamClientEntry
}

// NewUpstreamPool 创建独立的上游池，不与通用或 req 客户端池合并状态。
func NewUpstreamPool() *UpstreamPool {
	return &UpstreamPool{clients: make(map[string]*upstreamClientEntry)}
}

// Do 闭合获取、请求执行、解压及释放；成功响应仍由调用方关闭 Body。
// @project-doc docs/operations/upstream_transport_security.md#upstream_client_pool
func (s *UpstreamPool) Do(req *http.Request, opts UpstreamRequestOptions) (*http.Response, error) {
	entry, err := s.acquire(opts)
	if err != nil {
		return nil, err
	}
	release := func() {
		atomic.AddInt64(&entry.inFlight, -1)
		atomic.StoreInt64(&entry.lastUsed, time.Now().UnixNano())
	}
	client := entry.client
	if opts.PrepareClient != nil {
		client = opts.PrepareClient(client)
	}
	if req != nil {
		req = req.WithContext(servertiming.BeginHTTPTrace(req.Context()))
	}
	resp, err := servertiming.Do(client, req)
	if opts.ObserveResult != nil {
		opts.ObserveResult(err)
	}
	if err != nil {
		release()
		return nil, err
	}
	decompressResponseBody(resp)
	resp.Body = wrapTrackedBody(resp.Body, release)
	return resp, nil
}

// UpstreamSettings 描述已解析的连接池与响应头超时，不持有配置或平台实体。
type UpstreamSettings struct {
	MaxIdleConns          int           // 最大空闲连接总数
	MaxIdleConnsPerHost   int           // 每主机最大空闲连接数
	MaxConnsPerHost       int           // 每主机最大连接数（含活跃）
	IdleConnTimeout       time.Duration // 空闲连接超时时间
	ResponseHeaderTimeout time.Duration // 等待响应头超时时间
}

// upstreamClientEntry 上游客户端缓存条目
// 记录客户端实例及其元数据，用于连接池管理和淘汰策略
type upstreamClientEntry struct {
	client       *http.Client // HTTP 客户端实例
	proxyKey     string       // 代理标识（用于检测代理变更）
	poolKey      string       // 连接池配置标识（用于检测配置变更）
	protocolMode string       // 协议模式（default/openai_h1/openai_h2/openai_h1_fallback）
	lastUsed     int64        // 最后使用时间戳（纳秒），用于 LRU 淘汰
	inFlight     int64        // 当前进行中的请求数，>0 时不可淘汰
}

// acquire 保持读锁快速路径、写锁重查和只逐出空闲条目的原有顺序。
func (s *UpstreamPool) acquire(opts UpstreamRequestOptions) (*upstreamClientEntry, error) {
	isolation := opts.Isolation
	proxyKey, parsedProxy, err := proxy.NormalizePoolKey(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	settings := opts.Settings
	protocolMode := opts.Protocol.CacheVariant
	cacheKey := buildCacheKey(isolation, proxyKey, opts.AccountID, protocolMode)
	poolKey := buildPoolKey(settings, protocolMode)
	if opts.TLSProfile != nil {
		profileKey := tlsfingerprint.CacheKey(opts.TLSProfile)
		cacheKey = "tls:" + profileKey + ":" + cacheKey
		poolKey += ":tls:" + profileKey
	}
	now := time.Now()
	nowUnix := now.UnixNano()

	// 读锁快速路径：命中缓存直接返回，减少锁竞争
	s.mu.RLock()
	if entry, ok := s.clients[cacheKey]; ok && s.shouldReuseEntry(entry, isolation, proxyKey, poolKey) {
		atomic.StoreInt64(&entry.lastUsed, nowUnix)
		{
			atomic.AddInt64(&entry.inFlight, 1)
		}
		s.mu.RUnlock()
		return entry, nil
	}
	s.mu.RUnlock()

	// 写锁慢路径：创建或重建客户端
	s.mu.Lock()
	if entry, ok := s.clients[cacheKey]; ok {
		if s.shouldReuseEntry(entry, isolation, proxyKey, poolKey) {
			atomic.StoreInt64(&entry.lastUsed, nowUnix)
			{
				atomic.AddInt64(&entry.inFlight, 1)
			}
			s.mu.Unlock()
			return entry, nil
		}
		s.removeClientLocked(cacheKey, entry)
	}

	// 超出缓存上限时尝试淘汰，无法淘汰则拒绝新建
	if opts.MaxClients > 0 {
		s.evictIdleLocked(now, opts.IdleTTL)
		if len(s.clients) >= opts.MaxClients {
			if !s.evictOldestIdleLocked() {
				s.mu.Unlock()
				return nil, ErrUpstreamClientLimitReached
			}
		}
	}

	// 缓存未命中或需要重建，创建新客户端
	var transport http.RoundTripper
	if opts.TLSProfile == nil {
		transport, err = buildUpstreamTransport(settings, parsedProxy, opts.Protocol)
	} else {
		transport, err = buildUpstreamTransportWithTLSFingerprint(settings, parsedProxy, opts.TLSProfile, opts.Protocol)
	}
	if err != nil {
		s.mu.Unlock()
		if opts.TLSProfile != nil {
			return nil, fmt.Errorf("build TLS fingerprint transport: %w", err)
		}
		return nil, fmt.Errorf("build transport: %w", err)
	}
	client := &http.Client{Transport: transport}
	client.CheckRedirect = opts.CheckRedirect
	entry := &upstreamClientEntry{
		client:       client,
		proxyKey:     proxyKey,
		poolKey:      poolKey,
		protocolMode: protocolMode,
	}
	atomic.StoreInt64(&entry.lastUsed, nowUnix)
	{
		atomic.StoreInt64(&entry.inFlight, 1)
	}
	s.clients[cacheKey] = entry

	// 执行淘汰策略：先淘汰空闲超时的，再淘汰超出数量限制的
	s.evictIdleLocked(now, opts.IdleTTL)
	s.evictOverLimitLocked(opts.MaxClients)
	s.mu.Unlock()
	return entry, nil
}

// shouldReuseEntry 判断缓存条目是否可复用
// 若代理或连接池配置发生变化，则需要重建客户端
func (s *UpstreamPool) shouldReuseEntry(entry *upstreamClientEntry, isolation, proxyKey, poolKey string) bool {
	if entry == nil {
		return false
	}
	if isolation == "account" && entry.proxyKey != proxyKey {
		return false
	}
	if entry.poolKey != poolKey {
		return false
	}
	return true
}

// removeClientLocked 移除客户端（需持有锁）
// 从缓存中删除并关闭空闲连接
//
// 参数:
//   - key: 缓存键
//   - entry: 客户端条目
func (s *UpstreamPool) removeClientLocked(key string, entry *upstreamClientEntry) {
	delete(s.clients, key)
	if entry != nil && entry.client != nil {
		// 关闭空闲连接，释放系统资源
		// 注意：这不会中断活跃连接
		entry.client.CloseIdleConnections()
		if closer, ok := entry.client.Transport.(interface {
			CloseIdleConnections()
		}); ok {
			closer.CloseIdleConnections()
		}
	}
}

// evictIdleLocked 淘汰空闲超时的客户端（需持有锁）
// 遍历所有客户端，移除超过 TTL 且无活跃请求的条目
//
// 参数:
//   - now: 当前时间
func (s *UpstreamPool) evictIdleLocked(now time.Time, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	// 计算淘汰截止时间
	cutoff := now.Add(-ttl).UnixNano()
	for key, entry := range s.clients {
		// 跳过有活跃请求的客户端
		if atomic.LoadInt64(&entry.inFlight) != 0 {
			continue
		}
		// 淘汰超时的空闲客户端
		if atomic.LoadInt64(&entry.lastUsed) <= cutoff {
			s.removeClientLocked(key, entry)
		}
	}
}

// evictOldestIdleLocked 淘汰最久未使用且无活跃请求的客户端（需持有锁）
func (s *UpstreamPool) evictOldestIdleLocked() bool {
	var (
		oldestKey   string
		oldestEntry *upstreamClientEntry
		oldestTime  int64
	)
	// 查找最久未使用且无活跃请求的客户端
	for key, entry := range s.clients {
		// 跳过有活跃请求的客户端
		if atomic.LoadInt64(&entry.inFlight) != 0 {
			continue
		}
		lastUsed := atomic.LoadInt64(&entry.lastUsed)
		if oldestEntry == nil || lastUsed < oldestTime {
			oldestKey = key
			oldestEntry = entry
			oldestTime = lastUsed
		}
	}
	// 所有客户端都有活跃请求，无法淘汰
	if oldestEntry == nil {
		return false
	}
	s.removeClientLocked(oldestKey, oldestEntry)
	return true
}

// evictOverLimitLocked 淘汰超出数量限制的客户端（需持有锁）
// 使用 LRU 策略，优先淘汰最久未使用且无活跃请求的客户端
func (s *UpstreamPool) evictOverLimitLocked(maxClients int) bool {
	if maxClients <= 0 {
		return false
	}
	evicted := false
	// 循环淘汰直到满足数量限制
	for len(s.clients) > maxClients {
		if !s.evictOldestIdleLocked() {
			return evicted
		}
		evicted = true
	}
	return evicted
}

// buildPoolKey 构建连接池配置键，用于检测连接池配置变更。
func buildPoolKey(settings UpstreamSettings, protocolMode string) string {
	base := fmt.Sprintf(
		"idle:%d|idle_host:%d|max:%d|idle_timeout:%s|header_timeout:%s",
		settings.MaxIdleConns,
		settings.MaxIdleConnsPerHost,
		settings.MaxConnsPerHost,
		settings.IdleConnTimeout,
		settings.ResponseHeaderTimeout,
	)
	if protocolMode == "" || protocolMode == "default" {
		return base
	}
	return base + "|proto:" + protocolMode
}

// buildCacheKey 构建客户端缓存键
// 根据隔离策略决定缓存键的组成
//
// 参数:
//   - isolation: 隔离模式
//   - proxyKey: 代理标识
//   - accountID: 账户 ID
//
// 返回:
//   - string: 缓存键
//
// 缓存键格式:
//   - proxy 模式: "proxy:{proxyKey}"
//   - account 模式: "account:{accountID}"
//   - account_proxy 模式: "account:{accountID}|proxy:{proxyKey}"
func buildCacheKey(isolation, proxyKey string, accountID int64, protocolMode string) string {
	var base string
	switch isolation {
	case "account":
		base = fmt.Sprintf("account:%d", accountID)
	case "account_proxy":
		base = fmt.Sprintf("account:%d|proxy:%s", accountID, proxyKey)
	default:
		base = fmt.Sprintf("proxy:%s", proxyKey)
	}
	if protocolMode != "" && protocolMode != "default" {
		base += "|proto:" + protocolMode
	}
	return base
}

// newUpstreamDialer 构建上游 Transport 的 TCP dialer。
//
// 必须显式提供：http.Transport 的 DialContext 为 nil 时使用零值 net.Dialer，
// 建连没有任何超时上限，只能等内核 TCP 重传耗尽（Linux 约 130 秒）。
func newUpstreamDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   defaultUpstreamDialTimeout,
		KeepAlive: defaultUpstreamDialKeepAlive,
	}
}

// buildUpstreamTransport 构建上游请求的 Transport
// 使用配置文件中的连接池参数，支持生产环境调优
//
// 参数:
//   - settings: 连接池配置
//   - proxyURL: 代理 URL（nil 表示直连）
//
// 返回:
//   - *http.Transport: 配置好的 Transport 实例
//   - error: 代理配置错误
//
// Transport 参数说明:
//   - DialContext: DNS 解析 + TCP 建连超时（不设置则无上限，退化为内核默认重传）
//   - TLSHandshakeTimeout: TLS 握手超时
//   - MaxIdleConns: 所有主机的最大空闲连接总数
//   - MaxIdleConnsPerHost: 每主机最大空闲连接数（影响连接复用率）
//   - MaxConnsPerHost: 每主机最大连接数（达到后新请求等待）
//   - IdleConnTimeout: 空闲连接超时（超时后关闭）
//   - ResponseHeaderTimeout: 等待响应头超时（不影响流式传输）
func buildUpstreamTransport(settings UpstreamSettings, proxyURL *url.URL, protocol TransportProtocol) (*http.Transport, error) {
	transport := &http.Transport{
		DialContext:           newUpstreamDialer().DialContext,
		TLSHandshakeTimeout:   defaultUpstreamTLSHandshakeTimeout,
		MaxIdleConns:          settings.MaxIdleConns,
		MaxIdleConnsPerHost:   settings.MaxIdleConnsPerHost,
		MaxConnsPerHost:       settings.MaxConnsPerHost,
		IdleConnTimeout:       settings.IdleConnTimeout,
		ResponseHeaderTimeout: settings.ResponseHeaderTimeout,
	}
	switch {
	case protocol.HTTP2:
		transport.ForceAttemptHTTP2 = true
		// 显式配置 http2 并启用 PING 健康探测，剔除代理/NAT 静默掐断的死连接，
		// 避免请求挂在死连接上直到 TCP 重传超时（分钟级）。
		if _, err := enableHTTP2KeepAlive(transport); err != nil {
			return nil, err
		}
	case protocol.DisableHTTP2:
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	if err := proxy.ConfigureTransportProxy(transport, proxyURL); err != nil {
		return nil, err
	}
	return transport, nil
}

func enableHTTP2KeepAlive(transport *http.Transport) (*http2.Transport, error) {
	h2, err := http2.ConfigureTransports(transport)
	if err != nil {
		return nil, err
	}
	if h2 != nil {
		h2.ReadIdleTimeout = http2ReadIdleTimeout
		h2.PingTimeout = http2PingTimeout
	}
	return h2, nil
}

// buildUpstreamTransportWithTLSFingerprint 构建带 TLS 指纹伪装的 RoundTripper
// 使用 utls 库模拟 Claude CLI 的 TLS 指纹
//
// 参数:
//   - settings: 连接池配置
//   - proxyURL: 代理 URL（nil 表示直连）
//   - profile: TLS 指纹配置
//
// 返回:
//   - http.RoundTripper: 配置好的上游传输实例
//   - error: 配置错误
//
// 代理类型处理:
//   - nil/空: 直连，使用 TLSFingerprintDialer
//   - http: HTTP 代理，使用 HTTPProxyDialer（CONNECT 隧道 + utls 握手）
//   - https: 指纹拨号器不支持 TLS 代理，回退普通 transport
//   - socks5: SOCKS5 代理，使用 SOCKS5ProxyDialer（SOCKS5 隧道 + utls 握手）
func buildUpstreamTransportWithTLSFingerprint(settings UpstreamSettings, proxyURL *url.URL, profile *tlsfingerprint.Profile, protocol TransportProtocol) (http.RoundTripper, error) {
	useHTTP2 := protocol.HTTP2 && tlsfingerprint.SupportsHTTP2(profile)
	transport := &http.Transport{
		MaxIdleConns:          settings.MaxIdleConns,
		MaxIdleConnsPerHost:   settings.MaxIdleConnsPerHost,
		MaxConnsPerHost:       settings.MaxConnsPerHost,
		IdleConnTimeout:       settings.IdleConnTimeout,
		ResponseHeaderTimeout: settings.ResponseHeaderTimeout,
		// 使用自定义 DialTLSContext；OpenAI/Codex 模板 ALPN 声明 h2 时才交给 HTTP/2 RoundTripper。
		ForceAttemptHTTP2: useHTTP2,
	}

	// 根据代理类型选择合适的 TLS 指纹 Dialer
	var dialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error)
	if proxyURL == nil {
		// 直连：使用 TLSFingerprintDialer
		slog.Debug("tls_fingerprint_transport_direct")
		dialer := tlsfingerprint.NewDialer(profile, nil)
		dialTLSContext = dialer.DialTLSContext
	} else {
		scheme := strings.ToLower(proxyURL.Scheme)
		switch scheme {
		case "socks5", "socks5h":
			// SOCKS5 代理：使用 SOCKS5ProxyDialer
			slog.Debug("tls_fingerprint_transport_socks5", "proxy", proxyURL.Host)
			socks5Dialer := tlsfingerprint.NewSOCKS5ProxyDialer(profile, proxyURL)
			dialTLSContext = socks5Dialer.DialTLSContext
		case "https":
			// 指纹拨号器发送明文 CONNECT 前导，无法与 HTTPS 代理建 TLS，因此保留普通代理路由。
			return buildUpstreamTransport(settings, proxyURL, TransportProtocol{CacheVariant: "default"})
		case "http":
			// HTTP/HTTPS 代理：使用 HTTPProxyDialer（CONNECT 隧道）
			slog.Debug("tls_fingerprint_transport_http_connect", "proxy", proxyURL.Host)
			httpDialer := tlsfingerprint.NewHTTPProxyDialer(profile, proxyURL)
			dialTLSContext = httpDialer.DialTLSContext
		default:
			// 未知代理类型，回退到普通代理配置（无 TLS 指纹）
			slog.Debug("tls_fingerprint_transport_unknown_scheme_fallback", "scheme", scheme)
			if err := proxy.ConfigureTransportProxy(transport, proxyURL); err != nil {
				return nil, err
			}
		}
	}
	transport.DialTLSContext = dialTLSContext

	if useHTTP2 && dialTLSContext != nil {
		h2Transport := &http2.Transport{
			IdleConnTimeout: settings.IdleConnTimeout,
			ReadIdleTimeout: http2ReadIdleTimeout,
			PingTimeout:     http2PingTimeout,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				conn, err := dialTLSContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				negotiatedProtocol := tlsfingerprint.NegotiatedProtocol(conn)
				if negotiatedProtocol != "h2" {
					_ = conn.Close()
					return nil, fmt.Errorf("http2: unexpected ALPN protocol %q; want %q", negotiatedProtocol, "h2")
				}
				return conn, nil
			},
		}
		var roundTripper http.RoundTripper = h2Transport
		if settings.ResponseHeaderTimeout > 0 {
			roundTripper = &responseHeaderTimeoutRoundTripper{
				base:    h2Transport,
				timeout: settings.ResponseHeaderTimeout,
			}
		}
		return roundTripper, nil
	}

	return transport, nil
}

var errResponseHeaderTimeout = &responseHeaderTimeoutError{}

type responseHeaderTimeoutError struct{}

func (e *responseHeaderTimeoutError) Error() string {
	return "net/http: timeout awaiting response headers"
}

func (e *responseHeaderTimeoutError) Timeout() bool {
	return true
}

func (e *responseHeaderTimeoutError) Temporary() bool {
	return true
}

// responseHeaderTimeoutRoundTripper 为裸 http2.Transport 补齐等待响应头超时。
// 请求拿到响应头后会停止计时，避免长时间 SSE 响应在读取 Body 阶段被误取消。
type responseHeaderTimeoutRoundTripper struct {
	base    http.RoundTripper
	timeout time.Duration
}

// RoundTrip 只接受官方 CLI 身份、OAuth Bearer 和明确 Access denied 响应作为回退候选。
func (r *responseHeaderTimeoutRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if r == nil || r.base == nil {
		return nil, errors.New("response header timeout round tripper missing base")
	}
	if r.timeout <= 0 || req == nil {
		return r.base.RoundTrip(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	timeoutCh := make(chan struct{})
	timedOut := atomic.Bool{}
	timer := time.AfterFunc(r.timeout, func() {
		timedOut.Store(true)
		close(timeoutCh)
		cancel()
	})
	resp, err := r.base.RoundTrip(req.WithContext(ctx))
	if !timer.Stop() {
		<-timeoutCh
	}
	if timedOut.Load() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		cancel()
		return nil, errResponseHeaderTimeout
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		cancel()
		return resp, nil
	}
	resp.Body = &cancelOnCloseReadCloser{
		ReadCloser: resp.Body,
		cancel:     cancel,
	}
	return resp, nil
}

// CloseIdleConnections 透传底层连接池关闭能力，避免包装后空闲 h2 连接无法被回收。
func (r *responseHeaderTimeoutRoundTripper) CloseIdleConnections() {
	if r == nil || r.base == nil {
		return
	}
	if closer, ok := r.base.(interface {
		CloseIdleConnections()
	}); ok {
		closer.CloseIdleConnections()
	}
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close 关闭响应体并执行回调
// 使用 sync.Once 确保回调只执行一次
func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if r.cancel != nil {
		r.cancel()
	}
	return err
}

// trackedBody 带跟踪功能的响应体包装器
// 在 Close 时执行回调，用于更新请求计数
type trackedBody struct {
	io.ReadCloser // 原始响应体
	once          sync.Once
	onClose       func() // 关闭时的回调函数
}

// Close 关闭响应体并执行回调
// 使用 sync.Once 确保回调只执行一次
func (b *trackedBody) Close() error {
	err := b.ReadCloser.Close()
	if b.onClose != nil {
		b.once.Do(b.onClose)
	}
	return err
}

// wrapTrackedBody 包装响应体以跟踪关闭事件
// 用于在响应体关闭时更新 inFlight 计数
//
// 参数:
//   - body: 原始响应体
//   - onClose: 关闭时的回调函数
//
// 返回:
//   - io.ReadCloser: 包装后的响应体
func wrapTrackedBody(body io.ReadCloser, onClose func()) io.ReadCloser {
	if body == nil {
		return body
	}
	return &trackedBody{ReadCloser: body, onClose: onClose}
}

// decompressResponseBody 根据 Content-Encoding 解压响应体。
// 当请求显式设置了 accept-encoding 时，Go 的 Transport 不会自动解压，需要手动处理。
// 解压成功后会删除 Content-Encoding 和 Content-Length header（长度已不准确）。
func decompressResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if ce == "" {
		return
	}

	originalBody := resp.Body
	var reader io.Reader
	switch ce {
	case "gzip":
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return // 解压失败，保持原样
		}
		reader = gr
	case "br":
		reader = brotli.NewReader(resp.Body)
	case "deflate":
		reader = flate.NewReader(resp.Body)
	case "zstd":
		bufferedBody := bufio.NewReader(resp.Body)
		resp.Body = &decompressedBody{reader: bufferedBody, closer: originalBody}

		headerBytes, _ := bufferedBody.Peek(zstd.HeaderMaxSize)
		var header zstd.Header
		if err := header.Decode(headerBytes); err != nil {
			slog.Warn("zstd_decompress_failed", "error", err)
			return
		}

		zr, err := zstd.NewReader(bufferedBody)
		if err != nil {
			slog.Warn("zstd_decompress_failed", "error", err)
			return
		}
		reader = &zstdResponseReader{ReadCloser: zr.IOReadCloser()}
	default:
		return
	}

	resp.Body = &decompressedBody{reader: reader, closer: originalBody}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length") // 解压后长度不确定
	resp.ContentLength = -1
}

type zstdResponseReader struct {
	io.ReadCloser
	warnOnce sync.Once
}

func (r *zstdResponseReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.warnOnce.Do(func() {
			slog.Warn("zstd_decompress_failed", "error", err)
		})
	}
	return n, err
}

// decompressedBody 组合解压 reader 和原始 body 的 close。
type decompressedBody struct {
	reader io.Reader
	closer io.Closer
}

func (d *decompressedBody) Read(p []byte) (int, error) {
	return d.reader.Read(p)
}

func (d *decompressedBody) Close() error {
	// 如果 reader 本身也是 Closer（如 gzip.Reader），先关闭它
	if rc, ok := d.reader.(io.Closer); ok {
		_ = rc.Close()
	}
	return d.closer.Close()
}
