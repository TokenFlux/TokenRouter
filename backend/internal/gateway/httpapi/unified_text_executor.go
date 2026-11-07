package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// unified_text_executor.go dispatches compatible text requests to platform
// executors and converts native results for completion.
// Compatible text entry points can execute providers whose native token
// accounting differs from OpenAI total input. Shared preparation checks pricing
// and reasoning before dispatching the selected platform; conversion retains
// native usage alongside the common result so completion can apply the
// appropriate cache interpretation.
//
// Native Messages also needs local interception and serial message ownership
// before platform forwarding. Qoder credential recovery stays within the same
// target when its exchange returns nil result and leaves writer size unchanged.
// Header-only commitment without recorded heartbeats blocks recovery. Native result
// conversion copies token buckets but retains mutable metadata references, so
// completion capture creates its own recording input before later request state
// can change.

// UnifiedTextExecutor executes one exchange on the selected provider; the caller owns selection and failover.
type UnifiedTextExecutor struct {
	// Pricing optionally checks the effective billing model for every selected
	// text attempt. Nil skips this preflight, allowing execution to use the
	// configured platform binding directly.
	Pricing *admission.ModelPricing
	// OpenAI-compatible single exchange executor
	OpenAI      *OpenAIResponsesExecutor
	Anthropic   *MessagesExecutor            // native Messages and conversion executor
	Gemini      *GeminiExecutor              // native Gemini and conversion executor
	Antigravity *AntigravityExecutor         // Antigravity text conversion executor
	Qoder       *gatewayadapter.QoderRuntime // native Qoder exchange runtime
	// QoderRefresh optionally recovers eligible credential failures when the first
	// exchange returns nil result and leaves writer size unchanged. Nil disables
	// recovery and retains the first forwarding error.
	QoderRefresh *provideradapter.QoderRequestRefresh
	// MessageQueue optionally serializes or spaces real user messages on eligible
	// Anthropic targets. Nil omits queue handling while native client detection
	// and compatibility still run.
	MessageQueue NativeMessageQueue
	// MessageQueueMode is the default when an eligible provider has no explicit
	// mode. A provider mode takes precedence, and unsupported modes perform no
	// queue action.
	MessageQueueMode string
	MessageQueueWait time.Duration // native message queue wait budget
}

// EnforceClient installs the supplied execution context and checks
// OpenAI-compatible client policy. Other provider kinds proceed with no
// client-policy call here; callers provide the compatible request binding
// before eligible execution begins.
func (e *UnifiedTextExecutor) EnforceClient(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, match egress.TLSFingerprintRouterMatchResult) error {
	c.Request = c.Request.WithContext(ctx)
	if target.View().IsOpenAICompatible() {
		return e.OpenAI.Requests.EnforceClient(ctx, c, target, body, match)
	}
	return nil
}

// Responses executes one selected exchange after pricing and reasoning
// preparation. It installs the supplied context in Gin, then uses any
// preparation-updated context for compatible or native dispatch; provider
// selection, switching and completion remain with its caller.
func (e *UnifiedTextExecutor) Responses(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte) (result *forward.OpenAIResult, failure error) {
	defer func() { captureTextPerformance(c.Request.Context(), result, failure) }()
	c.Request = c.Request.WithContext(ctx)
	var err error
	target, body, err = e.prepare(c, target, body, protocol.ProtocolOpenAIResponses)
	if err != nil {
		return nil, err
	}
	ctx = c.Request.Context()
	if usesOpenAIExecutor(target) {
		return e.OpenAI.Forward(ctx, c, target, body)
	}
	return e.native(ctx, c, target, body, protocol.ProtocolOpenAIResponses)
}

