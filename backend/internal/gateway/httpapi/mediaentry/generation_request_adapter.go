package mediaentry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	routingerrors "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	upstreamgrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// generation_request_adapter.go connects media generation attempts to provider
// selection, output observations, and completion capture.
// A media attempt keeps its credential-bearing selected target inside
// request-local state and passes a separate provider snapshot to the media
// loop. Selection installs that target before acquisition, forwarding,
// feedback and completion. Ordinary Images can select Grok and then another
// platform, so the current target determines execution and presentation after
// each selection.
//
// Forwarding compares real response bytes, excluding JSON heartbeat padding
// for OpenAI image paths, before the loop decides whether a failed attempt
// can retry. Completion captures source payload, funding and mapped model
// identity synchronously before worker submission. Result conversions carry the
// immutable dispatch reference through the media loop to completion capture.
// Grok video creation stores pending pricing and saved ownership for later status
// or content lookup. Its task identifier remains separate from the stable
// financial request key used when completion is observed.

// generationRequestAdapter retains request identity and current media target
// across serial attempts.
type generationRequestAdapter struct {
	// grok restricts explicit Grok entry selection. Ordinary Images instead use
	// SelectImages, and usesGrok reads each current target to select execution.
	grok bool
	h    *Runtime // shared application media bindings
	// c, apiKey, subject and subscription retain authenticated request state.
	// Completion captures their recording values before enqueueing, so asynchronous
	// work does not read the Gin context or these request-local pointers.
	c       *gin.Context        // current HTTP request context
	apiKey  *apikey.APIKey      // current authenticated Key view
	subject authctx.AuthSubject // authenticated user and funding identity
	// subscription is the optional funding resolved at entry. Completion retains
	// that value when present instead of selecting another subscription after execution.
	subscription *billing.UserSubscription
	reqLog       *zap.Logger // request-scoped media diagnostic logger
	// streamStarted shares admission output state with the HTTP entry. Terminal
	// presentation dereferences this pointer, so the request factory supplies one.
	streamStarted *bool
	// parsed and body retain original image interpretation and request bytes across
	// attempts. Completion reads this storage before capture, and callers keep it
	// stable while forwarding and retry handling use the same request.
	parsed *gatewaymedia.ImageRequest // parsed Images endpoint input
	body   []byte                     // original media request bytes
	// requestModel retains the parsed input model and routingModel supplies provider
	// selection. sessionHash starts with entry correlation and can gain a pool-mode
	// identity during activation when the selected target requires one.
	// requestModel: parsed request model. routingModel: provider selection model.
	// sessionHash: retained selection session hash.
	requestModel, routingModel, sessionHash string
	groupMapping                            routingerrors.GroupMappingResult // retained group model mapping
	// endpoint identifies explicit Grok media operations and is adapted to Grok
	// Images generation or editing when ordinary Images selects that platform.
	endpoint upstreamgrok.GrokMediaEndpoint
	// requestID identifies an existing video task; contentType describes forwarding
	// bytes. videoCreated retains the entry's UTC RFC3339Nano creation start time
	// before provider attempts, for later deferred video duration observation.
	// requestID: existing video task identifier. contentType: forwarding content type.
	// videoCreated: video creation start time.
	requestID, contentType, videoCreated string
	boundProviderID                      int64 // saved video owner provider
	// selection and decision are replaced together by SelectGeneration. Subsequent
	// methods use this retained target instead of their snapshot parameter, so
	// callers perform one selection and its callbacks serially per request.
	selection *gatewaycapture.SelectionResult // retained current provider target
	decision  scheduler.PlatformDecision      // current provider scheduling observations
	oauth429  failover.OAuth429State          // request-local OAuth recovery state
	// writerBefore tracks preattempt output, excluding JSON padding for OpenAI
	// Images while using physical writer size on the Grok execution path.
	writerBefore int
}

