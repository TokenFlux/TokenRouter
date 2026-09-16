package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/wsrelay"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

type wsUsageDecoder struct{}

func (wsUsageDecoder) ServiceTier(body []byte) *string { return extractOpenAIServiceTierFromBody(body) }
func (wsUsageDecoder) ReasoningEffort(body []byte, models ...string) *string {
	return extractOpenAIReasoningEffortFromBody(body, models...)
}
func (wsUsageDecoder) RequestedReasoningEffort(body []byte, models ...string) *string {
	return CanonicalRequestedReasoningEffort(body, models...)
}

const openaiWSV2PassthroughModeFields = "ws_mode=passthrough ws_router=v2"

// 首输出与活跃读取超时由 gateway/ws 唯一拥有。

type openAIWSPassthroughFirstOutputTimeoutError = gatewayws.FirstOutputTimeoutError
type openAIWSPassthroughActiveTurnTimeoutError = gatewayws.ActiveTurnTimeoutError

// openAIWSCoreFrames 将供应商帧连接投影为网关接口，不改变连接释放责任。
type openAIWSCoreFrames struct{ openaiwsv2.FrameConn }

func (c openAIWSCoreFrames) ReadFrame(ctx context.Context) (int, []byte, error) {
	typ, body, err := c.FrameConn.ReadFrame(ctx)
	return int(typ), body, err
}
func (c openAIWSCoreFrames) WriteFrame(ctx context.Context, typ int, body []byte) error {
	return c.FrameConn.WriteFrame(ctx, coderws.MessageType(typ), body)
}

func (s *OpenAIGatewayService) proxyResponsesWebSocketV2Passthrough(
	ctx context.Context,
	c *gin.Context,
	clientConn *coderws.Conn,
	account *Account,
	token string,
	firstClientMessage []byte,
	hooks *OpenAIWSIngressHooks,
	wsDecision OpenAIWSProtocolDecision,
	tlsRouterMatch TLSFingerprintRouterMatchResult,
) error {
	if s == nil {
		return errors.New("service is nil")
	}
	if clientConn == nil {
		return errors.New("client websocket is nil")
	}
	if account == nil {
		return errors.New("account is nil")
	}
	if err := validateOpenAIWSBearerToken(account, token); err != nil {
		return err
	}
	port := &wsPassthroughAdapter{service: s, request: c, account: account, token: token, hooks: hooks, decision: wsDecision, router: tlsRouterMatch}
	var coreHooks *gatewayws.PassthroughHooks
	if hooks != nil {
		coreHooks = &gatewayws.PassthroughHooks{IngressHooks: wsIngressHooks(hooks), InitialRequestModel: hooks.InitialRequestModel, InitialTurnStartedAt: hooks.InitialTurnStartedAt, OnUpstreamError: hooks.OnUpstreamError}
	}
	runtime := gatewayws.PassthroughSession{Port: port, Hooks: coreHooks, Options: gatewayws.PassthroughOptions{AccountID: account.ID, OAuth: account.IsOpenAIOAuth(), WriteTimeout: s.openAIWSWriteTimeout(), IdleTimeout: s.openAIWSPassthroughIdleTimeout(), InterTurnIdleTimeout: s.openAIWSIngressInterTurnIdleTimeout()}}
	return runtime.Run(ctx, gatewayhttp.WSClientFrames{Conn: clientConn}, firstClientMessage)
}

func openAIWSPassthroughRelayClientClose(exit openaiwsv2.RelayExit, completedTurns int) (coderws.StatusCode, string, bool) {
	var closeErr *OpenAIWSClientCloseError
	if errors.As(exit.Err, &closeErr) {
		return closeErr.StatusCode(), closeErr.Reason(), true
	}
	var activeTurnTimeoutErr *openAIWSPassthroughActiveTurnTimeoutError
	if errors.As(exit.Err, &activeTurnTimeoutErr) {
		return coderws.StatusGoingAway, "upstream websocket read timeout; please reconnect", true
	}
	var firstOutputTimeoutErr *openAIWSPassthroughFirstOutputTimeoutError
	if errors.As(exit.Err, &firstOutputTimeoutErr) {
		if completedTurns > 0 || exit.WroteDownstream {
			return coderws.StatusGoingAway, "upstream produced no semantic output; please reconnect", true
		}
		return 0, "", false
	}
	if !exit.Graceful && exit.Stage == "read_upstream" {
		return coderws.StatusInternalError, "upstream websocket proxy failed", true
	}
	return 0, "", false
}

func markOpenAIWSV2PassthroughCyberPolicy(c *gin.Context, payload []byte) bool {
	hit, code, message := detectOpenAICyberPolicy(payload)
	if !hit {
		return false
	}
	usage := OpenAIUsage{}
	parseOpenAIWSResponseUsageFromCompletedEvent(payload, &usage)
	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:           code,
		Message:        message,
		Body:           truncateString(string(payload), 4096),
		UpstreamStatus: http.StatusOK,
		UpstreamInTok:  usage.InputTokens,
		UpstreamOutTok: usage.OutputTokens,
	})
	return true
}

func (s *OpenAIGatewayService) mapOpenAIWSPassthroughDialError(
	err error,
	statusCode int,
	handshakeHeaders http.Header,
) error {
	if err == nil {
		return nil
	}
	wrappedErr := err
	var dialErr *openAIWSDialError
	if !errors.As(err, &dialErr) {
		var handshakeErr *openAIWSHandshakeError
		var responseBody []byte
		if errors.As(err, &handshakeErr) && handshakeErr != nil {
			responseBody = append([]byte(nil), handshakeErr.Body...)
		}
		wrappedErr = &openAIWSDialError{
			StatusCode:      statusCode,
			ResponseHeaders: cloneHeader(handshakeHeaders),
			ResponseBody:    responseBody,
			Err:             err,
		}
	}

	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewOpenAIWSClientCloseError(
			coderws.StatusTryAgainLater,
			"upstream websocket connect timeout",
			wrappedErr,
		)
	}
	if statusCode == http.StatusTooManyRequests {
		return NewOpenAIWSClientCloseError(
			coderws.StatusTryAgainLater,
			"upstream websocket is busy, please retry later",
			wrappedErr,
		)
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"upstream websocket authentication failed",
			wrappedErr,
		)
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
		return NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"upstream websocket handshake rejected",
			wrappedErr,
		)
	}
	return fmt.Errorf("openai ws passthrough dial: %w", wrappedErr)
}

func logOpenAIWSV2Passthrough(format string, args ...any) {
	logger.LegacyPrintf(
		"service.openai_ws_v2",
		"[OpenAI WS v2 passthrough] %s "+format,
		append([]any{openaiWSV2PassthroughModeFields}, args...)...,
	)
}
