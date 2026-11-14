package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// entry_bindings.go binds WebSocket entry operations to HTTP authentication,
// routing, admission, and completion services. Later turns reauthenticate
// against current identity and funding data. The refreshed Key is published
// separately for request allowance checks; routing and completion continue to
// use the connection's original Key and subscription references. An accepted
// request's background completion therefore needs a turn-specific request ID
// and capture context even when several turns share one Gin request.
//
// Provider selection binds its execution target to the same provider and
// scheduler observations. The target checks model pricing after routing
// eligibility, translates per-turn hooks between gateway and HTTP
// representations, and captures completion before submission. Logger adaptation
// lets detached completion errors use retained log fields while turn settlement
// obtains its own request record and payload snapshot.

// Bindings connects WebSocket entry, admission, provider execution and completion
// to services owned by app. The adapters retain the supplied callback and service
// references for a connection; callers finish binding them before serving requests.
type Bindings struct {
	BindSticky   func(context.Context, *int64, string, int64) error // optional session provider binder
	Pricing      *admission.ModelPricing                            // selected model price admission
	Common       openaiattempt.Bindings                             // shared attempt support services
	Dependencies gatewayhttp.OpenAIDependencies                     // entry service availability checks
	Prompt       gatewayhttp.MessagesPrompt                         // user prompt replacement dependency
	// Keys supplies current authentication and request allowance operations. Nil
	// disables refreshed Fast reads and makes Key admission or reauthorization fail.
	Keys interface {
		// GetByKey loads a Key for the supplied secret when per-turn Fast policy
		// refresh is enabled. Missing or errored results make RefreshFast return false
		// so the entry loop retains its last usable policy.
		GetByKey(context.Context, string) (*apikey.APIKey, error)
		// Reauthenticate checks current Key identity and limits against the
		// connection's authenticated Key. AuthorizeTurn passes the client IP and
		// member-limit flag; success supplies a candidate for funding checks before
		// requestKey changes.
		Reauthenticate(context.Context, *apikey.APIKey, apikey.AuthenticationInput) (*apikey.APIKey, error)
		// AcquireRequest reserves this turn's Key concurrency and RPM allowance and
		// returns its admitted context and release callback. Entry retains admission
		// across provider retries; the adapter discards the reported wait duration and
		// presents limiter errors as close instructions.
		AcquireRequest(context.Context, *apikey.APIKey) (context.Context, func(), time.Duration, error)
	}
	Subscriptions   admission.SubscriptionReader                                                                                                                // current subscription funding reader
	CheckFunding    func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error                                                        // funding and limit eligibility check
	Blocks          *session.CyberBlocks                                                                                                                        // shared blocked Cyber sessions
	PlanRoute       func(context.Context, *apikey.APIKey, string) routing.RoutePlan                                                                             // Key and group route planner
	Isolate         func(context.Context, *apikey.APIKey, int64, string, string) error                                                                          // session group ownership checker
	ReportSelection func(*gatewayprovider.SelectionResult, int64, string, bool, *int)                                                                           // selected provider outcome reporter
	Stop429         func(*gatewayprovider.ExecutionProvider, int, int, *failover.OAuth429State) bool                                                            // OAuth 429 switch stopping policy
	Credential      func(context.Context, *gin.Context, *gatewayprovider.ExecutionProvider) (string, string, error)                                             // provider request credential resolver
	ResolveRouting  func(context.Context, *int64, *gatewayprovider.ExecutionProvider, string, provider.OpenAIEndpointCapability) (string, error)                // selected provider eligibility resolver
	BeginPreemption func(context.Context, *gin.Context, *gatewayprovider.ExecutionProvider, []byte) (context.Context, func(), bool)                             // session replacement registration callback
	Relay           func(context.Context, *gin.Context, *coderws.Conn, *gatewayprovider.ExecutionProvider, string, []byte, *gatewayws.OpenAIIngressHooks) error // upgraded session execution callback
}

// openAIWSHTTPBackend binds authenticated Gin requests to WS entry services.
type openAIWSHTTPBackend struct {
	bindings Bindings /* retained app service bindings */
}

// openAIWSEntryAdapter supplies entry operations; gateway/ws owns the loop and turn callbacks.
type openAIWSEntryAdapter struct {
	bindings Bindings        // retained app entry dependencies
	c        *gin.Context    // shared upgraded Gin request
	call     ResponsesWSCall // captured upgraded connection inputs
	// key is the original connection authentication reference used by routing and
	// completion. AuthorizeTurn publishes a separate requestKey after current checks.
	key *apikey.APIKey // initial authenticated connection Key
	// requestKey changes only after successful reauthentication and funding checks.
	// AcquireKey prefers this view for later allowance reservations over key.
	requestKey *apikey.APIKey
	subject    authctx.AuthSubject // initial user admission subject
	// subscription is loaded from Gin for initial funding and completion capture.
	// Later reauthorization checks current funding without replacing this retained reference.
	subscription *billing.UserSubscription // initial loaded funding subscription
	log          *zap.Logger               // current session diagnostic logger
}

// openAIWSEntryTarget exposes execution steps for the selected provider.
type openAIWSEntryTarget struct {
	// provider and selection identify the same scheduler attempt created by Select.
	// root retains the shared connection inputs; feedback and completion use this
	// target so another provider's record cannot be substituted for its outcome.
	root      *openAIWSEntryAdapter              // owning connection entry adapter
	provider  *gatewayprovider.ExecutionProvider // selected provider execution target
	selection *gatewayprovider.SelectionResult   // matching scheduler selection data
	// token changes only after Credential succeeds. A failed resolution leaves it
	// unchanged, and entry stops before Run can use that previous credential.
	token string // last successfully resolved credential
}