// SelectGeneration selects and retains the next target with its scheduling
// observations. Explicit Grok entries constrain capability and platform;
// ordinary Images use their parsed capability. Missing selection or provider
// returns false with any selection error, and later callbacks use this
// retained target.
func (p *generationRequestAdapter) SelectGeneration(ctx context.Context, excluded map[int64]struct{}) (gatewaymedia.GenerationSelection, bool, error) {
	var err error
	if p.parsed != nil {
		ctx = gatewaymedia.WithImageGIFRequirement(ctx, strings.EqualFold(p.parsed.OutputFormat, "gif"))
	}
	if p.grok && (p.endpoint == upstreamgrok.GrokMediaEndpointImagesGenerations || p.endpoint == upstreamgrok.GrokMediaEndpointImagesEdits) {
		ctx = apikey.WithForcePlatform(ctx, capability.PlatformGrok)
		p.selection, p.decision, err = p.h.bindings.Platform.SelectImages(ctx, p.apiKey.GroupID, p.sessionHash, p.requestModel, excluded, gatewaymedia.ImageCapabilityNative)
	} else if p.grok {
		p.selection, p.decision, err = p.h.bindings.Common.Selection.SelectProviderWithSchedulerForCapability(ctx, p.apiKey.GroupID, "", p.sessionHash, p.routingModel, excluded, egress.OpenAIUpstreamTransportHTTPSSE, grokMediaRequiredCapability(p.endpoint), false, false, capability.PlatformGrok)
	} else {
		p.selection, p.decision, err = p.h.bindings.Platform.SelectImages(ctx, p.apiKey.GroupID, p.sessionHash, p.requestModel, excluded, p.parsed.RequiredCapability)
	}
	if p.selection == nil || p.selection.Provider == nil {
		return gatewaymedia.GenerationSelection{}, false, err
	}
	return gatewaymedia.GenerationSelection{Provider: gatewaycapture.ExecutionSnapshot(p.selection.Provider), RetryLimit: p.selection.Provider.View().GetPoolModeRetryCount()}, true, err
}

// ActivateGeneration installs selected-target metadata before provider
// admission using retained selection state. Ordinary Images adapts its
// endpoint when the target is Grok, and pool-mode selection can supply a
// session hash before Ops records the target.
func (p *generationRequestAdapter) ActivateGeneration(_ gatewaymedia.GenerationSelection) {
	provider := p.selection.Provider
	if !p.grok && p.usesGrok() {
		p.endpoint = upstreamgrok.GrokMediaEndpointImagesGenerations
		if p.parsed != nil && p.parsed.IsEdits() {
			p.endpoint = upstreamgrok.GrokMediaEndpointImagesEdits
		}
	}
	if !p.usesGrok() {
		p.logSchedule()
	}
	p.sessionHash = openaiattempt.EnsureOpenAIPoolModeSessionHash(p.sessionHash, provider)
	if !p.usesGrok() {
		p.reqLog.Debug("openai.images.provider_selected", zap.Int64("provider_id", provider.Record.ID), zap.String("provider_name", provider.Record.Name))
	}
	gatewayhttp.SetOpsSelectedProvider(p.c, provider.Record.RoutingSnapshot())
}

// GenerationEligible delegates Grok media eligibility using the retained
// current target. Call it after usable selection; missing billing
// observations can require the optional runtime probe, and the media loop
// handles rejection before acquisition.
func (p *generationRequestAdapter) GenerationEligible(ctx context.Context, _ gatewaymedia.GenerationSelection) (bool, string, error) {
	return p.h.ensureGrokMediaProviderEligibility(ctx, p.selection.Provider)
}

// AcquireGeneration obtains the retained target's provider capacity using
// parsed image stream mode when available. Call it after selection and
// activation; false means HTTP admission handled rejection, while the media
// loop owns returned cleanup.
func (p *generationRequestAdapter) AcquireGeneration(_ context.Context, _ gatewaymedia.GenerationSelection) (func(), bool) {
	stream := false
	if p.parsed != nil {
		stream = p.parsed.Stream
	}
	return p.h.bindings.Common.Support.AcquireResponsesProviderSlot(p.c, p.apiKey.GroupID, p.sessionHash, p.selection, stream, p.streamStarted, p.reqLog)
}

// StartGenerationKeepalive installs nonstreaming JSON padding with the
// Runtime's configured interval. Nonpositive intervals return a usable no-op
// stopper, and the Images loop retains the returned function across attempts
// until request exit.
func (p *generationRequestAdapter) StartGenerationKeepalive() func() {
	return gatewayhttp.StartOpenAIImagesJSONKeepalive(p.c, p.h.bindings.Options.ImageKeepalive)
}

