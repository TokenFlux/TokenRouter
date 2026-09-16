package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) GrokRealtime(c *gin.Context) { h.AuxiliaryHTTPHandler().GrokRealtime(c) }

func grokRealtimeBillingResult(model string, elapsed time.Duration, audioObserved bool) *service.OpenAIForwardResult {
	usage := gatewaymedia.RealtimeAudioUsage(elapsed, audioObserved)
	if usage == nil {
		return nil
	}
	return &service.OpenAIForwardResult{RequestID: service.StableGrokRealtimeBillingRequestID(""), Model: model, Duration: elapsed, AudioUsage: usage}
}

func (h *OpenAIGatewayHandler) GrokVoice(c *gin.Context, endpoint string) {
	h.AuxiliaryHTTPHandler().GrokVoice(c, endpoint)
}

// recordGrokVoiceUsage 在存在 AudioUsage 时按分组音频价格结算 TTS、STT 或 Realtime。
func (h *OpenAIGatewayHandler) recordGrokVoiceUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	account *service.Account,
	subscription *service.UserSubscription,
	endpoint string,
	body []byte,
	result *service.OpenAIForwardResult,
) {
	if h == nil || c == nil || apiKey == nil || account == nil || result == nil {
		return
	}
	if result.AudioUsage == nil {
		return
	}
	// 即使调用方遗漏，也为 Realtime、TTS 和 STT 强制生成持久结算 ID。
	if mode := strings.TrimSpace(result.AudioUsage.Mode); mode == "realtime" {
		result.RequestID = service.StableGrokRealtimeBillingRequestID(result.RequestID)
	} else {
		result.RequestID = service.StableGrokAudioBillingRequestID(result.RequestID)
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	if requestPayloadHash == "" {
		requestPayloadHash = service.HashUsageRequestPayload([]byte(endpoint))
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = endpoint
	}

	channelFields := clientRequestedUsageFields(c, service.ChannelMappingResult{}, model, result.UpstreamModel)
	completionInput := service.CompletionOpenAIInput(c.Request.Context(), &service.OpenAIRecordUsageInput{
		Result:             result,
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            account,
		Subscription:       subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    sessionID,
		ChannelUsageFields: channelFields,
	})
	completionRecorder := h.completionRuntime()
	completionLog := logger.L().With(
		zap.String("component", "handler.openai_gateway.grok_voice"),
		zap.Int64("user_id", apiKey.User.ID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("endpoint", endpoint),
		zap.Int64("account_id", account.ID),
	)
	h.submitMandatoryUsageRecordTask(c, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			completionLog.Error("grok_voice.record_usage_failed", zap.Error(err))
		}
	})
}

// grokRealtimeAdapter 只转换已选账号与技术连接，不保留第二套候选循环。
type grokRealtimeAdapter struct {
	h         *OpenAIGatewayHandler
	c         *gin.Context
	apiKey    *service.APIKey
	reqLog    *zap.Logger
	selection *service.AccountSelectionResult
}