// wsEntryLogger holds a logger so background completion callbacks do not retain the HTTP request.
type wsEntryLogger struct {
	log *zap.Logger /* retained structured session logger */
}

// New constructs a Responses WS handler using the supplied entry bindings.
// Common.Support.Concurrency must be available at construction; the handler
// creates ingress leases and upgraded transports only when serving requests.
func New(options ResponsesWSOptions, b Bindings) *ResponsesWSHandler {
	return NewResponsesWSHandler(options, openAIWSHTTPBackend{bindings: b}, b.Common.Support.Concurrency)
}

// Access builds the connection's entry Key view from the
// middleware-authenticated API Key. Missing authentication returns false;
// model mappings and reasoning mapping slice storage are copied, while scalar
// values and the GroupID pointer are retained. RefreshFastPolicy records
// whether later secret lookup is possible without performing that lookup.
//
// The gateway core retains this view for the connection, so copying mutable
// map and slice storage prevents its tool-model or effort state from aliasing
// those collections in the authenticated Key. Group data is projected only
// when a Group exists, and callers keep the shared GroupID target stable while
// the connection runs.
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
		RefreshFastPolicy: p.bindings.Keys != nil && strings.TrimSpace(key.Key) != "",
	}
	if key.Group != nil {
		out.Group = &gatewayws.EntryGroup{
			MaxReasoningEffort:          key.Group.MaxReasoningEffort,
			MaxReasoningEffortOverLimit: key.Group.MaxReasoningEffortOverLimit,
			ReasoningEffortMappings:     append([]routing.ReasoningEffortMapping(nil), key.Group.ReasoningEffortMappings...),
			ImagesAllowed:               routing.GroupAllowsResponsesImages(key.Group),
		}
	}
	return out, true
}

// Transport records the WebSocket transport label for the current Gin request.
func (p openAIWSHTTPBackend) Transport(c *gin.Context) {
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportWS)
}

// Error writes an OpenAI HTTP failure using the supplied status, kind and
// message.
func (p openAIWSHTTPBackend) Error(c *gin.Context, status int, kind, message string) {
	gatewayhttp.DefaultOpenAIErrorOutput().WriteError(c, status, kind, message)
}

// Dependencies checks bound OpenAI entry services and writes any availability
// failure. False tells the handler to return before upgrading or acquiring
// ingress capacity; log receives the dependency diagnostic.
func (p openAIWSHTTPBackend) Dependencies(c *gin.Context, log *zap.Logger) bool {
	return p.bindings.Dependencies.Ensure(c, log)
}

// SummarizeRead returns HTTP-compatible close labels for a first-frame read
// error. Gateway ClientCloseError values are converted to the HTTP close type
// before summarization so their chosen status, reason and cause remain
// visible.
func (p openAIWSHTTPBackend) SummarizeRead(err error) (string, string) {
	if closed, ok := errors.AsType[*gatewayws.ClientCloseError](err); ok {
		err = gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusCode(closed.Status), closed.Reason, closed.Cause)
	}
	return gatewayhttp.SummarizeWSCloseErrorForLog(err)
}

// Entry binds session operations to the middleware Key and subject plus the
// upgraded call. The handler has already verified those context values; the
// adapter retains their references and uses the call's logger for entry
// diagnostics.
func (p openAIWSHTTPBackend) Entry(c *gin.Context, call ResponsesWSCall) gatewayws.EntryPorts {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subject, _ := authctx.GetAuthSubjectFromContext(c)
	return &openAIWSEntryAdapter{bindings: p.bindings, c: c, call: call, key: key, subject: subject, log: call.Logger}
}

// Logger returns the entry's current structured logger through the gateway
// interface.
func (p *openAIWSEntryAdapter) Logger() gatewayws.EntryLogger { return wsEntryLogger{p.log} }

// SetLogger replaces the entry logger with a wsEntryLogger returned by its
// logger adapter. Other implementations panic because this internal path needs
// the concrete retained zap logger for HTTP callbacks and completion error
// capture.
func (p *openAIWSEntryAdapter) SetLogger(log gatewayws.EntryLogger) {
	// Entry's With method returns the concrete logger needed by HTTP callbacks;
	// reject another implementation before replacing that retained diagnostic state.
	typed, ok := log.(wsEntryLogger)
	if !ok {
		panic("unexpected websocket entry logger type")
	}
	p.log = typed.log
}

// ClassifyPrevious labels the supplied continuation identifier using OpenAI
// protocol spelling rules.
func (p *openAIWSEntryAdapter) ClassifyPrevious(id string) string {
	return openai.ClassifyOpenAIPreviousResponseIDKind(id)
}

// Platform resolves the compatible request platform from the connection's
// original Key.
func (p *openAIWSEntryAdapter) Platform() string {
	return gatewayhttp.OpenAICompatibleRequestPlatform(p.key)
}

// Prompt applies configured user prompt replacement to a Responses request
// body.
func (p *openAIWSEntryAdapter) Prompt(ctx context.Context, body []byte) []byte {
	return p.bindings.Prompt.ApplyUserPromptReplacementToBody(ctx, body, "openai_responses")
}