// ForwardGeneration executes one retained target and returns media
// observations with recovery classification. The snapshot parameter is
// informational; the current selection supplies execution credentials and
// platform. OpenAI image dispatch first applies current client policy and TLS
// routing.
//
// Writer size is captured before forwarding and compared afterward, using
// heartbeat-adjusted counts on the OpenAI path. This leaves padding-only
// output distinct from actual media response content. Classified upstream
// failures contribute retry evidence, while image errors retain their own
// retryable, status, category and code observations for terminal handling.
func (p *generationRequestAdapter) ForwardGeneration(ctx context.Context, _ gatewaymedia.GenerationSelection, body []byte) gatewaymedia.GenerationOutcome {
	selected := telemetry.RequestProvider(p.c.Request.Context())
	ctx = telemetry.WithRequestProvider(ctx, selected.ProviderID, selected.Header)
	provider := p.selection.Provider
	var result *forwardcore.OpenAIResult
	var err error
	// JSON padding or SSE comments can commit HTTP before image content exists.
	// Exclude both formats so those bytes do not close the provider retry window.
	p.writerBefore = gatewayhttp.HeartbeatAdjustedWrittenSize(p.c)
	if p.usesGrok() {
		result, err = p.h.bindings.Platform.GrokMedia(ctx, p.c, provider, p.endpoint, p.requestID, body, p.contentType)
	} else {
		match := p.h.bindings.Common.Forward.MatchOpenAITLSFingerprintRouterForRequest(p.c, provider)
		err = p.h.bindings.Common.Forward.EnforceOpenAIClientPolicyForRequest(ctx, p.c, provider, body, match)
		if err == nil {
			result, err = p.h.bindings.Platform.Images(ctx, p.c, provider, body, p.parsed, p.routingModel, match)
		}
	}
	after := gatewayhttp.HeartbeatAdjustedWrittenSize(p.c)
	outcome := gatewaymedia.GenerationOutcome{Result: generationResultView(result), Err: err, OutputChanged: after != p.writerBefore}
	if failure, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		outcome.Failure = failure.RetryFailure()
		outcome.ReportFailure = failure.ShouldReportProviderScheduleFailure()
	}
	if imageErr, ok := errors.AsType[*openai.OpenAIImagesUpstreamError](err); ok {
		outcome.ImageError = true
		outcome.ImageErrorRetryable = openai.IsOpenAIImagesRetryableUpstreamError(imageErr)
		outcome.ImageErrorStatus = imageErr.StatusCode
		outcome.ImageErrorType = imageErr.ErrorType
		outcome.ImageErrorCode = imageErr.Code
	}
	return outcome
}

// ReportGeneration reports scheduling health using the retained selected
// target and supplied media result. Grok selects its media scheduling model
// and passes no error detail; other targets derive the OpenAI scheduling
// model and retain an error on failed feedback.
func (p *generationRequestAdapter) ReportGeneration(_ context.Context, _ gatewaymedia.GenerationSelection, value *gatewaymedia.GenerationResult, success bool, err error) {
	provider := p.selection.Provider
	result := legacyGenerationResult(value)
	if p.usesGrok() {
		p.h.bindings.Common.Selection.ReportOpenAIProviderScheduleResult(provider, grokMediaScheduleModel(provider, p.routingModel, result), success, nil)
		return
	}
	if success {
		p.h.bindings.Common.Selection.ReportOpenAIProviderScheduleResult(provider, openaiattempt.OpenAIProviderScheduleModel(p.c, provider, p.requestModel, false, result), true, nil)
		return
	}
	p.h.bindings.Common.Selection.ReportOpenAIProviderScheduleResult(provider, openaiattempt.OpenAIProviderScheduleModel(p.c, provider, p.requestModel, false, result), false, nil, err)
}

// SwitchGeneration records provider-transition feedback for the retained
// current selection.
func (p *generationRequestAdapter) SwitchGeneration(gatewaymedia.GenerationSelection) {
	p.h.bindings.Common.Selection.RecordOpenAIProviderSwitchForSelection(p.selection)
}

// StopGeneration429 applies the Runtime's OAuth stopping policy to the
// current target. The policy mutates request-local oauth429 state, so callers
// invoke it serially after transition count advances and before another
// selection.
func (p *generationRequestAdapter) StopGeneration429(_ gatewaymedia.GenerationSelection, status, count int) bool {
	return p.h.bindings.Platform.Stop429(p.selection.Provider, status, count, &p.oauth429)
}

// GenerationClientGone reports shared HTTP failure handling's observed client
// departure state.
func (p *generationRequestAdapter) GenerationClientGone() bool {
	return gatewayhttp.FailoverClientGone(p.c)
}

// logSchedule logs the current selection's scheduling observations under its
// active media component.
func (p *generationRequestAdapter) logSchedule() {
	name := "openai.images"
	if p.usesGrok() {
		name = "grok_media"
	}
	d := p.decision
	p.reqLog.Debug(name+".provider_schedule_decision", zap.String("layer", d.Layer), zap.Bool("sticky_session_hit", d.StickySessionHit), zap.Int("candidate_count", d.CandidateCount), zap.Int("top_k", d.TopK), zap.Int64("latency_ms", d.LatencyMs), zap.Float64("load_skew", d.LoadSkew))
}

