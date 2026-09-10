package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	proxyinfra "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"

	"golang.org/x/mod/semver"
)

// 默认配置常量
// 这些值在配置文件未指定时作为回退默认值使用
const (
	// defaultMaxIdleConns: 默认最大空闲连接总数
	// HTTP/2 场景下，单连接可多路复用，240 足以支撑高并发
	defaultMaxIdleConns = 240
	// defaultMaxIdleConnsPerHost: 默认每主机最大空闲连接数
	defaultMaxIdleConnsPerHost = 120
	// defaultMaxConnsPerHost: 默认每主机最大连接数（含活跃连接）
	// 达到上限后新请求会等待，而非无限创建连接
	defaultMaxConnsPerHost = 240
	// defaultIdleConnTimeout: 默认空闲连接超时时间（90秒）
	// 超时后连接会被关闭，释放系统资源（建议小于上游 LB 超时）
	defaultIdleConnTimeout = 90 * time.Second
	// defaultResponseHeaderTimeout: 默认等待响应头超时时间（5分钟）
	// LLM 请求可能排队较久，需要较长超时
	defaultResponseHeaderTimeout = 300 * time.Second
	// defaultMaxUpstreamClients: 默认最大客户端缓存数量
	// 超出后会淘汰最久未使用的客户端
	defaultMaxUpstreamClients = 5000
	// defaultClientIdleTTLSeconds: 默认客户端空闲回收阈值（15分钟）
	defaultClientIdleTTLSeconds = 900
	// OpenAI HTTP/2 代理回退策略默认值
	defaultOpenAIHTTP2FallbackErrorThreshold = 2
	defaultOpenAIHTTP2FallbackWindow         = 60 * time.Second
	defaultOpenAIHTTP2FallbackTTL            = 10 * time.Minute

	// Grok CLI 代理会拒绝未标识受支持客户端版本的请求。二进制内置已验证版本，
	// 同时允许运维人员通过环境变量升级，无需等待 TokenRouter 发版。
	grokCLIProxyHost       = "cli-chat-proxy.grok.com"
	grokOfficialAPIHost    = "api.x.ai"
	grokCLIStableVersion   = xai.CLIClientVersion // preferred pin (not the minimum floor)
	grokCLIVersionOverride = xai.CLIVersionEnv
	grokFallbackBodyLimit  = 64 << 10
)

const (
	upstreamProtocolModeDefault          = "default"
	upstreamProtocolModeOpenAIH1         = "openai_h1"
	upstreamProtocolModeOpenAIH2         = "openai_h2"
	upstreamProtocolModeOpenAIH1Fallback = "openai_h1_fallback"
	upstreamProtocolModeGrok             = "grok"
)

type openAIHTTP2Settings struct {
	enabled                   bool
	allowProxyFallbackToHTTP1 bool
	fallbackErrorThreshold    int
	fallbackWindow            time.Duration
	fallbackTTL               time.Duration
}

type openAIHTTP2FallbackState struct {
	mu            sync.Mutex
	windowStart   time.Time
	errorCount    int
	fallbackUntil time.Time
}

// httpClientForUpstreamRequest 按请求标记派生客户端，避免修改共享连接池客户端。
func httpClientForUpstreamRequest(s *httpUpstreamService, client *http.Client, req *http.Request) *http.Client {
	if client == nil || req == nil {
		return client
	}
	ctx := req.Context()
	switch {
	case service.HTTPUpstreamRedirectsDisabled(ctx):
		clone := *client
		clone.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		return &clone
	case service.HTTPUpstreamPublicHostsOnly(ctx) && s != nil:
		clone := *client
		clone.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			// 每跳继承下载安全标记，同时保留客户端已有的重定向约束。
			next = next.WithContext(service.WithHTTPUpstreamPublicHostsOnly(next.Context()))
			if err := s.redirectChecker(next, via); err != nil {
				return err
			}
			if client.CheckRedirect != nil {
				return client.CheckRedirect(next, via)
			}
			return nil
		}
		return &clone
	default:
		return client
	}
}

