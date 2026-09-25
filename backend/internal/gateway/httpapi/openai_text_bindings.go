package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAITextBindings 只绑定原生资源和明确用例端口，不持有旧聚合 Handler。
type OpenAITextBindings struct {
	Dependencies  OpenAIDependencies
	Resources     *OpenAIHTTPResources
	ResponseOwner func() gatewaysession.HTTPResponseOwnerReader
	Moderation    ModerationPort
	PlanRoute     func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	ReplaceModel  requeststate.ModelBodyReplacer
	Errors        *errorpolicy.ErrorPassthroughService
	Funding       interface {
		CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	}
	Cyber          *CyberHandler
	IsolateSession func(context.Context, *apikey.APIKey, int64, string, string) error
}
type openAITextHTTPBackend struct{ bindings OpenAITextBindings }

// NewBoundOpenAITextHandler 将固定原生端口与唯一执行器组合为 HTTP 入口。
func NewBoundOpenAITextHandler(options OpenAITextOptions, bindings OpenAITextBindings, prompt MessagesPrompt, executor execution.Executor) *OpenAITextHandler {
	return NewOpenAITextHandler(options, openAITextHTTPBackend{bindings}, prompt, executor)
}
func (p openAITextHTTPBackend) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	return apikey.CopyAPIKey(key), ok
}
func (p openAITextHTTPBackend) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.bindings.Dependencies.Ensure(c, log)
}
func (p openAITextHTTPBackend) ReadFailure(log *zap.Logger, r *http.Request, err error) {
	LogRequestBodyReadFailure(log, r, err)
}
func (p openAITextHTTPBackend) TransportHTTP(c *gin.Context) {
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
}

