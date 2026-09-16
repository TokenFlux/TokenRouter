// 媒体请求专属 Adapter 转换 HTTP、选择投影和完成快照；重试次序由 media 唯一实现。
package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type generationRequestAdapter struct {
	grok                                    bool
	h                                       *OpenAIGatewayHandler
	c                                       *gin.Context
	apiKey                                  *service.APIKey
	subject                                 middleware2.AuthSubject
	subscription                            *service.UserSubscription
	reqLog                                  *zap.Logger
	streamStarted                           *bool
	parsed                                  *service.OpenAIImagesRequest
	body                                    []byte
	requestModel, routingModel, sessionHash string
	channelMapping                          service.ChannelMappingResult
	endpoint                                service.GrokMediaEndpoint
	requestID, contentType, videoCreated    string
	boundAccountID                          int64
	selection                               *service.AccountSelectionResult
	decision                                service.OpenAIAccountScheduleDecision
	oauth429                                service.OpenAIOAuth429FailoverState
	writerBefore                            int
}

func (p *generationRequestAdapter) SelectGeneration(ctx context.Context, excluded map[int64]struct{}) (gatewaymedia.GenerationSelection, bool, error) {
	var err error
	if p.grok {
		p.selection, p.decision, err = p.h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", p.sessionHash, p.routingModel, excluded, service.OpenAIUpstreamTransportHTTPSSE, grokMediaRequiredCapability(p.endpoint), false, false, service.PlatformGrok)
	} else {
		p.selection, p.decision, err = p.h.gatewayService.SelectAccountWithSchedulerForImages(ctx, p.apiKey.GroupID, p.sessionHash, p.requestModel, excluded, p.parsed.RequiredCapability)
	}
	if p.selection == nil || p.selection.Account == nil {
		return gatewaymedia.GenerationSelection{}, false, err
	}
	return gatewaymedia.GenerationSelection{Account: service.AccountSnapshotView(p.selection.Account), RetryLimit: p.selection.Account.GetPoolModeRetryCount()}, true, err
}
func (p *generationRequestAdapter) ActivateGeneration(_ gatewaymedia.GenerationSelection) {
	account := p.selection.Account
	if !p.grok {
		p.logSchedule()
	}
	p.sessionHash = ensureOpenAIPoolModeSessionHash(p.sessionHash, account)
	if !p.grok {
		p.reqLog.Debug("openai.images.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
	}
	setOpsSelectedAccount(p.c, account.ID, account.Platform)
}

func (p *generationRequestAdapter) GenerationEligible(ctx context.Context, _ gatewaymedia.GenerationSelection) (bool, string, error) {
	return p.h.ensureGrokMediaAccountEligibility(ctx, p.selection.Account)
}
func (p *generationRequestAdapter) AcquireGeneration(_ context.Context, _ gatewaymedia.GenerationSelection) (func(), bool) {
	stream := false
	if p.parsed != nil {
		stream = p.parsed.Stream
	}
	return p.h.acquireResponsesAccountSlot(p.c, p.apiKey.GroupID, p.sessionHash, p.selection, stream, p.streamStarted, p.reqLog)
}
func (p *generationRequestAdapter) StartGenerationKeepalive() func() {
	return service.StartOpenAIImagesJSONKeepalive(p.c, p.h.openAIImagesJSONKeepaliveInterval())
}
func (p *generationRequestAdapter) ForwardGeneration(ctx context.Context, _ gatewaymedia.GenerationSelection, body []byte) gatewaymedia.GenerationOutcome {
	account := p.selection.Account
	var result *service.OpenAIForwardResult
	var err error
	p.writerBefore = p.c.Writer.Size()
	if p.grok {
		result, err = p.h.gatewayService.ForwardGrokMedia(ctx, p.c, account, p.endpoint, p.requestID, body, p.contentType)
	} else {
		p.writerBefore = service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(p.c)
		match := p.h.gatewayService.MatchOpenAITLSFingerprintRouterForRequest(p.c, account)
		err = p.h.gatewayService.EnforceOpenAIClientPolicyForRequest(ctx, p.c, account, body, match)
		if err == nil {
			result, err = p.h.gatewayService.ForwardImages(ctx, p.c, account, body, p.parsed, p.routingModel, match)
		}
	}
	after := p.c.Writer.Size()
	if !p.grok {
		after = service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(p.c)
	}
	outcome := gatewaymedia.GenerationOutcome{Result: generationResultView(result), Err: err, OutputChanged: after != p.writerBefore}
	var failure *service.UpstreamFailoverError
	if errors.As(err, &failure) {
		outcome.Failure = failure.RetryFailure()
		outcome.ReportFailure = failure.ShouldReportAccountScheduleFailure()
	}
	var imageErr *service.OpenAIImagesUpstreamError
	if errors.As(err, &imageErr) {
		outcome.ImageError = true
		outcome.ImageErrorRetryable = service.IsOpenAIImagesRetryableUpstreamError(imageErr)
		outcome.ImageErrorStatus = imageErr.StatusCode
		outcome.ImageErrorType = imageErr.ErrorType
		outcome.ImageErrorCode = imageErr.Code
	}
	return outcome
}
func (p *generationRequestAdapter) ReportGeneration(_ context.Context, _ gatewaymedia.GenerationSelection, value *gatewaymedia.GenerationResult, success bool, err error) {
	account := p.selection.Account
	result := legacyGenerationResult(value)
	if p.grok {
		p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, grokMediaScheduleModel(account, p.routingModel, result), success, nil)
		return
	}
	if success {
		p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(p.c, account, p.requestModel, false, result), true, nil)
		return
	}
	p.h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(p.c, account, p.requestModel, false, result), false, nil, err)
}
func (p *generationRequestAdapter) SwitchGeneration(gatewaymedia.GenerationSelection) {
	p.h.gatewayService.RecordOpenAIAccountSwitchForSelection(p.selection)
}
func (p *generationRequestAdapter) StopGeneration429(_ gatewaymedia.GenerationSelection, status, count int) bool {
	return p.h.gatewayService.ShouldStopOpenAIOAuth429Failover(p.selection.Account, status, count, &p.oauth429)
}
func (p *generationRequestAdapter) GenerationClientGone() bool { return failoverClientGone(p.c) }
func (p *generationRequestAdapter) logSchedule() {
	name := "openai.images"
	if p.grok {
		name = "grok_media"
	}
	d := p.decision
	p.reqLog.Debug(name+".account_schedule_decision", zap.String("layer", d.Layer), zap.Bool("sticky_session_hit", d.StickySessionHit), zap.Int("candidate_count", d.CandidateCount), zap.Int("top_k", d.TopK), zap.Int64("latency_ms", d.LatencyMs), zap.Float64("load_skew", d.LoadSkew))
}
func (p *generationRequestAdapter) ObserveGeneration(e gatewaymedia.GenerationEvent) {
	name := "openai.images"
	if p.grok {
		name = "grok_media"
	}
	status := 0
	if e.Outcome.Failure != nil {
		status = e.Outcome.Failure.StatusCode
	}
	switch e.Kind {
	case "selecting":
		p.reqLog.Debug(name+".account_selecting", zap.Int("excluded_account_count", e.Excluded))
	case "select_failed":
		p.reqLog.Warn(name+".account_select_failed", zap.Error(e.Outcome.Err), zap.Int("excluded_account_count", e.Excluded))
	case "select_canceled":
		p.reqLog.Info(name+".account_select_aborted_client_disconnected", zap.Error(e.Outcome.Err))
	case "forward_canceled":
		p.reqLog.Info(name+".failover_aborted_client_disconnected", zap.Int64("account_id", e.Selection.Account.ID), zap.Int("upstream_status", status))
	case "schedule":
		p.logSchedule()
	case "ineligible":
		p.reqLog.Warn(name+".account_eligibility_rejected", zap.Int64("account_id", e.Selection.Account.ID), zap.String("reason", e.Reason), zap.Bool("probe_failed", e.ProbeFailed))
	case "routing":
		service.SetOpsLatencyMs(p.c, service.OpsRoutingLatencyMsKey, e.Elapsed.Milliseconds())
	case "response":
		elapsed := e.Elapsed.Milliseconds()
		upstream, _ := getContextInt64(p.c, service.OpsUpstreamLatencyMsKey)
		if upstream > 0 && elapsed > upstream {
			elapsed -= upstream
		}
		service.SetOpsLatencyMs(p.c, service.OpsResponseLatencyMsKey, elapsed)
		if !p.grok && e.Outcome.Result != nil && e.Outcome.Result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(p.c, service.OpsTimeToFirstTokenMsKey, int64(*e.Outcome.Result.FirstTokenMs))
		}
	case "partial":
		p.reqLog.Warn(name+".forward_partial_error_with_image_result", zap.Int64("account_id", e.Selection.Account.ID), zap.Int("image_count", e.Outcome.Result.ImageCount), zap.Error(e.Outcome.Err))
	case "image_error":
		event := "upstream_user_error"
		if e.Outcome.ImageErrorRetryable {
			event = "upstream_server_error_after_flush"
		}
		p.reqLog.Warn(name+"."+event, zap.Int64("account_id", e.Selection.Account.ID), zap.Int("status_code", e.Outcome.ImageErrorStatus), zap.String("error_type", e.Outcome.ImageErrorType), zap.String("error_code", e.Outcome.ImageErrorCode), zap.Error(e.Outcome.Err))
	case "after_output":
		p.reqLog.Warn(name+".upstream_failover_skipped_after_flush", zap.Int64("account_id", e.Selection.Account.ID), zap.Int("upstream_status", status))
	case "retry":
		p.reqLog.Warn(name+".pool_mode_same_account_retry", zap.Int64("account_id", e.Selection.Account.ID), zap.Int("upstream_status", status), zap.Int("retry_limit", e.RetryLimit), zap.Int("retry_count", e.RetryCount), zap.Duration("retry_delay", e.Delay))
	case "switch":
		p.reqLog.Warn(name+".upstream_failover_switching", zap.Int64("account_id", e.Selection.Account.ID), zap.Int("upstream_status", status), zap.Int("switch_count", e.Switches), zap.Int("max_switches", e.MaxSwitches))
	case "completed":
		p.reqLog.Debug(name+".request_completed", zap.Int64("account_id", e.Selection.Account.ID), zap.Int("switch_count", e.Switches))
	}
}
func (p *generationRequestAdapter) EndGeneration(f gatewaymedia.GenerationFailure) {
	id := int64(0)
	if p.selection != nil && p.selection.Account != nil {
		id = p.selection.Account.ID
	}
	gatewayhttp.WriteGenerationFailure(f, gatewayhttp.MediaFailureContext{Grok: p.grok, Generation: p.endpoint.IsGenerationRequest(), StreamStarted: *p.streamStarted, BoundAccountID: p.boundAccountID, AccountID: id, WriterBefore: p.writerBefore, Context: p.c, Log: p.reqLog}, p)
}

