package handler

import (
	"context"
	"errors"
	"strings"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/account"
	openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// NewResponsesWSHTTPHandler 只投影既有单例和静态预算，不创建池或完成 worker。
func (h *OpenAIGatewayHandler) NewResponsesWSHTTPHandler() *gatewayhttp.ResponsesWSHandler {
	max := 0
	if h.cfg != nil {
		max = h.cfg.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey
	}
	options := gatewayhttp.ResponsesWSOptions{
		MaxIngressConnectionsPerAPIKey: max,
		ReadLimit:                      service.ResolveOpenAIWSClientReadLimitBytes(h.cfg),
		FirstMessageTimeout:            service.ResolveOpenAIWSClientFirstMessageTimeout(h.cfg),
		MaxAccountSwitches:             h.maxAccountSwitches,
	}
	return gatewayhttp.NewResponsesWSHandler(options, openAIWSHTTPBackend{h}, h.concurrencyHelper)
}

type openAIWSHTTPBackend struct{ h *OpenAIGatewayHandler }

func (p openAIWSHTTPBackend) Access(c *gin.Context) (*gatewayws.EntryKey, bool) {
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	if !ok {
		return nil, false
	}
	out := &gatewayws.EntryKey{
		ID:                key.ID,
		UserID:            key.UserID,
		GroupID:           key.GroupID,
		ModelMapping:      apikey.CloneModelMapping(key.ModelMapping),
		FastModePolicy:    key.FastModePolicy,
		RefreshFastPolicy: p.h.apiKeyService != nil && strings.TrimSpace(key.Key) != "",
	}
	if key.Group != nil {
		out.Group = &gatewayws.EntryGroup{
			Platform:                    key.Group.Platform,
			MaxReasoningEffort:          key.Group.MaxReasoningEffort,
			MaxReasoningEffortOverLimit: key.Group.MaxReasoningEffortOverLimit,
			ReasoningEffortMappings:     append([]routing.ReasoningEffortMapping(nil), key.Group.ReasoningEffortMappings...),
			ImagesAllowed:               routing.GroupAllowsResponsesImages(key.Group),
		}
	}
	return out, true
}
func (p openAIWSHTTPBackend) Transport(c *gin.Context) { setOpenAIClientTransportWS(c) }
func (p openAIWSHTTPBackend) Error(c *gin.Context, status int, kind, message string) {
	gatewayhttp.DefaultOpenAIErrorOutput().WriteError(c, status, kind, message)
}
func (p openAIWSHTTPBackend) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.h.ensureResponsesDependencies(c, log)
}
func (p openAIWSHTTPBackend) SummarizeRead(err error) (string, string) {
	var closed *gatewayws.ClientCloseError
	if errors.As(err, &closed) {
		err = gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusCode(closed.Status), closed.Reason, closed.Cause)
	}
	return gatewayhttp.SummarizeWSCloseErrorForLog(err)
}
func (p openAIWSHTTPBackend) Entry(c *gin.Context, call gatewayhttp.ResponsesWSCall) gatewayws.EntryPorts {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subject, _ := authctx.GetAuthSubjectFromContext(c)
	return &openAIWSEntryAdapter{h: p.h, c: c, call: call, key: key, subject: subject, log: call.Logger}
}

// openAIWSEntryAdapter 的每个方法仅接入一个已有能力，主循环与 turn 回调在 gateway/ws。
type openAIWSEntryAdapter struct {
	h            *OpenAIGatewayHandler
	c            *gin.Context
	call         gatewayhttp.ResponsesWSCall
	key          *apikey.APIKey
	subject      authctx.AuthSubject
	subscription *billing.UserSubscription
	log          *zap.Logger
}