func (p openAITextHTTPBackend) StartCompact(c *gin.Context, t time.Duration) func() {
	return StartOpenAICompactSSEKeepalive(c, t)
}
func (p openAITextHTTPBackend) StopCompact(c *gin.Context) bool {
	return StopOpenAICompactSSEKeepaliveCommitted(c)
}
func (p openAITextHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	SetOpsRequestContext(c, model, stream)
}
func (p openAITextHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}
func (p openAITextHTTPBackend) Snapshot(c *gin.Context, proto protocol.ProtocolID, body []byte) {
	SetOpenAICyberWarningRequestSnapshot(c, openAITextModerationProtocol(proto), body)
}
func (p openAITextHTTPBackend) Reasoning(c *gin.Context, key *apikey.APIKey, body []byte) ([]byte, bool, error) {
	return ApplyOpenAIReasoningEffortPolicyForRequest(c, apikey.CopyAPIKey(key), body)
}
func (p openAITextHTTPBackend) MessageReasoning(c *gin.Context, key *apikey.APIKey, body []byte) {
	BindOpenAIReasoningEffortPolicyForMessagesRequest(c, apikey.CopyAPIKey(key), body)
}
func (p openAITextHTTPBackend) PolicyDenied(c *gin.Context) {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p openAITextHTTPBackend) NormalizeBootstrap(body []byte, delegation bool) ([]byte, bool) {
	if delegation {
		return requeststate.NormalizeCodexDelegationBootstrap(body)
	}
	return requeststate.NormalizeCodexAutomationBootstrap(body)
}
func (p openAITextHTTPBackend) ValidateTier(body []byte) error {
	_, err := protocolopenai.ValidateServiceTierField(body)
	return err
}
func (p openAITextHTTPBackend) PreviousKind(id string) string {
	return protocolopenai.ClassifyOpenAIPreviousResponseIDKind(id)
}
func (p openAITextHTTPBackend) ValidateOwner(ctx context.Context, group int64, id string, user, key int64) (bool, error) {
	return gatewaysession.ValidateHTTPResponseOwner(ctx, p.bindings.ResponseOwner, group, id, user, key)
}
func (p openAITextHTTPBackend) SetOwner(c *gin.Context, user, key int64) {
	SetHTTPResponseOwner(c, user, key)
}
func (p openAITextHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, proto protocol.ProtocolID, model string, body []byte) *moderation.Decision {
	return RunContentModeration(GatewayModerationEndpoints{}, c, log, p.bindings.Moderation, apikey.CopyAPIKey(key), subject, openAITextModerationProtocol(proto), model, body)
}
func (p openAITextHTTPBackend) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	old := apikey.CopyAPIKey(key)
	return p.bindings.PlanRoute(ctx, old, model)
}
func (p openAITextHTTPBackend) BindPlan(c *gin.Context, plan routing.RoutePlan) {
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
}
func (p openAITextHTTPBackend) ImageIntent(model string, body []byte, mapping routing.ChannelMappingResult, platform string) ([]byte, string, bool) {
	return ChannelMappedImageIntent("/v1/responses", model, body, mapping, platform, p.bindings.ReplaceModel)
}
func (p openAITextHTTPBackend) ExplicitImageIntent(path, model string, body []byte) bool {
	return gatewayprovider.ImageIntent().IsExplicitImageGenerationIntent(path, model, body)
}
func (p openAITextHTTPBackend) PassthroughContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIHTTPPassthroughRouting(ctx)
}
func (p openAITextHTTPBackend) ImageContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIImageGenerationIntent(ctx)
}
func (p openAITextHTTPBackend) AllowsImages(key *apikey.APIKey) bool {
	return routing.GroupAllowsResponsesImages(apikey.CopyAPIKey(key).Group)
}
func (p openAITextHTTPBackend) FeatureDenied(c *gin.Context) {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
}
func (p openAITextHTTPBackend) ImagePermissionMessage() string {
	return media.ImageGenerationPermissionMessage
}
func (p openAITextHTTPBackend) ImageSlot(c *gin.Context, started bool) (func(), bool) {
	return p.bindings.Resources.AcquireImage(c, started)
}
func (p openAITextHTTPBackend) SeedImageIntent(c *gin.Context, mapped, image bool) {
	SeedOpenAIForwardImageIntentHint(c, mapped, image)
}
func (p openAITextHTTPBackend) ValidateTools(c *gin.Context, body []byte, log *zap.Logger) bool {
	return ValidateOpenAIFunctionCallOutput(c, body, log)
}
func (p openAITextHTTPBackend) BindErrors(c *gin.Context) {
	if p.bindings.Errors != nil {
		BindErrorPassthroughService(c, p.bindings.Errors)
	}
}
func (p openAITextHTTPBackend) Platform(key *apikey.APIKey) string {
	return OpenAICompatibleRequestPlatform(apikey.CopyAPIKey(key))
}
func (p openAITextHTTPBackend) AuthLatency(c *gin.Context, ms int64) {
	SetOpsLatencyMs(c, OpsAuthLatencyMsKey, ms)
}
func (p openAITextHTTPBackend) UserSlot(c *gin.Context, user int64, limit int, stream bool, started *bool, log *zap.Logger) (func(), bool) {
	return p.bindings.Resources.AcquireUser(c, user, limit, stream, started, log)
}
func (p openAITextHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	old := apikey.CopyAPIKey(key)
	return p.bindings.Funding.CheckKey(ctx, old, sub, admission.QuotaPlatform(ctx, old), false)
}
func (p openAITextHTTPBackend) SessionHash(c *gin.Context, kind OpenAISessionInput, body []byte) string {
	switch kind {
	case OpenAIExplicitSession:
		return GenerateExplicitOpenAISessionHash(c, body)
	case OpenAIPromptCacheSession:
		return ExplicitOpenAIRequestSessionID(c, body)
	default:
		return GenerateOpenAISessionHash(c, body)
	}
}
func (p openAITextHTTPBackend) RejectCyber(c *gin.Context, key *apikey.APIKey, body []byte, model string, proto protocol.ProtocolID) bool {
	format := CyberBlockResponses
	if proto == protocol.ProtocolAnthropicMessages {
		format = CyberBlockAnthropic
	}
	if proto == protocol.ProtocolOpenAIChatCompletions {
		format = CyberBlockChat
	}
	return p.bindings.Cyber.RejectSession(c, apikey.CopyAPIKey(key), body, model, format)
}
func (p openAITextHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, user int64, source, hash string) error {
	if p.bindings.IsolateSession == nil {
		return nil
	}
	return p.bindings.IsolateSession(ctx, apikey.CopyAPIKey(key), user, source, hash)
}
func (p openAITextHTTPBackend) GuardianContext(ctx context.Context, c *gin.Context, body []byte, model string) context.Context {
	return WithOpenAIGuardianParentAffinity(ctx, c, body, model)
}

func (p openAITextHTTPBackend) AllowsMessages(key *apikey.APIKey) bool {
	projected := apikey.CopyAPIKey(key)
	return projected == nil || projected.Group == nil || projected.Group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages)
}
func (p openAITextHTTPBackend) MessageAccountModel(ctx context.Context, key *apikey.APIKey, model string) string {
	return ResolveOpenAIMessagesAccountLayerModelForRequest(ctx, apikey.CopyAPIKey(key), model)
}

func (p openAITextHTTPBackend) MetadataSession(c *gin.Context, hash, key, model string, body []byte) (string, string) {
	return gatewaysession.MessagesMetadataSession(ClaudeCodeSessionIDFromHeader(c), hash, key, model, body)
}
func (p openAITextHTTPBackend) ChatImageModel(model string, mapping routing.ChannelMappingResult) bool {
	return media.IsGPTImageGenerationModel(requeststate.ChannelMappedModel(model, mapping))
}
func (p openAITextHTTPBackend) ErrorMetadata(c *gin.Context) (string, string) {
	return ErrorRequestID(c), ErrorRequestModel(c)
}
func (p openAITextHTTPBackend) MarkStream(c *gin.Context, kind, message string, status int) {
	MarkOpsStreamError(c, kind, message, status)
}
func (p openAITextHTTPBackend) EnsureFallback(c *gin.Context, started bool) bool {
	return DefaultOpenAIErrorOutput().EnsureFallback(c, started)
}

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
	MarkOpsStreamFailure(c, kind, code, message, status)
}
