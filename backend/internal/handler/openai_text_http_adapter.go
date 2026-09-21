// OpenAI 文本旧装配只投影实体、绑定单步用例，不再拥有 HTTP 前置组合。
package handler

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	upstream "github.com/TokenFlux/TokenRouter/internal/upstream"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	media "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

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
	return apikey.CopyAPIKey(key), ok
}
func (p openAITextHTTPBackend) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.h.ensureResponsesDependencies(c, log)
}
func (p openAITextHTTPBackend) ReadFailure(log *zap.Logger, r *http.Request, err error) {
	gatewayhttp.LogRequestBodyReadFailure(log, r, err)
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
	return gatewayhttp.StartOpenAICompactSSEKeepalive(c, t)
}
func (p openAITextHTTPBackend) StopCompact(c *gin.Context) bool {
	return gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted(c)
}
func (p openAITextHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	gatewayhttp.SetOpsRequestContext(c, model, stream)
}
func (p openAITextHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	gatewayhttp.SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}
func (p openAITextHTTPBackend) Snapshot(c *gin.Context, proto protocol.ProtocolID, body []byte) {
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, openAITextModerationProtocol(proto), body)
}
func (p openAITextHTTPBackend) Reasoning(c *gin.Context, key *apikey.APIKey, body []byte) ([]byte, bool, error) {
	return applyOpenAIReasoningEffortPolicyForRequest(c, apikey.CopyAPIKey(key), body)
}
func (p openAITextHTTPBackend) MessageReasoning(c *gin.Context, key *apikey.APIKey, body []byte) {
	bindOpenAIReasoningEffortPolicyForMessagesRequest(c, apikey.CopyAPIKey(key), body)
}
func (p openAITextHTTPBackend) PolicyDenied(c *gin.Context) {
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p openAITextHTTPBackend) NormalizeBootstrap(body []byte, delegation bool) ([]byte, bool) {
	if delegation {
		return normalizeCodexDelegationBootstrap(body)
	}
	return normalizeCodexAutomationBootstrap(body)
}
func (p openAITextHTTPBackend) ValidateTier(body []byte) error {
	_, err := protocolopenai.ValidateServiceTierField(body)
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
	return p.h.checkContentModeration(c, log, apikey.CopyAPIKey(key), subject, openAITextModerationProtocol(proto), model, body)
}
func (p openAITextHTTPBackend) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	old := apikey.CopyAPIKey(key)
	return p.h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(old), old.GroupID, model)
}
func (p openAITextHTTPBackend) BindPlan(c *gin.Context, plan routing.RoutePlan) {
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
}
func (p openAITextHTTPBackend) ImageIntent(model string, body []byte, mapping routing.ChannelMappingResult, platform string) ([]byte, string, bool) {
	return resolveOpenAIChannelMappedImageIntent("/v1/responses", model, body, routing.ChannelMappingResult(mapping), platform, p.h.gatewayService.ReplaceModelInBody)
}
func (p openAITextHTTPBackend) ExplicitImageIntent(path, model string, body []byte) bool {
	return service.IsExplicitImageGenerationIntent(path, model, body)
}
func (p openAITextHTTPBackend) PassthroughContext(ctx context.Context) context.Context {
	return service.WithOpenAIHTTPPassthroughRouting(ctx)
}
func (p openAITextHTTPBackend) ImageContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIImageGenerationIntent(ctx)
}
func (p openAITextHTTPBackend) AllowsImages(key *apikey.APIKey) bool {
	return service.GroupAllowsResponsesImages(apikey.CopyAPIKey(key).Group)
}
func (p openAITextHTTPBackend) FeatureDenied(c *gin.Context) {
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
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
		gatewayhttp.BindErrorPassthroughService(c, p.h.errorPassthroughService)
	}
}
func (p openAITextHTTPBackend) Platform(key *apikey.APIKey) string {
	return openAICompatibleRequestPlatform(apikey.CopyAPIKey(key))
}
func (p openAITextHTTPBackend) AuthLatency(c *gin.Context, ms int64) {
	gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsAuthLatencyMsKey, ms)
}
func (p openAITextHTTPBackend) UserSlot(c *gin.Context, user int64, limit int, stream bool, started *bool, log *zap.Logger) (func(), bool) {
	return p.h.acquireResponsesUserSlot(c, user, limit, stream, started, log)
}
func (p openAITextHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	old := apikey.CopyAPIKey(key)
	return p.h.billingCacheService.CheckKey(ctx, old, sub, service.QuotaPlatform(ctx, old), false)
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
	return p.h.rejectIfCyberSessionBlocked(c, apikey.CopyAPIKey(key), body, model, format)
}
func (p openAITextHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, user int64, source, hash string) error {
	return p.h.ensureOpenAISessionIsolation(ctx, apikey.CopyAPIKey(key), user, source, hash)
}
func (p openAITextHTTPBackend) GuardianContext(ctx context.Context, c *gin.Context, body []byte, model string) context.Context {
	return service.WithOpenAIGuardianParentAffinity(ctx, c, body, model)
}
func (p openAITextHTTPBackend) RequiredCapability(image, native, legacy bool, platform string) account.OpenAIEndpointCapability {
	return openAIResponsesRequiredCapabilityForRequest(image, native, legacy, platform)
}
func (p openAITextHTTPBackend) AllowsMessages(key *apikey.APIKey) bool {
	return allowOpenAICompatibleMessagesDispatch(apikey.CopyAPIKey(key))
}
func (p openAITextHTTPBackend) MessageAccountModel(ctx context.Context, key *apikey.APIKey, model string) string {
	return resolveOpenAIMessagesAccountLayerModelForRequest(ctx, apikey.CopyAPIKey(key), model)
}