// grokAccessDeniedFallbackTransport 保持订阅 CLI 代理为 OAuth 主路由；仅当代理返回
// 兼容性特有的 403 "Access denied" 且请求体可重放时，才向 api.x.ai 重试一次。
// 其它授权失败继续返回原响应，避免改变账号调度语义。
type grokAccessDeniedFallbackTransport struct {
	base http.RoundTripper
}

// httpClientWithGrokAccessDeniedFallback 复制客户端并在单次请求外层增加窄范围回退。
func httpClientWithGrokAccessDeniedFallback(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &grokAccessDeniedFallbackTransport{base: base}
	return &clone
}

// RoundTrip 只接受官方 CLI 身份、OAuth Bearer 和明确 Access denied 响应作为回退候选。
func (t *grokAccessDeniedFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || !isGrokCLIAccessDeniedFallbackCandidate(req, resp) {
		return resp, err
	}

	body, ok := bufferSmallResponseBody(resp, grokFallbackBodyLimit)
	if !ok || !isGrokCLICompatibilityAccessDenied(body) {
		return resp, nil
	}

	fallbackReq, err := newGrokOfficialAPIFallbackRequest(req)
	if err != nil {
		return resp, nil
	}
	fallbackResp, fallbackErr := t.base.RoundTrip(fallbackReq)
	if fallbackErr != nil {
		slog.Debug("grok_cli_access_denied_api_fallback_failed", "path", req.URL.EscapedPath(), "error", fallbackErr)
		return resp, nil
	}
	if fallbackResp.StatusCode < http.StatusOK || fallbackResp.StatusCode >= http.StatusMultipleChoices {
		if fallbackResp.Body != nil {
			_ = fallbackResp.Body.Close()
		}
		return resp, nil
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	slog.Warn("grok_cli_access_denied_api_fallback_succeeded", "method", req.Method, "path", req.URL.EscapedPath())
	return fallbackResp, nil
}

// isGrokCLICompatibilityAccessDenied 识别旧版兼容拒绝与结构化聊天端点权限拒绝。
func isGrokCLICompatibilityAccessDenied(body []byte) bool {
	lower := bytes.ToLower(body)
	if bytes.Contains(lower, []byte("access denied")) {
		return true
	}
	var payload struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !strings.EqualFold(strings.TrimSpace(payload.Code), "permission_denied") {
		return false
	}
	const chatEndpointDeniedPrefix = "access to the chat endpoint is denied. please ensure you're using the correct credentials. if you believe this is a mistake, please"
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(payload.Error)), chatEndpointDeniedPrefix)
}

// isGrokCLIAccessDeniedFallbackCandidate 在读取响应体前校验路由、身份和可重放边界。
func isGrokCLIAccessDeniedFallbackCandidate(req *http.Request, resp *http.Response) bool {
	return req != nil && req.URL != nil && req.GetBody != nil && resp != nil &&
		resp.StatusCode == http.StatusForbidden &&
		strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), grokCLIProxyHost) &&
		strings.EqualFold(strings.TrimSpace(req.Header.Get("X-XAI-Token-Auth")), "xai-grok-cli") &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Header.Get("Authorization"))), "bearer ")
}

// newGrokOfficialAPIFallbackRequest 重建请求体，并移除仅属于 CLI 客户端身份的请求头。
func newGrokOfficialAPIFallbackRequest(req *http.Request) (*http.Request, error) {
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	fallbackReq := req.Clone(req.Context())
	fallbackReq.Body = body
	fallbackReq.URL = cloneURL(req.URL)
	fallbackReq.URL.Scheme = "https"
	fallbackReq.URL.Host = grokOfficialAPIHost
	fallbackReq.Host = ""
	fallbackReq.RequestURI = ""
	fallbackReq.Header = req.Header.Clone()
	for _, header := range []string{
		"X-XAI-Token-Auth",
		"X-Grok-Client-Version",
		"X-Grok-Client-Surface",
		"X-UserID",
		"X-Email",
		"User-Agent",
	} {
		fallbackReq.Header.Del(header)
	}
	return fallbackReq, nil
}