func (p *openAIWSEntryAdapter) Logger() gatewayws.EntryLogger { return wsEntryLogger{p.log} }
func (p *openAIWSEntryAdapter) SetLogger(log gatewayws.EntryLogger) {
	// 固定适配器的 With 返回同类记录器；违反此内部约定仍作为编程错误失败。
	typed, ok := log.(wsEntryLogger)
	if !ok {
		panic("unexpected websocket entry logger type")
	}
	p.log = typed.log
}
func (p *openAIWSEntryAdapter) ClassifyPrevious(id string) string {
	return openai.ClassifyOpenAIPreviousResponseIDKind(id)
}
func (p *openAIWSEntryAdapter) Platform() string {
	return gatewayhttp.OpenAICompatibleRequestPlatform(p.key)
}
func (p *openAIWSEntryAdapter) Prompt(ctx context.Context, body []byte) []byte {
	return p.h.prompts.ApplyUserPromptReplacementToBody(ctx, body, "openai_responses")
}
func (p *openAIWSEntryAdapter) Redirect(ctx context.Context, model string) (context.Context, string) {
	return gatewayhttp.APIKeyModelRedirectContext(ctx, p.key, model)
}
func (p *openAIWSEntryAdapter) BindContext(ctx context.Context) {
	p.c.Request = p.c.Request.WithContext(ctx)
}
func (p *openAIWSEntryAdapter) ObserveFirst(model string) {
	gatewayhttp.SetOpsRequestContext(p.c, model, true)
	gatewayhttp.SetOpsEndpointContext(p.c, "", int16(usage.RequestTypeWSV2))
}
func (p *openAIWSEntryAdapter) CaptureCyber(body []byte) gatewayws.EntryCyberSnapshot {
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(p.c, moderation.ContentModerationProtocolOpenAIResponses, body)
	return gatewayws.EntryCyberSnapshot{Excerpt: gatewayhttp.CurrentOpenAICyberWarningPromptExcerpt(p.c), Input: gatewayhttp.CurrentOpenAICyberWarningSnapshot(p.c)}
}
func (p *openAIWSEntryAdapter) Moderate(_ context.Context, model string, body []byte) *moderation.Decision {
	return p.h.checkContentModeration(p.c, p.log, p.key, p.subject, moderation.ContentModerationProtocolOpenAIResponses, model, body)
}
func (p *openAIWSEntryAdapter) ModerationError(ctx context.Context, d *moderation.Decision) {
	gatewayhttp.WriteResponsesWSModeration(ctx, p.call.Conn, d)
}
func (p *openAIWSEntryAdapter) BlockedSession(ctx context.Context, body []byte) string {
	return findBlockedCyberSessionKey(ctx, p.h.gatewayService, p.key.ID, p.c, body)
}
func (p *openAIWSEntryAdapter) BlockedError(ctx context.Context) {
	gatewayhttp.WriteResponsesWSCyberBlocked(ctx, p.call.Conn, cyberSessionBlockedClientMsg)
}
func (p *openAIWSEntryAdapter) BlockedMessage() string { return cyberSessionBlockedClientMsg }
func (p *openAIWSEntryAdapter) BlockedOps(model, key string) {
	p.h.enqueueCyberSessionBlockedOpsEntry(p.c, p.key, model, key)
}
func (p *openAIWSEntryAdapter) Plan(ctx context.Context, model string) (context.Context, routing.ChannelMappingResult) {
	plan := p.h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(p.key), p.key.GroupID, model)
	return requeststate.WithRoutePlan(ctx, plan), routing.ChannelMappingResult(service.ChannelMappingFromRoutePlan(plan))
}
func (p *openAIWSEntryAdapter) ImageIntent(model string, body []byte, mapping routing.ChannelMappingResult) ([]byte, string, bool) {
	return resolveOpenAIChannelMappedImageIntent("/v1/responses", model, body, routing.ChannelMappingResult(mapping), p.Platform(), p.h.gatewayService.ReplaceModelInBody)
}
func (p *openAIWSEntryAdapter) ExplicitImage(model string, body []byte) bool {
	return gatewayprovider.ImageIntent().IsExplicitImageGenerationIntent("/v1/responses", model, body)
}
func (p *openAIWSEntryAdapter) ImageContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIImageGenerationIntent(ctx)
}
func (p *openAIWSEntryAdapter) ImagesAllowed() bool {
	return routing.GroupAllowsResponsesImages(p.key.Group)
}
func (p *openAIWSEntryAdapter) ImageDeniedMessage() string {
	return gatewaymedia.ImageGenerationPermissionMessage
}
func (p *openAIWSEntryAdapter) PolicyDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p *openAIWSEntryAdapter) FeatureDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (p *openAIWSEntryAdapter) AcquireUser(ctx context.Context) (func(), bool, error) {
	return p.h.concurrencyHelper.TryAcquireUserSlotForAPIKey(ctx, p.subject.UserID, p.subject.Concurrency, p.key.ID)
}
func (p *openAIWSEntryAdapter) AcquireAccount(ctx context.Context, id int64, limit int) (func(), bool, error) {
	return p.h.concurrencyHelper.TryAcquireAccountSlot(ctx, id, limit)
}
func (p *openAIWSEntryAdapter) WrapRelease(ctx context.Context, release func()) func() {
	return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, release)
}
func (p *openAIWSEntryAdapter) LoadSubscription() {
	p.subscription, _ = gatewayhttp.SubscriptionFromContext(p.c)
}
func (p *openAIWSEntryAdapter) Eligibility(ctx context.Context) error {
	return p.h.billingCacheService.CheckKey(ctx, p.key, p.subscription, admission.QuotaPlatform(p.c.Request.Context(), p.key), false)
}
func (p *openAIWSEntryAdapter) SessionHash(body []byte, seed string) string {
	return gatewayhttp.GenerateOpenAISessionHashWithFallback(p.c, body, seed)
}
func (p *openAIWSEntryAdapter) ExplicitHash(body []byte) string {
	return gatewayhttp.GenerateExplicitOpenAISessionHash(p.c, body)
}
func (p *openAIWSEntryAdapter) Isolate(ctx context.Context, source, hash string) error {
	return p.h.ensureOpenAISessionIsolation(ctx, p.key, p.subject.UserID, source, hash)
}
func (p *openAIWSEntryAdapter) IsolationError(ctx context.Context, err error) {
	gatewayhttp.WriteResponsesWSIsolation(ctx, p.call.Conn, err)
}
func (p *openAIWSEntryAdapter) IsolationReason(err error) string {
	return gatewayhttp.ResponsesWSIsolationCloseReason(err)
}
func (p *openAIWSEntryAdapter) Guardian(ctx context.Context, body []byte, model string) context.Context {
	return gatewayhttp.WithOpenAIGuardianParentAffinity(ctx, p.c, body, model)
}
func (p *openAIWSEntryAdapter) Select(ctx context.Context, previous, hash, model string, excluded map[int64]struct{}, responses, move bool, platform string) (*gatewayws.EntrySelection, gatewayws.EntryDecision, error) {
	capability := account.OpenAIEndpointCapabilityTextGeneration
	if responses {
		capability = account.OpenAIEndpointCapabilityResponses
	}
	selection, decision, err := p.h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, p.key.GroupID, previous, hash, model, excluded, egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress, capability, false, move, platform)
	d := gatewayws.EntryDecision{Layer: decision.Layer, CandidateCount: decision.CandidateCount, StickyPreviousHit: decision.StickyPreviousHit}
	if selection == nil {
		return nil, d, err
	}
	out := &gatewayws.EntrySelection{Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc, WaitPlan: selection.WaitPlan}
	if a := selection.Account; a != nil {
		out.Account = &gatewayws.EntryAccount{
			AccountSnapshot: account.AccountSnapshot{ID: a.Record.ID, Platform: a.Record.Platform, Type: a.Record.Type, Concurrency: a.Record.Concurrency},
			Name:            a.Record.Name,
			Shadow:          a.View().IsShadow(),
		}
		out.Target = &openAIWSEntryTarget{root: p, account: a, selection: selection}
	}
	return out, d, err
}
func (p *openAIWSEntryAdapter) SelectionLogError(err error, platform string) error {
	return gatewayhttp.OpenAICompatibleSelectionErrorForLog(err, platform)
}
func (p *openAIWSEntryAdapter) BindSticky(ctx context.Context, hash string, id int64) error {
	return p.h.gatewayService.BindStickySession(ctx, p.key.GroupID, hash, id)
}
func (p *openAIWSEntryAdapter) RefreshFast(ctx context.Context) (string, bool) {
	key, err := p.h.apiKeyService.GetByKey(ctx, p.key.Key)
	if err != nil || key == nil {
		return "", false
	}
	return apikey.NormalizeAPIKeyFastModePolicy(key.FastModePolicy)
}
func (p *openAIWSEntryAdapter) Failover(err error) (*gatewayws.EntryFailure, bool) {
	var failure *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failure) || failure == nil {
		return nil, false
	}
	return &gatewayws.EntryFailure{Err: failure, StatusCode: failure.StatusCode, ReportScheduleFailure: failure.ShouldReportAccountScheduleFailure(), RetryNext: failure.ShouldRetryNextAccount()}, true
}
func (p *openAIWSEntryAdapter) CloseFailover(failure *gatewayws.EntryFailure) {
	var old *forwardcore.UpstreamFailoverError
	if failure != nil {
		_ = errors.As(failure.Err, &old)
	}
	gatewayhttp.CloseResponsesWSFailure(p.c, p.call.Conn, wsFailoverPresentation(old), gatewayhttp.MarkOpsStreamFailure)
}
func (p *openAIWSEntryAdapter) Close(status int, reason string) {
	gatewayhttp.CloseResponsesWS(p.call.Conn, coderws.StatusCode(status), reason)
}
func (p *openAIWSEntryAdapter) CloseError(status int, reason string, err error) error {
	return gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusCode(status), reason, err)
}
func (p *openAIWSEntryAdapter) CloseInfo(err error) gatewayws.EntryClose {
	return gatewayhttp.ResponsesWSCloseInfo(err)
}

