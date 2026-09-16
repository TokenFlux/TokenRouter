// OpenAI 文本旧装配只投影实体、绑定单步用例，不再拥有 HTTP 前置组合。
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAITextHTTPBackend struct{ h *OpenAIGatewayHandler }

// NewOpenAITextHTTPHandler 固定绑定现有唯一服务与完成记录器，不新增缓存或后台任务。
func (h *OpenAIGatewayHandler) NewOpenAITextHTTPHandler() *gatewayhttp.OpenAITextHandler {
	options := gatewayhttp.OpenAITextOptions{}
	var prompt gatewayhttp.MessagesPrompt
	if h != nil {
		options.MaxBodyBytes = gatewayMaxBodySize(h.cfg)
		options.MaxSwitches = h.maxAccountSwitches
		options.CompactKeepaliveInterval = h.openAICompactKeepaliveInterval()
		prompt = h.gatewayService
	}
	return gatewayhttp.NewOpenAITextHandler(options, openAITextHTTPBackend{h}, prompt, h.NewOpenAITextExecutor())
}
func (p openAITextHTTPBackend) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := gatewayhttp.EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	return service.APIKeyView(key), ok
}
func (p openAITextHTTPBackend) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.h.ensureResponsesDependencies(c, log)
}
func (p openAITextHTTPBackend) ReadFailure(log *zap.Logger, r *http.Request, err error) {
	logRequestBodyReadFailure(log, r, err)
}
func (p openAITextHTTPBackend) TransportHTTP(c *gin.Context) { setOpenAIClientTransportHTTP(c) }
func (p openAITextHTTPBackend) CompactOutcome(c *gin.Context, t time.Time) {
	p.h.logOpenAIRemoteCompactOutcome(c, t)
}
func (p openAITextHTTPBackend) NormalizeCompact(c *gin.Context, log *zap.Logger, body []byte) ([]byte, bool) {
	return p.h.normalizeOpenAIResponsesCompactRequest(c, log, body)
}
func (p openAITextHTTPBackend) CompactFlags(c *gin.Context, body []byte) (bool, bool) {
	return isOpenAILegacyCompactPath(c), isBareOpenAIResponsesPath(c) && isOpenAIRemoteCompactionV2Request(body)
}
func (p openAITextHTTPBackend) StartCompact(c *gin.Context, t time.Duration) func() {
	return service.StartOpenAICompactSSEKeepalive(c, t)
}
func (p openAITextHTTPBackend) StopCompact(c *gin.Context) bool {
	return service.StopOpenAICompactSSEKeepaliveCommitted(c)
}
func (p openAITextHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	setOpsRequestContext(c, model, stream)
}
func (p openAITextHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(stream, false)))
}
func (p openAITextHTTPBackend) Snapshot(c *gin.Context, proto protocol.ProtocolID, body []byte) {
	setOpenAICyberWarningRequestSnapshot(c, openAITextModerationProtocol(proto), body)
}
func (p openAITextHTTPBackend) Reasoning(c *gin.Context, key *apikey.APIKey, body []byte) ([]byte, bool, error) {
	return applyOpenAIReasoningEffortPolicyForRequest(c, service.APIKeyFromView(key), body)
}
func (p openAITextHTTPBackend) MessageReasoning(c *gin.Context, key *apikey.APIKey, body []byte) {
	bindOpenAIReasoningEffortPolicyForMessagesRequest(c, service.APIKeyFromView(key), body)
}
func (p openAITextHTTPBackend) PolicyDenied(c *gin.Context) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p openAITextHTTPBackend) NormalizeBootstrap(body []byte, delegation bool) ([]byte, bool) {
	if delegation {
		return normalizeCodexDelegationBootstrap(body)
	}
	return normalizeCodexAutomationBootstrap(body)
}
func (p openAITextHTTPBackend) ValidateTier(body []byte) error {
	_, err := service.ValidateOpenAIServiceTierField(body)
	return err
}
func (p openAITextHTTPBackend) PreviousKind(id string) string {
	return service.ClassifyOpenAIPreviousResponseIDKind(id)
}
func (p openAITextHTTPBackend) ValidateOwner(ctx context.Context, group int64, id string, user, key int64) (bool, error) {
	return p.h.gatewayService.ValidateOpenAIHTTPResponseOwner(ctx, group, id, user, key)
}
func (p openAITextHTTPBackend) SetOwner(c *gin.Context, user, key int64) {
	service.SetOpenAIHTTPResponseOwner(c, user, key)
}
func (p openAITextHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, proto protocol.ProtocolID, model string, body []byte) *moderation.Decision {
	return p.h.checkContentModeration(c, log, service.APIKeyFromView(key), subject, openAITextModerationProtocol(proto), model, body)
}
func (p openAITextHTTPBackend) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	old := service.APIKeyFromView(key)
	return p.h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(old), old.GroupID, model)
}
func (p openAITextHTTPBackend) BindPlan(c *gin.Context, plan routing.RoutePlan) {
	c.Request = c.Request.WithContext(service.WithRoutePlan(c.Request.Context(), plan))
}
func (p openAITextHTTPBackend) ImageIntent(model string, body []byte, mapping routing.ChannelMappingResult, platform string) ([]byte, string, bool) {
	return resolveOpenAIChannelMappedImageIntent("/v1/responses", model, body, service.ChannelMappingResult(mapping), platform, p.h.gatewayService.ReplaceModelInBody)
}
func (p openAITextHTTPBackend) ExplicitImageIntent(path, model string, body []byte) bool {
	return service.IsExplicitImageGenerationIntent(path, model, body)
}
func (p openAITextHTTPBackend) PassthroughContext(ctx context.Context) context.Context {
	return service.WithOpenAIHTTPPassthroughRouting(ctx)
}
func (p openAITextHTTPBackend) ImageContext(ctx context.Context) context.Context {
	return service.WithOpenAIImageGenerationIntent(ctx)
}
func (p openAITextHTTPBackend) AllowsImages(key *apikey.APIKey) bool {
	return service.GroupAllowsResponsesImages(service.APIKeyFromView(key).Group)
}
func (p openAITextHTTPBackend) FeatureDenied(c *gin.Context) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (p openAITextHTTPBackend) ImagePermissionMessage() string {
	return service.ImageGenerationPermissionMessage()
}
func (p openAITextHTTPBackend) ImageSlot(c *gin.Context, started bool) (func(), bool) {
	return p.h.acquireImageGenerationSlot(c, started)
}
func (p openAITextHTTPBackend) SeedImageIntent(c *gin.Context, mapped, image bool) {
	seedOpenAIForwardImageIntentHint(c, mapped, image)
}
func (p openAITextHTTPBackend) ValidateTools(c *gin.Context, body []byte, log *zap.Logger) bool {
	return p.h.validateFunctionCallOutputRequest(c, body, log)
}
func (p openAITextHTTPBackend) BindErrors(c *gin.Context) {
	if p.h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, p.h.errorPassthroughService)
	}
}
func (p openAITextHTTPBackend) Platform(key *apikey.APIKey) string {
	return openAICompatibleRequestPlatform(service.APIKeyFromView(key))
}
func (p openAITextHTTPBackend) AuthLatency(c *gin.Context, ms int64) {
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, ms)
}
func (p openAITextHTTPBackend) UserSlot(c *gin.Context, user int64, limit int, stream bool, started *bool, log *zap.Logger) (func(), bool) {
	return p.h.acquireResponsesUserSlot(c, user, limit, stream, started, log)
}
func (p openAITextHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	old := service.APIKeyFromView(key)
	return p.h.billingCacheService.CheckBillingEligibility(ctx, old.User, old, old.Group, sub, service.QuotaPlatform(ctx, old))
}
func (p openAITextHTTPBackend) SessionHash(c *gin.Context, kind gatewayhttp.OpenAISessionInput, body []byte) string {
	switch kind {
	case gatewayhttp.OpenAIExplicitSession:
		return p.h.gatewayService.GenerateExplicitSessionHash(c, body)
	case gatewayhttp.OpenAIPromptCacheSession:
		return p.h.gatewayService.ExtractSessionID(c, body)
	default:
		return p.h.gatewayService.GenerateSessionHash(c, body)
	}
}
func (p openAITextHTTPBackend) RejectCyber(c *gin.Context, key *apikey.APIKey, body []byte, model string, proto protocol.ProtocolID) bool {
	format := cyberBlockFormatResponses
	if proto == protocol.ProtocolAnthropicMessages {
		format = cyberBlockFormatAnthropic
	}
	if proto == protocol.ProtocolOpenAIChatCompletions {
		format = cyberBlockFormatChat
	}
	return p.h.rejectIfCyberSessionBlocked(c, service.APIKeyFromView(key), body, model, format)
}
func (p openAITextHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, user int64, source, hash string) error {
	return p.h.ensureOpenAISessionIsolation(ctx, service.APIKeyFromView(key), user, source, hash)
}
func (p openAITextHTTPBackend) GuardianContext(ctx context.Context, c *gin.Context, body []byte, model string) context.Context {
	return service.WithOpenAIGuardianParentAffinity(ctx, c, body, model)
}
func (p openAITextHTTPBackend) RequiredCapability(image, native, legacy bool, platform string) account.OpenAIEndpointCapability {
	return openAIResponsesRequiredCapabilityForRequest(image, native, legacy, platform)
}
func (p openAITextHTTPBackend) AllowsMessages(key *apikey.APIKey) bool {
	return allowOpenAICompatibleMessagesDispatch(service.APIKeyFromView(key))
}
func (p openAITextHTTPBackend) MessageAccountModel(ctx context.Context, key *apikey.APIKey, model string) string {
	return resolveOpenAIMessagesAccountLayerModelForRequest(ctx, service.APIKeyFromView(key), model)
}