// ObserveGeneration records media stages and timing evidence using the
// current target's platform for log naming. The media loop supplies
// selected-target observations for provider events and a nonnil result for
// partial image output. Other unused event fields can remain zero.
//
// Response processing time subtracts separately recorded upstream latency
// only when it is positive and smaller than the measured total; otherwise the
// total remains. Non-Grok results can also supply captured performance observations. Those
// observations are written in milliseconds before stage-specific logging,
// keeping response handling and upstream latency represented separately in
// Ops.
func (p *generationRequestAdapter) ObserveGeneration(e gatewaymedia.GenerationEvent) {
	name := "openai.images"
	if p.usesGrok() {
		name = "grok_media"
	}
	status := 0
	if e.Outcome.Failure != nil {
		status = e.Outcome.Failure.StatusCode
	}
	switch e.Kind {
	case "selecting":
		p.reqLog.Debug(name+".provider_selecting", zap.Int("excluded_provider_count", e.Excluded))
	case "select_failed":
		p.reqLog.Warn(name+".provider_select_failed", zap.Error(e.Outcome.Err), zap.Int("excluded_provider_count", e.Excluded))
	case "select_canceled":
		p.reqLog.Info(name+".provider_select_aborted_client_disconnected", zap.Error(e.Outcome.Err))
	case "forward_canceled":
		p.reqLog.Info(name+".failover_aborted_client_disconnected", zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("upstream_status", status))
	case "schedule":
		p.logSchedule()
	case "ineligible":
		p.reqLog.Warn(name+".provider_eligibility_rejected", zap.Int64("provider_id", e.Selection.Provider.ID), zap.String("reason", e.Reason), zap.Bool("probe_failed", e.ProbeFailed))
	case "routing":
		// Ops latency fields store the loop's elapsed duration in whole milliseconds.
		gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsRoutingLatencyMsKey, e.Elapsed.Milliseconds())
	case "response":
		elapsed := e.Elapsed.Milliseconds()
		upstream, _ := openaiattempt.GetContextInt64(p.c, gatewayhttp.OpsUpstreamLatencyMsKey)
		// Separate upstream wait from HTTP response handling when both timings are
		// observed; retain the total when subtraction would exhaust the measured interval.
		if upstream > 0 && elapsed > upstream {
			elapsed -= upstream
		}
		gatewayhttp.SetOpsLatencyMs(p.c, gatewayhttp.OpsResponseLatencyMsKey, elapsed)

	case "partial":
		p.reqLog.Warn(name+".forward_partial_error_with_image_result", zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("image_count", e.Outcome.Result.ImageCount), zap.Error(e.Outcome.Err))
	case "image_error":
		event := "upstream_user_error"
		if e.Outcome.ImageErrorRetryable {
			event = "upstream_server_error_after_flush"
		}
		p.reqLog.Warn(name+"."+event, zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("status_code", e.Outcome.ImageErrorStatus), zap.String("error_type", e.Outcome.ImageErrorType), zap.String("error_code", e.Outcome.ImageErrorCode), zap.Error(e.Outcome.Err))
	case "after_output":
		p.reqLog.Warn(name+".upstream_failover_skipped_after_flush", zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("upstream_status", status))
	case "retry":
		p.reqLog.Warn(name+".pool_mode_same_provider_retry", zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("upstream_status", status), zap.Int("retry_limit", e.RetryLimit), zap.Int("retry_count", e.RetryCount), zap.Duration("retry_delay", e.Delay))
	case "switch":
		p.reqLog.Warn(name+".upstream_failover_switching", zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("upstream_status", status), zap.Int("switch_count", e.Switches), zap.Int("max_switches", e.MaxSwitches))
	case "completed":
		p.reqLog.Debug(name+".request_completed", zap.Int64("provider_id", e.Selection.Provider.ID), zap.Int("switch_count", e.Switches))
	}
}