func (p *grokRealtimeAdapter) SelectRealtime(ctx context.Context, excluded map[int64]struct{}) (accountcore.AccountSnapshot, bool, error) {
	selected, _, err := p.h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", "", "", excluded, service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapabilityTextGeneration, false, false, service.PlatformGrok)
	p.selection = selected
	if selected == nil || selected.Account == nil {
		return accountcore.AccountSnapshot{}, false, err
	}
	return service.AccountSnapshotView(selected.Account), true, err
}
func (p *grokRealtimeAdapter) AcquireRealtime(_ context.Context, _ accountcore.AccountSnapshot) (func(), bool) {
	var started bool
	return p.h.acquireResponsesAccountSlot(p.c, p.apiKey.GroupID, "", p.selection, false, &started, p.reqLog)
}
func (p *grokRealtimeAdapter) RealtimeCredential(ctx context.Context, _ accountcore.AccountSnapshot) (string, error) {
	token, _, err := p.h.gatewayService.GetRequestCredential(ctx, p.c, p.selection.Account)
	return token, err
}
func (p *grokRealtimeAdapter) OpenRealtime(ctx context.Context, _ accountcore.AccountSnapshot, token, model string) (upstream.FrameConn, error) {
	conn, err := p.h.gatewayService.OpenGrokRealtime(ctx, p.selection.Account, token, model)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
func (p *grokRealtimeAdapter) RealtimeOpenFailed(ctx context.Context, selected accountcore.AccountSnapshot, err error) {
	p.reqLog.Warn("grok_realtime.pre_accept_failed", zap.Int64("account_id", selected.ID), zap.Error(err))
	status := http.StatusBadGateway
	var dialErr *service.GrokRealtimeDialError
	if errors.As(err, &dialErr) && dialErr.StatusCode > 0 {
		status = dialErr.StatusCode
	}
	p.h.gatewayService.HandleGrokRealtimeUpstreamError(ctx, p.selection.Account, status, []byte(err.Error()))
}

// grokVoiceAdapter 只桥接单次选择、HTTP 原生执行及完成投影。
type grokVoiceAdapter struct {
	h            *OpenAIGatewayHandler
	c            *gin.Context
	apiKey       *service.APIKey
	subscription *service.UserSubscription
	reqLog       *zap.Logger
	selection    *service.AccountSelectionResult
}

func (p *grokVoiceAdapter) SelectVoice(ctx context.Context, excluded map[int64]struct{}) (accountcore.AccountSnapshot, bool, error) {
	selected, _, err := p.h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", "", "grok-4.5", excluded, service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapabilityTextGeneration, false, false, service.PlatformGrok)
	p.selection = selected
	if selected == nil || selected.Account == nil {
		return accountcore.AccountSnapshot{}, false, err
	}
	return service.AccountSnapshotView(selected.Account), true, err
}
func (p *grokVoiceAdapter) AcquireVoice(_ context.Context, _ accountcore.AccountSnapshot) (func(), bool) {
	var started bool
	return p.h.acquireResponsesAccountSlot(p.c, p.apiKey.GroupID, "", p.selection, false, &started, p.reqLog)
}
func (p *grokVoiceAdapter) ForwardVoice(ctx context.Context, _ accountcore.AccountSnapshot, request gatewaymedia.VoiceRequest) gatewaymedia.VoiceOutcome {
	result, err := p.h.gatewayService.ForwardGrokVoice(ctx, p.c, p.selection.Account, request.Endpoint, request.Body, request.ContentType)
	outcome := gatewaymedia.VoiceOutcome{Err: err}
	if result != nil {
		outcome.Result = &gatewaymedia.VoiceResult{RequestID: result.RequestID, Headers: result.UpstreamHeaders.Clone(), Model: result.Model, UpstreamModel: result.UpstreamModel, Duration: result.Duration, AudioUsage: cloneGrokAudioUsage(result.AudioUsage)}
	}
	var failure *service.UpstreamFailoverError
	if errors.As(err, &failure) {
		outcome.RetryNext = failure.ShouldRetryNextAccount()
	}
	return outcome
}
func (p *grokVoiceAdapter) CompleteVoice(_ context.Context, _ accountcore.AccountSnapshot, request gatewaymedia.VoiceRequest, result *gatewaymedia.VoiceResult) {
	if result == nil {
		return
	}
	value := &service.OpenAIForwardResult{RequestID: result.RequestID, UpstreamHeaders: http.Header(result.Headers).Clone(), Model: result.Model, UpstreamModel: result.UpstreamModel, Duration: result.Duration, AudioUsage: cloneGrokAudioUsage(result.AudioUsage)}
	p.h.recordGrokVoiceUsage(p.c, p.apiKey, p.selection.Account, p.subscription, request.Endpoint, request.Body, value)
}

// cloneGrokAudioUsage 在同步执行与完成输入之间保留独立计量快照。
func cloneGrokAudioUsage(value *service.AudioUsage) *service.AudioUsage {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (p *grokRealtimeAdapter) Relay(ctx context.Context, client, server upstream.FrameConn) (bool, error) {
	return p.h.gatewayService.RelayGrokRealtimeFrames(ctx, client, server)
}
func (p *grokRealtimeAdapter) CompleteRealtime(_ context.Context, _ accountcore.AccountSnapshot, model string, elapsed time.Duration) {
	subscription, _ := middleware2.GetSubscriptionFromContext(p.c)
	result := grokRealtimeBillingResult(model, elapsed, true)
	if result != nil {
		p.h.recordGrokVoiceUsage(p.c, p.apiKey, p.selection.Account, subscription, "realtime", nil, result)
	}
}
