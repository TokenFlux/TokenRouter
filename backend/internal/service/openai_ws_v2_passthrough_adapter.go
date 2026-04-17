package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type openAIWSClientFrameConn struct {
	conn *coderws.Conn
}

const openaiWSV2PassthroughModeFields = "ws_mode=passthrough ws_router=v2"

var _ openaiwsv2.FrameConn = (*openAIWSClientFrameConn)(nil)

func (c *openAIWSClientFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.conn == nil {
		return coderws.MessageText, nil, errOpenAIWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.conn.Read(ctx)
}

func (c *openAIWSClientFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.conn == nil {
		return errOpenAIWSConnClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.conn.Write(ctx, msgType, payload)
}

func (c *openAIWSClientFrameConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_ = c.conn.Close(coderws.StatusNormalClosure, "")
	_ = c.conn.CloseNow()
	return nil
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
	if strings.TrimSpace(token) == "" {
		return errors.New("token is empty")
	}
	requestModel := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "model").String())
	requestServiceTier := extractOpenAIServiceTierFromBody(firstClientMessage)
	requestPreviousResponseID := strings.TrimSpace(gjson.GetBytes(firstClientMessage, "previous_response_id").String())
	logOpenAIWSV2Passthrough(
		"relay_start account_id=%d model=%s previous_response_id=%s first_message_type=%s first_message_bytes=%d",
		account.ID,
		truncateOpenAIWSLogValue(requestModel, openAIWSLogValueMaxLen),
		truncateOpenAIWSLogValue(requestPreviousResponseID, openAIWSIDValueMaxLen),
		openaiwsv2RelayMessageTypeName(coderws.MessageText),
		len(firstClientMessage),
	)

	wsURL, err := s.buildOpenAIResponsesWSURL(account)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}
	wsHost := "-"
	wsPath := "-"
	if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
		wsHost = normalizeOpenAIWSLogValue(parsedURL.Host)
		wsPath = normalizeOpenAIWSLogValue(parsedURL.Path)
	}
	logOpenAIWSV2Passthrough(
		"relay_dial_start account_id=%d ws_host=%s ws_path=%s proxy_enabled=%v",
		account.ID,
		wsHost,
		wsPath,
		account.ProxyID != nil && account.Proxy != nil,
	)

	isCodexCLI := false
	if c != nil {
		isCodexCLI = openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		isCodexCLI = true
	}
	headers, _ := s.buildOpenAIWSHeaders(c, account, token, wsDecision, isCodexCLI, "", "", "")
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	dialer := s.getOpenAIWSPassthroughDialer()
	if dialer == nil {
		return errors.New("openai ws passthrough dialer is nil")
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, s.openAIWSDialTimeout())
	defer cancelDial()
	upstreamConn, statusCode, handshakeHeaders, err := dialer.Dial(dialCtx, wsURL, headers, proxyURL)
	if err != nil {
		logOpenAIWSV2Passthrough(
			"relay_dial_failed account_id=%d status_code=%d err=%s",
			account.ID,
			statusCode,
			truncateOpenAIWSLogValue(err.Error(), openAIWSLogValueMaxLen),
		)
		return s.mapOpenAIWSPassthroughDialError(err, statusCode, handshakeHeaders)
	}
	defer func() {
		_ = upstreamConn.Close()
	}()
	logOpenAIWSV2Passthrough(
		"relay_dial_ok account_id=%d status_code=%d upstream_request_id=%s",
		account.ID,
		statusCode,
		openAIWSHeaderValueForLog(handshakeHeaders, "x-request-id"),
	)

	upstreamFrameConn, ok := upstreamConn.(openaiwsv2.FrameConn)
	if !ok {
		return errors.New("openai ws passthrough upstream connection does not support frame relay")
	}

	completedTurns := atomic.Int32{}
	relayResult, relayExit := openaiwsv2.RunEntry(openaiwsv2.EntryInput{
		Ctx:                ctx,
		ClientConn:         &openAIWSClientFrameConn{conn: clientConn},
		UpstreamConn:       upstreamFrameConn,
		FirstClientMessage: firstClientMessage,
		Options: openaiwsv2.RelayOptions{
			WriteTimeout:     s.openAIWSWriteTimeout(),
			IdleTimeout:      s.openAIWSPassthroughIdleTimeout(),
			FirstMessageType: coderws.MessageText,
			OnUsageParseFailure: func(eventType string, usageRaw string) {
				logOpenAIWSV2Passthrough(
					"usage_parse_failed event_type=%s usage_raw=%s",
					truncateOpenAIWSLogValue(eventType, openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(usageRaw, openAIWSLogValueMaxLen),
				)
			},
			OnTurnComplete: func(turn openaiwsv2.RelayTurnResult) {
				turnNo := int(completedTurns.Add(1))
				turnResult := &OpenAIForwardResult{
					RequestID: turn.RequestID,
					Usage: OpenAIUsage{
						InputTokens:              turn.Usage.InputTokens,
						OutputTokens:             turn.Usage.OutputTokens,
						CacheCreationInputTokens: turn.Usage.CacheCreationInputTokens,
						CacheReadInputTokens:     turn.Usage.CacheReadInputTokens,
					},
					Model:           turn.RequestModel,
					ServiceTier:     requestServiceTier,
					Stream:          true,
					OpenAIWSMode:    true,
					ResponseHeaders: cloneHeader(handshakeHeaders),
					Duration:        turn.Duration,
					FirstTokenMs:    turn.FirstTokenMs,
				}
				logOpenAIWSV2Passthrough(
					"relay_turn_completed account_id=%d turn=%d request_id=%s terminal_event=%s duration_ms=%d first_token_ms=%d input_tokens=%d output_tokens=%d cache_read_tokens=%d",
					account.ID,
					turnNo,
					truncateOpenAIWSLogValue(turnResult.RequestID, openAIWSIDValueMaxLen),
					truncateOpenAIWSLogValue(turn.TerminalEventType, openAIWSLogValueMaxLen),
					turnResult.Duration.Milliseconds(),
					openAIWSFirstTokenMsForLog(turnResult.FirstTokenMs),
					turnResult.Usage.InputTokens,
					turnResult.Usage.OutputTokens,
					turnResult.Usage.CacheReadInputTokens,
				)
				if hooks != nil && hooks.AfterTurn != nil {
					hooks.AfterTurn(turnNo, turnResult, nil)
				}
			},
			OnTrace: func(event openaiwsv2.RelayTraceEvent) {
				logOpenAIWSV2Passthrough(
					"relay_trace account_id=%d stage=%s direction=%s msg_type=%s bytes=%d graceful=%v wrote_downstream=%v err=%s",
					account.ID,
					truncateOpenAIWSLogValue(event.Stage, openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(event.Direction, openAIWSLogValueMaxLen),
					truncateOpenAIWSLogValue(event.MessageType, openAIWSLogValueMaxLen),
					event.PayloadBytes,
					event.Graceful,
					event.WroteDownstream,
					truncateOpenAIWSLogValue(event.Error, openAIWSLogValueMaxLen),
				)
			},
		},
	})

	result := &OpenAIForwardResult{
		RequestID: relayResult.RequestID,
		Usage: OpenAIUsage{
			InputTokens:              relayResult.Usage.InputTokens,
			OutputTokens:             relayResult.Usage.OutputTokens,
			CacheCreationInputTokens: relayResult.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     relayResult.Usage.CacheReadInputTokens,
		},
		Model:           relayResult.RequestModel,
		ServiceTier:     requestServiceTier,
		Stream:          true,
		OpenAIWSMode:    true,
		ResponseHeaders: cloneHeader(handshakeHeaders),
		Duration:        relayResult.Duration,
		FirstTokenMs:    relayResult.FirstTokenMs,
	}

	turnCount := int(completedTurns.Load())
	if relayExit == nil {
		logOpenAIWSV2Passthrough(
			"relay_completed account_id=%d request_id=%s terminal_event=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
			account.ID,
			truncateOpenAIWSLogValue(result.RequestID, openAIWSIDValueMaxLen),
			truncateOpenAIWSLogValue(relayResult.TerminalEventType, openAIWSLogValueMaxLen),
			result.Duration.Milliseconds(),
			relayResult.ClientToUpstreamFrames,
			relayResult.UpstreamToClientFrames,
			relayResult.DroppedDownstreamFrames,
			turnCount,
		)
		// 正常路径按 terminal 事件逐 turn 已回调；仅在零 turn 场景兜底回调一次。
		if turnCount == 0 && hooks != nil && hooks.AfterTurn != nil {
			hooks.AfterTurn(1, result, nil)
		}
		return nil
	}
	logOpenAIWSV2Passthrough(
		"relay_failed account_id=%d stage=%s wrote_downstream=%v err=%s duration_ms=%d c2u_frames=%d u2c_frames=%d dropped_frames=%d turns=%d",
		account.ID,
		truncateOpenAIWSLogValue(relayExit.Stage, openAIWSLogValueMaxLen),
		relayExit.WroteDownstream,
		truncateOpenAIWSLogValue(relayErrorText(relayExit.Err), openAIWSLogValueMaxLen),
		result.Duration.Milliseconds(),
		relayResult.ClientToUpstreamFrames,
		relayResult.UpstreamToClientFrames,
		relayResult.DroppedDownstreamFrames,
		turnCount,
	)

	relayErr := relayExit.Err
	if relayExit.Stage == "idle_timeout" {
		relayErr = NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"client websocket idle timeout",
			relayErr,
		)
	}
	turnErr := wrapOpenAIWSIngressTurnError(
		relayExit.Stage,
		relayErr,
		relayExit.WroteDownstream,
	)
	if hooks != nil && hooks.AfterTurn != nil {
		hooks.AfterTurn(turnCount+1, nil, turnErr)
	}
	return turnErr
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
		wrappedErr = &openAIWSDialError{
			StatusCode:      statusCode,
			ResponseHeaders: cloneHeader(handshakeHeaders),
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

func openaiwsv2RelayMessageTypeName(msgType coderws.MessageType) string {
	switch msgType {
	case coderws.MessageText:
		return "text"
	case coderws.MessageBinary:
		return "binary"
	default:
		return fmt.Sprintf("unknown(%d)", msgType)
	}
}

func relayErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func openAIWSFirstTokenMsForLog(firstTokenMs *int) int {
	if firstTokenMs == nil {
		return -1
	}
	return *firstTokenMs
}

func logOpenAIWSV2Passthrough(format string, args ...any) {
	logger.LegacyPrintf(
		"service.openai_ws_v2",
		"[OpenAI WS v2 passthrough] %s "+format,
		append([]any{openaiWSV2PassthroughModeFields}, args...)...,
	)
}
