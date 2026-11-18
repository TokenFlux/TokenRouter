package textattempt

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// bindings.go binds application callbacks and shared services to text attempt
// runtimes.
// Text attempt runtimes share application services while each Open call owns
// its current selection, provider and forwarding result. Binding copies
// callback values into a dependency object, connecting platform attempts to the
// common text loop and synchronous HTTP error output before requests begin.

// ForwardPorts supplies single-attempt platform execution; gateway/text owns
// provider failover.
type ForwardPorts struct {
	// Bedrock client compatibility transformation
	BedrockCompat func(*gin.Context, []byte, string, *gatewaycapture.ExecutionProvider, *int64) []byte
	// Antigravity Messages attempt execution
	ForwardAntigravity func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, []byte, bool) (*forwardcore.MessagesResult, error)
	// Antigravity native Gemini execution
	ForwardAntigravityGemini func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, string, string, bool, []byte, bool, ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error)
	// Gemini Messages conversion execution
	ForwardGemini func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, []byte) (*forwardcore.MessagesResult, error)
	// native Gemini action execution
	ForwardGeminiNative func(ctx context.Context, c *gin.Context, provider *gatewaycapture.ExecutionProvider, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error)
	// native Messages attempt execution
	ForwardMessages func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error)
	// digest-based Gemini affinity persistence
	SaveGeminiSession func(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, providerID int64, oldDigestChain string) error
	// mapped Claude failure presentation
	WriteMappedClaudeError func(*gin.Context, *gatewaycapture.ExecutionProvider, int, string, []byte) error
}

// SelectionPorts supplies shared provider selection, session, and scheduling
// feedback operations.
type SelectionPorts struct {
	// selected-provider switch observation
	ProviderSwitched func(*gatewaycapture.SelectionResult)
	// selected provider affinity persistence
	BindSticky func(context.Context, *int64, string, int64) error
	// successful provider RPM increment
	IncrementRPM func(context.Context, int64) error
	// request session attempt tracker factory
	NewSessionAttempts func() *scheduler.SessionAttempts
	// attempt scheduler feedback callback
	ReportSchedule func(*gatewaycapture.SelectionResult, int64, bool, *forwardcore.MessagesResult)
	// cached sticky provider lookup
	CachedSession func(context.Context, *int64, string) (int64, error)
	// provider selection with credential target
	SelectProvider func(context.Context, *int64, string, string, map[int64]struct{}) (*gatewaycapture.SelectionResult, error)
	// single-provider group eligibility check
	SingleProviderGroup func(context.Context, *int64) bool
	// exhausted failure scheduling penalty
	TempUnschedule func(context.Context, int64, *forwardcore.UpstreamFailoverError)
	// selected provider session tracking
	TrackSession func(*scheduler.SessionAttempts, *gatewaycapture.ExecutionProvider, string)
}

// Bindings fixes runtime dependencies during construction; Open creates
// per-request attempt state.
type Bindings struct {
	Forward   ForwardPorts   // bound platform execution callbacks
	Selection SelectionPorts // bound selection and feedback callbacks
	// authorized replacement-group funding resolver
	ResolveFallback func(context.Context, *apikey.APIKey, int64, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error)
	// model availability failure diagnoser
	Diagnoser routing.ModelAvailabilityDiagnoser
	Quota     gatewaycapture.QuotaUpdater // Key quota update service
	Recorder  *completion.Recorder        // shared usage completion recorder
	// Pricing optionally checks attempt billing models before generation.
	// Nil keeps this admission check disabled while the selected platform
	// execution path remains available.
	Pricing     *admission.ModelPricing
	Concurrency *gatewayhttp.ConcurrencyHelper  // shared HTTP slot management
	Queue       *gatewayhttp.UserMsgQueueHelper // shared serial message queue
	QueueWait   time.Duration                   // queue acquisition timeout duration

	// effective group route planner
	PlanRoute func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	// QueueMode supplies the default when an eligible provider has no queue
	// mode. An absent Queue or ineligible request leaves serial queue
	// handling disabled.
	QueueMode  string
	Errors     *errorpolicy.ErrorPassthroughService // shared final error-rule service
	Submission gatewayhttp.CompletionSubmission     // bound completion task submission
}