func (p openAITextHTTPBackend) MetadataSession(c *gin.Context, hash, key, model string, body []byte) (string, string) {
	return resolveOpenAIMessagesMetadataSession(c, hash, key, model, body)
}
func (p openAITextHTTPBackend) ChatImageModel(model string, mapping routing.ChannelMappingResult) bool {
	return service.IsGPTImageGenerationModel(openAIChannelMappedModel(model, service.ChannelMappingResult(mapping)))
}
func (p openAITextHTTPBackend) ErrorMetadata(c *gin.Context) (string, string) {
	return failedResponseRequestID(c), requestModel(c)
}
func (p openAITextHTTPBackend) MarkStream(c *gin.Context, kind, message string, status int) {
	service.MarkOpsStreamError(c, kind, message, status)
}
func (p openAITextHTTPBackend) EnsureFallback(c *gin.Context, started bool) bool {
	return p.h.ensureForwardErrorResponse(c, started)
}

// Execution 只构造原生投影到既有单步适配器，不执行第二套切号或计量逻辑。

// 审核的历史标识与协议 ID 不同，必须在边界显式投影。
func openAITextModerationProtocol(proto protocol.ProtocolID) string {
	switch proto {
	case protocol.ProtocolAnthropicMessages:
		return moderation.ContentModerationProtocolAnthropicMessages
	case protocol.ProtocolOpenAIChatCompletions:
		return moderation.ContentModerationProtocolOpenAIChat
	default:
		return moderation.ContentModerationProtocolOpenAIResponses
	}
}