func (p *openAIWSEntryAdapter) SessionPreempted(err error) bool {
	return gatewayws.IsSessionPreemptedError(err)
}
func (p *openAIWSEntryAdapter) RemovePrevious(body []byte) []byte {
	return openai.RemovePreviousResponseIDFromBody(body)
}

func (p *openAIWSEntryAdapter) LocalPolicyError(err error) bool {
	var policy *routing.ReasoningEffortOverLimitError
	return errors.As(err, &policy)
}
func (p *openAIWSEntryAdapter) ReportFailure(err error) bool {
	return gatewayws.EntryShouldReportFailure(err)
}
func (p *openAIWSEntryAdapter) EndedByClient(err error) bool {
	return gatewayhttp.ResponsesWSEndedByClient(err, p.CloseInfo(err))
}
func (p *openAIWSEntryAdapter) ClearCyber() { clearCyberPolicyTurnState(p.c) }
func (p *openAIWSEntryAdapter) CompletionRecorder() gatewayws.EntryCompletion {
	return p.h.completionRuntime()
}
func (p *openAIWSEntryAdapter) CompletionObserver() func(int64, string, error) {
	log := p.log
	return func(id int64, requestID string, err error) {
		log.Error("openai.websocket_record_usage_failed", zap.Int64("account_id", id), zap.String("request_id", requestID), zap.Error(err))
	}
}
func (p *openAIWSEntryAdapter) SubmitCompletion(result *gatewayws.ForwardResult, task func(context.Context)) {
	p.h.submitOpenAIUsageRecordTask(p.c, service.LegacyWSForwardResult(result), task)
}