// cloneURL 复制 URL，避免回退时修改原始请求。
func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// bufferSmallResponseBody 有界读取响应体；超限或失败时恢复已读前缀供调用方继续消费。
func bufferSmallResponseBody(resp *http.Response, limit int64) ([]byte, bool) {
	if resp == nil || resp.Body == nil || limit <= 0 {
		return nil, false
	}
	original := resp.Body
	body, err := io.ReadAll(io.LimitReader(original, limit+1))
	if err != nil || int64(len(body)) > limit {
		resp.Body = &prefixedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(body), original),
			Closer: original,
		}
		return nil, false
	}
	_ = original.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return body, true
}

// prefixedReadCloser 将已探测前缀与剩余响应体重新拼接，并保留原关闭语义。
type prefixedReadCloser struct {
	io.Reader
	io.Closer
}

// applyGrokCLIProxyHeaders 在最终共享 transport 边界写入官方 Grok Build 客户端身份。
// 仅精确匹配 CLI 代理主机，避免改变直连 api.x.ai 的流量，并统一覆盖 Responses、
// Chat Completions、媒体、额度探测和账号测试请求。
func applyGrokCLIProxyHeaders(req *http.Request) {
	if req == nil || req.URL == nil || !strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), grokCLIProxyHost) {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	version := strings.TrimSpace(os.Getenv(grokCLIVersionOverride))
	if !isSupportedGrokCLIVersion(version) {
		version = grokCLIStableVersion
	}
	req.Header.Set("X-XAI-Token-Auth", xai.CLITokenAuth)
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("x-grok-client-identifier", xai.CLIClientIdentifier)
	req.Header.Set("User-Agent", xai.CLIUserAgent(version))
}

// isSupportedGrokCLIVersion 校验覆盖版本是否为规范 SemVer，且不低于内置最低版本。
func isSupportedGrokCLIVersion(version string) bool {
	canonical := "v" + version
	minimum := "v" + xai.CLIClientVersion
	return semver.IsValid(canonical) &&
		semver.Canonical(canonical) == canonical &&
		semver.Compare(canonical, minimum) >= 0
}

func (s *httpUpstreamService) shouldValidateResolvedIP() bool {
	if s.cfg == nil {
		return false
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return false
	}
	return !s.cfg.Security.URLAllowlist.AllowPrivateHosts
}

func (s *httpUpstreamService) validateRequestHost(req *http.Request) error {
	publicHostsOnly := req != nil && service.HTTPUpstreamPublicHostsOnly(req.Context())
	if !s.shouldValidateResolvedIP() && !publicHostsOnly {
		return nil
	}
	if req == nil || req.URL == nil {
		return errors.New("request url is nil")
	}
	host := strings.TrimSpace(req.URL.Hostname())
	if host == "" {
		return errors.New("request host is empty")
	}
	if err := urlvalidator.ValidateResolvedIP(host); err != nil {
		return err
	}
	return nil
}

func (s *httpUpstreamService) redirectChecker(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return s.validateRequestHost(req)
}

// getIsolationMode 获取连接池隔离模式
// 从配置中读取，无效值回退到 account_proxy 模式
//
// 返回:
//   - string: 隔离模式（proxy/account/account_proxy）
func (s *httpUpstreamService) getIsolationMode() string {
	if s.cfg == nil {
		return config.ConnectionPoolIsolationAccountProxy
	}
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Gateway.ConnectionPoolIsolation))
	if mode == "" {
		return config.ConnectionPoolIsolationAccountProxy
	}
	switch mode {
	case config.ConnectionPoolIsolationProxy, config.ConnectionPoolIsolationAccount, config.ConnectionPoolIsolationAccountProxy:
		return mode
	default:
		return config.ConnectionPoolIsolationAccountProxy
	}
}

// maxUpstreamClients 获取最大客户端缓存数量
// 从配置中读取，无效值使用默认值
func (s *httpUpstreamService) maxUpstreamClients() int {
	if s.cfg == nil {
		return defaultMaxUpstreamClients
	}
	if s.cfg.Gateway.MaxUpstreamClients > 0 {
		return s.cfg.Gateway.MaxUpstreamClients
	}
	return defaultMaxUpstreamClients
}