func (p *generationRequestAdapter) CompleteGeneration(ctx context.Context, _ gatewaymedia.GenerationSelection, value *gatewaymedia.GenerationResult) {
	if p.grok {
		p.completeGrok(ctx, value)
	} else {
		p.completeImages(value)
	}
}
func (p *generationRequestAdapter) completeImages(value *gatewaymedia.GenerationResult) {
	h, c, apiKey, account, requestModel, parsed, body, subscription, subject, channelMapping := p.h, p.c, p.apiKey, p.selection.Account, p.requestModel, p.parsed, p.body, p.subscription, p.subject, p.channelMapping
	result := legacyGenerationResult(value)
	if result != nil {
		// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
		if account.Type == service.AccountTypeOAuth && !account.IsShadow() {
			h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
		}
		h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), true, result.FirstTokenMs)
	} else {
		h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, requestModel, false, result), true, nil)
	}

	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	if parsed.Multipart {
		requestPayloadHash = service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	clientSessionID := service.ExtractClientSessionID(c)

	upstreamModel := ""
	if result != nil {
		upstreamModel = result.UpstreamModel
	}
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
		RequestBody:        body,
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      quotaPlatform,
		ClientSessionID:    clientSessionID,
		ChannelUsageFields: channelMapping.ToUsageFields(requestModel, upstreamModel),
	})
	completionRecorder := h.completionRuntime()
	completionLog := logger.L().With(
		zap.String("component", "handler.openai_gateway.images"),
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", requestModel),
		zap.Int64("account_id", account.ID),
	)
	h.submitMandatoryUsageRecordTask(c, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			completionLog.Error("openai.images.record_usage_failed", zap.Error(err))
		}
	})

}
func (p *generationRequestAdapter) completeGrok(requestCtx context.Context, value *gatewaymedia.GenerationResult) {
	h, c, apiKey, reqLog, account, requestModel, body, subscription, subject, channelMapping, endpoint, requestID, videoCreateStartedAt := p.h, p.c, p.apiKey, p.reqLog, p.selection.Account, p.requestModel, p.body, p.subscription, p.subject, p.channelMapping, p.endpoint, p.requestID, p.videoCreated
	result := legacyGenerationResult(value)
	if isGrokVideoCreateEndpoint(endpoint) && strings.TrimSpace(result.ResponseID) != "" {
		// 视频创建阶段暂不扣费，保存模型、时长和分辨率供完成查询定价。
		pending := service.GrokVideoPendingBilling{
			Model:                requestModel,
			BillingModel:         firstNonEmptyString(result.BillingModel, requestModel),
			UpstreamModel:        result.UpstreamModel,
			VideoResolution:      result.VideoResolution,
			VideoDurationSeconds: result.VideoDurationSeconds,
			OriginalModel:        clientRequestedModel(c, requestModel),
			// 用创建受理到首次发现完成的墙钟时间记录端到端耗时。
			CreatedAt: videoCreateStartedAt,
		}
		h.gatewayService.MediaVideoTasks().TrackCreated(requestCtx, apiKey.GroupID, result.ResponseID, subject.UserID, apiKey.ID, account.ID, pending, grokVideoObserver{log: reqLog})
	}
	if endpoint == service.GrokMediaEndpointVideoStatus || endpoint == service.GrokMediaEndpointVideoContent {
		taskID := strings.TrimSpace(requestID)
		if billResult := prepareGrokVideoCompletionBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result); billResult != nil {
			recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, account, billResult, billResult.Model, channelMapping, body, taskID)
		}
	} else if shouldRecordGrokMediaUsage(endpoint, requestModel, result) {
		recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, account, result, requestModel, channelMapping, body, requestID)
	}
}