// Redirect applies the connection Key's model redirect and returns its trace
// context.
func (p *openAIWSEntryAdapter) Redirect(ctx context.Context, model string) (context.Context, string) {
	return gatewayhttp.APIKeyModelRedirectContext(ctx, p.key, model)
}

// BindContext publishes the gateway control context to the shared Gin request.
func (p *openAIWSEntryAdapter) BindContext(ctx context.Context) {
	p.c.Request = p.c.Request.WithContext(ctx)
}

// ObserveFirst records the initial model, streaming mode and WS request type
// for Ops.
func (p *openAIWSEntryAdapter) ObserveFirst(model string) {
	gatewayhttp.SetOpsRequestContext(p.c, model, true)
	gatewayhttp.SetOpsEndpointContext(p.c, "", int16(usage.RequestTypeWSV2))
}

// CaptureCyber snapshots the current Responses prompt and moderation input for
// turn warnings. It updates the Gin observation first, then returns the
// captured excerpt and input that entry retains across provider work.
func (p *openAIWSEntryAdapter) CaptureCyber(body []byte) gatewayws.EntryCyberSnapshot {
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(p.c, moderation.ContentModerationProtocolOpenAIResponses, body)
	return gatewayws.EntryCyberSnapshot{Excerpt: gatewayhttp.CurrentOpenAICyberWarningPromptExcerpt(p.c), Input: gatewayhttp.CurrentOpenAICyberWarningSnapshot(p.c)}
}

// Moderate runs Responses content moderation using the shared Gin request
// context and original Key. The supplied context parameter is unused; the
// adapter's logger, authenticated subject and moderation service supply the
// request observations and decision.
func (p *openAIWSEntryAdapter) Moderate(_ context.Context, model string, body []byte) *moderation.Decision {
	return gatewayhttp.RunContentModeration(gatewayhttp.GatewayModerationEndpoints{}, p.c, p.log, p.bindings.Common.Support.Moderation, p.key, p.subject, moderation.ContentModerationProtocolOpenAIResponses, model, body)
}

// ModerationError writes the moderation decision as a Responses WS client
// rejection.
func (p *openAIWSEntryAdapter) ModerationError(ctx context.Context, d *moderation.Decision) {
	gatewayhttp.WriteResponsesWSModeration(ctx, p.call.Conn, d)
}

// BlockedSession returns the blocked Cyber session matched by this Key and
// request body. Missing block storage or an unmatched session returns empty,
// which lets entry continue its ordinary admission checks.
func (p *openAIWSEntryAdapter) BlockedSession(ctx context.Context, body []byte) string {
	return gatewayhttp.FindBlockedCyberSession(ctx, p.bindings.Blocks, p.key.ID, p.c, body)
}

// BlockedError writes the fixed Cyber session-blocked message to the upgraded
// client.
func (p *openAIWSEntryAdapter) BlockedError(ctx context.Context) {
	gatewayhttp.WriteResponsesWSCyberBlocked(ctx, p.call.Conn, moderationflow.SessionBlockedClientMessage)
}

// BlockedMessage returns the client explanation for an already blocked Cyber
// session.
func (p *openAIWSEntryAdapter) BlockedMessage() string {
	return moderationflow.SessionBlockedClientMessage
}

// BlockedOps enqueues the model and matched session key for Cyber observation.
func (p *openAIWSEntryAdapter) BlockedOps(model, key string) {
	p.bindings.Common.Support.Cyber.EnqueueBlocked(p.c, p.key, model, key)
}

// Plan computes a Key and group route and returns its model mapping. The
// returned context carries that same plan so selected-provider eligibility and
// completion tracing read the route chosen for this request.
func (p *openAIWSEntryAdapter) Plan(ctx context.Context, model string) (context.Context, routing.GroupMappingResult) {
	plan := p.bindings.PlanRoute(ctx, p.key, model)
	return requeststate.WithRoutePlan(ctx, plan), plan.Mapping()
}

// ImageIntent derives the group-mapped inspection body, model and broad image
// intent. It passes the request platform and bound model replacement operation
// to the shared HTTP helper so image checks use the group mapping
// consistently.
func (p *openAIWSEntryAdapter) ImageIntent(model string, body []byte, mapping routing.GroupMappingResult) ([]byte, string, bool) {
	return gatewayhttp.GroupMappedImageIntent("/v1/responses", model, body, mapping, p.Platform(), p.bindings.Common.Forward.ReplaceModelInBody)
}

// ExplicitImage reports explicit image generation intent in the supplied model
// and body.
func (p *openAIWSEntryAdapter) ExplicitImage(model string, body []byte) bool {
	return gatewayprovider.ImageIntent().IsExplicitImageGenerationIntent("/v1/responses", model, body)
}

// ImageContext marks broad OpenAI image generation intent on the request
// context.
func (p *openAIWSEntryAdapter) ImageContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIImageGenerationIntent(ctx)
}

// ImagesAllowed reports the connection group's Responses image gate. A nil
// group returns true; otherwise the shared helper accepts a nonempty
// ResponsesImagePolicy or the legacy AllowImageGeneration flag.
func (p *openAIWSEntryAdapter) ImagesAllowed() bool {
	return routing.GroupAllowsResponsesImages(p.key.Group)
}