// openAIWSEntryTarget 将选中账号的单步能力投影给核心，不持有 turn 或 failover 循环。
type openAIWSEntryTarget struct {
	root      *openAIWSEntryAdapter
	account   *gatewayprovider.ExecutionAccount
	selection *gatewayprovider.SelectionResult
	token     string
}

func (t *openAIWSEntryTarget) MappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(t.account).Mapped(model)
}
func (t *openAIWSEntryTarget) Report(model string, ok bool, first *int) {
	t.root.h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(t.selection, t.account.Record.ID, model, ok, first)
}
func (t *openAIWSEntryTarget) Switched() {
	t.root.h.gatewayService.RecordOpenAIAccountSwitchForSelection(t.selection)
}
func (t *openAIWSEntryTarget) Stop429(status, count int, state *failover.OAuth429State) bool {
	return t.root.h.gatewayService.ShouldStopOpenAIOAuth429Failover(t.account, status, count, state)
}
func (t *openAIWSEntryTarget) Credential(ctx context.Context) error {
	token, _, err := t.root.h.gatewayService.GetRequestCredential(ctx, t.root.c, t.account)
	if err == nil {
		t.token = token
	}
	return err
}
func (t *openAIWSEntryTarget) EnforceClient(ctx context.Context, first []byte) error {
	router := t.root.h.gatewayService.MatchOpenAITLSFingerprintRouterForRequest(t.root.c, t.account)
	return t.root.h.gatewayService.EnforceOpenAIClientPolicyForRequest(ctx, t.root.c, t.account, first, router)
}
func (t *openAIWSEntryTarget) ResolveRouting(ctx context.Context, model string, responses bool) (string, error) {
	capability := account.OpenAIEndpointCapabilityTextGeneration
	if responses {
		capability = account.OpenAIEndpointCapabilityResponses
	}
	return t.root.h.gatewayService.ResolveOpenAIWSRoutingModelForAccount(ctx, t.root.key.GroupID, t.account, model, capability)
}
func (t *openAIWSEntryTarget) Warning(_ context.Context, model string, status int, body []byte, message string, snapshot gatewayws.EntryCyberSnapshot) {
	p := t.root
	p.h.recordOpenAICyberWarningWithSnapshot(p.c, p.log, p.key, t.account, model, status, body, message, snapshot.Excerpt, snapshot.Input)
}
func (t *openAIWSEntryTarget) RecordMarked(_ context.Context, model string, failed bool, body []byte, mapping routing.ChannelUsageFields, hash string) bool {
	p := t.root
	return p.h.recordCyberPolicyIfMarked(p.c, p.key, t.account, p.subscription, model, failed, body, mapping, hash)
}
func (t *openAIWSEntryTarget) UpdateUsage(ctx context.Context, headers map[string][]string) {
	t.root.h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(ctx, t.account.Record.ID, headers)
}
func (t *openAIWSEntryTarget) PrepareCompletion(ctx context.Context, result *gatewayws.ForwardResult, capture gatewayws.TurnCapture, model string, mapping routing.ChannelMappingResult, body []byte, cyber bool) *completion.Input {
	p := t.root
	legacy := service.LegacyWSForwardResult(result)
	// 这里只转换已有资金/用量字段；复制发生在提交前，回调不捕获 Gin。
	return gatewayprovider.CaptureOpenAI(gatewayhttp.PropagateAPIKeyModelRedirectTrace(gatewayhttp.CompletionContext(p.c), ctx), &gatewayprovider.OpenAICapture{
		Result: legacy, APIKey: p.key, User: p.key.User, Account: gatewayprovider.ExecutionCompletionRecord(t.account), Subscription: p.subscription,
		InboundEndpoint: gatewayhttp.GetInboundEndpoint(p.c), UpstreamEndpoint: resolveOpenAIUpstreamEndpoint(p.c, t.account, legacy), UserAgent: p.call.UserAgent, IPAddress: p.call.ClientIP,
		RequestPayloadHash: billing.HashUsageRequestPayload(body), RequestBody: append([]byte(nil), body...), PricingAt: capture.StartedAt, APIKeyService: p.h.apiKeyService,
		QuotaPlatform: admission.QuotaPlatform(p.c.Request.Context(), p.key), ClientSessionID: gatewayhttp.ExtractClientSessionID(p.c), ChannelUsageFields: mapping.ToUsageFields(model, result.UpstreamModel), CyberBlocked: cyber,
	})
}
func (t *openAIWSEntryTarget) BeginPreemption(ctx context.Context, first []byte) (context.Context, func(), bool) {
	return t.root.h.gatewayService.BeginOpenAIWSIngressSessionPreemption(ctx, t.root.c, t.account, first)
}
func (t *openAIWSEntryTarget) Run(ctx context.Context, client gatewayws.ClientSocket, first []byte, hooks *gatewayws.EntryHooks) error {
	frames, ok := client.(gatewayhttp.WSClientFrames)
	if !ok {
		return errors.New("unsupported websocket HTTP frame adapter")
	}
	old := &gatewayws.OpenAIIngressHooks{
		ClientLifecycleContext:      hooks.ClientLifecycleContext,
		InitialRequestModel:         hooks.InitialRequestModel,
		InitialTurnStartedAt:        hooks.InitialTurnStartedAt,
		MaxReasoningEffort:          hooks.MaxReasoningEffort,
		MaxReasoningEffortOverLimit: hooks.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:     hooks.ReasoningEffortMappings,
		ResolveFastModePolicy:       hooks.ResolveFastModePolicy,
		ResolveRoutingModel:         hooks.ResolveRoutingModel,
		TurnStarted:                 hooks.TurnStarted,
		BeforeTurn:                  hooks.BeforeTurn,
		BeforeRequest:               hooks.BeforeRequest,
		OnUpstreamError:             hooks.OnUpstreamError,
	}
	if hooks.AfterTurn != nil {
		old.AfterTurn = func(c gatewayws.OpenAITurnCapture) {
			hooks.AfterTurn(gatewayws.TurnCapture{Turn: c.Turn, StartedAt: c.StartedAt, RequestBody: c.RequestBody, OriginalModel: c.OriginalModel, PreviousResponseID: c.PreviousResponseID, Result: service.ProjectWSForwardResult(c.Result), Err: c.Err, PayloadSource: c.PayloadSource})
		}
	}
	return t.root.h.gatewayService.ProxyResponsesWebSocketFromClient(ctx, t.root.c, frames.Conn, t.account, t.token, first, old)
}
func (t *openAIWSEntryTarget) LogFailure(err error) {
	status, reason := gatewayhttp.SummarizeWSCloseErrorForLog(err)
	fields := []zap.Field{zap.Int64("account_id", t.account.Record.ID), zap.Error(err), zap.String("close_status", status), zap.String("close_reason", reason)}
	fields = appendOpenAIAccountProxyLogFields(fields, t.account)
	t.root.log.Warn("openai.websocket_proxy_failed", fields...)
}