// EndGeneration presents terminal stage evidence using current target,
// saved-video owner and writer baseline. A usable shared stream flag is
// required; absent selection yields provider ID zero while failure-specific
// output remains in WriteGenerationFailure. A committed image SSE heartbeat
// keeps terminal failures marked failed despite wire HTTP 200, while canceled
// requests retain their existing lifecycle classification.
// Call after provider recovery ends; successfully retained image results use
// CompleteGeneration, including partial output with a tail error.
func (p *generationRequestAdapter) EndGeneration(f gatewaymedia.GenerationFailure) {
	if !p.usesGrok() &&
		p.c.Request.Context().Err() == nil &&
		(gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted(p.c) || *p.streamStarted) {
		// Terminal error output cannot change a heartbeat's HTTP status. Mark
		// request failure here after recovery ends so earlier failed attempts do
		// not overwrite a later successful generation or a client cancellation.
		telemetry.UpdateRequest(p.c.Request.Context(), func(record *telemetry.RequestRecord) {
			record.State = "failed"
			if record.ErrorCode == "" {
				record.ErrorCode = "stream_error"
			}
		})
	}
	id := int64(0)
	if p.selection != nil && p.selection.Provider != nil {
		id = p.selection.Provider.Record.ID
	}
	gatewayhttp.WriteGenerationFailure(f, gatewayhttp.MediaFailureContext{Grok: p.usesGrok(), Generation: p.endpoint.IsGenerationRequest(), StreamStarted: *p.streamStarted, BoundProviderID: p.boundProviderID, ProviderID: id, WriterBefore: p.writerBefore, Context: p.c, Log: p.reqLog}, p)
}

// CompleteGeneration records successful or retained partial media
// observations for the current target. Grok completion requires a nonnil
// result; Images completion also requires retained parsed input, and both
// capture request facts before asynchronous recording begins.
func (p *generationRequestAdapter) CompleteGeneration(ctx context.Context, _ gatewaymedia.GenerationSelection, value *gatewaymedia.GenerationResult) {
	if p.usesGrok() {
		p.completeGrok(ctx, value)
	} else {
		p.completeImages(value)
	}
}

// completeImages captures Images usage and request identity before mandatory
// completion submission. It requires current Key, selected target and parsed
// input, while a nil result still follows the capture path. OAuth header
// quota observation skips shadow targets and scheduling receives available
// timing.
//
// Original bytes identify nonmultipart payloads. Multipart requests hash the
// parsed endpoint, model, size and prompt seed; a blank prompt adds BodyHash
// when that retained digest is nonempty. Client and upstream endpoints,
// session identity and group mapping
// fields are read before capture. The worker closure retains the completion
// snapshot, recorder and a separate logger, so recording after HTTP exit does
// not read Gin state.
func (p *generationRequestAdapter) completeImages(value *gatewaymedia.GenerationResult) {
	h, c, apiKey, provider, requestModel, parsed, body, subscription, subject, groupMapping := p.h, p.c, p.apiKey, p.selection.Provider, p.requestModel, p.parsed, p.body, p.subscription, p.subject, p.groupMapping
	result := legacyGenerationResult(value)
	if result != nil {
		// Shadow targets use their separate quota query path. Header-based OAuth
		// observation applies here when the selected provider is not shadow.
		if provider.Record.Type == capability.ProviderTypeOAuth && !provider.View().IsShadow() {
			h.bindings.Common.Selection.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), provider.Record.ID, result.ResponseHeaders)
		}
		h.bindings.Common.Selection.ReportOpenAIProviderScheduleResult(provider, openaiattempt.OpenAIProviderScheduleModel(c, provider, requestModel, false, result), true, result.Timings.TTFTMs())
	} else {
		h.bindings.Common.Selection.ReportOpenAIProviderScheduleResult(provider, openaiattempt.OpenAIProviderScheduleModel(c, provider, requestModel, false, result), true, nil)
	}

	userAgent := c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(c)
	requestPayloadHash := billing.HashUsageRequestPayload(body)
	// Multipart completion uses parsed endpoint, model, size and prompt correlation.
	// Blank prompts include a nonempty BodyHash when available, retaining raw payload
	// evidence for that case while prompt-bearing requests use the selected seed fields.
	if parsed.Multipart {
		requestPayloadHash = billing.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
	}
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(c, provider.Record.Platform)

	clientSessionID := gatewayhttp.ExtractClientSessionID(c)

	upstreamModel := ""
	if result != nil {
		upstreamModel = result.UpstreamModel
	}
	completionInput := gatewaycapture.CaptureOpenAI(c.Request.Context(), &gatewaycapture.OpenAICapture{
		Result:             result,
		APIKey:             apiKey,
		User:               apiKey.User,
		Provider:           gatewaycapture.ExecutionCompletionRecord(provider),
		Subscription:       subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        body,
		APIKeyService:      h.bindings.Quota,

		ClientSessionID:    clientSessionID,
		PricingUsageFields: groupMapping.ToUsageFields(requestModel, upstreamModel),
	})
	completionRecorder := h.bindings.Common.Recorder
	completionLog := logging.L().With(
		zap.String("component", "handler.openai_gateway.images"),
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", requestModel),
		zap.Int64("provider_id", provider.Record.ID),
	)
	// Capture finishes before submission, keeping Gin reads on the request and
	// letting mandatory recording use independent completion data after HTTP exit.
	h.bindings.Common.Support.Submission.SubmitMandatory(c, func(ctx context.Context) {
		if err := completionRecorder.Record(ctx, completionInput, true); err != nil {
			completionLog.Error("openai.images.record_usage_failed", zap.Error(err))
		}
	})
}

