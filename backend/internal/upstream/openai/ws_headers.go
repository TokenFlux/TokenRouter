// WebSocket 握手复用同一账号身份端口，保留平台专属头的原始应用顺序。
package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const WSTurnStateHeader = "x-codex-turn-state"

type WSHeaderOptions struct {
	ResponsesRequestOptions
	AgentIdentity, LegacyWS                 bool
	Token                                   string `json:"-"`
	TurnState, TurnMetadata, BetaV1, BetaV2 string
	ResolveSession                          func() (string, string)
	InboundHeaders                          func() http.Header
	UserAgent                               func() string
	ApplyWSUserAgent                        func(http.Header)
}

func (WSHeaderOptions) String() string     { return "openai websocket header options" }
func (v WSHeaderOptions) GoString() string { return v.String() }

// BuildWSHeaders 不读取入站 Context，不持有凭据存储或客户端。
func BuildWSHeaders(ctx context.Context, options WSHeaderOptions) (http.Header, error) {

	headers := make(http.Header)
	if !options.AgentIdentity {
		headers.Set("authorization", "Bearer "+options.Token)
	}

	sessionID, conversationID := options.ResolveSession()
	if options.InboundHeaders() != nil {
		if v := strings.TrimSpace(options.InboundHeaders().Get("accept-language")); v != "" {
			headers.Set("accept-language", v)
		}
		// Codex beta feature 会参与上游 WS 握手，也用于连接池兼容性隔离。
		for _, value := range options.InboundHeaders().Values("x-codex-beta-features") {
			if value = strings.TrimSpace(value); value != "" {
				headers.Add("x-codex-beta-features", value)
			}
		}
		// 仅转发 Codex 明确使用的窗口与安装身份提示，不开放任意客户端头透传。
		for _, name := range [...]string{
			"x-codex-window-id",
			"x-codex-installation-id",
			"session-id",
			"thread-id",
			"x-client-request-id",
		} {
			if value := strings.TrimSpace(options.InboundHeaders().Get(name)); value != "" {
				headers.Set(name, value)
			}
		}
	}
	// OAuth 账号：将 apiKeyID 混入 session 标识符，防止跨用户会话碰撞。
	if options.UsesCodex() {
		apiKeyID := options.APIKeyID()
		if sessionID != "" {
			headers.Set("session_id", options.IsolateSession(apiKeyID, sessionID))
		}
		if conversationID != "" {
			headers.Set("conversation_id", options.IsolateSession(apiKeyID, conversationID))
		}
	} else {
		if sessionID != "" {
			headers.Set("session_id", sessionID)
		}
		if conversationID != "" {
			headers.Set("conversation_id", conversationID)
		}
	}
	if state := strings.TrimSpace(options.TurnState); state != "" {
		headers.Set(WSTurnStateHeader, state)
	}
	if metadata := strings.TrimSpace(options.TurnMetadata); metadata != "" {
		headers.Set(WSTurnMetadataHeader, metadata)
	}
	options.ApplyAccountIdentity(headers)
	options.ApplyFingerprint(headers)

	if options.UsesCodex() {
		if err := options.AccountHeaders(ctx, headers); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}
		headers.Set("originator", options.Originator())
	}

	betaValue := options.BetaV2
	if options.LegacyWS {
		betaValue = options.BetaV1
	}
	headers.Set("OpenAI-Beta", betaValue)

	if ua := strings.TrimSpace(options.UserAgent()); ua != "" {
		headers.Set("user-agent", ua)
	}
	options.ApplyWSUserAgent(headers)

	// 终态收口：originator 必须与最终 user-agent 首段配套且为官方身份，非官方 UA 整体回退为
	// 默认 Codex TUI 身份，同时避免 originator 与 UA 首段错配导致上游 404，详见 issue #3901。
	if options.UsesCodex() {
		EnforceCodexIdentityHeadersWithUA(headers, "")
	}

	// 账号级请求头覆写（仅 openai api_key 账号启用时生效；OAuth 路径 no-op）。
	// 覆盖所有 WS 模式（ctx_pool/dedicated/passthrough）的握手头。
	options.OverrideHeaders(headers)
	// HTTP 与 WebSocket 共用同一份 Codex 会话级能力协商，连接池也会据此
	// 隔离不兼容握手。
	options.BetaFeatures(headers)
	options.RoutingHint(headers, nil)
	options.Diagnostics(headers, nil)

	return headers, nil
}
