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
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) Embeddings(c *gin.Context) { h.AuxiliaryHTTPHandler().Embeddings(c) }

// embeddingRequestAdapter 只适配当前请求、已选账号和 HTTP；尝试循环由 media 唯一拥有。
type embeddingRequestAdapter struct {
	userID         int64
	h              *OpenAIGatewayHandler
	c              *gin.Context
	apiKey         *apikey.APIKey
	subscription   *billing.UserSubscription
	reqModel       string
	channelMapping routing.ChannelMappingResult
	reqLog         *zap.Logger
	streamStarted  *bool
	selection      *gatewaycapture.SelectionResult
}

func (p *embeddingRequestAdapter) SelectEmbedding(ctx context.Context, excluded map[int64]struct{}) (accountcore.AccountSnapshot, bool, error) {
	selection, _, err := p.h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", "", p.reqModel, excluded, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityEmbeddings, false, false)
	p.selection = selection
	if selection == nil || selection.Account == nil {
		return accountcore.AccountSnapshot{}, false, err
	}
	return gatewaycapture.ExecutionSnapshot(selection.Account), true, err
}
func (p *embeddingRequestAdapter) AcquireEmbedding(_ context.Context, _ accountcore.AccountSnapshot) (func(), bool) {
	return p.h.acquireResponsesAccountSlot(p.c, p.apiKey.GroupID, "", p.selection, false, p.streamStarted, p.reqLog)
}
func (p *embeddingRequestAdapter) ForwardEmbedding(ctx context.Context, _ accountcore.AccountSnapshot, body []byte) gatewaymedia.EmbeddingOutcome {
	forwardBody := body
	if p.channelMapping.Mapped {
		forwardBody = p.h.gatewayService.ReplaceModelInBody(body, p.channelMapping.MappedModel)
	}
	size := p.c.Writer.Size()
	result, err := p.h.gatewayService.ForwardEmbeddings(ctx, p.c, p.selection.Account, forwardBody, "")
	outcome := gatewaymedia.EmbeddingOutcome{Result: embeddingResultView(result), Err: err, OutputChanged: p.c.Writer.Size() != size}
	var failure *forwardcore.UpstreamFailoverError
	if errors.As(err, &failure) {
		outcome.Failover = true
		outcome.StatusCode = failure.StatusCode
	}
	return outcome
}
func (p *embeddingRequestAdapter) ReportEmbedding(_ context.Context, _ accountcore.AccountSnapshot, result *gatewaymedia.EmbeddingResult, success bool, err error) {
	account := p.selection.Account
	if success {
		p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(p.c, account, p.reqModel, false, legacyEmbeddingResult(result)), true, nil)
		return
	}
	p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(p.c, account, p.reqModel, false, legacyEmbeddingResult(result)), false, nil, err)
}
func (p *embeddingRequestAdapter) SwitchEmbedding(_ accountcore.AccountSnapshot) {
	p.h.gatewayService.RecordOpenAIAccountSwitchForSelection(p.selection)
}
func (p *embeddingRequestAdapter) ClientGone() bool { return gatewayhttp.FailoverClientGone(p.c) }
func (p *embeddingRequestAdapter) ObserveEmbedding(event gatewaymedia.EmbeddingEvent) {
	switch event.Kind {
	case "selected":
		gatewayhttp.SetOpsSelectedAccount(p.c, event.Account.ID, event.Account.Platform)
	case "routing":
		gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsRoutingLatencyMsKey, event.Elapsed.Milliseconds())
	case "response":
		elapsed := event.Elapsed.Milliseconds()
		upstream, _ := getContextInt64(p.c, gatewayhttp.OpsUpstreamLatencyMsKey)
		if upstream > 0 && elapsed > upstream {
			elapsed -= upstream
		}
		gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsResponseLatencyMsKey, elapsed)
	case "select_canceled":
		p.reqLog.Info("openai_embeddings.account_select_aborted_client_disconnected", zap.Error(event.Outcome.Err))
	case "select_failed":
		p.reqLog.Warn("openai_embeddings.account_select_failed", zap.Error(event.Outcome.Err), zap.Int("excluded_account_count", event.Excluded))
	case "forward_canceled":
		p.reqLog.Info("openai_embeddings.failover_aborted_client_disconnected", zap.Int64("account_id", event.Account.ID), zap.Int("upstream_status", event.Outcome.StatusCode))
	case "switch":
		p.reqLog.Warn("openai_embeddings.upstream_failover_switching", zap.Int64("account_id", event.Account.ID), zap.Int("upstream_status", event.Outcome.StatusCode), zap.Int("switch_count", event.Switches), zap.Int("max_switches", event.MaxSwitches))
	case "completed":
		p.reqLog.Debug("openai_embeddings.request_completed", zap.Int64("account_id", event.Account.ID), zap.Int("switch_count", event.Switches))
	}
}
func (p *embeddingRequestAdapter) renderFailure(f *gatewaymedia.EmbeddingFailure) {
	if f == nil {
		return
	}
	switch f.Stage {
	case "selection":
		if f.Excluded == 0 {
			if p.h.handleOpenAISelectionBusinessError(p.c, f.Err, *p.streamStarted) {
				return
			}
			cls := classifyNoAccountErrorFromGin(p.c, p.h.gatewayService, p.apiKey, p.reqModel, p.reqModel, capability.PlatformOpenAI)
			if !cls.ModelNotFound {
				gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, f.Err)
			}
			p.h.errorResponse(p.c, cls.Status, cls.ErrType, cls.Message)
			return
		}
		var failure *forwardcore.UpstreamFailoverError
		if errors.As(f.Outcome.Err, &failure) {
			p.h.handleFailoverExhausted(p.c, failure, false)
		} else {
			p.h.errorResponse(p.c, http.StatusBadGateway, "api_error", "Upstream request failed")
		}
	case "empty_selection":
		cls := classifyNoAccountErrorFromGin(p.c, p.h.gatewayService, p.apiKey, p.reqModel, p.reqModel, capability.PlatformOpenAI)
		if !cls.ModelNotFound {
			gatewayhttp.MarkOpsRoutingCapacityLimited(p.c)
		}
		p.h.errorResponse(p.c, cls.Status, cls.ErrType, cls.Message)
	case "exhausted":
		var failure *forwardcore.UpstreamFailoverError
		if errors.As(f.Outcome.Err, &failure) {
			p.h.handleFailoverExhausted(p.c, failure, f.Outcome.OutputChanged)
		}
	case "forward":
		if !f.Outcome.OutputChanged {
			p.h.errorResponse(p.c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		}
		p.reqLog.Warn("openai_embeddings.forward_failed", zap.Int64("account_id", p.selection.Account.Record.ID), zap.Error(f.Err))
	}
}