// completeGrok handles immediate Grok usage and deferred video ownership from
// a nonnil result. Successful video creation with a task ID stores pending
// model, duration and resolution evidence under the authenticated group,
// user, Key and selected provider. The stored start timestamp comes from HTTP
// entry before provider attempts.
//
// Status and content lookup record a related task state with task and billing
// aliases, then try preparing billable completion. The task ID identifies
// upstream ownership, while StableGrokVideoBillingRequestID supplies the
// financial identity shared across polling. Other eligible media results use
// immediate recording with retained source bytes and group mapping. Callers
// provide current selection, request authentication and a usable Runtime task
// service.
func (p *generationRequestAdapter) completeGrok(requestCtx context.Context, value *gatewaymedia.GenerationResult) {
	h, c, apiKey, reqLog, provider, requestModel, body, subscription, subject, groupMapping, endpoint, requestID, videoCreateStartedAt := p.h, p.c, p.apiKey, p.reqLog, p.selection.Provider, p.requestModel, p.body, p.subscription, p.subject, p.groupMapping, p.endpoint, p.requestID, p.videoCreated
	result := legacyGenerationResult(value)
	if isGrokVideoCreateEndpoint(endpoint) && strings.TrimSpace(result.ResponseID) != "" {
		// Creation stores pending pricing beside saved provider ownership. Later status
		// or content observation claims completion before submitting the stable billing key.
		pending := gatewaymedia.GrokVideoPendingBilling{
			Model:                requestModel,
			BillingModel:         firstNonEmptyString(result.BillingModel, requestModel),
			UpstreamModel:        result.UpstreamModel,
			VideoResolution:      result.VideoResolution,
			VideoDurationSeconds: result.VideoDurationSeconds,
			OriginalModel:        gatewayhttp.ClientRequestedModel(c, requestModel),
			// Retain the preattempt creation start for later end-to-end duration observation.
			CreatedAt: videoCreateStartedAt,
		}
		h.bindings.VideoTasks().TrackCreated(requestCtx, apiKey.GroupID, result.ResponseID, subject.UserID, apiKey.ID, provider.Record.ID, pending, grokVideoObserver{log: reqLog})
	}
	// Correlate repeated task polling through one stable billing identity while
	// retaining the upstream task alias and this Key's separate related-request record.
	if endpoint == upstreamgrok.GrokMediaEndpointVideoStatus || endpoint == upstreamgrok.GrokMediaEndpointVideoContent {
		taskID := strings.TrimSpace(requestID)
		billingKey := gatewaymedia.StableGrokVideoBillingRequestID(taskID)
		telemetry.RecordRelatedRequest(requestCtx, telemetry.RequestRecord{RequestID: telemetry.StableRequestID(fmt.Sprintf("%d/%s", apiKey.ID, billingKey)), State: firstNonEmptyString(result.VideoState, "running"), Aliases: []telemetry.RequestAlias{{Kind: "task", Value: taskID}, {Kind: "billing", Value: billingKey}}})
		if billResult := prepareGrokVideoCompletionBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result); billResult != nil {
			recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, provider, billResult, billResult.Model, groupMapping, body, taskID)
		}
	} else if shouldRecordGrokMediaUsage(endpoint, requestModel, result) {
		recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, provider, result, requestModel, groupMapping, body, requestID)
	}
}