func (p openAITextHTTPBackend) MetadataSession(c *gin.Context, hash, key, model string, body []byte) (string, string) {
	return resolveOpenAIMessagesMetadataSession(c, hash, key, model, body)
}
func (p openAITextHTTPBackend) ChatImageModel(model string, mapping routing.ChannelMappingResult) bool {
	return media.IsGPTImageGenerationModel(openAIChannelMappedModel(model, routing.ChannelMappingResult(mapping)))
}
func (p openAITextHTTPBackend) ErrorMetadata(c *gin.Context) (string, string) {
	return gatewayhttp.ErrorRequestID(c), gatewayhttp.ErrorRequestModel(c)
}
func (p openAITextHTTPBackend) MarkStream(c *gin.Context, kind, message string, status int) {
	gatewayhttp.MarkOpsStreamError(c, kind, message, status)
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
	gatewayhttp.MarkOpsStreamFailure(c, kind, code, message, status)
}

// projectOpenAIFailoverError 只投影平台已确认的展示信息，规则解释在新 HTTP 层。
func projectOpenAIFailoverError(err *forwardcore.UpstreamFailoverError) *gatewayhttp.OpenAIFailoverError {
	if err == nil {
		return nil
	}
	credentialStatus, credentialMessage := credentialFailoverClientResponse(err)
	return &gatewayhttp.OpenAIFailoverError{Status: err.StatusCode, ClientStatus: err.ClientStatusCode, ClientMessage: err.ClientMessage, CredentialStatus: credentialStatus, CredentialMessage: credentialMessage, Headers: err.ResponseHeaders, Body: err.ResponseBody, TooLarge: gatewayprovider.IsOpenAIRequestBodyTooLarge(err), TooLargeMessage: service.OpenAIRequestBodyTooLargeClientMessage, ContinuationUnsupported: err.Reason == service.OpenAIHTTPContinuationUnsupportedReason, Credential: err.IsCredentialFailure(), CapacityShed: gatewayprovider.IsOpenAICapacityShed(err), SilentRefusal: service.IsOpenAISilentRefusalErrorBody(err.ResponseBody), SilentMessage: service.OpenAISilentRefusalClientMessage(), CyberWarning: gatewayprovider.IsOpenAICyberWarningPayload(err.ResponseBody, ""), CyberMessage: gatewayprovider.ExtractOpenAICyberWarningMessage(err.ResponseBody, ""), UpstreamMessage: upstream.ExtractErrorMessage(err.ResponseBody)}
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