func generationResultView(r *service.OpenAIForwardResult) *gatewaymedia.GenerationResult {
	if r == nil {
		return nil
	}
	return gatewaymedia.CloneGenerationResult(&gatewaymedia.GenerationResult{RequestID: r.RequestID, ResponseID: r.ResponseID, Model: r.Model, BillingModel: r.BillingModel, UpstreamModel: r.UpstreamModel, Usage: r.Usage, Stream: r.Stream, Duration: r.Duration, FirstTokenMs: r.FirstTokenMs, ImageCount: r.ImageCount, VideoCount: r.VideoCount, VideoDurationSeconds: r.VideoDurationSeconds, ImageSize: r.ImageSize, ImageInputSize: r.ImageInputSize, ImageOutputSize: r.ImageOutputSize, ImageSizeSource: r.ImageSizeSource, VideoResolution: r.VideoResolution, ImageOutputSizes: r.ImageOutputSizes, ImageSizeBreakdown: r.ImageSizeBreakdown, Headers: r.UpstreamHeaders.Clone(), ResponseHeaders: r.ResponseHeaders.Clone()})
}

func legacyGenerationResult(r *gatewaymedia.GenerationResult) *service.OpenAIForwardResult {
	if r == nil {
		return nil
	}
	r = gatewaymedia.CloneGenerationResult(r)
	return &service.OpenAIForwardResult{RequestID: r.RequestID, ResponseID: r.ResponseID, Model: r.Model, BillingModel: r.BillingModel, UpstreamModel: r.UpstreamModel, Usage: r.Usage, Stream: r.Stream, Duration: r.Duration, FirstTokenMs: r.FirstTokenMs, ImageCount: r.ImageCount, VideoCount: r.VideoCount, VideoDurationSeconds: r.VideoDurationSeconds, ImageSize: r.ImageSize, ImageInputSize: r.ImageInputSize, ImageOutputSize: r.ImageOutputSize, ImageSizeSource: r.ImageSizeSource, VideoResolution: r.VideoResolution, ImageOutputSizes: r.ImageOutputSizes, ImageSizeBreakdown: r.ImageSizeBreakdown, UpstreamHeaders: http.Header(r.Headers).Clone(), ResponseHeaders: http.Header(r.ResponseHeaders).Clone()}
}