// Messages clears the current interception marker and executes one selected
// exchange after common preparation. Compatible targets receive cache/model and
// optional TLS inputs; native targets use native preparation and return nil
// result for handled local probes.
func (e *UnifiedTextExecutor) Messages(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, cacheKey, model string, tls ...egress.TLSFingerprintRouterMatchResult) (result *forward.OpenAIResult, failure error) {
	defer func() { captureTextPerformance(c.Request.Context(), result, failure) }()
	c.Request = c.Request.WithContext(ctx)
	c.Set(nativeMessageInterceptedKey, false)
	var err error
	target, body, err = e.prepare(c, target, body, protocol.ProtocolAnthropicMessages)
	if err != nil {
		return nil, err
	}
	ctx = c.Request.Context()
	if usesOpenAIExecutor(target) {
		return e.OpenAI.Text.Messages(ctx, c, target, body, cacheKey, model, tls...)
	}
	return e.native(ctx, c, target, body, protocol.ProtocolAnthropicMessages)
}

// Chat executes one selected exchange after pricing and reasoning preparation
// in the supplied context. Compatible targets receive cache/model and optional
// TLS inputs, while native targets use their platform bindings; callers own
// selection and completion.
func (e *UnifiedTextExecutor) Chat(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, cacheKey, model string, tls ...egress.TLSFingerprintRouterMatchResult) (result *forward.OpenAIResult, failure error) {
	defer func() { captureTextPerformance(c.Request.Context(), result, failure) }()
	c.Request = c.Request.WithContext(ctx)
	var err error
	target, body, err = e.prepare(c, target, body, protocol.ProtocolOpenAIChatCompletions)
	if err != nil {
		return nil, err
	}
	ctx = c.Request.Context()
	if usesOpenAIExecutor(target) {
		return e.OpenAI.Text.Chat(ctx, c, target, body, cacheKey, model, tls...)
	}
	return e.native(ctx, c, target, body, protocol.ProtocolOpenAIChatCompletions)
}

// prepare resolves the attempt route and records actual selected-provider
// attribution before checking price and platform reasoning policy. It returns
// the routed target, whose upstream protocol selects the executor. Anthropic
// and OpenAI policy failures write local permission output in the inbound
// format; other platforms retain the supplied bytes after pricing, and caller
// context changes remain in Gin.
func (e *UnifiedTextExecutor) prepare(c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, source protocol.ProtocolID) (*gatewayadapter.ExecutionProvider, []byte, error) {
	target, err := gatewayadapter.ProviderForProtocolAttempt(c.Request.Context(), target)
	if err != nil {
		return nil, nil, err
	}
	// Failover can move to a provider served by the other executor family.
	// Clear both observation slots so a failed attempt cannot attribute its
	// predecessor's endpoint to this provider.
	ClearActualOpenAIUpstreamEndpoint(c)
	SetActualUpstreamEndpoint(c, "")
	SetOpsSelectedProvider(c, target.Record.RoutingSnapshot())
	if err := e.checkPricing(c, target, body, source); err != nil {
		return nil, nil, err
	}
	key, _ := EffectiveAPIKey(c)
	var result []byte
	switch target.Record.Platform {
	case provider.PlatformAnthropic:
		result, _, err = ApplyAnthropicReasoningEffortPolicyForRequest(c, key, body)
	case provider.PlatformOpenAI:
		result, _, err = ApplyOpenAIReasoningEffortPolicyForRequest(c, key, body)
	default:
		return target, body, nil
	}
	if err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		if source == protocol.ProtocolAnthropicMessages {
			c.JSON(http.StatusForbidden, gin.H{"type": "error", "error": gin.H{"type": "permission_error", "message": err.Error()}})
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": err.Error(), "code": "reasoning_effort_limit"}})
		}
	}
	return target, result, err
}

// usesOpenAIExecutor reports whether the OpenAI-compatible executor serves the
// target's attempt. Compatible platforms use it for every text protocol except
// a Gemini route, whose request encoding belongs to the Gemini executor.
func usesOpenAIExecutor(target *gatewayadapter.ExecutionProvider) bool {
	return target.View().IsOpenAICompatible() && target.Route.Protocol() != protocol.ProtocolGeminiGenerateContent
}