// clientIdleTTL 获取客户端空闲回收阈值
// 从配置中读取，无效值使用默认值
func (s *httpUpstreamService) clientIdleTTL() time.Duration {
	if s.cfg == nil {
		return time.Duration(defaultClientIdleTTLSeconds) * time.Second
	}
	if s.cfg.Gateway.ClientIdleTTLSeconds > 0 {
		return time.Duration(s.cfg.Gateway.ClientIdleTTLSeconds) * time.Second
	}
	return time.Duration(defaultClientIdleTTLSeconds) * time.Second
}

// resolvePoolSettings 解析连接池配置
// 根据隔离策略和账户并发数动态调整连接池参数
//
// 参数:
//   - isolation: 隔离模式
//   - accountConcurrency: 账户并发限制
//
// 返回:
//   - poolSettings: 连接池配置
//
// 说明:
//   - 账户隔离模式下，连接池大小与账户并发数对应
//   - 这确保了单账户不会占用过多连接资源
func (s *httpUpstreamService) resolvePoolSettings(isolation string, accountConcurrency int) poolSettings {
	settings := defaultPoolSettings(s.cfg)
	// 账户隔离模式下，根据账户并发数调整连接池大小
	if (isolation == config.ConnectionPoolIsolationAccount || isolation == config.ConnectionPoolIsolationAccountProxy) && accountConcurrency > 0 {
		settings.MaxIdleConns = accountConcurrency
		settings.MaxIdleConnsPerHost = accountConcurrency
		settings.MaxConnsPerHost = accountConcurrency
	}
	return settings
}

func (s *httpUpstreamService) applyProfilePoolSettings(settings poolSettings, profile service.HTTPUpstreamProfile) poolSettings {
	switch profile {
	case service.HTTPUpstreamProfileOpenAI:
		settings.ResponseHeaderTimeout = 0
		if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIResponseHeaderTimeout > 0 {
			settings.ResponseHeaderTimeout = time.Duration(s.cfg.Gateway.OpenAIResponseHeaderTimeout) * time.Second
		}
	case service.HTTPUpstreamProfileGrok:
		// Grok can stall before its first byte under capacity pressure. Keep the
		// generic 600s gateway timeout from turning one request into a 10-minute
		// resource hold; streaming after headers is unaffected.
		settings.ResponseHeaderTimeout = 120 * time.Second
		if s != nil && s.cfg != nil {
			settings.ResponseHeaderTimeout = time.Duration(s.cfg.Gateway.GrokResponseHeaderTimeout) * time.Second
		}
	}
	return settings
}

func (s *httpUpstreamService) resolveOpenAIHTTP2Settings() openAIHTTP2Settings {
	settings := openAIHTTP2Settings{
		enabled:                   false,
		allowProxyFallbackToHTTP1: true,
		fallbackErrorThreshold:    defaultOpenAIHTTP2FallbackErrorThreshold,
		fallbackWindow:            defaultOpenAIHTTP2FallbackWindow,
		fallbackTTL:               defaultOpenAIHTTP2FallbackTTL,
	}
	if s == nil || s.cfg == nil {
		return settings
	}
	cfg := s.cfg.Gateway.OpenAIHTTP2
	settings.enabled = cfg.Enabled
	settings.allowProxyFallbackToHTTP1 = cfg.AllowProxyFallbackToHTTP1
	if cfg.FallbackErrorThreshold > 0 {
		settings.fallbackErrorThreshold = cfg.FallbackErrorThreshold
	}
	if cfg.FallbackWindowSeconds > 0 {
		settings.fallbackWindow = time.Duration(cfg.FallbackWindowSeconds) * time.Second
	}
	if cfg.FallbackTTLSeconds > 0 {
		settings.fallbackTTL = time.Duration(cfg.FallbackTTLSeconds) * time.Second
	}
	return settings
}

