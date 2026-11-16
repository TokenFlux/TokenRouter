package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// openai_text_bindings.go binds text HTTP admission to routing, moderation,
// funding and session services.
// Text entries prepare HTTP state through app-bound resources and policy
// callbacks before the provider loop runs. API-key copies carry the currently
// admitted group into route, moderation, funding and session checks, while
// optional error and isolation bindings determine their documented default
// behavior.

// OpenAITextBindings carries app-owned text-entry resources and synchronous
// policy callbacks. HTTP backend methods borrow these services during
// admission; callers keep their bindings available while live requests wait
// and execute.
type OpenAITextBindings struct {
	// ClientVersions supplies minimum/maximum bounds for recognized Claude Code
	// clients. PrepareMessages skips that range check when this callback is nil.
	// Messages client version resolver
	ClientVersions func(context.Context) (string, string)
	// protocol destination-group fallback resolver
	ClientGroupFallback ClientGroupFallbackResolver
	// bound entry dependency presence
	Dependencies OpenAIDependencies
	// Resources supplies configured user admission during text entry. Image
	// admission uses its optional limiter/settings and can bypass with nil cleanup.
	Resources *OpenAIHTTPResources // shared HTTP admission resources
	// ResponseOwner supplies continuation ownership lookup. ValidateOwner
	// rejects unavailable evidence when this factory is nil or returns nil.
	ResponseOwner func() gatewaysession.HTTPResponseOwnerReader
	Moderation    ModerationPort // shared gateway content moderation
	// admitted group route planner
	PlanRoute func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	// group-mapped request body replacer
	ReplaceModel requeststate.ModelBodyReplacer
	// Errors is bound by BindErrors when present. A nil value leaves final
	// output using ordinary protocol error presentation rather than administrator rules.
	// optional error presentation service
	Errors *errorpolicy.ErrorPassthroughService
	// Funding supplies the post-slot eligibility call. The HTTP binding passes
	// afterWait false, so the common checker includes its normal RPM admission.
	Funding interface {
		// CheckKey validates admitted key funding with its subscription and platform.
		// afterWait selects a funding-only recheck in the common admission service;
		// this text backend invokes the normal path with false after user-slot waiting.
		CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	}
	Cyber *CyberHandler // shared session cyber-risk handler
	// IsolateSession performs explicit identity admission when present. Isolate
	// returns nil when absent, so that binding can permit execution without this check.
	// optional explicit-session admission callback
	IsolateSession func(context.Context, *apikey.APIKey, int64, string, string) error
}

// openAITextHTTPBackend adapts app text bindings into synchronous HTTP
// admission methods.
type openAITextHTTPBackend struct {
	bindings OpenAITextBindings // captured app text-entry bindings
}

// ResolveOpenAIMessagesProviderLayerModel trims the admitted group-mapped
// model for later provider rules.
func ResolveOpenAIMessagesProviderLayerModel(groupMappedModel string) string {
	return strings.TrimSpace(groupMappedModel)
}

// ResolveOpenAIMessagesProviderLayerModelForRequest trims the group-mapped
// model and registers it in response model tracing. This stage does not apply
// another provider mapping; later provider execution consumes the returned
// model with its own policy.
func ResolveOpenAIMessagesProviderLayerModelForRequest(ctx context.Context, groupMappedModel string) string {
	model := ResolveOpenAIMessagesProviderLayerModel(groupMappedModel)
	modeltrace.RegisterStage(ctx, model)
	return model
}

// NewBoundOpenAITextHandler captures app bindings behind the HTTP backend and
// supplied executor. It delegates construction without validation or resource
// acquisition; callers complete RequestLifetime and dependency assembly before
// serving text protocol entries.
func NewBoundOpenAITextHandler(options OpenAITextOptions, bindings OpenAITextBindings, prompt MessagesPrompt, executor execution.Executor) *OpenAITextHandler {
	return NewOpenAITextHandler(options, openAITextHTTPBackend{bindings}, prompt, executor)
}

// Access selects the request-effective key, or copies the authenticated key
// when its slot is absent or differently typed. The boolean retains the
// source pointer assertion, so typed nil can return nil with true; entries
// rely on authentication publishing a nonnil admitted key.
func (p openAITextHTTPBackend) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	return apikey.CopyAPIKey(key), ok
}

// Dependencies reports whether text-entry dependencies are ready and logs
// missing names. Failure can write 503 while the writer is untouched; callers
// stop admission on false before reading the body.
func (p openAITextHTTPBackend) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.bindings.Dependencies.Ensure(c, log)
}

// ReadFailure delegates structured diagnostics for request-body reading
// through the supplied logger.
func (p openAITextHTTPBackend) ReadFailure(log *zap.Logger, r *http.Request, err error) {
	LogRequestBodyReadFailure(log, r, err)
}