// MarkStreamFailure 与普通流错误分别保留 SLA 口径。
func (p openAITextHTTPBackend) MarkStreamFailure(c *gin.Context, kind, code, message string, status int) {
	service.MarkOpsStreamFailure(c, kind, code, message, status)
}

// projectOpenAIFailoverError 只投影平台已确认的展示信息，规则解释在新 HTTP 层。
func projectOpenAIFailoverError(err *service.UpstreamFailoverError) *gatewayhttp.OpenAIFailoverError {
	if err == nil {
		return nil
	}
	credentialStatus, credentialMessage := credentialFailoverClientResponse(err)
	return &gatewayhttp.OpenAIFailoverError{Status: err.StatusCode, ClientStatus: err.ClientStatusCode, ClientMessage: err.ClientMessage, CredentialStatus: credentialStatus, CredentialMessage: credentialMessage, Headers: err.ResponseHeaders, Body: err.ResponseBody, TooLarge: err.IsOpenAIRequestBodyTooLarge(), TooLargeMessage: service.OpenAIRequestBodyTooLargeClientMessage, ContinuationUnsupported: err.Reason == service.OpenAIHTTPContinuationUnsupportedReason, Credential: err.IsCredentialFailure(), CapacityShed: err.IsOpenAICapacityShed(), SilentRefusal: service.IsOpenAISilentRefusalErrorBody(err.ResponseBody), SilentMessage: service.OpenAISilentRefusalClientMessage(), CyberWarning: service.IsOpenAICyberWarningPayload(err.ResponseBody, ""), CyberMessage: service.ExtractOpenAICyberWarningMessage(err.ResponseBody, ""), UpstreamMessage: service.ExtractUpstreamErrorMessage(err.ResponseBody)}
}

// NewOpenAITextExecutor 在唯一 Recorder 已绑定后构造，供 Responses/Chat/Messages 共用。
func (h *OpenAIGatewayHandler) NewOpenAITextExecutor() *textflow.ResponsesExecutor {
	maxSwitches := 0
	if h != nil {
		maxSwitches = h.maxAccountSwitches
	}
	return textflow.NewResponsesExecutor(&fixedOpenAITextRuntime{dependencies: newOpenAIExecutionDependencies(h)}, textflow.ResponseOptions{MaxSwitches: maxSwitches}, textflow.ResponseOptions{MaxSwitches: maxSwitches, FirstOutputBudget: true})
}

// MappedBodyCache 仅为尚未改绑的无消费 token 入口保留。
func (p openAITextHTTPBackend) MappedBodyCache(body []byte) func(bool, string) []byte {
	return newOpenAIModelMappedBodyCache(body, p.h.gatewayService.ReplaceModelInBody)
}