// wsEntryLogger 只持有日志句柄，后台完成回调不保留 HTTP 请求对象。
type wsEntryLogger struct{ log *zap.Logger }

func wsEntryLogFields(fields []gatewayws.EntryField) []zap.Field {
	out := make([]zap.Field, 0, len(fields))
	for _, f := range fields {
		switch f.Kind {
		case 1:
			out = append(out, zap.String(f.Key, f.Text))
		case 2:
			out = append(out, zap.Int64(f.Key, f.Integer))
		case 3:
			out = append(out, zap.Bool(f.Key, f.Flag))
		case 4:
			out = append(out, zap.Error(f.Err))
		}
	}
	return out
}
func (l wsEntryLogger) With(fields ...gatewayws.EntryField) gatewayws.EntryLogger {
	return wsEntryLogger{l.log.With(wsEntryLogFields(fields)...)}
}
func (l wsEntryLogger) Info(message string, fields ...gatewayws.EntryField) {
	l.log.Info(message, wsEntryLogFields(fields)...)
}
func (l wsEntryLogger) Warn(message string, fields ...gatewayws.EntryField) {
	l.log.Warn(message, wsEntryLogFields(fields)...)
}
func (l wsEntryLogger) Error(message string, fields ...gatewayws.EntryField) {
	l.log.Error(message, wsEntryLogFields(fields)...)
}
func (l wsEntryLogger) Debug(message string, fields ...gatewayws.EntryField) {
	l.log.Debug(message, wsEntryLogFields(fields)...)
}

// wsFailoverPresentation 只转换错误展示字段，HTTP 映射由原生 Adapter 唯一持有。
func wsFailoverPresentation(err *forwardcore.UpstreamFailoverError) *gatewayhttp.ResponsesWSFailure {
	if err == nil {
		return nil
	}
	return &gatewayhttp.ResponsesWSFailure{
		Reason:            string(err.Reason),
		StatusCode:        err.StatusCode,
		AccountAuth:       err.Stage == forwardcore.GatewayFailureStageAccountAuth,
		CredentialMessage: forwardcore.GrokCredentialUnavailableClientMessage,
	}
}