func (s *httpUpstreamService) resolveProtocolMode(profile service.HTTPUpstreamProfile, proxyKey string, parsedProxy *url.URL) string {
	if profile == service.HTTPUpstreamProfileGrok {
		return upstreamProtocolModeGrok
	}
	if profile != service.HTTPUpstreamProfileOpenAI {
		return upstreamProtocolModeDefault
	}
	settings := s.resolveOpenAIHTTP2Settings()
	if !settings.enabled {
		return upstreamProtocolModeOpenAIH1
	}
	if parsedProxy == nil {
		return upstreamProtocolModeOpenAIH2
	}
	scheme := strings.ToLower(parsedProxy.Scheme)
	if scheme != "http" && scheme != "https" {
		return upstreamProtocolModeOpenAIH2
	}
	if settings.allowProxyFallbackToHTTP1 && s.isOpenAIHTTP2FallbackActive(proxyKey) {
		return upstreamProtocolModeOpenAIH1Fallback
	}
	return upstreamProtocolModeOpenAIH2
}

func (s *httpUpstreamService) resolveTLSFingerprintProtocolMode(profile service.HTTPUpstreamProfile, proxyKey string, parsedProxy *url.URL, tlsProfile *tlsfingerprint.Profile) string {
	protocolMode := s.resolveProtocolMode(profile, proxyKey, parsedProxy)
	if protocolMode != upstreamProtocolModeOpenAIH2 || tlsfingerprint.SupportsHTTP2(tlsProfile) {
		return protocolMode
	}
	return upstreamProtocolModeOpenAIH1
}

func resolveTLSFingerprintTransportProfile(profile *tlsfingerprint.Profile, protocolMode string) *tlsfingerprint.Profile {
	if protocolMode != upstreamProtocolModeOpenAIH2 {
		return tlsfingerprint.HTTP1OnlyProfile(profile)
	}
	return profile
}

func (s *httpUpstreamService) isOpenAIHTTP2FallbackActive(proxyKey string) bool {
	raw, ok := s.openAIHTTP2Fallbacks.Load(proxyKey)
	if !ok {
		return false
	}
	state, ok := raw.(*openAIHTTP2FallbackState)
	if !ok || state == nil {
		return false
	}
	return state.isFallbackActive(time.Now())
}

func (s *httpUpstreamService) getOrCreateOpenAIHTTP2FallbackState(proxyKey string) *openAIHTTP2FallbackState {
	state := &openAIHTTP2FallbackState{}
	actual, _ := s.openAIHTTP2Fallbacks.LoadOrStore(proxyKey, state)
	cached, ok := actual.(*openAIHTTP2FallbackState)
	if !ok || cached == nil {
		return state
	}
	return cached
}

func isHTTPProxyKey(proxyKey string) bool {
	return strings.HasPrefix(proxyKey, "http://") || strings.HasPrefix(proxyKey, "https://")
}