// TransportHTTP publishes HTTP client transport in this request's execution
// state.
func (p openAITextHTTPBackend) TransportHTTP(c *gin.Context) {
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
}

// StartCompact delegates admission-sensitive Compact heartbeat startup and
// returns its stop callback. The HTTP entry invokes cleanup on return; the
// heartbeat helper owns its writer synchronization and any enabled goroutine
// until stopped.
func (p openAITextHTTPBackend) StartCompact(c *gin.Context, t time.Duration) func() {
	return StartOpenAICompactSSEKeepalive(c, t)
}

// StopCompact prevents later Compact beats and waits for any active heartbeat
// write. The result reports first-beat framing even when its body write
// failed, so error output can attempt a Responses terminal; worker exit is
// independent.
func (p openAITextHTTPBackend) StopCompact(c *gin.Context) bool {
	return StopOpenAICompactSSEKeepaliveCommitted(c)
}

// ObserveRequest records the client model and streaming preference in request
// Ops state.
func (p openAITextHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	SetOpsRequestContext(c, model, stream)
}

// ObserveEndpoint records the compatible endpoint and request type derived
// from streaming preference.
func (p openAITextHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}

// Snapshot records canonical bytes for cyber-warning attribution under the
// selected client protocol.
func (p openAITextHTTPBackend) Snapshot(c *gin.Context, proto protocol.ProtocolID, body []byte) {
	SetOpenAICyberWarningRequestSnapshot(c, openAITextModerationProtocol(proto), body)
}

// Reasoning applies requested-effort attribution and OpenAI group policy using
// a copied API key. Changed bytes and policy errors return to the entry before
// moderation and execution, keeping the admitted key preparation separate from
// that policy read.
func (p openAITextHTTPBackend) Reasoning(c *gin.Context, key *apikey.APIKey, body []byte) ([]byte, bool, error) {
	return ApplyOpenAIReasoningEffortPolicyForRequest(c, apikey.CopyAPIKey(key), body)
}

// MessageReasoning binds effort policy for later Messages conversion using a
// copied admitted key. The shared helper requires a usable client
// output_config.effort before binding enforcement, so an absent client field
// leaves bridge defaults outside that policy.
func (p openAITextHTTPBackend) MessageReasoning(c *gin.Context, key *apikey.APIKey, body []byte) {
	BindOpenAIReasoningEffortPolicyForMessagesRequest(c, apikey.CopyAPIKey(key), body)
}

// PolicyDenied marks local policy limitation in the current request's Ops
// state.
func (p openAITextHTTPBackend) PolicyDenied(c *gin.Context) {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}

// NormalizeBootstrap delegates delegation or automation bootstrap repair
// according to the supplied flag. Each helper returns body and adoption state,
// and the HTTP entry installs returned bytes only for a reported change.
func (p openAITextHTTPBackend) NormalizeBootstrap(body []byte, delegation bool) ([]byte, bool) {
	if delegation {
		return requeststate.NormalizeCodexDelegationBootstrap(body)
	}
	return requeststate.NormalizeCodexAutomationBootstrap(body)
}

// NormalizeBootstraps repairs both automation and delegation Codex bootstrap
// call-output shapes in one shared pass, returning adopted bytes and separate
// change flags for request logging.
func (p openAITextHTTPBackend) NormalizeBootstraps(body []byte) ([]byte, bool, bool) {
	return requeststate.NormalizeCodexBootstraps(body)
}

// ValidateTier delegates service-tier field validation before route execution
// and provider policy.
func (p openAITextHTTPBackend) ValidateTier(body []byte) error {
	_, err := protocolopenai.ValidateServiceTierField(body)
	return err
}

// PreviousKind classifies the supplied previous-response identifier for
// admission diagnostics.
func (p openAITextHTTPBackend) PreviousKind(id string) string {
	return protocolopenai.ClassifyOpenAIPreviousResponseIDKind(id)
}

// ValidateOwner delegates grouped HTTP response ownership lookup using the
// configured reader callback. Its boolean and error reach Responses admission
// independently; the session service owns same-user cross-key compatibility
// and denies unavailable ownership evidence.
func (p openAITextHTTPBackend) ValidateOwner(ctx context.Context, group int64, id string, user, key int64) (bool, error) {
	return gatewaysession.ValidateHTTPResponseOwner(ctx, p.bindings.ResponseOwner, group, id, user, key)
}

// SetOwner binds the authenticated user and key for later HTTP response-owner
// publication.
func (p openAITextHTTPBackend) SetOwner(c *gin.Context, user, key int64) {
	SetHTTPResponseOwner(c, user, key)
}