func embeddingResultView(result *forwardcore.OpenAIResult) *gatewaymedia.EmbeddingResult {
	if result == nil {
		return nil
	}
	return &gatewaymedia.EmbeddingResult{RequestID: result.RequestID, Model: result.Model, BillingModel: result.BillingModel, UpstreamModel: result.UpstreamModel, Headers: http.Header(result.UpstreamHeaders).Clone(), Usage: result.Usage, Duration: result.Duration}
}
func legacyEmbeddingResult(result *gatewaymedia.EmbeddingResult) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{RequestID: result.RequestID, Model: result.Model, BillingModel: result.BillingModel, UpstreamModel: result.UpstreamModel, UpstreamHeaders: http.Header(result.Headers).Clone(), Usage: result.Usage, Duration: result.Duration}
}
func (p *embeddingRequestAdapter) CompleteEmbedding(_ context.Context, _ accountcore.AccountSnapshot, value *gatewaymedia.EmbeddingResult) {
	c, h, apiKey, account := p.c, p.h, p.apiKey, p.selection.Account
	result := legacyEmbeddingResult(value)
	userAgent := c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(c)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(c, account.Record.Platform)
	quotaPlatform := admission.QuotaPlatform(c.Request.Context(), apiKey)
	clientSessionID := gatewayhttp.ExtractClientSessionID(c)
	// 异步任务只读取此处固化的渠道结果，不能再读取可变 HTTP Context。
	channelFields := p.channelMapping.ToUsageFields(p.reqModel, result.UpstreamModel)
	subscription, reqModel, userID := p.subscription, p.reqModel, p.userID
	completionInput := gatewaycapture.CaptureOpenAI(c.Request.Context(), &gatewaycapture.OpenAICapture{Result: result, APIKey: apiKey, User: apiKey.User, Account: gatewaycapture.ExecutionCompletionRecord(account), Subscription: subscription, InboundEndpoint: inboundEndpoint, UpstreamEndpoint: upstreamEndpoint, UserAgent: userAgent, IPAddress: clientIP, APIKeyService: h.apiKeyService, QuotaPlatform: quotaPlatform, ClientSessionID: clientSessionID, ChannelUsageFields: channelFields})
	completionRecorder := h.completionRuntime()
	completionLog := logging.L().With(zap.String("component", "handler.openai_gateway.embeddings"), zap.Int64("user_id", userID), zap.Int64("api_key_id", apiKey.ID), zap.Any("group_id", apiKey.GroupID), zap.String("model", reqModel), zap.Int64("account_id", account.Record.ID))
	h.submitOpenAIUsageRecordTask(c, result, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			completionLog.Error("openai_embeddings.record_usage_failed", zap.Error(err))
		}
	})
}