func isOpenAIHTTP2CompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	if isUpstreamTimeoutError(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	markers := []string{
		"alpn",
		"no application protocol",
		"protocol error",
		"stream error",
		"goaway",
		"refused_stream",
		"frame too large",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isUpstreamTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	timeoutMarkers := []string{
		"timeout awaiting response headers",
		"i/o timeout",
		"context deadline exceeded",
		"client.timeout exceeded while awaiting headers",
		"tls handshake timeout",
	}
	for _, marker := range timeoutMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (s *httpUpstreamService) recordOpenAIHTTP2Failure(profile service.HTTPUpstreamProfile, protocolMode, proxyKey string, err error) {
	if profile != service.HTTPUpstreamProfileOpenAI || protocolMode != upstreamProtocolModeOpenAIH2 {
		return
	}
	settings := s.resolveOpenAIHTTP2Settings()
	if !settings.enabled || !settings.allowProxyFallbackToHTTP1 {
		return
	}
	if !isHTTPProxyKey(proxyKey) || !isOpenAIHTTP2CompatibilityError(err) {
		return
	}
	state := s.getOrCreateOpenAIHTTP2FallbackState(proxyKey)
	activated, until := state.recordFailure(time.Now(), settings.fallbackErrorThreshold, settings.fallbackWindow, settings.fallbackTTL)
	if activated {
		slog.Warn("openai_http2_proxy_fallback_activated",
			"proxy", proxyKey,
			"fallback_until", until.Format(time.RFC3339))
	}
}

func (s *httpUpstreamService) recordOpenAIHTTP2Success(profile service.HTTPUpstreamProfile, protocolMode, proxyKey string) {
	if profile != service.HTTPUpstreamProfileOpenAI || protocolMode != upstreamProtocolModeOpenAIH2 {
		return
	}
	if !isHTTPProxyKey(proxyKey) {
		return
	}
	raw, ok := s.openAIHTTP2Fallbacks.Load(proxyKey)
	if !ok {
		return
	}
	state, ok := raw.(*openAIHTTP2FallbackState)
	if !ok || state == nil {
		return
	}
	state.resetErrorWindow()
}

func (s *openAIHTTP2FallbackState) isFallbackActive(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fallbackUntil.IsZero() {
		return false
	}
	if now.Before(s.fallbackUntil) {
		return true
	}
	s.fallbackUntil = time.Time{}
	return false
}

func (s *openAIHTTP2FallbackState) resetErrorWindow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowStart = time.Time{}
	s.errorCount = 0
}

func (s *openAIHTTP2FallbackState) recordFailure(now time.Time, threshold int, window, ttl time.Duration) (bool, time.Time) {
	if threshold <= 0 {
		threshold = defaultOpenAIHTTP2FallbackErrorThreshold
	}
	if window <= 0 {
		window = defaultOpenAIHTTP2FallbackWindow
	}
	if ttl <= 0 {
		ttl = defaultOpenAIHTTP2FallbackTTL
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.fallbackUntil.IsZero() && now.Before(s.fallbackUntil) {
		return false, s.fallbackUntil
	}
	if !s.fallbackUntil.IsZero() && !now.Before(s.fallbackUntil) {
		s.fallbackUntil = time.Time{}
	}

	if s.windowStart.IsZero() || now.Sub(s.windowStart) > window {
		s.windowStart = now
		s.errorCount = 0
	}
	s.errorCount++
	if s.errorCount < threshold {
		return false, time.Time{}
	}

	s.fallbackUntil = now.Add(ttl)
	s.windowStart = time.Time{}
	s.errorCount = 0
	return true, s.fallbackUntil
}

// normalizeProxyURL 标准化代理 URL
// 处理空值和解析错误，返回标准化的键和解析后的 URL
//
// 参数:
//   - raw: 原始代理 URL 字符串
//
// 返回:
//   - string: 标准化的代理键（空返回 "direct"）
//   - *url.URL: 解析后的 URL（空返回 nil）
//   - error: 非空代理 URL 解析失败时返回错误（禁止回退到直连）
func normalizeProxyURL(raw string) (string, *url.URL, error) {
	return proxyinfra.NormalizePoolKey(raw)
}

// defaultPoolSettings 获取默认连接池配置
// 从全局配置中读取，无效值使用常量默认值
//
// 参数:
//   - cfg: 全局配置
//
// 返回:
//   - poolSettings: 连接池配置
func defaultPoolSettings(cfg *config.Config) poolSettings {
	maxIdleConns := defaultMaxIdleConns
	maxIdleConnsPerHost := defaultMaxIdleConnsPerHost
	maxConnsPerHost := defaultMaxConnsPerHost
	idleConnTimeout := defaultIdleConnTimeout
	responseHeaderTimeout := defaultResponseHeaderTimeout

	if cfg != nil {
		if cfg.Gateway.MaxIdleConns > 0 {
			maxIdleConns = cfg.Gateway.MaxIdleConns
		}
		if cfg.Gateway.MaxIdleConnsPerHost > 0 {
			maxIdleConnsPerHost = cfg.Gateway.MaxIdleConnsPerHost
		}
		if cfg.Gateway.MaxConnsPerHost >= 0 {
			maxConnsPerHost = cfg.Gateway.MaxConnsPerHost
		}
		if cfg.Gateway.IdleConnTimeoutSeconds > 0 {
			idleConnTimeout = time.Duration(cfg.Gateway.IdleConnTimeoutSeconds) * time.Second
		}
		if cfg.Gateway.ResponseHeaderTimeout >= 0 {
			responseHeaderTimeout = time.Duration(cfg.Gateway.ResponseHeaderTimeout) * time.Second
		}
	}

	return poolSettings{
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		MaxConnsPerHost:       maxConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
}

// poolSettings 是技术参数的兼容别名，配置投影仍归旧适配入口。
type poolSettings = httpclient.UpstreamSettings

// upstreamPool 是平台适配与通用连接池之间的执行契约，也供测试替换外部传输。
type upstreamPool interface {
	Do(*http.Request, httpclient.UpstreamRequestOptions) (*http.Response, error)
}
type httpUpstreamService struct {
	cfg                  *config.Config
	pool                 upstreamPool
	openAIHTTP2Fallbacks sync.Map
}

// NewHTTPUpstream 保持 Wire 与旧调用方构造器不变。
func NewHTTPUpstream(cfg *config.Config) service.HTTPUpstream {
	return &httpUpstreamService{cfg: cfg, pool: httpclient.NewUpstreamPool()}
}

// transportOptions 将平台和配置转为一次执行使用的技术快照。
func (s *httpUpstreamService) transportOptions(req *http.Request, proxyURL string, accountID int64, concurrency int, tlsProfile *tlsfingerprint.Profile) (httpclient.UpstreamRequestOptions, error) {
	proxyKey, parsedProxy, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return httpclient.UpstreamRequestOptions{}, err
	}
	profile := service.HTTPUpstreamProfileDefault
	if req != nil {
		profile = service.HTTPUpstreamProfileFromContext(req.Context())
	}
	isolation := s.getIsolationMode()
	settings := s.applyProfilePoolSettings(s.resolvePoolSettings(isolation, concurrency), profile)
	mode := s.resolveProtocolMode(profile, proxyKey, parsedProxy)
	if tlsProfile != nil {
		mode = s.resolveTLSFingerprintProtocolMode(profile, proxyKey, parsedProxy, tlsProfile)
		tlsProfile = resolveTLSFingerprintTransportProfile(tlsProfile, mode)
	}
	opts := httpclient.UpstreamRequestOptions{
		ProxyURL:   proxyURL,
		AccountID:  accountID,
		Isolation:  isolation,
		MaxClients: s.maxUpstreamClients(),
		IdleTTL:    s.clientIdleTTL(),
		Settings:   settings,
		TLSProfile: tlsProfile,
		Protocol: httpclient.TransportProtocol{
			CacheVariant: mode,
			HTTP2:        mode == upstreamProtocolModeOpenAIH2,
			DisableHTTP2: mode == upstreamProtocolModeOpenAIH1 || mode == upstreamProtocolModeOpenAIH1Fallback,
		},
	}
	if s.shouldValidateResolvedIP() {
		opts.CheckRedirect = s.redirectChecker
	}
	opts.PrepareClient = func(client *http.Client) *http.Client {
		return httpClientWithGrokAccessDeniedFallback(httpClientForUpstreamRequest(s, client, req))
	}
	opts.ObserveResult = func(err error) {
		if err != nil {
			s.recordOpenAIHTTP2Failure(profile, mode, proxyKey, err)
		} else {
			s.recordOpenAIHTTP2Success(profile, mode, proxyKey)
		}
	}
	return opts, nil
}

// Do 保留请求 Header、目标验证与平台策略的执行顺序。
func (s *httpUpstreamService) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	applyGrokCLIProxyHeaders(req)
	if err := s.validateRequestHost(req); err != nil {
		return nil, err
	}
	opts, err := s.transportOptions(req, proxyURL, accountID, concurrency, nil)
	if err != nil {
		return nil, err
	}
	return s.pool.Do(req, opts)
}

// DoWithTLS 保留 nil/明文 HTTP 回退，并将已选定的指纹交给通用池。
func (s *httpUpstreamService) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if profile == nil || (req != nil && req.URL != nil && strings.EqualFold(req.URL.Scheme, "http")) {
		return s.Do(req, proxyURL, accountID, concurrency)
	}
	applyGrokCLIProxyHeaders(req)
	if err := s.validateRequestHost(req); err != nil {
		return nil, err
	}
	opts, err := s.transportOptions(req, proxyURL, accountID, concurrency, profile)
	if err != nil {
		return nil, err
	}
	return s.pool.Do(req, opts)
}