// native dispatches one native-platform exchange and converts its retained
// result for common completion. Native Messages first applies client detection,
// local interception, queue ownership and Bedrock compatibility, with returned
// cleanup deferred around execution.
//
// Platform adapters receive their specific protocol and session inputs; partial
// results remain available alongside errors. Qoder can refresh credentials and
// retry that same target once when the result is nil, writer size is unchanged
// and the error allows refresh. Header-only commitment without recorded
// heartbeats changes -1 to zero and prevents this retry. A successful refresh replaces the target record before
// retry; failed refresh leaves the first execution error. No provider selection
// is repeated by this function.
func (e *UnifiedTextExecutor) native(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, source protocol.ProtocolID) (*forward.OpenAIResult, error) {
	var result *forward.MessagesResult
	var err error
	// Clear the previous native endpoint before dispatch so a failed attempt
	// cannot retain attribution from a different platform or earlier exchange.
	SetActualUpstreamEndpoint(c, "")
	var parsed *requeststate.ParsedRequest
	if source == protocol.ProtocolAnthropicMessages {
		var release func()
		var intercepted bool
		parsed, release, intercepted, err = e.prepareNativeMessages(c, target, body)
		if release != nil {
			defer release()
		}
		if err != nil || intercepted {
			return nil, err
		}
		body = parsed.Body.Bytes()
		ctx = c.Request.Context()
	}
	switch target.Record.Platform {
	case provider.PlatformAnthropic:
		switch source {
		case protocol.ProtocolAnthropicMessages:
			result, err = e.Anthropic.Forward(ctx, c, target, parsed)
		case protocol.ProtocolOpenAIResponses:
			result, err = e.Anthropic.ForwardAsResponses(ctx, c, target, body, nil)
		default:
			result, err = e.Anthropic.ForwardAsChatCompletions(ctx, c, target, body, nil)
		}
	case provider.PlatformGemini:
		result, err = e.forwardGemini(ctx, c, target, body, source)
	case provider.PlatformAntigravity:
		SetActualUpstreamEndpoint(c, EndpointAntigravityGenerateContent)
		switch source {
		case protocol.ProtocolAnthropicMessages:
			sticky, ok := requeststate.PrefetchedStickyProviderIDFromContext(ctx)
			result, err = e.Antigravity.Forward(ctx, c, target, body, ok && sticky == target.Record.ID)
		case protocol.ProtocolOpenAIResponses:
			result, err = e.Antigravity.ForwardAsResponses(ctx, c, target, body, nil)
		default:
			result, err = e.Antigravity.ForwardAsChatCompletions(ctx, c, target, body, nil)
		}
	case provider.PlatformQoder:
		before := HeartbeatAdjustedWrittenSize(c)
		result, err = ForwardQoderAttempt(ctx, c, e.Qoder, &target.Record, body, source)
		// Retry within this target when its exchange has no result or writer-size
		// change after subtracting heartbeats. Headers committed without a recorded
		// heartbeat retain Gin's zero size and still close this recovery path.
		if err != nil && result == nil && HeartbeatAdjustedWrittenSize(c) == before && qoder.MayRefreshAttempt(err) && e.QoderRefresh != nil {
			fresh, refreshErr := e.QoderRefresh.RefreshProviderSession(ctx, &target.Record)
			// A usable refreshed record replaces this attempt target before its one
			// retry; unsuccessful refresh retains the first exchange error for outer
			// failure handling.
			if refreshErr == nil && fresh != nil {
				target.Record = *fresh
				result, err = ForwardQoderAttempt(ctx, c, e.Qoder, &target.Record, body, source)
			}
		}
	default:
		// A compatible platform can route a model through Gemini encoding; that
		// route uses the Gemini executor regardless of the provider platform.
		if target.Route.Protocol() != protocol.ProtocolGeminiGenerateContent {
			return nil, fmt.Errorf("unsupported provider platform %q", target.Record.Platform)
		}
		result, err = e.forwardGemini(ctx, c, target, body, source)
	}
	return nativeTextResult(result, GetUpstreamEndpoint(c, target.Record.Platform)), err
}