// messageExecutionDependencies retains shared application operations read by
// serial text attempt adapters.
type messageExecutionDependencies struct {
	// native Gemini action execution
	forwardGeminiNative func(ctx context.Context, c *gin.Context, provider *gatewaycapture.ExecutionProvider, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error)
	// digest-based Gemini affinity persistence
	saveGeminiSession func(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, providerID int64, oldDigestChain string) error
	// native Gemini exhaustion presentation
	handleGeminiFailoverExhausted func(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError)

	// per-attempt group input preparation
	prepareGatewayAttemptRequest func(context.Context, *requeststate.ParsedRequest, []byte, *apikey.APIKey, string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error)
	// provider target and capacity selection
	selectProvider func(context.Context, *int64, string, string, map[int64]struct{}) (*gatewaycapture.SelectionResult, error)
	// selected provider session tracking
	trackSession func(*scheduler.SessionAttempts, *gatewaycapture.ExecutionProvider, string)
	// request session attempt tracker factory
	newSessionAttempts func() *scheduler.SessionAttempts
	// single-provider group eligibility check
	singleProviderGroup func(context.Context, *int64) bool
	// observed attempt scheduling feedback
	reportSchedule func(*gatewaycapture.SelectionResult, int64, bool, *forwardcore.MessagesResult)
	// successful provider RPM update
	incrementRPM func(context.Context, int64) error
	// selected provider affinity persistence
	bindSticky func(context.Context, *int64, string, int64) error
	// cached sticky provider lookup
	cachedSession func(context.Context, *int64, string) (int64, error)
	// selected provider switch observation
	providerSwitched func(*gatewaycapture.SelectionResult)
	// exhausted failure scheduling penalty
	tempUnschedule func(context.Context, int64, *forwardcore.UpstreamFailoverError)
	// Bedrock client compatibility transformation
	bedrockCompat func(*gin.Context, []byte, string, *gatewaycapture.ExecutionProvider, *int64) []byte
	// native Messages attempt execution
	forwardMessages func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error)
	// Antigravity Messages attempt execution
	forwardAntigravity func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, []byte, bool) (*forwardcore.MessagesResult, error)
	// Gemini Messages conversion execution
	forwardGemini func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, []byte) (*forwardcore.MessagesResult, error)
	// Antigravity native Gemini execution
	forwardAntigravityGemini func(context.Context, *gin.Context, *gatewaycapture.ExecutionProvider, string, string, bool, []byte, bool, ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error)
	// mapped Claude failure presentation
	writeMappedClaudeError func(*gin.Context, *gatewaycapture.ExecutionProvider, int, string, []byte) error
	// authorized replacement-group funding resolver
	resolveFallback func(context.Context, *apikey.APIKey, int64, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error)
	// effective group route planner
	planRoute func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	// model availability failure diagnoser
	diagnoser     routing.ModelAvailabilityDiagnoser
	apiKeyService gatewaycapture.QuotaUpdater // Key quota update service
	// shared usage completion recorder
	recorder *completion.Recorder
	// pricing enables billing-model admission for generation attempts when
	// nonnil. A nil service leaves preflight disabled; the platform still
	// handles its selected action.
	pricing *admission.ModelPricing
	// shared HTTP slot management
	concurrencyHelper *gatewayhttp.ConcurrencyHelper
	// shared serial message queue
	userMsgQueueHelper *gatewayhttp.UserMsgQueueHelper
	messageWaitTimeout time.Duration // message queue wait budget
	// eligible provider queue-mode resolver
	getUserMsgQueueMode func(*gatewaycapture.ExecutionProvider, *requeststate.ParsedRequest) string
	// Messages JSON error presentation
	errorResponse func(*gin.Context, int, string, string)
	// Messages stream-aware error presentation
	handleStreamingAwareError func(*gin.Context, int, string, string, bool)
	// coded Messages stream error presentation
	handleStreamingAwareErrorWithCode func(*gin.Context, int, string, string, string, bool)
	// slot acquisition failure presentation
	handleConcurrencyError func(*gin.Context, error, string, bool)
	// Messages exhausted-attempt error presentation
	handleFailoverExhausted func(*gin.Context, *forwardcore.UpstreamFailoverError, string, bool)
	// Messages exhausted-status error presentation
	handleFailoverExhaustedSimple func(*gin.Context, int, bool)
	// uncommunicated failure output completion
	ensureForwardErrorResponse func(*gin.Context, bool) bool
	// configured completion task submission
	submitUsageRecordTask func(*gin.Context, completion.UsageRecordTask)
}

