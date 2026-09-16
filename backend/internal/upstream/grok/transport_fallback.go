// CLI 403 回退只派生当前请求的客户端，不修改共享传输或账号策略。
package grok

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	grokCLIProxyHost       = CLIProxyHost
	grokOfficialAPIHost    = "api.x.ai"
	grokCLIStableVersion   = CLIClientVersion
	grokCLIVersionOverride = CLIVersionEnv
	grokFallbackBodyLimit  = 64 << 10
)

// AccessDeniedFallbackTransport 保持订阅 CLI 代理为 OAuth 主路由；仅当代理返回
// 兼容性特有的 403 "Access denied" 且请求体可重放时，才向 api.x.ai 重试一次。
// 其它授权失败继续返回原响应，避免改变账号调度语义。
type AccessDeniedFallbackTransport struct {
	Base http.RoundTripper
}

// ClientWithAccessDeniedFallback 复制客户端并在单次请求外层增加窄范围回退。
func ClientWithAccessDeniedFallback(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &AccessDeniedFallbackTransport{Base: base}
	return &clone
}

// RoundTrip 只接受官方 CLI 身份、OAuth Bearer 和明确 Access denied 响应作为回退候选。
func (t *AccessDeniedFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Base.RoundTrip(req)
	if err != nil || !IsCLIAccessDeniedFallbackCandidate(req, resp) {
		return resp, err
	}

	body, ok := bufferSmallResponseBody(resp, grokFallbackBodyLimit)
	if !ok || !IsCLICompatibilityAccessDenied(body) {
		return resp, nil
	}

	fallbackReq, err := newGrokOfficialAPIFallbackRequest(req)
	if err != nil {
		return resp, nil
	}
	fallbackResp, fallbackErr := t.Base.RoundTrip(fallbackReq)
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

// IsCLICompatibilityAccessDenied 识别旧版兼容拒绝与结构化聊天端点权限拒绝。
func IsCLICompatibilityAccessDenied(body []byte) bool {
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

// IsCLIAccessDeniedFallbackCandidate 在读取响应体前校验路由、身份和可重放边界。
func IsCLIAccessDeniedFallbackCandidate(req *http.Request, resp *http.Response) bool {
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

// ApplyTransportCLIHeaders 在最终共享 transport 边界写入官方 Grok Build 客户端身份。
// 仅精确匹配 CLI 代理主机，避免改变直连 api.x.ai 的流量，并统一覆盖 Responses、
// Chat Completions、媒体、额度探测和账号测试请求。
func ApplyTransportCLIHeaders(req *http.Request) {
	if req == nil || req.URL == nil || !strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), grokCLIProxyHost) {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	version := strings.TrimSpace(os.Getenv(grokCLIVersionOverride))
	if !IsTransportCLIVersionSupported(version) {
		version = grokCLIStableVersion
	}
	req.Header.Set("X-XAI-Token-Auth", CLITokenAuth)
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("x-grok-client-identifier", CLIClientIdentifier)
	req.Header.Set("User-Agent", CLIUserAgent(version))
}

// IsTransportCLIVersionSupported 校验覆盖版本是否为规范 SemVer，且不低于内置最低版本。
func IsTransportCLIVersionSupported(version string) bool {
	canonical := "v" + version
	minimum := "v" + CLIClientVersion
	return semver.IsValid(canonical) &&
		semver.Canonical(canonical) == canonical &&
		semver.Compare(canonical, minimum) >= 0
}