// ImageDeniedMessage returns the standard explanation for denied image
// generation permission.
func (p *openAIWSEntryAdapter) ImageDeniedMessage() string {
	return gatewaymedia.ImageGenerationPermissionMessage
}

// PolicyDenied marks a local policy refusal in the Gin request's Ops
// observations.
func (p *openAIWSEntryAdapter) PolicyDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}

// FeatureDenied marks a local feature gate refusal in the Gin request's Ops
// observations.
func (p *openAIWSEntryAdapter) FeatureDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
}

// AcquireKey reserves the turn's Key allowance and returns its admitted
// context and release callback. It prefers the last successfully authorized
// requestKey, otherwise the initial key. Entry owns release after this result
// and retains it across provider retries. A Token rate ceiling violation closes
// with policy status 1008; missing dependencies or unavailable verification use
// 1011, and exhausted concurrency or RPM capacity uses retry status 1013.
func (p *openAIWSEntryAdapter) AcquireKey(ctx context.Context) (context.Context, func(), error) {
	if p.bindings.Keys == nil {
		return ctx, nil, p.CloseError(1011, "API key request limiter unavailable", apikey.ErrKeyLimiterUnavailable)
	}
	key := p.requestKey
	if key == nil {
		key = p.key
	}
	admitted, release, _, err := p.bindings.Keys.AcquireRequest(ctx, key)
	if err != nil {
		status := 1011
		message := err.Error()
		if errors.Is(err, apikey.ErrTokenRateLimitExceeded) {
			status = 1008
			message = apperror.Message(err)
		}
		if errors.Is(err, apikey.ErrTokenRateUnavailable) {
			message = apikey.ErrTokenRateUnavailable.Message
		}
		if errors.Is(err, apikey.ErrKeyConcurrencyExceeded) || errors.Is(err, apikey.ErrKeyRPMExceeded) {
			status = 1013
		}
		return ctx, nil, p.CloseError(status, message, err)
	}
	return admitted, release, nil
}

// AcquireUser attempts the initial subject's user slot for this connection
// Key. Its release callback belongs to the gateway turn lifecycle, which
// returns the slot after that turn finishes or is rejected.
func (p *openAIWSEntryAdapter) AcquireUser(ctx context.Context) (func(), bool, error) {
	return p.bindings.Common.Support.Concurrency.TryAcquireUserSlotForAPIKey(ctx, p.subject.UserID, p.subject.Concurrency, p.key.ID)
}

// AcquireProvider attempts the supplied provider's concurrent request slot
// immediately. The entry loop owns its release callback and uses the acquired
// flag to distinguish capacity rejection from a storage or lease error.
func (p *openAIWSEntryAdapter) AcquireProvider(ctx context.Context, id int64, limit int) (func(), bool, error) {
	return p.bindings.Common.Support.Concurrency.TryAcquireProviderSlot(ctx, id, limit)
}

// WrapRelease makes a slot callback release on control-context cancellation as
// well as explicit cleanup. The scheduler wrapper coordinates repeated calls
// so entry's normal turn release and cancellation can use the same callback.
func (p *openAIWSEntryAdapter) WrapRelease(ctx context.Context, release func()) func() {
	return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, release)
}

// LoadSubscription retains the subscription already bound to the Gin request
// for completion. An absent context subscription sets nil; AuthorizeTurn reads
// current funding separately and leaves this retained reference in place.
func (p *openAIWSEntryAdapter) LoadSubscription() {
	p.subscription, _ = gatewayhttp.SubscriptionFromContext(p.c)
}

// Eligibility checks initial funding with the retained Key and subscription.
// It supplies an empty model and false after-wait flag; current later-turn
// funding checks are performed separately by AuthorizeTurn before Key
// allowance admission.
func (p *openAIWSEntryAdapter) Eligibility(ctx context.Context) error {
	return p.bindings.CheckFunding(ctx, p.key, p.subscription, "", false)
}

// AuthorizeTurn verifies current Key identity, WS group permission and funding
// before publishing requestKey for later allowance reservation. Missing
// authentication or funding services fail closed. Reauthentication receives
// the client IP and member-limit check flag; revoked protocol permission stops
// before subscription resolution or funding checks.
//
// Current funding is resolved from the reauthenticated Key and checked before
// requestKey changes. Any failure leaves the previously published requestKey
// available but causes entry to reject the turn. The adapter retains its
// initial key and subscription for routing and completion, so this operation
// updates request allowance inputs separately from those connection
// references.
func (p *openAIWSEntryAdapter) AuthorizeTurn(ctx context.Context) error {
	if p.bindings.Keys == nil || p.bindings.CheckFunding == nil {
		return apikey.ErrAuthenticationStopped
	}
	key, err := p.bindings.Keys.Reauthenticate(ctx, p.key, apikey.AuthenticationInput{
		ClientIP: p.call.ClientIP, CheckMemberLimits: true,
	})
	if err != nil {
		return err
	}
	if key == nil || key.Group == nil || !key.Group.AllowsClientProtocol(protocol.ProtocolResponsesWebSocket) {
		return errors.New("responses websocket protocol is not allowed by group")
	}
	funding, err := admission.ResolveFundingFromKey(ctx, key, p.bindings.Subscriptions, true)
	if err != nil {
		return err
	}
	if err := p.bindings.CheckFunding(ctx, key, funding.Subscription, "", false); err != nil {
		return err
	}
	p.requestKey = key
	return nil
}

