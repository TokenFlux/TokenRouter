package handler

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"context"
	"errors"
	"net/http"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) AlphaSearch(c *gin.Context) { h.AuxiliaryHTTPHandler().AlphaSearch(c) }

// recordAlphaSearchUsage 为一次成功的 alpha/search 网页搜索落按次计费用量行
// （上游不返回 usage 字段，按 WebSearchCalls 走分组单价 × 倍率的按次口径）。
// 与 images 一致使用 mandatory 池提交，池满时同步兜底执行，保证扣费不丢。
func (h *OpenAIGatewayHandler) recordAlphaSearchUsage(
	c *gin.Context,
	apiKey *apikey.APIKey,
	account *gatewaycapture.ExecutionAccount,
	subscription *billing.UserSubscription,
	channelMapping routing.ChannelMappingResult,
	requestedModel string,
	body []byte,
	result *forwardcore.OpenAIResult,
	userID int64,
) {
	userAgent := c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(c)
	sessionID := gatewayhttp.ExtractClientSessionID(c)
	requestPayloadHash := billing.HashUsageRequestPayload(body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(c, account.Record.Platform)
	quotaPlatform := admission.QuotaPlatform(c.Request.Context(), apiKey)

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
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    sessionID,
		ChannelUsageFields: channelMapping.ToUsageFields(requestedModel, result.UpstreamModel),
	})
	completionRecorder := h.completionRuntime()
	completionLog := logging.L().With(
		zap.String("component", "handler.openai_gateway.alpha_search"),
		zap.Int64("user_id", userID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", requestedModel),
		zap.Int64("account_id", account.Record.ID),
	)
	h.submitMandatoryUsageRecordTask(c, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			completionLog.Error("openai_alpha_search.record_usage_failed", zap.Error(err))
		}
	})
}

// alphaRequestAdapter 保留 HTTP、平台恢复与观察端口，账号尝试次序由 media 拥有。
type alphaRequestAdapter struct {
	h                           *OpenAIGatewayHandler
	c                           *gin.Context
	apiKey                      *apikey.APIKey
	subscription                *billing.UserSubscription
	channelMapping              routing.ChannelMappingResult
	requestedModel, sessionHash string
	originalBody                []byte
	userID                      int64
	reqLog                      *zap.Logger
	streamStarted               *bool
	selection                   *gatewaycapture.SelectionResult
	oauth429                    failover.OAuth429State
}