// Moderate supplies a copied admitted key and normalized client protocol to
// shared moderation. An absent decision permits the entry to continue, while
// blocked observations produce protocol-specific rejection before provider
// execution; the shared moderation adapter handles read errors.
func (p openAITextHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, proto protocol.ProtocolID, model string, body []byte) *moderation.Decision {
	return RunContentModeration(GatewayModerationEndpoints{}, c, log, p.bindings.Moderation, apikey.CopyAPIKey(key), subject, openAITextModerationProtocol(proto), model, body)
}

// Plan calls the configured route planner with a copied admitted key and
// client model.
func (p openAITextHTTPBackend) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	old := apikey.CopyAPIKey(key)
	return p.bindings.PlanRoute(ctx, old, model)
}

// BindPlan installs the resolved route plan in the active request context
// before execution.
func (p openAITextHTTPBackend) BindPlan(c *gin.Context, plan routing.RoutePlan) {
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
}

// ImageIntent delegates group-mapped Responses body, model and broad
// image-intent preparation. It uses the configured body replacer; callers keep
// canonical request bytes separately for session identity while using returned
// mapped input for explicit admission.
func (p openAITextHTTPBackend) ImageIntent(model string, body []byte, mapping routing.GroupMappingResult, platform string) ([]byte, string, bool) {
	return GroupMappedImageIntent("/v1/responses", model, body, mapping, platform, p.bindings.ReplaceModel)
}

// ExplicitImageIntent delegates client-declared image generation detection for
// HTTP admission.
func (p openAITextHTTPBackend) ExplicitImageIntent(path, model string, body []byte) bool {
	return gatewayprovider.ImageIntent().IsExplicitImageGenerationIntent(path, model, body)
}

// ImageContext derives a context carrying the admitted Responses image-intent
// marker.
func (p openAITextHTTPBackend) ImageContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIImageGenerationIntent(ctx)
}

// AllowsImages evaluates Responses image permission from a copied admitted key
// group. Callers supply a nonnil key, because this binding dereferences the
// copy; the routing helper owns group inheritance and default behavior.
func (p openAITextHTTPBackend) AllowsImages(key *apikey.APIKey) bool {
	return routing.GroupAllowsResponsesImages(apikey.CopyAPIKey(key).Group)
}

// FeatureDenied marks local feature-gate rejection in the current request's
// Ops state.
func (p openAITextHTTPBackend) FeatureDenied(c *gin.Context) {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
}

// ImagePermissionMessage returns the shared client explanation for denied
// image-generation permission.
func (p openAITextHTTPBackend) ImagePermissionMessage() string {
	return media.ImageGenerationPermissionMessage
}

// ImageSlot delegates image admission with the current started state and
// returns release/admission. Missing optional limiter settings can bypass
// admission with nil cleanup; callers invoke any returned release when
// execution finishes.
func (p openAITextHTTPBackend) ImageSlot(c *gin.Context, started bool) (func(), bool) {
	return p.bindings.Resources.AcquireImage(c, started)
}

// SeedImageIntent seeds canonical or mapped broad image intent for later
// request preparation.
func (p openAITextHTTPBackend) SeedImageIntent(c *gin.Context, mapped, image bool) {
	SeedOpenAIForwardImageIntentHint(c, mapped, image)
}

// ValidateTools checks function-call-output context through the shared
// validator. False means that helper wrote the protocol rejection and the
// entry stops; nonblank continuation or inline call context can admit the
// supplied history before standalone output checks.
func (p openAITextHTTPBackend) ValidateTools(c *gin.Context, body []byte, log *zap.Logger) bool {
	return ValidateOpenAIFunctionCallOutput(c, body, log)
}

// BindErrors installs the configured error presentation service in the current
// request. A nil service is ignored, allowing final error writers to use their
// ordinary status and message defaults.
func (p openAITextHTTPBackend) BindErrors(c *gin.Context) {
	if p.bindings.Errors != nil {
		BindErrorPassthroughService(c, p.bindings.Errors)
	}
}

// Platform derives the OpenAI-compatible request platform from a copied
// admitted key.
func (p openAITextHTTPBackend) Platform(key *apikey.APIKey) string {
	return OpenAICompatibleRequestPlatform(apikey.CopyAPIKey(key))
}

// AuthLatency records authentication/admission preparation duration in integer
// milliseconds for Ops.
func (p openAITextHTTPBackend) AuthLatency(c *gin.Context, ms int64) {
	SetOpsLatencyMs(c, OpsAuthLatencyMsKey, ms)
}