// forwardGemini runs one Gemini-encoded exchange for the client source and
// records the Gemini endpoint before dispatch. Partial results remain
// available alongside errors for the caller's conversion.
func (e *UnifiedTextExecutor) forwardGemini(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, source protocol.ProtocolID) (*forward.MessagesResult, error) {
	SetActualUpstreamEndpoint(c, EndpointGeminiModels)
	switch source {
	case protocol.ProtocolAnthropicMessages:
		return e.Gemini.Forward(ctx, c, target, body)
	case protocol.ProtocolOpenAIResponses:
		return e.Gemini.ForwardAsResponses(ctx, c, target, body, nil)
	default:
		return e.Gemini.ForwardAsChatCompletions(ctx, c, target, body)
	}
}

// nativeTextResult converts retained native observations into the common text
// result while copying its token usage value. Nil input returns nil. Headers,
// image collections and optional timing, effort, tier and audio pointers remain
// shared with the supplied result.
//
// NativeUsage retains the complete native buckets and processing speed
// separately from the OpenAI-shaped numeric view. Completion uses that native
// flag to interpret input and cache tokens according to their source protocol,
// preventing subtraction intended for OpenAI total-input counts from changing
// native billed input. Callers capture the result before mutating shared
// metadata or handing work to an asynchronous recorder.
func nativeTextResult(value *forward.MessagesResult, endpoint string) *forward.OpenAIResult {
	if value == nil {
		return nil
	}
	// Copy the native token buckets before constructing the common view, retaining
	// their source interpretation for completion while other metadata references
	// remain shared.
	usage := value.Usage
	return &forward.OpenAIResult{
		RequestID: value.RequestID, UpstreamHeaders: value.UpstreamHeaders,
		Model: value.Model, UpstreamModel: value.UpstreamModel, UpstreamEndpoint: endpoint,
		NativeUsage: &usage,
		Usage:       openaiwire.ForwardUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheReadInputTokens: usage.CacheReadInputTokens, ImageOutputTokens: usage.ImageOutputTokens},
		Stream:      value.Stream, Duration: value.Duration, Timings: value.Timings, ClientDisconnect: value.ClientDisconnect,
		ReasoningEffort: value.ReasoningEffort, RequestedReasoningEffort: value.RequestedReasoningEffort,
		ServiceTier:                 value.ServiceTier,
		UpstreamResponseServiceTier: value.UpstreamResponseServiceTier,
		UpstreamResponseModel:       value.UpstreamResponseModel,
		ImageCount:                  value.ImageCount,
		ImageFrameCount:             value.ImageFrameCount, ImageSize: value.ImageSize, ImageInputSize: value.ImageInputSize, ImageOutputSize: value.ImageOutputSize,
		ImageOutputSizes: value.ImageOutputSizes, ImageSizeSource: value.ImageSizeSource, ImageSizeBreakdown: value.ImageSizeBreakdown,
		SearchCount: value.SearchCount, AudioUsage: value.AudioUsage,
	}
}

// captureTextPerformance freezes transport and downstream observations after forwarding.
// Platform executors can additionally confirm buffered usage; that evidence survives
// the final snapshot refresh. A conversion failure invalidates throughput eligibility
// while retaining the upstream observations available for diagnostics.
//
// The refresh runs after the adapter's final writes so delivery observations are
// present before asynchronous capture. A completed disconnect drain keeps its
// upstream completeness evidence; ordinary execution errors can retain partial
// usage for accounting while leaving throughput unavailable.
func captureTextPerformance(ctx context.Context, result *forward.OpenAIResult, failure error) {
	if result == nil {
		return
	}
	p := timing.PerformanceFromContext(ctx)
	if p == nil {
		return
	}
	if !p.Stream {
		p.Complete = failure == nil
		p.UsageComplete = result.Timings != nil && result.Timings.UsageComplete
	}
	if failure != nil && !result.ClientDisconnect {
		p.UsageComplete = false
	}
	result.Timings = p
}
