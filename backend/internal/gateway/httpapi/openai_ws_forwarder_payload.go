package httpapi

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func validateOpenAIWSBearerToken(account *gatewayprovider.ExecutionAccount, token string) error {
	if account == nil {
		return errors.New("account is nil")
	}
	if strings.TrimSpace(token) == "" && !account.View().IsOpenAIAgentIdentity() {
		return errors.New("token is empty")
	}
	return nil
}

func (s *OpenAIWebSocketExecutor) buildOpenAIResponsesWSURL(account *gatewayprovider.ExecutionAccount) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	var targetURL string
	switch account.Record.Type {
	case capability.AccountTypeOAuth:
		targetURL = chatgptCodexURL
	case capability.AccountTypeSetupToken:
		if account.View().IsOpenAIOAuthLike() {
			targetURL = chatgptCodexURL
		} else {
			targetURL = openaiPlatformAPIURL
		}
	case capability.AccountTypeAPIKey:
		baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL()
		if _, unified := account.Record.Credentials[accountcore.UpstreamProtocolsKey]; gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses() && (unified || gatewayprovider.ExecutionProtocolTarget(account).IsAdaptiveAPIProtocol()) {
			baseURL = gatewayprovider.ExecutionProtocolTarget(account).GetCNProtocolBaseURL(accountcore.APIProtocolResponses)
		}
		if baseURL == "" {
			targetURL = openaiPlatformAPIURL
		} else {
			validatedURL, err := s.Requests.ValidateBaseURL(baseURL)
			if err != nil {
				return "", err
			}
			targetURL = forward.ResponsesEndpoint(account.Record.Platform, validatedURL)
		}
	default:
		targetURL = openaiPlatformAPIURL
	}

	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil {
		return "", fmt.Errorf("invalid target url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
		// 保持不变
	default:
		return "", fmt.Errorf("unsupported scheme for ws: %s", parsed.Scheme)
	}
	return parsed.String(), nil
}

func (s *OpenAIWebSocketExecutor) buildOpenAIWSHeaders(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	token string,
	decision egress.OpenAIWSProtocolDecision,
	isCodexCLI bool,
	turnState string,
	turnMetadata string,
	promptCacheKey string,
	routingModel string,
	routingServiceTier string,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) (http.Header, OpenAIWSSessionHeaderResolution, error) {
	var sessionResolution OpenAIWSSessionHeaderResolution
	headers, err := upstreamopenai.BuildWSHeaders(ctx, upstreamopenai.WSHeaderOptions{
		AgentIdentity: account != nil && account.View().IsOpenAIAgentIdentity(), Token: token,
		TurnState: turnState, TurnMetadata: turnMetadata,
		BetaV1: openAIWSBetaV1Value, BetaV2: openAIWSBetaV2Value,
		LegacyWS: decision.Transport == egress.OpenAIUpstreamTransportResponsesWebsocket,
		ResolveSession: func() (string, string) {
			sessionResolution = ResolveOpenAIWSSessionHeaders(c, promptCacheKey)
			return sessionResolution.SessionID, sessionResolution.ConversationID
		},
		InboundHeaders: func() http.Header {
			if c != nil && c.Request != nil {
				return c.Request.Header
			}
			return nil
		},
		UserAgent: func() string {
			if c != nil {
				return c.GetHeader("User-Agent")
			}
			return ""
		},
		ApplyWSUserAgent: func(headers http.Header) {
			reqCtx := context.Background()
			if c != nil && c.Request != nil {
				reqCtx = c.Request.Context()
			}
			s.Requests.ApplyUserAgentHeader(reqCtx, c, account, headers, true, routerMatch...)
		},
		ResponsesRequestOptions: upstreamopenai.ResponsesRequestOptions{
			UsesCodex: func() bool { return account != nil && account.View().UsesOpenAICodexProtocol() },
			APIKeyID:  func() int64 { return APIKeyIDFromContext(c) },
			IsolateSession: func(keyID int64, value string) string {
				return upstreamopenai.IsolateOpenAIUpstreamSessionID(keyID, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(c, account.View())), value)
			},
			ApplyAccountIdentity: func(headers http.Header) {
				upstreamopenai.ApplyCodexAccountIdentityHeaders(headers, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(c, account.View())), APIKeyIDFromContext(c))
			},
			ApplyFingerprint: func(headers http.Header) { ApplyStagedCodexFingerprintHeaders(c, account.View(), headers) },
			AccountHeaders: func(ctx context.Context, headers http.Header) error {
				return gatewayprovider.CredentialChatGPTHeaders(ctx, s.Requests.Accounts, headers, account)
			},
			Originator:      func() string { return ResolveOpenAIUpstreamOriginator(c, isCodexCLI, routerMatch...) },
			OverrideHeaders: gatewayprovider.BindExecutionHeaders(account),
			BetaFeatures: func(headers http.Header) {
				ApplyOpenAICodexBetaFeatures(c, account != nil && account.View().IsOpenAIOAuthLike(), headers)
			},
			RoutingHint: func(headers http.Header, _ []byte) {
				SetOpenAICodexRoutingHint(headers, account, routingModel, routingServiceTier)
			},
			Diagnostics: func(headers http.Header, _ []byte) {
				LogOpenAIRoutingDiagnostics(ctx, account, string(decision.Transport), routingModel, routingServiceTier, strings.TrimSpace(headers.Get(OpenAICodexRoutingHintHeader)) != "", "soft_routing_hint")
			},
		},
	})
	return headers, sessionResolution, err
}

