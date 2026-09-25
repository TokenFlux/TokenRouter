package mediaentry

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func grokRealtimeBillingResult(model string, elapsed time.Duration, audioObserved bool) *forwardcore.OpenAIResult {
	usage := gatewaymedia.RealtimeAudioUsage(elapsed, audioObserved)
	if usage == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{RequestID: gatewaycapture.StableRealtimeBillingRequestID(""), Model: model, Duration: elapsed, AudioUsage: usage}
}

// recordGrokVoiceUsage 在存在 AudioUsage 时按分组音频价格结算 TTS、STT 或 Realtime。
func (h *Runtime) recordGrokVoiceUsage(
	c *gin.Context,
	apiKey *apikey.APIKey,
	account *gatewaycapture.ExecutionAccount,
	subscription *billing.UserSubscription,
	endpoint string,
	body []byte,
	result *forwardcore.OpenAIResult,
) {
	if h == nil || c == nil || apiKey == nil || account == nil || result == nil {
		return
	}
	if result.AudioUsage == nil {
		return
	}
	// 即使调用方遗漏，也为 Realtime、TTS 和 STT 强制生成持久结算 ID。
	if mode := strings.TrimSpace(result.AudioUsage.Mode); mode == "realtime" {
		result.RequestID = gatewaycapture.StableRealtimeBillingRequestID(result.RequestID)
	} else {
		result.RequestID = gatewaycapture.StableAudioBillingRequestID(result.RequestID)
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(c)
	sessionID := gatewayhttp.ExtractClientSessionID(c)
	requestPayloadHash := billing.HashUsageRequestPayload(body)
	if requestPayloadHash == "" {
		requestPayloadHash = billing.HashUsageRequestPayload([]byte(endpoint))
	}
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(c, account.Record.Platform)
	quotaPlatform := admission.QuotaPlatform(c.Request.Context(), apiKey)
	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = endpoint
	}

	channelFields := gatewayhttp.ClientRequestedUsageFields(c, routing.ChannelMappingResult{}, model, result.UpstreamModel)
	completionInput := gatewaycapture.CaptureOpenAI(c.Request.Context(), &gatewaycapture.OpenAICapture{
		Result:             result,
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            gatewaycapture.ExecutionCompletionRecord(account),
		Subscription:       subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.bindings.Quota,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    sessionID,
		ChannelUsageFields: channelFields,
	})
	completionRecorder := h.bindings.Common.Recorder
	completionLog := logging.L().With(
		zap.String("component", "handler.openai_gateway.grok_voice"),
		zap.Int64("user_id", apiKey.User.ID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("endpoint", endpoint),
		zap.Int64("account_id", account.Record.ID),
	)
	h.bindings.Common.Support.Submission.SubmitMandatory(c, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			completionLog.Error("grok_voice.record_usage_failed", zap.Error(err))
		}
	})
}

// grokRealtimeAdapter 只转换已选账号与技术连接，不保留第二套候选循环。
type grokRealtimeAdapter struct {
	h         *Runtime
	c         *gin.Context
	apiKey    *apikey.APIKey
	reqLog    *zap.Logger
	selection *gatewaycapture.SelectionResult
}