// generationResultView converts selected media fields from OpenAI forwarding
// into a cloned generation result. Nil input returns nil. Request, response,
// billing and upstream model identities retain their separate values along
// with scalar token, image, video and timing observations. The HTTP dispatch
// reference retains its provider and ordinal through both result conversions.
//
// The generation clone copies the performance maps, image-size slice and map,
// and both header maps with their value slices, retaining nil and empty
// collection shapes. This media schema is the result consumed by the attempt
// loop; other OpenAI text and WebSocket fields are outside that schema.
func generationResultView(r *forwardcore.OpenAIResult) *gatewaymedia.GenerationResult {
	if r == nil {
		return nil
	}
	// Copy mutable media observations before the loop retains them, so completion
	// conversions can keep their own timing, size and header evidence across callbacks.
	return gatewaymedia.CloneGenerationResult(&gatewaymedia.GenerationResult{
		RequestAttempt:       r.RequestAttempt,
		RequestID:            r.RequestID,
		ResponseID:           r.ResponseID,
		Model:                r.Model,
		BillingModel:         r.BillingModel,
		UpstreamModel:        r.UpstreamModel,
		Usage:                r.Usage,
		Stream:               r.Stream,
		Duration:             r.Duration,
		Timings:              r.Timings,
		ImageCount:           r.ImageCount,
		ImageFrameCount:      r.ImageFrameCount,
		VideoCount:           r.VideoCount,
		VideoState:           r.VideoState,
		VideoDurationSeconds: r.VideoDurationSeconds,
		ImageSize:            r.ImageSize,
		ImageInputSize:       r.ImageInputSize,
		ImageOutputSize:      r.ImageOutputSize,
		ImageSizeSource:      r.ImageSizeSource,
		VideoResolution:      r.VideoResolution,
		ImageOutputSizes:     r.ImageOutputSizes,
		ImageSizeBreakdown:   r.ImageSizeBreakdown,
		Headers:              r.UpstreamHeaders,
		ResponseHeaders:      r.ResponseHeaders,
	})
}

// legacyGenerationResult converts a cloned media result into the OpenAI
// fields used for feedback and recording. Nil input returns nil. Model
// identities and scalar usage retain their separate roles, while collection
// and timing storage is copied through CloneGenerationResult before
// construction.
//
// Both header maps, image output sizes and size breakdown come from the
// private clone, so the returned view can be retained independently of the
// media loop's result. Nil and empty collections keep their observed shapes.
// The HTTP dispatch reference survives the conversion so completion updates the
// producing attempt even when response headers cannot distinguish retries.
func legacyGenerationResult(r *gatewaymedia.GenerationResult) *forwardcore.OpenAIResult {
	if r == nil {
		return nil
	}
	// The forwarding view takes private media storage before copying fields, letting
	// scheduling and completion consumers retain observations independently of the loop.
	r = gatewaymedia.CloneGenerationResult(r)
	return &forwardcore.OpenAIResult{
		RequestAttempt:       r.RequestAttempt,
		RequestID:            r.RequestID,
		ResponseID:           r.ResponseID,
		Model:                r.Model,
		BillingModel:         r.BillingModel,
		UpstreamModel:        r.UpstreamModel,
		Usage:                r.Usage,
		Stream:               r.Stream,
		Duration:             r.Duration,
		Timings:              r.Timings,
		ImageCount:           r.ImageCount,
		ImageFrameCount:      r.ImageFrameCount,
		VideoCount:           r.VideoCount,
		VideoState:           r.VideoState,
		VideoDurationSeconds: r.VideoDurationSeconds,
		ImageSize:            r.ImageSize,
		ImageInputSize:       r.ImageInputSize,
		ImageOutputSize:      r.ImageOutputSize,
		ImageSizeSource:      r.ImageSizeSource,
		VideoResolution:      r.VideoResolution,
		ImageOutputSizes:     r.ImageOutputSizes,
		ImageSizeBreakdown:   r.ImageSizeBreakdown,
		UpstreamHeaders:      r.Headers,
		ResponseHeaders:      r.ResponseHeaders,
	}
}

// MediaClassify diagnoses the retained request's model and capacity failure
// for HTTP presentation. Grok uses its routing model and platform
// restriction; ordinary Images uses its parsed request model with no fixed
// platform restriction.
func (p *generationRequestAdapter) MediaClassify() gatewayhttp.MediaNoProvider {
	platform := ""
	routing := p.requestModel
	if p.usesGrok() {
		platform = capability.PlatformGrok
		routing = p.routingModel
	}
	result := openaiattempt.ClassifyNoProviderErrorFromGin(p.c, p.h.bindings.Common.Diagnoser, p.apiKey, p.requestModel, routing, platform)
	return gatewayhttp.MediaNoProvider{ModelNotFound: result.ModelNotFound, Status: result.Status, Type: result.ErrType, Message: result.Message}
}

// MediaNoAvailable reports whether the error chain contains scheduler
// candidate absence.
func (p *generationRequestAdapter) MediaNoAvailable(err error) bool {
	return errors.Is(err, scheduler.ErrNoAvailableProviders)
}