// UserSlot delegates waiting user admission and returns its release callback
// and success flag. The shared resources bind cancellation cleanup to the
// original request context and write rejection on failure; the entry also
// defers returned cleanup.
func (p openAITextHTTPBackend) UserSlot(c *gin.Context, user int64, limit int, stream bool, started *bool, log *zap.Logger) (func(), bool) {
	return p.bindings.Resources.AcquireUser(c, user, limit, stream, started, log)
}

// Eligibility invokes the configured funding checker with a copied key,
// subscription, empty platform and afterWait false. The common
// FundingAdmission therefore runs its normal funding/RPM path, even though the
// HTTP entry calls this after slot waiting.
func (p openAITextHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	old := apikey.CopyAPIKey(key)
	return p.bindings.Funding.CheckKey(ctx, old, sub, "", false)
}

// SessionHash selects explicit isolation hash, raw prompt-cache seed or
// general provider affinity according to kind. Unknown values use the general
// affinity helper, so callers select OpenAIExplicitSession when
// derived-content fallback must be excluded.
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

// RejectCyber delegates session-risk rejection using a copied key and
// client-protocol error format. Responses is the default format; Messages and
// Chat select their respective forms, and true tells the HTTP entry to
// terminate processing.
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

// Isolate delegates explicit-session admission with a copied effective key
// when its callback is configured. A nil callback returns nil, while conflicts
// and service errors propagate for the entry's protocol-specific rejection
// handling.
func (p openAITextHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, user int64, source, hash string) error {
	if p.bindings.IsolateSession == nil {
		return nil
	}
	return p.bindings.IsolateSession(ctx, apikey.CopyAPIKey(key), user, source, hash)
}

// GuardianContext derives canonical-input parent affinity for the Responses
// selection context.
func (p openAITextHTTPBackend) GuardianContext(ctx context.Context, c *gin.Context, body []byte, model string) context.Context {
	return WithOpenAIGuardianParentAffinity(ctx, c, body, model)
}

// AllowsMessages admits nil keys/groups or a group allowing the Messages
// protocol. The copied group supplies the current protocol collection, and the
// entry handles false before dependency checks or body reading.
func (p openAITextHTTPBackend) AllowsMessages(key *apikey.APIKey) bool {
	projected := apikey.CopyAPIKey(key)
	return projected == nil || projected.Group == nil || projected.Group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages)
}

// MessageProviderModel registers the normalized group-mapped Messages model in
// request trace state. Its key argument is unused, and the model helper trims
// rather than applying provider mapping at this stage.
func (p openAITextHTTPBackend) MessageProviderModel(ctx context.Context, key *apikey.APIKey, model string) string {
	return ResolveOpenAIMessagesProviderLayerModelForRequest(ctx, model)
}

// MetadataSession delegates Messages session/cache derivation using Claude
// Code headers and body metadata. The shared session helper determines their
// precedence; returned values remain separate selection identity and upstream
// prompt-cache seed.
func (p openAITextHTTPBackend) MetadataSession(c *gin.Context, hash, key, model string, body []byte) (string, string) {
	return gatewaysession.MessagesMetadataSession(ClaudeCodeSessionIDFromHeader(c), hash, key, model, body)
}

// ErrorMetadata reads current request ID and model for dedicated terminal
// failure events.
func (p openAITextHTTPBackend) ErrorMetadata(c *gin.Context) (string, string) {
	return ErrorRequestID(c), ErrorRequestModel(c)
}

// MarkStream records an ordinary started-stream error with its intended HTTP
// status.
func (p openAITextHTTPBackend) MarkStream(c *gin.Context, kind, message string, status int) {
	MarkOpsStreamError(c, kind, message, status)
}

// EnsureFallback delegates final heartbeat-aware output when no dedicated
// error has been supplied. The shared output checks current commitment and
// adjusted bytes, and the returned boolean indicates invocation rather than
// confirmed network delivery.
func (p openAITextHTTPBackend) EnsureFallback(c *gin.Context, started bool) bool {
	return DefaultOpenAIErrorOutput().EnsureFallback(c, started)
}

// openAITextModerationProtocol maps client protocol IDs to the shared
// moderation identifiers.
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

// MarkStreamFailure records an SLA-counted stream failure with its
// classification and intended status.
func (p openAITextHTTPBackend) MarkStreamFailure(c *gin.Context, kind, code, message string, status int) {
	MarkOpsStreamFailure(c, kind, code, message, status)
}

// PrepareMessages binds client identity and eligibility before Messages
// provider selection. The configured client-version callback feeds the shared
// validator, including mixed-scheduling Claude Code rules; an error reaches
// the HTTP entry before group resolution and moderation.
func (p openAITextHTTPBackend) PrepareMessages(c *gin.Context, body []byte) error {
	return PrepareMessageClientContext(c, body, p.bindings.ClientVersions)
}
