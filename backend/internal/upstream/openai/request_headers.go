// Responses 出站请求按既有顺序组合 Header，业务身份与目标策略由外层投影。
package openai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ResponsesRequestOptions 不持有账号/config，动态身份在原取得位置通过窄端口读取。
type ResponsesRequestOptions struct {
	URL                                                                                    string
	ForwardHeaders                                                                         func() http.Header
	Authenticate                                                                           func(context.Context) (http.Header, error)
	AccountHeaders                                                                         func(context.Context, http.Header) error
	UsesCodex, IsCompact, ForceCodexCLI                                                    func() bool
	AllowHeader                                                                            func(string) bool
	GuardTurnState                                                                         func(http.Header)
	MessagesBridge                                                                         func([]byte) bool
	Originator, CompactSession                                                             func() string
	APIKeyID                                                                               func() int64
	IsolateSession                                                                         func(int64, string) string
	ApplyUserAgent                                                                         func(*http.Request)
	ApplyAccountIdentity, ApplyFingerprint, OverrideHeaders, OpenCodeSession, BetaFeatures func(http.Header)
	RoutingHint, Diagnostics                                                               func(http.Header, []byte)
}

// BuildResponsesRequest 只构造当前请求，不创建客户端、不选择账号或重试。
func BuildResponsesRequest(ctx context.Context, body []byte, promptCacheKey string, options ResponsesRequestOptions) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", options.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))

	// Agent Identity 在这里为当前请求生成新 assertion，其它认证模式继续使用原有 Bearer 语义。
	authHeaders, err := options.Authenticate(ctx)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Set headers specific to OAuth accounts (ChatGPT internal API)
	if options.UsesCodex() {
		// Required: set Host for ChatGPT API (must use req.Host, not Header.Set)
		req.Host = "chatgpt.com"
		if err := options.AccountHeaders(ctx, req.Header); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}
	}

	// Whitelist passthrough headers
	for key, values := range options.ForwardHeaders() {
		lowerKey := strings.ToLower(key)
		if options.AllowHeader(lowerKey) {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}
	// 故障转移换号后，不能把已知由旧账号签发的回合状态继续送往新账号。
	options.GuardTurnState(req.Header)
	if options.UsesCodex() {
		compatMessagesBridge := options.MessagesBridge(body)
		// 清除客户端透传的 session 头，后续用隔离后的值重新设置，防止跨用户会话碰撞。
		clientConversationID := strings.TrimSpace(req.Header.Get("conversation_id"))
		req.Header.Del("conversation_id")
		req.Header.Del("session_id")

		if compatMessagesBridge {
			req.Header.Del("OpenAI-Beta")
			req.Header.Del("originator")
		} else {
			req.Header.Set("originator", options.Originator())
		}
		apiKeyID := options.APIKeyID()
		if options.IsCompact() {
			req.Header.Set("accept", "application/json")
			if req.Header.Get("version") == "" {
				req.Header.Set("version", CodexCanonicalClientVersion())
			}
			compactSession := options.CompactSession()
			req.Header.Set("session_id", options.IsolateSession(apiKeyID, compactSession))
		} else {
			req.Header.Set("accept", "text/event-stream")
		}
		if promptCacheKey != "" {
			isolated := options.IsolateSession(apiKeyID, promptCacheKey)
			req.Header.Set("session_id", isolated)
			if !compatMessagesBridge || clientConversationID != "" {
				req.Header.Set("conversation_id", isolated)
			}
		}
	} else if options.IsCompact() {
		// compact 上游是 unary JSON 协议：API-key 账号也显式声明 Accept，
		// 避免 OpenAI 兼容网关按 SSE 返回（#3777 期望行为 4）。
		req.Header.Set("accept", "application/json")
	}

	// 根据 TLS 路由规则、账号配置与全局兜底决定最终上游 User-Agent。
	options.ApplyUserAgent(req)

	// 若开启 ForceCodexCLI，则强制将上游 User-Agent 伪装为规范 Codex 身份。
	// 用于网关未透传/改写 User-Agent 时，仍能命中 Codex 侧识别逻辑。
	if options.ForceCodexCLI() {
		req.Header.Set("user-agent", CodexCanonicalUserAgent())
	}

	// 账号 namespace 不改变客户端身份基数，但确保 scheduler failover 后不会把
	// 同一组 Codex IDs 发送给另一份 OAuth 凭据。可选指纹收敛随后仍可覆盖这些值。
	options.ApplyAccountIdentity(req.Header)

	// 指纹收敛：使用 Forward() 中预计算的收敛 ID 改写出站头，与请求体使用同一份 IDs。
	options.ApplyFingerprint(req.Header)

	// 终态收口：强制统一 OAuth 出站身份（User-Agent / originator / version 同源自洽）。
	// 客户端自报身份不参与构造，浏览器型 UA 也因此不会再到达上游（原浏览器 UA 兜底已被吸收）。
	if options.UsesCodex() {
		EnforceCodexIdentityHeadersWithUA(req.Header, "")
	}

	// Ensure required headers exist
	if req.Header.Get("content-type") == "" {
		req.Header.Set("content-type", "application/json")
	}

	// 账号级请求头覆写（仅 openai api_key 账号启用时生效；OAuth 路径 no-op）
	options.OverrideHeaders(req.Header)
	// 原生 V2 必须携带协商能力；OAuth 的普通 Responses 请求也对齐 Codex 的
	// 会话级 beta 头行为。
	options.OpenCodeSession(req.Header)
	// x-codex-beta-features：按真实 Codex 的会话级行为补注（在账号级覆写之后，
	// 保证不被覆盖丢失）。
	options.BetaFeatures(req.Header)
	options.RoutingHint(req.Header, body)
	options.Diagnostics(req.Header, body)

	return req, nil
}

// String 避免调试输出展开客户端透传 Header。
func (ResponsesRequestOptions) String() string     { return "openai responses request options" }
func (o ResponsesRequestOptions) GoString() string { return o.String() }