// New binds application-owned forwarding, selection, recording and HTTP output
// dependencies to a Runtime. Each Open call creates serial request-local state
// while these service references stay shared. Queue mode uses provider settings
// when nonempty, otherwise the configured binding default; callers configure
// every operation used by their selected entry kind.
func New(b Bindings) *Runtime {
	output := gatewayhttp.MessagesErrorOutput{Rules: b.Errors}
	d := &messageExecutionDependencies{
		bedrockCompat:                     b.Forward.BedrockCompat,
		forwardAntigravity:                b.Forward.ForwardAntigravity,
		forwardAntigravityGemini:          b.Forward.ForwardAntigravityGemini,
		forwardGemini:                     b.Forward.ForwardGemini,
		forwardGeminiNative:               b.Forward.ForwardGeminiNative,
		forwardMessages:                   b.Forward.ForwardMessages,
		saveGeminiSession:                 b.Forward.SaveGeminiSession,
		writeMappedClaudeError:            b.Forward.WriteMappedClaudeError,
		providerSwitched:                  b.Selection.ProviderSwitched,
		bindSticky:                        b.Selection.BindSticky,
		incrementRPM:                      b.Selection.IncrementRPM,
		newSessionAttempts:                b.Selection.NewSessionAttempts,
		reportSchedule:                    b.Selection.ReportSchedule,
		cachedSession:                     b.Selection.CachedSession,
		selectProvider:                    b.Selection.SelectProvider,
		singleProviderGroup:               b.Selection.SingleProviderGroup,
		tempUnschedule:                    b.Selection.TempUnschedule,
		trackSession:                      b.Selection.TrackSession,
		resolveFallback:                   b.ResolveFallback,
		planRoute:                         b.PlanRoute,
		diagnoser:                         b.Diagnoser,
		apiKeyService:                     b.Quota,
		recorder:                          b.Recorder,
		pricing:                           b.Pricing,
		concurrencyHelper:                 b.Concurrency,
		userMsgQueueHelper:                b.Queue,
		messageWaitTimeout:                b.QueueWait,
		handleGeminiFailoverExhausted:     output.GeminiExhausted,
		errorResponse:                     output.Error,
		handleStreamingAwareError:         output.StreamError,
		handleStreamingAwareErrorWithCode: output.StreamErrorWithCode,
		handleConcurrencyError:            output.ConcurrencyError,
		handleFailoverExhausted:           output.Exhausted,
		handleFailoverExhaustedSimple:     output.ExhaustedStatus,
		ensureForwardErrorResponse:        output.EnsureResponse,
		submitUsageRecordTask:             b.Submission.Submit,
		prepareGatewayAttemptRequest: func(ctx context.Context, parsed *requeststate.ParsedRequest, body []byte, key *apikey.APIKey, model string) (*requeststate.ParsedRequest, routing.GroupMappingResult, error) {
			return gatewayhttp.PrepareGroupAttempt(ctx, parsed, body, key, model, b.PlanRoute)
		},
		getUserMsgQueueMode: func(value *gatewaycapture.ExecutionProvider, parsed *requeststate.ParsedRequest) string {
			if b.Queue == nil || !value.View().IsAnthropicOAuthOrSetupToken() || !requeststate.IsRealUserMessage(parsed) {
				return ""
			}
			mode := gatewaycapture.ExecutionRuntimeConfig(value).GetUserMsgQueueMode()
			if mode == "" {
				mode = b.QueueMode
			}
			return mode
		},
	}
	return &Runtime{dependencies: d}
}

// binding returns the shared dependencies retained by this request attempt.
func (b *messageAttemptBridge) binding() *messageExecutionDependencies { return b.fixed }