func (p *alphaRequestAdapter) SelectAlpha(ctx context.Context, excluded map[int64]struct{}) (gatewaymedia.AlphaSelection, bool, error) {
	selected, _, err := p.h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", p.sessionHash, p.requestedModel, excluded, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityAlphaSearch, false, false, capability.PlatformOpenAI)
	p.selection = selected
	if selected == nil || selected.Account == nil {
		return gatewaymedia.AlphaSelection{}, false, err
	}
	return gatewaymedia.AlphaSelection{Account: gatewaycapture.ExecutionSnapshot(selected.Account), RetryLimit: selected.Account.View().GetPoolModeRetryCount()}, true, err
}
func (p *alphaRequestAdapter) AcquireAlpha(_ context.Context, _ gatewaymedia.AlphaSelection) (func(), bool) {
	return p.h.acquireResponsesAccountSlot(p.c, p.apiKey.GroupID, p.sessionHash, p.selection, false, p.streamStarted, p.reqLog)
}
func (p *alphaRequestAdapter) ForwardAlpha(ctx context.Context, _ gatewaymedia.AlphaSelection, body []byte) gatewaymedia.AlphaOutcome {
	size := p.c.Writer.Size()
	account := p.selection.Account
	match := p.h.gatewayService.MatchOpenAITLSFingerprintRouterForRequest(p.c, account)
	var result *forwardcore.OpenAIResult
	err := p.h.gatewayService.EnforceOpenAIClientPolicyForRequest(ctx, p.c, account, body, match)
	if err == nil {
		result, err = p.h.gatewayService.ForwardAlphaSearch(ctx, p.c, account, body, match)
	}
	outcome := gatewaymedia.AlphaOutcome{Result: alphaResultView(result), Err: err, OutputChanged: p.c.Writer.Size() != size}
	var failure *forwardcore.UpstreamFailoverError
	if errors.As(err, &failure) {
		outcome.Failure = failure.RetryFailure()
	}
	return outcome
}
func (p *alphaRequestAdapter) ReportAlpha(_ context.Context, _ gatewaymedia.AlphaSelection, result *gatewaymedia.AlphaResult, success bool, err error) {
	account := p.selection.Account
	if success {
		p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(p.c, account, p.requestedModel, false, legacyAlphaResult(result)), true, nil)
		return
	}
	p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(p.c, account, p.requestedModel, false, legacyAlphaResult(result)), false, nil, err)
}
func (p *alphaRequestAdapter) CompleteAlpha(_ context.Context, _ gatewaymedia.AlphaSelection, result *gatewaymedia.AlphaResult) {
	p.h.recordAlphaSearchUsage(p.c, p.apiKey, p.selection.Account, p.subscription, p.channelMapping, p.requestedModel, p.originalBody, legacyAlphaResult(result), p.userID)
}
func (p *alphaRequestAdapter) SwitchAlpha(gatewaymedia.AlphaSelection) {
	p.h.gatewayService.RecordOpenAIAccountSwitch()
}
func (p *alphaRequestAdapter) StopAlpha429(_ gatewaymedia.AlphaSelection, status, count int) bool {
	return p.h.gatewayService.ShouldStopOpenAIOAuth429Failover(p.selection.Account, status, count, &p.oauth429)
}
func (p *alphaRequestAdapter) AlphaClientGone() bool { return gatewayhttp.FailoverClientGone(p.c) }
func (p *alphaRequestAdapter) ObserveAlpha(e gatewaymedia.AlphaEvent) {
	switch e.Kind {
	case "selected":
		gatewayhttp.SetOpsSelectedAccount(p.c, e.Account.ID, e.Account.Platform)
	case "routing":
		gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsRoutingLatencyMsKey, e.Elapsed.Milliseconds())
	case "response":
		gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsResponseLatencyMsKey, e.Elapsed.Milliseconds())
	case "select_canceled":
		p.reqLog.Info("openai_alpha_search.account_select_aborted_client_disconnected", zap.Error(e.Outcome.Err))
	case "forward_canceled":
		p.reqLog.Info("openai_alpha_search.failover_aborted_client_disconnected", zap.Int64("account_id", e.Account.ID), zap.Int("upstream_status", e.Outcome.Failure.StatusCode))
	case "retry":
		p.reqLog.Warn("openai_alpha_search.same_account_retry", zap.Int64("account_id", e.Account.ID), zap.Int("upstream_status", e.Outcome.Failure.StatusCode), zap.Int("retry_limit", e.RetryLimit), zap.Int("retry_count", e.RetryCount), zap.Duration("retry_delay", e.RetryDelay))
	case "switch":
		p.reqLog.Warn("openai_alpha_search.upstream_failover_switching", zap.Int64("account_id", e.Account.ID), zap.Int("upstream_status", e.Outcome.Failure.StatusCode), zap.Int("switch_count", e.Switches), zap.Int("max_switches", e.MaxSwitches))
	}
}
func (p *alphaRequestAdapter) renderFailure(f *gatewaymedia.AlphaFailure) {
	if f == nil {
		return
	}
	switch f.Stage {
	case "selection":
		if f.Excluded == 0 {
			if f.Err != nil && p.h.handleOpenAISelectionBusinessError(p.c, f.Err, *p.streamStarted) {
				return
			}
			cls := classifyNoAccountErrorFromGin(p.c, p.h.gatewayService, p.apiKey, p.requestedModel, p.requestedModel, capability.PlatformOpenAI)
			if !cls.ModelNotFound {
				gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, f.Err)
			}
			p.h.errorResponse(p.c, cls.Status, cls.ErrType, cls.Message)
			return
		}
		var last *forwardcore.UpstreamFailoverError
		if errors.As(f.Outcome.Err, &last) {
			p.h.handleFailoverExhausted(p.c, last, false)
		} else {
			p.h.errorResponse(p.c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		}
	case "forward":
		if !f.Outcome.OutputChanged {
			p.h.errorResponse(p.c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		}
		p.reqLog.Warn("openai_alpha_search.forward_failed", zap.Int64("account_id", p.selection.Account.Record.ID), zap.Error(f.Err))
	case "exhausted":
		var last *forwardcore.UpstreamFailoverError
		if errors.As(f.Outcome.Err, &last) {
			p.h.handleFailoverExhausted(p.c, last, f.Outcome.OutputChanged)
		}
	}
}
func alphaResultView(result *forwardcore.OpenAIResult) *gatewaymedia.AlphaResult {
	if result == nil {
		return nil
	}
	return &gatewaymedia.AlphaResult{RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, UpstreamEndpoint: result.UpstreamEndpoint, Headers: http.Header(result.UpstreamHeaders).Clone(), ResponseHeaders: http.Header(result.ResponseHeaders).Clone(), Duration: result.Duration, Calls: result.WebSearchCalls}
}
func legacyAlphaResult(result *gatewaymedia.AlphaResult) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, UpstreamEndpoint: result.UpstreamEndpoint, UpstreamHeaders: http.Header(result.Headers).Clone(), ResponseHeaders: http.Header(result.ResponseHeaders).Clone(), Duration: result.Duration, WebSearchCalls: result.Calls}
}