// 媒体最终错误接口只适配父层共同错误分类、风控观察和响应写入。
func (p *generationRequestAdapter) MediaClassify() gatewayhttp.MediaNoAccount {
	platform := service.PlatformOpenAI
	routing := p.requestModel
	if p.grok {
		platform = service.PlatformGrok
		routing = p.routingModel
	}
	result := classifyNoAccountErrorFromGin(p.c, p.h.gatewayService, p.apiKey, p.requestModel, routing, platform)
	return gatewayhttp.MediaNoAccount{ModelNotFound: result.ModelNotFound, Status: result.Status, Type: result.ErrType, Message: result.Message}
}
func (p *generationRequestAdapter) MediaNoAvailable(err error) bool {
	return errors.Is(err, service.ErrNoAvailableAccounts)
}
func (p *generationRequestAdapter) MediaCapacity(err error, conditional bool) {
	if conditional {
		markOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
	} else {
		markOpsRoutingCapacityLimited(p.c)
	}
}
func (p *generationRequestAdapter) MediaError(status int, typ, message string, stream bool) {
	if stream {
		p.h.handleStreamingAwareError(p.c, status, typ, message, *p.streamStarted)
	} else {
		p.h.errorResponse(p.c, status, typ, message)
	}
}
func (p *generationRequestAdapter) MediaFailover(err error, stream bool) {
	var value *service.UpstreamFailoverError
	if errors.As(err, &value) {
		p.h.handleFailoverExhausted(p.c, value, stream)
	}
}
func (p *generationRequestAdapter) MediaSimpleExhausted() {
	p.h.handleFailoverExhaustedSimple(p.c, 502, *p.streamStarted)
}
func (p *generationRequestAdapter) mediaStatus() int {
	status, _ := getContextInt64(p.c, service.OpsUpstreamStatusCodeKey)
	return int(status)
}
func (p *generationRequestAdapter) MediaForwardCyber(err error) bool {
	return p.h.recordOpenAIForwardErrorCyberWarning(p.c, p.reqLog, p.apiKey, p.selection.Account, p.requestModel, p.mediaStatus(), err)
}
func (p *generationRequestAdapter) MediaCyber(err error) {
	p.h.recordOpenAICyberWarning(p.c, p.reqLog, p.apiKey, p.selection.Account, p.requestModel, p.mediaStatus(), nil, err.Error())
}
func (p *generationRequestAdapter) MediaReportUnexpected(result *gatewaymedia.GenerationResult, err error) {
	p.ReportGeneration(p.c.Request.Context(), gatewaymedia.GenerationSelection{}, result, false, err)
}
func (p *generationRequestAdapter) MediaCommunicated(err error) bool {
	if p.grok {
		return service.IsResponseCommitted(p.c)
	}
	return openAIForwardErrorAlreadyCommunicated(p.c, p.writerBefore, err)
}
func (p *generationRequestAdapter) MediaEnsureFallback(err error) bool {
	return p.h.ensureOpenAIForwardErrorResponse(p.c, *p.streamStarted, err)
}
func (p *generationRequestAdapter) MediaWarnFailure(wrote bool) bool {
	return shouldLogOpenAIForwardFailureAsWarn(p.c, wrote)
}