// SessionHash derives request affinity from normal explicit session and content
// signals before using the supplied fallback seed. When both normal lookup and
// the trimmed seed are empty, it returns an empty hash.
func (p *openAIWSEntryAdapter) SessionHash(body []byte, seed string) string {
	return gatewayhttp.GenerateOpenAISessionHashWithFallback(p.c, body, seed)
}

// ExplicitHash derives affinity from client-provided session signals for this
// body. An absent usable session returns empty; a successful derivation also
// attaches the corresponding legacy hash to Gin for compatibility lookup.
func (p *openAIWSEntryAdapter) ExplicitHash(body []byte) string {
	return gatewayhttp.GenerateExplicitOpenAISessionHash(p.c, body)
}

// Isolate checks first-group ownership using the original Key and
// authenticated subject.
func (p *openAIWSEntryAdapter) Isolate(ctx context.Context, source, hash string) error {
	return p.bindings.Isolate(ctx, p.key, p.subject.UserID, source, hash)
}

// IsolationError presents an ownership conflict through the Responses WS error
// writer.
func (p *openAIWSEntryAdapter) IsolationError(ctx context.Context, err error) {
	gatewayhttp.WriteResponsesWSIsolation(ctx, p.call.Conn, err)
}

// IsolationReason returns the client close reason for an ownership isolation
// error.
func (p *openAIWSEntryAdapter) IsolationReason(err error) string {
	return gatewayhttp.ResponsesWSIsolationCloseReason(err)
}

// Guardian adds the shared HTTP Guardian parent-affinity hint to the request
// context.
func (p *openAIWSEntryAdapter) Guardian(ctx context.Context, body []byte, model string) context.Context {
	return gatewayhttp.WithOpenAIGuardianParentAffinity(ctx, p.c, body, model)
}

// Select asks the compatible scheduler for a WS ingress provider and exposes
// its slot and wait outcome to RunEntry. The responses flag chooses Responses
// versus general text capability, while previous ID, affinity hash, exclusions
// and platform constrain candidates. A missing selection retains the scheduler
// decision and error without constructing a target.
//
// A selected provider yields both a credential-free EntryProvider snapshot and
// an openAIWSEntryTarget retaining that exact execution provider and
// selection. Keeping them together lets admission use the snapshot while
// credential, model, feedback and completion operations use the corresponding
// selected attempt. Existing slot release and wait-plan values remain owned by
// the entry loop.
func (p *openAIWSEntryAdapter) Select(ctx context.Context, previous, hash, model string, excluded map[int64]struct{}, responses, move bool, platform string) (*gatewayws.EntrySelection, gatewayws.EntryDecision, error) {
	capability := provider.OpenAIEndpointCapabilityTextGeneration
	if responses {
		capability = provider.OpenAIEndpointCapabilityResponses
	}
	selection, decision, err := p.bindings.Common.Selection.SelectProviderWithSchedulerForCapability(ctx, p.key.GroupID, previous, hash, model, excluded, egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress, capability, false, move, platform)
	d := gatewayws.EntryDecision{Layer: decision.Layer, CandidateCount: decision.CandidateCount, StickyPreviousHit: decision.StickyPreviousHit}
	if selection == nil {
		return nil, d, err
	}
	out := &gatewayws.EntrySelection{Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc, WaitPlan: selection.WaitPlan}
	if a := selection.Provider; a != nil {
		out.Provider = &gatewayws.EntryProvider{
			ProviderSnapshot: provider.ProviderSnapshot{ID: a.Record.ID, Platform: a.Record.Platform, Type: a.Record.Type, Concurrency: a.Record.Concurrency, RequestIDHeader: provider.UpstreamRequestIDHeaderName(a.View())},
			Name:             a.Record.Name,
			Shadow:           a.View().IsShadow(),
		}
		out.Target = &openAIWSEntryTarget{root: p, provider: a, selection: selection}
	}
	return out, d, err
}

// SelectionLogError returns the platform-compatible scheduling error used by
// entry diagnostics.
func (p *openAIWSEntryAdapter) SelectionLogError(err error, platform string) error {
	return gatewayhttp.OpenAICompatibleSelectionErrorForLog(err, platform)
}

// BindSticky binds the session hash to the selected provider for this Key
// group. A supplied callback takes precedence; otherwise the shared attempt
// Sticky service performs the binding and returns its storage error.
func (p *openAIWSEntryAdapter) BindSticky(ctx context.Context, hash string, id int64) error {
	// Tests and app can provide a direct sticky binder; otherwise use the shared
	// attempt service so this connection participates in the same affinity storage.
	if p.bindings.BindSticky != nil {
		return p.bindings.BindSticky(ctx, p.key.GroupID, hash, id)
	}
	return p.bindings.Common.Support.Sticky.BindStickySession(ctx, p.key.GroupID, hash, id)
}

// RefreshFast loads current Key policy by its authenticated secret and returns
// normalized Fast policy with its usable flag. Lookup error or absent Key
// returns false so the core keeps its last accepted policy; callers require
// Keys when enabling refresh.
func (p *openAIWSEntryAdapter) RefreshFast(ctx context.Context) (string, bool) {
	key, err := p.bindings.Keys.GetByKey(ctx, p.key.Key)
	// Failed refresh supplies no replacement policy; entry retains its last
	// usable Fast value instead of admitting a turn under an invented policy.
	if err != nil || key == nil {
		return "", false
	}
	return apikey.NormalizeAPIKeyFastModePolicy(key.FastModePolicy)
}