// MediaCapacity records route capacity limitation on this request. A true
// flag marks only scheduler unavailability errors; false marks capacity
// directly when terminal handling has already established the absence.
func (p *generationRequestAdapter) MediaCapacity(err error, conditional bool) {
	if conditional {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
	} else {
		gatewayhttp.MarkOpsRoutingCapacityLimited(p.c)
	}
}

// MediaError presents terminal media failure using the supplied
// classification and presentation mode. Stream-aware mode reads the shared
// startup flag; ordinary mode delegates the shared OpenAI HTTP error
// response.
func (p *generationRequestAdapter) MediaError(status int, typ, message string, stream bool) {
	if stream {
		gatewayhttp.DefaultOpenAIErrorOutput().StreamError(p.c, status, typ, message, *p.streamStarted)
	} else {
		gatewayhttp.DefaultOpenAIErrorOutput().WriteError(p.c, status, typ, message)
	}
}

// MediaFailover presents exhaustion when the error chain contains a
// classified upstream failure. Other errors produce no additional output
// here, and the supplied stream flag constrains the shared terminal
// presentation.
func (p *generationRequestAdapter) MediaFailover(err error, stream bool) {
	if value, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		p.h.bindings.Common.Support.HandleFailoverExhausted(p.c, value, stream)
	}
}

// MediaSimpleExhausted presents generic image exhaustion with the shared
// stream startup state.
func (p *generationRequestAdapter) MediaSimpleExhausted() {
	p.h.bindings.Common.Support.HandleFailoverExhaustedSimple(p.c, 502, *p.streamStarted)
}

// mediaStatus reads upstream status from Ops context and returns zero when
// absent. GetContextInt64 handles compatible stored integer forms; this
// helper converts that observation to the HTTP status type.
func (p *generationRequestAdapter) mediaStatus() int {
	status, _ := openaiattempt.GetContextInt64(p.c, gatewayhttp.OpsUpstreamStatusCodeKey)
	return int(status)
}

// MediaForwardCyber records warning evidence carried by the forward error
// chain for the current target. Its true result reports recognized warning
// value, even when the underlying optional moderation recorder performs no
// write.
func (p *generationRequestAdapter) MediaForwardCyber(err error) bool {
	return p.h.bindings.Common.Support.RecordOpenAIForwardErrorCyberWarning(p.c, p.reqLog, p.apiKey, p.selection.Provider, p.requestModel, p.mediaStatus(), err)
}

// MediaCyber records terminal error text with retained image request and
// provider identity. Callers provide a nonnil error and usable selection;
// underlying warning recording determines whether the text qualifies as Cyber
// evidence.
func (p *generationRequestAdapter) MediaCyber(err error) {
	p.h.bindings.Common.Support.RecordOpenAICyberWarning(p.c, p.reqLog, p.apiKey, p.selection.Provider, p.requestModel, p.mediaStatus(), nil, err.Error())
}

// MediaReportUnexpected reports the current target's failed scheduling result
// using media observations.
func (p *generationRequestAdapter) MediaReportUnexpected(result *gatewaymedia.GenerationResult, err error) {
	p.ReportGeneration(p.c.Request.Context(), gatewaymedia.GenerationSelection{}, result, false, err)
}

// MediaCommunicated reports the adapter's evidence that an error response
// already reached presentation. Grok uses commitment state; OpenAI compares
// heartbeat-adjusted output and recognized failure markers against the stored
// preattempt size.
func (p *generationRequestAdapter) MediaCommunicated(err error) bool {
	if p.usesGrok() {
		return gatewayhttp.IsResponseCommitted(p.c)
	}
	return gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(p.c, p.writerBefore, err)
}

// MediaEnsureFallback attempts a terminal OpenAI error using the shared
// stream startup flag. EnsureResponse checks existing output and heartbeat
// state; true reports attempted presentation for failure logging, not client
// delivery.
func (p *generationRequestAdapter) MediaEnsureFallback(err error) bool {
	return gatewayhttp.DefaultOpenAIErrorOutput().EnsureResponse(p.c, *p.streamStarted, err)
}

// MediaWarnFailure selects log severity from request output and fallback
// presentation evidence.
func (p *generationRequestAdapter) MediaWarnFailure(wrote bool) bool {
	return gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(p.c, wrote)
}

// usesGrok reports the retained current target's platform, falling back to
// explicit entry mode when selection is absent. Each new selection replaces
// the previous target, so ordinary Images can return to another platform
// after Grok.
func (p *generationRequestAdapter) usesGrok() bool {
	if p.selection == nil || p.selection.Provider == nil {
		return p.grok
	}
	return p.selection.Provider.Record.Platform == capability.PlatformGrok
}