func (p *grokRealtimeAdapter) SelectRealtime(ctx context.Context, excluded map[int64]struct{}) (accountcore.AccountSnapshot, bool, error) {
	selected, _, err := p.h.bindings.Common.Selection.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", "", "", excluded, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityTextGeneration, false, false, capability.PlatformGrok)
	p.selection = selected
	if selected == nil || selected.Account == nil {
		return accountcore.AccountSnapshot{}, false, err
	}
	return gatewaycapture.ExecutionSnapshot(selected.Account), true, err
}
func (p *grokRealtimeAdapter) AcquireRealtime(_ context.Context, _ accountcore.AccountSnapshot) (func(), bool) {
	var started bool
	return p.h.bindings.Common.Support.AcquireResponsesAccountSlot(p.c, p.apiKey.GroupID, "", p.selection, false, &started, p.reqLog)
}
func (p *grokRealtimeAdapter) RealtimeCredential(ctx context.Context, _ accountcore.AccountSnapshot) (string, error) {
	token, _, err := p.h.bindings.Platform.Credential(ctx, p.c, p.selection.Account)
	return token, err
}
func (p *grokRealtimeAdapter) OpenRealtime(ctx context.Context, _ accountcore.AccountSnapshot, token, model string) (upstream.FrameConn, error) {
	conn, err := p.h.bindings.Platform.OpenRealtime(ctx, p.selection.Account, token, model)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
func (p *grokRealtimeAdapter) RealtimeOpenFailed(ctx context.Context, selected accountcore.AccountSnapshot, err error) {
	p.reqLog.Warn("grok_realtime.pre_accept_failed", zap.Int64("account_id", selected.ID), zap.Error(err))
	status := http.StatusBadGateway
	var dialErr *grok.RealtimeDialError
	if errors.As(err, &dialErr) && dialErr.StatusCode > 0 {
		status = dialErr.StatusCode
	}
	p.h.bindings.Platform.RealtimeError(ctx, p.selection.Account, status, []byte(err.Error()))
}

// grokVoiceAdapter 只桥接单次选择、HTTP 原生执行及完成投影。
type grokVoiceAdapter struct {
	h            *Runtime
	c            *gin.Context
	apiKey       *apikey.APIKey
	subscription *billing.UserSubscription
	reqLog       *zap.Logger
	selection    *gatewaycapture.SelectionResult
}

func (p *grokVoiceAdapter) SelectVoice(ctx context.Context, excluded map[int64]struct{}) (accountcore.AccountSnapshot, bool, error) {
	selected, _, err := p.h.bindings.Common.Selection.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", "", "grok-4.5", excluded, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityTextGeneration, false, false, capability.PlatformGrok)
	p.selection = selected
	if selected == nil || selected.Account == nil {
		return accountcore.AccountSnapshot{}, false, err
	}
	return gatewaycapture.ExecutionSnapshot(selected.Account), true, err
}
func (p *grokVoiceAdapter) AcquireVoice(_ context.Context, _ accountcore.AccountSnapshot) (func(), bool) {
	var started bool
	return p.h.bindings.Common.Support.AcquireResponsesAccountSlot(p.c, p.apiKey.GroupID, "", p.selection, false, &started, p.reqLog)
}
func (p *grokVoiceAdapter) ForwardVoice(ctx context.Context, _ accountcore.AccountSnapshot, request gatewaymedia.VoiceRequest) gatewaymedia.VoiceOutcome {
	result, err := p.h.bindings.Platform.Voice(ctx, p.c, p.selection.Account, request.Endpoint, request.Body, request.ContentType)
	outcome := gatewaymedia.VoiceOutcome{Err: err}
	if result != nil {
		outcome.Result = &gatewaymedia.VoiceResult{RequestID: result.RequestID, Headers: http.Header(result.UpstreamHeaders).Clone(), Model: result.Model, UpstreamModel: result.UpstreamModel, Duration: result.Duration, AudioUsage: cloneGrokAudioUsage(result.AudioUsage)}
	}
	var failure *forwardcore.UpstreamFailoverError
	if errors.As(err, &failure) {
		outcome.RetryNext = failure.ShouldRetryNextAccount()
	}
	return outcome
}
func (p *grokVoiceAdapter) CompleteVoice(_ context.Context, _ accountcore.AccountSnapshot, request gatewaymedia.VoiceRequest, result *gatewaymedia.VoiceResult) {
	if result == nil {
		return
	}
	value := &forwardcore.OpenAIResult{RequestID: result.RequestID, UpstreamHeaders: http.Header(result.Headers).Clone(), Model: result.Model, UpstreamModel: result.UpstreamModel, Duration: result.Duration, AudioUsage: cloneGrokAudioUsage(result.AudioUsage)}
	p.h.recordGrokVoiceUsage(p.c, p.apiKey, p.selection.Account, p.subscription, request.Endpoint, request.Body, value)
}

// cloneGrokAudioUsage 在同步执行与完成输入之间保留独立计量快照。
func cloneGrokAudioUsage(value *protocol.AudioUsage) *protocol.AudioUsage {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (p *grokRealtimeAdapter) Relay(ctx context.Context, client, server upstream.FrameConn) (bool, error) {
	return p.h.bindings.Platform.RelayRealtime(ctx, client, server)
}
func (p *grokRealtimeAdapter) CompleteRealtime(_ context.Context, _ accountcore.AccountSnapshot, model string, elapsed time.Duration) {
	subscription, _ := gatewayhttp.SubscriptionFromContext(p.c)
	result := grokRealtimeBillingResult(model, elapsed, true)
	if result != nil {
		p.h.recordGrokVoiceUsage(p.c, p.apiKey, p.selection.Account, subscription, "realtime", nil, result)
	}
}