// Failover extracts a typed upstream failure into entry's feedback and retry
// decisions. Unrecognized errors return false; recognized failures retain the
// same error and report flags used before releasing or replacing the selected
// provider.
func (p *openAIWSEntryAdapter) Failover(err error) (*gatewayws.EntryFailure, bool) {
	var failure *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failure) || failure == nil {
		return nil, false
	}
	return &gatewayws.EntryFailure{Err: failure, StatusCode: failure.StatusCode, ReportScheduleFailure: failure.ShouldReportProviderScheduleFailure(), RetryNext: failure.ShouldRetryNextProvider()}, true
}

// CloseFailover translates a typed upstream failure into Responses WS client
// presentation. Missing or unrecognized failure data passes a nil presentation
// to the HTTP helper, which owns Ops failure marking and the selected close
// response.
func (p *openAIWSEntryAdapter) CloseFailover(failure *gatewayws.EntryFailure) {
	var old *forwardcore.UpstreamFailoverError
	if failure != nil {
		_ = errors.As(failure.Err, &old)
	}
	gatewayhttp.CloseResponsesWSFailure(p.c, p.call.Conn, FailoverPresentation(old), gatewayhttp.MarkOpsStreamFailure)
}

// Close sends the supplied WebSocket status and reason through the HTTP close
// helper.
func (p *openAIWSEntryAdapter) Close(status int, reason string) {
	gatewayhttp.CloseResponsesWS(p.call.Conn, coderws.StatusCode(status), reason)
}

// CloseError constructs the HTTP adapter's typed WS close instruction with its
// cause.
func (p *openAIWSEntryAdapter) CloseError(status int, reason string, err error) error {
	return gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusCode(status), reason, err)
}

// CloseInfo extracts the HTTP adapter's recognized close status and reason for
// entry.
func (p *openAIWSEntryAdapter) CloseInfo(err error) gatewayws.EntryClose {
	return gatewayhttp.ResponsesWSCloseInfo(err)
}

// SessionPreempted recognizes the gateway's scoped session replacement error
// through its chain.
func (p *openAIWSEntryAdapter) SessionPreempted(err error) bool {
	return gatewayws.IsSessionPreemptedError(err)
}

// RemovePrevious returns request bytes with the continuation response ID
// removed. The protocol helper determines whether mutation succeeds; callers
// use this for movable first-frame continuation before selecting a replacement
// provider.
func (p *openAIWSEntryAdapter) RemovePrevious(body []byte) []byte {
	return openai.RemovePreviousResponseIDFromBody(body)
}

// LocalPolicyError recognizes reasoning ceiling and model capability rejections
// in the supplied error chain, including failures returned before upstream execution.
func (p *openAIWSEntryAdapter) LocalPolicyError(err error) bool {
	var policy *routing.ReasoningEffortOverLimitError
	return routing.IsModelAttributeError(err) || errors.As(err, &policy)
}

// ReportFailure reports whether gateway entry treats the error as provider
// scheduling failure.
func (p *openAIWSEntryAdapter) ReportFailure(err error) bool {
	return gatewayws.EntryShouldReportFailure(err)
}

// EndedByClient classifies this error and its recognized close instruction as
// client termination.
func (p *openAIWSEntryAdapter) EndedByClient(err error) bool {
	return gatewayhttp.ResponsesWSEndedByClient(err, p.CloseInfo(err))
}

// ClearCyber clears the completed turn's Cyber observations on the shared Gin
// request.
func (p *openAIWSEntryAdapter) ClearCyber() {
	gatewayhttp.ClearCyberTurnState(p.c, gatewayhttp.ClearOpsCyberPolicy)
}

// CompletionRecorder returns the shared recorder used to settle accepted turn
// snapshots.
func (p *openAIWSEntryAdapter) CompletionRecorder() gatewayws.EntryCompletion {
	return p.bindings.Common.Recorder
}

// CompletionObserver captures the current zap logger for background completion
// failures. The returned callback records provider and request identity with
// its error, so later Gin request mutations are not needed by that diagnostic.
func (p *openAIWSEntryAdapter) CompletionObserver() func(int64, string, error) {
	log := p.log // current session diagnostic logger
	return func(id int64, requestID string, err error) {
		log.Error("openai.websocket_record_usage_failed", zap.Int64("provider_id", id), zap.String("request_id", requestID), zap.Error(err))
	}
}

// SubmitCompletion queues the supplied turn task through the shared
// image-aware submission service. A nil result supplies zero images; nonnil
// results supply ImageCount, while the service owns its background or overflow
// execution policy.
func (p *openAIWSEntryAdapter) SubmitCompletion(source context.Context, result *gatewayws.ForwardResult, task func(context.Context)) {
	images := 0
	if result != nil {
		images = result.ImageCount
	}
	p.bindings.Common.Support.Submission.SubmitImages(source, images, task)
}

// MappedModel resolves the selected provider's mapped model for entry
// feedback.
func (t *openAIWSEntryTarget) MappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(t.provider).Mapped(model)
}

// Report submits the selected provider outcome and optional TTFT
// observation to the bound reporter.
func (t *openAIWSEntryTarget) Report(model string, ok bool, first *int) {
	t.root.bindings.ReportSelection(t.selection, t.provider.Record.ID, model, ok, first)
}