func (s *OpenAIWebSocketExecutor) buildOpenAIWSCreatePayload(reqBody map[string]any, account *gatewayprovider.ExecutionAccount) map[string]any {
	// OpenAI WS Mode 协议：response.create 字段与 HTTP /responses 基本一致。
	// 保留 stream 字段（与 Codex CLI 一致），仅移除 background。
	payload := make(map[string]any, len(reqBody)+1)
	for k, v := range reqBody {
		payload[k] = v
	}

	delete(payload, "background")
	if _, exists := payload["stream"]; !exists {
		payload["stream"] = true
	}
	payload["type"] = "response.create"

	// OAuth 默认保持 store=false，避免误依赖服务端历史。
	if account != nil && account.View().UsesOpenAICodexProtocol() && !s.isOpenAIWSStoreRecoveryAllowed(account) {
		payload["store"] = false
	}
	return payload
}

func (s *OpenAIWebSocketExecutor) isOpenAIWSStoreRecoveryAllowed(account *gatewayprovider.ExecutionAccount) bool {
	if account != nil && account.View().IsOpenAIWSAllowStoreRecoveryEnabled() {
		return true
	}
	if s != nil && s.Options != nil && s.Options.AllowStoreRecovery {
		return true
	}
	return false
}

func (s *OpenAIWebSocketExecutor) isOpenAIWSStoreDisabledInRequest(reqBody map[string]any, account *gatewayprovider.ExecutionAccount) bool {
	if account != nil && account.View().UsesOpenAICodexProtocol() && !s.isOpenAIWSStoreRecoveryAllowed(account) {
		return true
	}
	if len(reqBody) == 0 {
		return false
	}
	rawStore, ok := reqBody["store"]
	if !ok {
		return false
	}
	storeEnabled, ok := rawStore.(bool)
	if !ok {
		return false
	}
	return !storeEnabled
}

func (s *OpenAIWebSocketExecutor) isOpenAIWSStoreDisabledInRequestRaw(reqBody []byte, account *gatewayprovider.ExecutionAccount) bool {
	if account != nil && account.View().UsesOpenAICodexProtocol() && !s.isOpenAIWSStoreRecoveryAllowed(account) {
		return true
	}
	if len(reqBody) == 0 {
		return false
	}
	storeValue := gjson.GetBytes(reqBody, "store")
	if !storeValue.Exists() {
		return false
	}
	if storeValue.Type != gjson.True && storeValue.Type != gjson.False {
		return false
	}
	return !storeValue.Bool()
}

func (s *OpenAIWebSocketExecutor) openAIWSStoreDisabledConnMode() string {
	if s == nil || s.Options == nil {
		return openAIWSStoreDisabledConnModeStrict
	}
	mode := strings.ToLower(strings.TrimSpace(s.Options.StoreDisabledConnMode))
	switch mode {
	case openAIWSStoreDisabledConnModeStrict, openAIWSStoreDisabledConnModeAdaptive, openAIWSStoreDisabledConnModeOff:
		return mode
	case "":
		// 兼容旧配置：仅配置了布尔开关时按旧语义推导。
		if s.Options.StoreDisabledForceNewConn {
			return openAIWSStoreDisabledConnModeStrict
		}
		return openAIWSStoreDisabledConnModeOff
	default:
		return openAIWSStoreDisabledConnModeStrict
	}
}

// Replay 状态所有权不变式：replay 序列中的 json.RawMessage 正文一经放入即视为
// 不可变，所有持有者共享同一份字节，任何修改都必须整体替换元素或重建 payload。
// 序列头数组在跨持有者保存时必须新建（combineOpenAIWSReplayItems），禁止通过
// 共享头 append，否则会写入其他持有者可见的底层数组。
