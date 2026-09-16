package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func validateOpenAIWSBearerToken(account *Account, token string) error {
	if account == nil {
		return errors.New("account is nil")
	}
	if strings.TrimSpace(token) == "" && !account.IsOpenAIAgentIdentity() {
		return errors.New("token is empty")
	}
	return nil
}

func (s *OpenAIGatewayService) buildOpenAIResponsesWSURL(account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	var targetURL string
	switch account.Type {
	case AccountTypeOAuth:
		targetURL = chatgptCodexURL
	case AccountTypeSetupToken:
		if account.IsOpenAIOAuthLike() {
			targetURL = chatgptCodexURL
		} else {
			targetURL = openaiPlatformAPIURL
		}
	case AccountTypeAPIKey:
		baseURL := account.GetOpenAIBaseURL()
		if _, unified := account.Credentials[upstreamProtocolsKey]; account.UsesNativeCNResponses() && (unified || account.IsAdaptiveAPIProtocol()) {
			baseURL = account.GetCNProtocolBaseURL(APIProtocolResponses)
		}
		if baseURL == "" {
			targetURL = openaiPlatformAPIURL
		} else {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return "", err
			}
			targetURL = buildOpenAIResponsesURLForPlatform(account.Platform, validatedURL)
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

func (s *OpenAIGatewayService) buildOpenAIWSHeaders(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token string,
	decision OpenAIWSProtocolDecision,
	isCodexCLI bool,
	turnState string,
	turnMetadata string,
	promptCacheKey string,
	routingModel string,
	routingServiceTier string,
	routerMatch ...TLSFingerprintRouterMatchResult,
) (http.Header, openAIWSSessionHeaderResolution, error) {
	var sessionResolution openAIWSSessionHeaderResolution
	headers, err := native.BuildWSHeaders(ctx, native.WSHeaderOptions{
		AgentIdentity: account != nil && account.IsOpenAIAgentIdentity(), Token: token,
		TurnState: turnState, TurnMetadata: turnMetadata,
		BetaV1: openAIWSBetaV1Value, BetaV2: openAIWSBetaV2Value,
		LegacyWS: decision.Transport == OpenAIUpstreamTransportResponsesWebsocket,
		ResolveSession: func() (string, string) {
			sessionResolution = resolveOpenAIWSSessionHeaders(c, promptCacheKey)
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
			s.applyOpenAIUpstreamUserAgentHeader(reqCtx, c, account, headers, true, routerMatch...)
		},
		ResponsesRequestOptions: native.ResponsesRequestOptions{
			UsesCodex: func() bool { return account != nil && account.UsesOpenAICodexProtocol() },
			APIKeyID:  func() int64 { return getAPIKeyIDFromContext(c) },
			IsolateSession: func(keyID int64, value string) string {
				return isolateOpenAIUpstreamSessionID(keyID, codexAccountIdentitySource(c, account), value)
			},
			ApplyAccountIdentity: func(headers http.Header) {
				applyCodexAccountIdentityHeaders(headers, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
			},
			ApplyFingerprint: func(headers http.Header) { applyStagedCodexFingerprintHeaders(c, account, headers) },
			AccountHeaders: func(ctx context.Context, headers http.Header) error {
				return resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account)
			},
			Originator:      func() string { return resolveOpenAIUpstreamOriginator(c, isCodexCLI, routerMatch...) },
			OverrideHeaders: account.ApplyHeaderOverrides,
			BetaFeatures:    func(headers http.Header) { applyOpenAICodexBetaFeatures(c, account, headers) },
			RoutingHint: func(headers http.Header, _ []byte) {
				setOpenAICodexRoutingHint(headers, account, routingModel, routingServiceTier)
			},
			Diagnostics: func(headers http.Header, _ []byte) {
				logOpenAIRoutingDiagnostics(ctx, account, string(decision.Transport), routingModel, routingServiceTier, strings.TrimSpace(headers.Get(openAICodexRoutingHintHeader)) != "", "soft_routing_hint")
			},
		},
	})
	return headers, sessionResolution, err
}

func (s *OpenAIGatewayService) buildOpenAIWSCreatePayload(reqBody map[string]any, account *Account) map[string]any {
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
	if account != nil && account.UsesOpenAICodexProtocol() && !s.isOpenAIWSStoreRecoveryAllowed(account) {
		payload["store"] = false
	}
	return payload
}

func setOpenAIWSTurnMetadata(payload map[string]any, turnMetadata string) {
	native.SetOpenAIWSTurnMetadata(payload, turnMetadata)
}

func (s *OpenAIGatewayService) isOpenAIWSStoreRecoveryAllowed(account *Account) bool {
	if account != nil && account.IsOpenAIWSAllowStoreRecoveryEnabled() {
		return true
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.AllowStoreRecovery {
		return true
	}
	return false
}

func (s *OpenAIGatewayService) isOpenAIWSStoreDisabledInRequest(reqBody map[string]any, account *Account) bool {
	if account != nil && account.UsesOpenAICodexProtocol() && !s.isOpenAIWSStoreRecoveryAllowed(account) {
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

func (s *OpenAIGatewayService) isOpenAIWSStoreDisabledInRequestRaw(reqBody []byte, account *Account) bool {
	if account != nil && account.UsesOpenAICodexProtocol() && !s.isOpenAIWSStoreRecoveryAllowed(account) {
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

func (s *OpenAIGatewayService) openAIWSStoreDisabledConnMode() string {
	if s == nil || s.cfg == nil {
		return openAIWSStoreDisabledConnModeStrict
	}
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Gateway.OpenAIWS.StoreDisabledConnMode))
	switch mode {
	case openAIWSStoreDisabledConnModeStrict, openAIWSStoreDisabledConnModeAdaptive, openAIWSStoreDisabledConnModeOff:
		return mode
	case "":
		// 兼容旧配置：仅配置了布尔开关时按旧语义推导。
		if s.cfg.Gateway.OpenAIWS.StoreDisabledForceNewConn {
			return openAIWSStoreDisabledConnModeStrict
		}
		return openAIWSStoreDisabledConnModeOff
	default:
		return openAIWSStoreDisabledConnModeStrict
	}
}

func shouldForceNewConnOnStoreDisabled(mode, lastFailureReason string) bool {
	return native.ShouldForceNewConnOnStoreDisabled(mode, lastFailureReason)
}

func dropPreviousResponseIDFromRawPayload(payload []byte) ([]byte, bool, error) {
	return native.DropPreviousResponseIDFromRawPayload(payload)
}

func dropPreviousResponseIDFromRawPayloadWithDeleteFn(
	payload []byte,
	deleteFn func([]byte, string) ([]byte, error),
) ([]byte, bool, error) {
	return native.DropPreviousResponseIDFromRawPayloadWithDeleteFn(payload, deleteFn)
}

func setPreviousResponseIDToRawPayload(payload []byte, previousResponseID string) ([]byte, error) {
	return native.SetPreviousResponseIDToRawPayload(payload, previousResponseID)
}

func shouldInferIngressFunctionCallOutputPreviousResponseID(
	storeDisabled bool,
	turn int,
	signals ToolContinuationSignals,
	currentPreviousResponseID string,
	expectedPreviousResponseID string,
) bool {
	return native.ShouldInferIngressFunctionCallOutputPreviousResponseID(storeDisabled, turn, signals, currentPreviousResponseID, expectedPreviousResponseID)
}

func alignStoreDisabledPreviousResponseID(
	payload []byte,
	expectedPreviousResponseID string,
) ([]byte, bool, error) {
	return native.AlignStoreDisabledPreviousResponseID(payload, expectedPreviousResponseID)
}

// Replay 状态所有权不变式：replay 序列中的 json.RawMessage 正文一经放入即视为
// 不可变，所有持有者共享同一份字节，任何修改都必须整体替换元素或重建 payload。
// 序列头数组在跨持有者保存时必须新建（combineOpenAIWSReplayItems），禁止通过
// 共享头 append，否则会写入其他持有者可见的底层数组。

func combineOpenAIWSReplayItems(history, delta []json.RawMessage) []json.RawMessage {
	return native.CombineOpenAIWSReplayItems(history, delta)
}

func normalizeOpenAIWSJSONForCompare(raw []byte) ([]byte, error) {
	return native.NormalizeOpenAIWSJSONForCompare(raw)
}

func normalizeOpenAIWSJSONForCompareOrRaw(raw []byte) []byte {
	return native.NormalizeOpenAIWSJSONForCompareOrRaw(raw)
}

func normalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(payload []byte) ([]byte, error) {
	return native.NormalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(payload)
}

func openAIWSExtractNormalizedInputSequence(payload []byte) ([]json.RawMessage, bool, error) {
	return native.OpenAIWSExtractNormalizedInputSequence(payload)
}

func openAIWSInputIsPrefixExtended(previousPayload, currentPayload []byte) (bool, error) {
	return native.OpenAIWSInputIsPrefixExtended(previousPayload, currentPayload)
}

func openAIWSRawItemsHasFunctionCallOutput(items []json.RawMessage) bool {
	return native.OpenAIWSRawItemsHasFunctionCallOutput(items)
}

func openAIWSRawItemsHaveToolCallContextForOutputs(items []json.RawMessage) bool {
	return native.OpenAIWSRawItemsHaveToolCallContextForOutputs(items)
}

func openAIWSRawPayloadHasToolCallOutput(payload []byte) bool {
	return native.OpenAIWSRawPayloadHasToolCallOutput(payload)
}

func buildOpenAIWSReplayInputSequenceFromItems(
	previousFullInput []json.RawMessage,
	previousFullInputExists bool,
	currentItems []json.RawMessage,
	currentExists bool,
	hasPreviousResponseID bool,
) ([]json.RawMessage, bool) {
	return native.BuildOpenAIWSReplayInputSequenceFromItems(previousFullInput, previousFullInputExists, currentItems, currentExists, hasPreviousResponseID)
}

func buildOpenAIWSReplayInputSequence(
	previousFullInput []json.RawMessage,
	previousFullInputExists bool,
	currentPayload []byte,
	hasPreviousResponseID bool,
) ([]json.RawMessage, bool, error) {
	return native.BuildOpenAIWSReplayInputSequence(previousFullInput, previousFullInputExists, currentPayload, hasPreviousResponseID)
}

func setOpenAIWSPayloadInputSequence(
	payload []byte,
	fullInput []json.RawMessage,
	fullInputExists bool,
) ([]byte, error) {
	return native.SetOpenAIWSPayloadInputSequence(payload, fullInput, fullInputExists)
}

func buildOpenAIWSCurrentTurnRetryPayload(
	payload []byte,
	fullInput []json.RawMessage,
	fullInputExists bool,
	originalModel string,
) ([]byte, bool, error) {
	return native.BuildOpenAIWSCurrentTurnRetryPayload(payload, fullInput, fullInputExists, originalModel)
}

func shouldKeepIngressPreviousResponseID(
	previousPayload []byte,
	currentPayload []byte,
	lastTurnResponseID string,
	hasFunctionCallOutput bool,
) (bool, string, error) {
	return native.ShouldKeepIngressPreviousResponseID(previousPayload, currentPayload, lastTurnResponseID, hasFunctionCallOutput)
}

type openAIWSIngressPreviousTurnStrictState = native.WSPreviousTurnStrictState

func buildOpenAIWSIngressPreviousTurnStrictState(payload []byte) (*openAIWSIngressPreviousTurnStrictState, error) {
	return native.BuildOpenAIWSIngressPreviousTurnStrictState(payload)
}

func shouldKeepIngressPreviousResponseIDWithStrictState(
	previousState *openAIWSIngressPreviousTurnStrictState,
	currentPayload []byte,
	lastTurnResponseID string,
	hasFunctionCallOutput bool,
) (bool, string, error) {
	return native.ShouldKeepIngressPreviousResponseIDWithStrictState(previousState, currentPayload, lastTurnResponseID, hasFunctionCallOutput)
}