// Switched records this selection's provider replacement in the shared
// scheduler statistics.
func (t *openAIWSEntryTarget) Switched() {
	t.root.bindings.Common.Selection.RecordOpenAIProviderSwitchForSelection(t.selection)
}

// Stop429 asks the bound OAuth policy whether status and switch history end
// provider replacement.
func (t *openAIWSEntryTarget) Stop429(status, count int, state *failover.OAuth429State) bool {
	return t.root.bindings.Stop429(t.provider, status, count, state)
}

// Credential resolves request credentials for the selected provider and
// retains the token on success. A resolution error leaves the prior token
// unchanged; entry returns the error before invoking the target's session Run.
func (t *openAIWSEntryTarget) Credential(ctx context.Context) error {
	token, _, err := t.root.bindings.Credential(ctx, t.root.c, t.provider)
	if err == nil {
		t.token = token
	}
	return err
}

// EnforceClient evaluates the selected provider's client restrictions using
// its request TLS-router match. It passes the first body and authenticated Gin
// request to the shared forward helper before WS execution begins.
func (t *openAIWSEntryTarget) EnforceClient(ctx context.Context, first []byte) error {
	router := t.root.bindings.Common.Forward.MatchOpenAITLSFingerprintRouterForRequest(t.root.c, t.provider)
	return t.root.bindings.Common.Forward.EnforceOpenAIClientPolicyForRequest(ctx, t.root.c, t.provider, first, router)
}

// ResolveRouting rechecks the selected provider's model capability and then
// verifies pricing for WS ingress. The responses flag chooses the applicable
// endpoint capability. Routing errors return immediately; missing price
// admission marks local policy denial and returns its error before the turn
// can be forwarded.
func (t *openAIWSEntryTarget) ResolveRouting(ctx context.Context, model string, responses bool) (string, error) {
	capability := provider.OpenAIEndpointCapabilityTextGeneration
	if responses {
		capability = provider.OpenAIEndpointCapabilityResponses
	}
	mapped, err := t.root.bindings.ResolveRouting(ctx, t.root.key.GroupID, t.provider, model, capability)
	if err != nil {
		return "", err
	}
	err = gatewayhttp.CheckTextModelPricing(ctx, t.root.bindings.Pricing, t.root.key, t.provider, model, mapped, nil, protocol.ProtocolResponsesWebSocket)
	if err != nil {
		gatewayhttp.MarkOpsClientBusinessLimited(t.root.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		return "", err
	}
	return mapped, nil
}

// Warning records a selected-provider Cyber warning using the captured prompt
// excerpt and input. The supplied context is unused; recording uses the
// retained Gin request, Key and logger plus this target's provider identity.
func (t *openAIWSEntryTarget) Warning(_ context.Context, model string, status int, body []byte, message string, snapshot gatewayws.EntryCyberSnapshot) {
	p := t.root
	p.bindings.Common.Support.RecordOpenAICyberWarningWithSnapshot(p.c, p.log, p.key, t.provider, model, status, body, message, snapshot.Excerpt, snapshot.Input)
}

// RecordMarked handles marked Cyber policy for this target and turn body using
// the retained subscription, Key, pricing mapping and payload hash. True
// reports handling or prior deduplication; the Cyber service sets its handling
// flag before warning persistence and completion dispatch, so it does not
// establish successful storage or task acceptance.
func (t *openAIWSEntryTarget) RecordMarked(_ context.Context, model string, failed bool, body []byte, mapping routing.PricingUsageFields, hash string) bool {
	p := t.root
	return p.bindings.Common.Support.RecordCyberPolicyIfMarked(p.c, p.key, t.provider, p.subscription, model, failed, body, mapping, hash)
}

// UpdateUsage records Codex quota observations for this selected provider from
// response headers.
func (t *openAIWSEntryTarget) UpdateUsage(ctx context.Context, headers map[string][]string) {
	t.root.bindings.Common.Selection.UpdateCodexUsageSnapshotFromHeaders(ctx, t.provider.Record.ID, headers)
}

// PrepareCompletion captures settlement input for the supplied completed turn
// and selected provider. It carries the turn's request ID and request-record
// capture into the Gin-derived completion context, along with Key redirect
// tracing, so a connection ID cannot replace the child's identity. Request
// body bytes are copied before background submission.
//
// The capture uses the initial Key and retained subscription, this target's
// provider record, the supplied group mapping, and the actual upstream model.
// PricingAt comes from this turn's StartedAt. CaptureOpenAI constructs the
// completion snapshot with client endpoint, upstream endpoint, client session
// and network metadata; result must be nonnil because mapping reads its
// UpstreamModel.
func (t *openAIWSEntryTarget) PrepareCompletion(ctx context.Context, result *gatewayws.ForwardResult, capture gatewayws.TurnCapture, model string, mapping routing.GroupMappingResult, body []byte, cyber bool) *completion.Input {
	p := t.root
	legacy := gatewayprovider.ForwardResultFromWS(result)
	// Restore this child request's identity and record before CaptureOpenAI reads
	// them; the shared Gin context still carries the connection-level request ID.
	captureContext := gatewayhttp.PropagateAPIKeyModelRedirectTrace(gatewayhttp.CompletionContext(p.c), ctx)
	if id := telemetry.RequestIDValue(ctx); id != "" {
		captureContext = context.WithValue(captureContext, telemetry.RequestID, id)
	}
	captureContext = telemetry.CopyRequestCapture(captureContext, ctx)
	return gatewayprovider.CaptureOpenAI(captureContext, &gatewayprovider.OpenAICapture{
		Result: legacy, APIKey: p.key, User: p.key.User, Provider: gatewayprovider.ExecutionCompletionRecord(t.provider), Subscription: p.subscription,
		InboundEndpoint: gatewayhttp.GetInboundEndpoint(p.c), UpstreamEndpoint: openaiattempt.ResolveOpenAIUpstreamEndpoint(p.c, t.provider, legacy), UserAgent: p.call.UserAgent, IPAddress: p.call.ClientIP,
		RequestPayloadHash: billing.HashUsageRequestPayload(body), RequestBody: append([]byte(nil), body...), PricingAt: capture.StartedAt, APIKeyService: p.bindings.Common.Support.Quota,
		ClientSessionID: gatewayhttp.ExtractClientSessionID(p.c), PricingUsageFields: mapping.ToUsageFields(model, result.UpstreamModel), CyberBlocked: cyber,
	})
}

// BeginPreemption delegates scoped registration for this provider and first
// client message. The returned cleanup belongs to the entry loop across
// provider attempts, and nested execution adapters reuse an already armed
// context.
func (t *openAIWSEntryTarget) BeginPreemption(ctx context.Context, first []byte) (context.Context, func(), bool) {
	return t.root.bindings.BeginPreemption(ctx, t.root.c, t.provider, first)
}

// Run invokes the bound WS relay with this selected provider and resolved
// token. Client must be the value-form WSClientFrames adapter; an unsupported
// transport returns an error. Hooks is required, and optional AfterTurn
// converts each HTTP forward result back into the gateway capture while
// retaining request, model and replay metadata.
func (t *openAIWSEntryTarget) Run(ctx context.Context, client gatewayws.ClientSocket, first []byte, hooks *gatewayws.EntryHooks) error {
	frames, ok := client.(gatewayhttp.WSClientFrames)
	if !ok {
		return errors.New("unsupported websocket HTTP frame adapter")
	}
	old := &gatewayws.OpenAIIngressHooks{
		ContextForTurn:              hooks.ContextForTurn,
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
			hooks.AfterTurn(gatewayws.TurnCapture{Turn: c.Turn, StartedAt: c.StartedAt, RequestBody: c.RequestBody, OriginalModel: c.OriginalModel, PreviousResponseID: c.PreviousResponseID, Result: gatewayprovider.ProjectWSResult(c.Result), Err: c.Err, PayloadSource: c.PayloadSource})
		}
	}
	return t.root.bindings.Relay(ctx, t.root.c, frames.Conn, t.provider, t.token, first, old)
}

// LogFailure records the selected provider's WS error and summarized close
// labels. Shared proxy fields are appended before writing the warning so
// provider failure diagnostics retain the egress route used by this attempt.
func (t *openAIWSEntryTarget) LogFailure(err error) {
	status, reason := gatewayhttp.SummarizeWSCloseErrorForLog(err)
	fields := []zap.Field{zap.Int64("provider_id", t.provider.Record.ID), zap.Error(err), zap.String("close_status", status), zap.String("close_reason", reason)}
	fields = openaiattempt.AppendOpenAIProviderProxyLogFields(fields, t.provider)
	t.root.log.Warn("openai.websocket_proxy_failed", fields...)
}

// wsEntryLogFields converts gateway entry fields to their zap equivalents for
// the logger adapter. Kind values one through four represent string, int64,
// bool and error; unknown kinds are omitted from the returned slice.
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

// With returns a wsEntryLogger with the supplied gateway fields attached.
func (l wsEntryLogger) With(fields ...gatewayws.EntryField) gatewayws.EntryLogger {
	return wsEntryLogger{l.log.With(wsEntryLogFields(fields)...)}
}

// Info writes an informational entry message with converted structured gateway
// fields.
func (l wsEntryLogger) Info(message string, fields ...gatewayws.EntryField) {
	l.log.Info(message, wsEntryLogFields(fields)...)
}

// Warn writes a warning entry message with converted structured gateway
// fields.
func (l wsEntryLogger) Warn(message string, fields ...gatewayws.EntryField) {
	l.log.Warn(message, wsEntryLogFields(fields)...)
}

// Error writes an error entry message with converted structured gateway
// fields.
func (l wsEntryLogger) Error(message string, fields ...gatewayws.EntryField) {
	l.log.Error(message, wsEntryLogFields(fields)...)
}

// Debug writes a debug entry message with converted structured gateway fields.
func (l wsEntryLogger) Debug(message string, fields ...gatewayws.EntryField) {
	l.log.Debug(message, wsEntryLogFields(fields)...)
}

// FailoverPresentation returns the display fields needed by the WS failure
// writer. Nil errors return nil; nonnil values retain reason and status, mark
// provider-auth stage, and include the shared credential-unavailable client
// message.
func FailoverPresentation(err *forwardcore.UpstreamFailoverError) *gatewayhttp.ResponsesWSFailure {
	if err == nil {
		return nil
	}
	return &gatewayhttp.ResponsesWSFailure{
		Reason:            string(err.Reason),
		StatusCode:        err.StatusCode,
		ProviderAuth:      err.Stage == forwardcore.GatewayFailureStageProviderAuth,
		CredentialMessage: forwardcore.GrokCredentialUnavailableClientMessage,
	}
}
