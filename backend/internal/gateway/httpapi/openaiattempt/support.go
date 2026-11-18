package openaiattempt

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	openaierrors "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// support.go qualifies replay and captures scheduling, error and Cyber
// completion evidence.
// Retry readiness distinguishes HTTP commitment from actual output that makes
// replay unsafe. The attempt supplies a pre-forward adjusted byte count;
// unchanged output permits retry, while changed output requires classified
// recovery permission. Terminal errors then use protocol-specific writers and
// configured rules so already delivered semantic output is not mistaken for an
// unwritten response.
//
// Scheduling and completion observations need the actual upstream model and
// endpoint, which can differ from the client's entry or group alias. Result
// metadata takes precedence over request observations and provider defaults.
// Cyber policy captures funding and usage snapshots before delegating queued
// recording, while selected-provider slot acquisition delegates wait and
// release rules to the shared HTTP resource adapter.

// cyberSessionBlockWritePlan pairs request-scoped block keys with their
// transcript scope.
type cyberSessionBlockWritePlan struct {
	scopeKey string   // transcript matching scope key
	keys     []string // ordered session blocking keys
}

// Support binds app-owned HTTP resources and side effects for selected text
// attempts.
type Support struct {
	Concurrency *gatewayhttp.ConcurrencyHelper       // shared selected-slot admission helper
	Sticky      gatewayhttp.SlotStickyBinder         // selected-provider affinity write service
	Rules       *errorpolicy.ErrorPassthroughService // optional terminal error-rule matcher
	Cyber       *gatewayhttp.CyberHandler            // request Cyber-policy recording service
	Quota       gatewayprovider.QuotaUpdater         // captured Key quota update service
	Moderation  gatewayhttp.ModerationPort           // upstream warning moderation recorder
	Submission  gatewayhttp.CompletionSubmission     // shared completion task submission service
}

// OpenAIProviderScheduleModel chooses the model used for selected-provider
// scheduling feedback. It prefers a nonblank result upstream model, then a
// string Ops observation, then provider policy applied to forwardModel with the
// Compact requirement.
func OpenAIProviderScheduleModel(c *gin.Context, provider *gatewayprovider.ExecutionProvider, forwardModel string, requireCompact bool, result *forwardcore.OpenAIResult) string {
	if result != nil {
		if actual := strings.TrimSpace(result.UpstreamModel); actual != "" {
			return actual
		}
	}
	if c != nil {
		if value, ok := c.Get(gatewayhttp.OpsUpstreamModelKey); ok {
			if actual, ok := value.(string); ok && strings.TrimSpace(actual) != "" {
				return strings.TrimSpace(actual)
			}
		}
	}
	return gatewayprovider.ExecutionModelPolicy(provider).OpenAIUpstream(forwardModel, requireCompact)
}

// AppendOpenAIProviderProxyLogFields appends selected proxy ID, name, host, and
// port observations. A missing loaded proxy can supply its reference ID alone;
// missing provider returns fields unchanged. Appending can reuse the caller's
// slice storage, and credentials are excluded from the fields constructed here.
func AppendOpenAIProviderProxyLogFields(fields []zap.Field, provider *gatewayprovider.ExecutionProvider) []zap.Field {
	if provider == nil {
		return fields
	}
	if provider.Record.Proxy != nil {
		return append(fields,
			zap.Int64("proxy_id", provider.Record.Proxy.ID),
			zap.String("proxy_name", provider.Record.Proxy.Name),
			zap.String("proxy_host", provider.Record.Proxy.Host),
			zap.Int("proxy_port", provider.Record.Proxy.Port),
		)
	}
	if provider.Record.ProxyID != nil {
		return append(fields, zap.Int64p("proxy_id", provider.Record.ProxyID))
	}
	return fields
}

// HandleOpenAISelectionBusinessError writes recognized group-selection errors
// using OpenAI framing. Native Chat tool incompatibility is a client request
// error. Callers continue availability or exhausted-retry classification only
// when this returns false.
func (h *Support) HandleOpenAISelectionBusinessError(c *gin.Context, err error, streamStarted bool) bool {
	if apperror.Reason(err) == gatewayprovider.NativeChatFunctionToolsUnsupported {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalModelConfiguration)
		gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, http.StatusBadRequest, "invalid_request_error", apperror.Message(err), streamStarted)
		return true
	}
	return handleGroupSelectionBusinessError(c, err, streamStarted, func(status int, errType string, message string, streamStarted bool) {
		gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, status, errType, message, streamStarted)
	})
}

// HandleAnthropicFailoverExhausted writes terminal retry failure using Messages
// error framing. Callers supply a nonnil failure and the current stream marker.
// Oversized request classification takes precedence, followed by credential and
// capacity failures with client-safe messages; remaining statuses use the
// shared upstream-error mapping.
func (h *Support) HandleAnthropicFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	if failoverErr != nil && gatewayprovider.IsOpenAIRequestBodyTooLarge(failoverErr) {
		gatewayhttp.SetOpsUpstreamError(c, http.StatusRequestEntityTooLarge, forwardcore.OpenAIRequestBodyTooLargeClientMessage, "")
		gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(
			c,
			http.StatusRequestEntityTooLarge,
			"invalid_request_error",
			forwardcore.OpenAIRequestBodyTooLargeClientMessage,
			streamStarted,
		)
		return
	}
	if failoverErr != nil {
		gatewayhttp.CopyFailoverRetryAfter(c, failoverErr.ResponseHeaders)
	}
	if failoverErr != nil && failoverErr.IsCredentialFailure() {
		status, message := gatewayhttp.CredentialFailoverClientResponse(failoverErr)
		gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, "api_error", message, streamStarted)
		return
	}
	if failoverErr != nil && gatewayprovider.IsOpenAICapacityShed(failoverErr) && strings.TrimSpace(failoverErr.ClientMessage) != "" {
		status := failoverErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, "api_error", failoverErr.ClientMessage, streamStarted)
		return
	}
	status, errType, errMsg := gatewayhttp.MapOpenAIUpstreamError(failoverErr.StatusCode)
	gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, errType, errMsg, streamStarted)
}

// EnsureAnthropicErrorResponse supplies a default Messages upstream error when
// output is unwritten. Nil context, missing writer, or committed headers return
// false. A written fallback returns true so the attempt can report which
// terminal response it supplied.
func (h *Support) EnsureAnthropicErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
	return true
}

// AcquireResponsesProviderSlot acquires selected capacity with the default
// OpenAI error writer. It delegates waiting and cancellation to
// AcquireOpenAIProviderSlot. The caller owns any returned release callback and
// invokes it after its forwarding operation ends.
func (h *Support) AcquireResponsesProviderSlot(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	selection *gatewayprovider.SelectionResult,
	reqStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
) (func(), bool) {
	release, result := h.AcquireOpenAIProviderSlot(c, groupID, sessionHash, selection, reqStream, streamStarted, reqLog, nil)
	return release, result
}

// AcquireOpenAIProviderSlot adopts or waits for the selected provider's
// capacity. Callers supply the active request, stream marker, logger, and
// shared concurrency services. A nil error writer selects OpenAI output;
// returned cleanup is owned by forwarding, with Qoder streams retained until
// explicit completion cleanup.
func (h *Support) AcquireOpenAIProviderSlot(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	selection *gatewayprovider.SelectionResult,
	reqStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
	writeError func(int, string, string, string),
) (func(), bool) {
	if writeError == nil {
		writeError = func(status int, errType, code, message string) {
			gatewayhttp.DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, status, errType, code, message, *streamStarted, false)
		}
	}
	var projected *gatewayhttp.SelectedProviderSlot
	// Qoder streaming reads tail usage after client cancellation. Pass its
	// completion-release choice to the common slot adapter while other
	// attempts use cancellation cleanup.
	if selection != nil && selection.Provider != nil {
		projected = &gatewayhttp.SelectedProviderSlot{CompleteBeforeRelease: reqStream && selection.Provider.Record.Platform == capability.PlatformQoder, ProviderID: selection.Provider.Record.ID, Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc, WaitPlan: selection.WaitPlan}
	}
	release, ok := gatewayhttp.AcquireSelectedProviderSlot(c, groupID, sessionHash, projected, reqStream, streamStarted, reqLog, writeError, h.Concurrency, h.Sticky, gatewayhttp.ProviderSlotHooks{Acquired: gatewayhttp.MarkOpsProviderSlotAcquired, CapacityLimited: gatewayhttp.MarkOpsRoutingCapacityLimited})
	if !ok {
		return release, false
	}
	return release, true
}

// GetContextInt64 reads a numeric HTTP observation as an int64 value. It
// accepts int, int32, int64, and float64, whose fractional part is discarded.
// Nil context, empty key, absent values, and other types return zero with
// false.
func GetContextInt64(c *gin.Context, key string) (int64, bool) {
	if c == nil || key == "" {
		return 0, false
	}
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case float64:
		// Numeric observations may be decoded as floating point.
		// Discard fractions because these consumers record integral
		// millisecond or status values in the request context.
		return int64(t), true
	default:
		return 0, false
	}
}

// HandleFailoverExhausted writes terminal OpenAI recovery output under
// configured error rules. Rules are optional. The shared writer updates
// upstream observations or skip-monitoring state through synchronous hooks,
// using the supplied stream marker and projected failure.
func (h *Support) HandleFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	var rules gatewayhttp.ErrorRuleMatcher
	if h.Rules != nil {
		rules = h.Rules
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteFailoverExhausted(c, gatewayhttp.ProjectOpenAIFailoverError(failoverErr), streamStarted, rules, gatewayhttp.FailoverErrorHooks{Upstream: func(c *gin.Context, status int, message string) {
		gatewayhttp.SetOpsUpstreamError(c, status, message, "")
	}, SkipMonitoring: func(c *gin.Context) { c.Set(gatewayhttp.OpsSkipPassthroughKey, true) }})
}

// HandleFailoverExhaustedSimple maps an upstream status to safe terminal OpenAI
// output. It records that upstream status and mapped message before writing.
// Callers use this path when no classified upstream failure is available.
func (h *Support) HandleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := gatewayhttp.MapOpenAIUpstreamError(statusCode)
	gatewayhttp.SetOpsUpstreamError(c, statusCode, errMsg, "")
	gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, status, errType, errMsg, streamStarted)
}

// EnsureOpenAIStreamReadErrorResponse writes a recognized stream-read failure
// when semantic output is uncommitted. Unrecognized errors or
// unavailable/committed responses return false. Already-written HTTP headers
// select streaming output; the returned true reports fallback emission.
func (h *Support) EnsureOpenAIStreamReadErrorResponse(c *gin.Context, err error, streamStarted bool) bool {
	code, message, ok := openaierrors.OpenAIUpstreamStreamReadErrorDetails(err)
	if !ok || c == nil || c.Writer == nil || gatewayhttp.IsResponseCommitted(c) {
		return false
	}
	if c.Writer.Written() {
		streamStarted = true
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(
		c, http.StatusBadGateway, "upstream_error", code, message, streamStarted, true,
	)
	return true
}

// RecordCyberPolicyIfMarked captures the current HTTP Cyber mark for policy
// recording. Absent marks return false. String block input supplies an explicit
// key; byte input with an API key derives a scoped transcript plan for the
// shared recorder.
//
// User, provider, subscription, endpoint, usage, and request observations are
// captured before CyberHandler receives the call. The first optional compaction
// flag overrides the request-context value. Callers supply request-local
// metadata and configured Cyber support, and the returned bool reflects that
// handler's policy processing rather than successful generation completion.
func (h *Support) RecordCyberPolicyIfMarked(c *gin.Context, apiKey *apikey.APIKey, provider *gatewayprovider.ExecutionProvider, subscription *billing.UserSubscription, model string, forwardErrored bool, cyberBlockArg any, pricingFields routing.PricingUsageFields, requestPayloadHash string, nativeCompaction ...bool) bool {
	mark := gatewayhttp.GetOpsCyberPolicy(c)
	if mark == nil || c == nil {
		return false
	}
	call := gatewayhttp.CyberPolicyCall{
		Key:            apikey.CopyAPIKey(apiKey),
		Provider:       moderationProviderView(provider),
		Model:          model,
		ForwardErrored: forwardErrored,
	}
	switch value := cyberBlockArg.(type) {
	case string:
		call.BlockKey = value
	case []byte:
		if apiKey != nil {
			plan := buildCyberSessionBlockWritePlan(apiKey.ID, c, value)
			call.Plan = moderationflow.BlockPlan{
				ScopeKey: plan.scopeKey,
				Keys:     plan.keys,
			}
			call.HasPlan = true
		}
	}
	// Capture request and usage values before handing the mark to
	// CyberHandler, so queued policy recording uses these snapshots after
	// the HTTP attempt finishes.
	compaction := gatewayhttp.IsOpenAINativeCompactionV2(c)
	if len(nativeCompaction) > 0 {
		compaction = nativeCompaction[0]
	}
	platform := capability.PlatformOpenAI
	if provider != nil && strings.TrimSpace(provider.Record.Platform) != "" {
		platform = provider.Record.Platform
	}
	call.Usage = gatewayprovider.CaptureCyber(c.Request.Context(), gatewayprovider.CyberCapture{
		APIKey:             apiKey,
		Provider:           gatewayprovider.ExecutionCompletionRecord(provider),
		Subscription:       subscription,
		RequestID:          telemetry.RequestIDValue(c.Request.Context()),
		Model:              model,
		Stream:             requestIsStream(c),
		InputTokens:        mark.UpstreamInTok,
		OutputTokens:       mark.UpstreamOutTok,
		InboundEndpoint:    gatewayhttp.GetInboundEndpoint(c),
		UpstreamEndpoint:   gatewayhttp.GetUpstreamEndpoint(c, platform),
		UserAgent:          c.GetHeader("User-Agent"),
		IPAddress:          clientip.GetClientIP(c),
		ClientSessionID:    gatewayhttp.ExtractClientSessionID(c),
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.Quota,

		NativeCompactionV2: compaction,
		PricingUsageFields: pricingFields,
	})
	return h.Cyber.RecordPolicy(c, call)
}

// requestIsStream reports the boolean stream observation stored on the HTTP
// request. Nil context, missing value, and nonboolean values return false,
// giving Cyber usage capture a nonstreaming default when that observation is
// unavailable.
func requestIsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(gatewayhttp.OpsStreamKey); ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// OpenAIForwardMayFailover evaluates retry permission against meaningful-output
// size and explicit recovery evidence. Callers supply the adjusted size
// captured before forwarding, using the same Compact keepalive accounting at
// both points. Missing context or writer denies retry.
//
// An unchanged adjusted byte count permits replay even if headers were
// committed. Changed bytes require SafeToFailoverAfterWrite from the classified
// failure; the helper does not infer that permission from arbitrary content.
// Request cancellation is checked separately by the attempt's replay guard
// before entering another provider.
func OpenAIForwardMayFailover(c *gin.Context, writerSizeBeforeForward int, failoverErr *forwardcore.UpstreamFailoverError) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if gatewayhttp.HeartbeatAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return true
	}
	return failoverErr != nil && failoverErr.SafeToFailoverAfterWrite
}

// OpenAIRequestAllowsFailoverReplay reports whether the request remains
// available for another attempt. Missing HTTP input returns false.
// FailoverClientGone supplies the active cancellation and disconnection
// decision; output-size eligibility is evaluated separately during retry
// readiness.
func OpenAIRequestAllowsFailoverReplay(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return !gatewayhttp.FailoverClientGone(c)
}

// EnsureOpenAIPoolModeSessionHash supplies a sticky seed when pool retries lack
// one. Existing nonempty hashes and nonpool providers pass through. A new
// UUID-based request key is returned for an unbound pool provider so later
// retries can reuse it.
func EnsureOpenAIPoolModeSessionHash(sessionHash string, provider *gatewayprovider.ExecutionProvider) string {
	if sessionHash != "" || provider == nil || !provider.View().IsPoolMode() {
		return sessionHash
	}
	// Create the seed once when the attempt first lacks a pool identity.
	// Its returned value is retained by the request bridge for subsequent
	// same-provider selections.
	return "openai-pool-retry-" + uuid.NewString()
}

// buildCyberSessionBlockWritePlan derives explicit and transcript block keys
// for the current key. Transcript keys receive the request's IP/user-agent
// scope when present. BuildBlockPlan combines those inputs, and the result is
// passed as the current Cyber recording plan.
func buildCyberSessionBlockWritePlan(apiKeyID int64, c *gin.Context, body []byte) cyberSessionBlockWritePlan {
	explicit := gatewayhttp.CyberSessionExplicitBlockKey(apiKeyID, c, body)
	transcript := gatewaysession.CyberSessionTranscriptBlockKeys(apiKeyID, body)
	scope := ""
	if len(transcript) > 0 {
		scope = cyberSessionScopeKey(apiKeyID, c)
	}
	plan := moderationflow.BuildBlockPlan(explicit, transcript, scope)
	return cyberSessionBlockWritePlan{scopeKey: plan.ScopeKey, keys: plan.Keys}
}

// cyberSessionScopeKey derives transcript scope from API-key identity, client
// IP, and user-agent. Nil Gin context returns empty scope. The session helper
// supplies the stable scope encoding used by the Cyber block plan.
func cyberSessionScopeKey(apiKeyID int64, c *gin.Context) string {
	if c == nil {
		return ""
	}
	return gatewaysession.CyberSessionScopeKey(apiKeyID, strings.TrimSpace(clientip.GetClientIP(c)), c.GetHeader("User-Agent"))
}

// handleGroupSelectionBusinessError delegates recognized selection failures to
// the supplied protocol writer. It uses the authenticated context key and
// model-display catalog for client text. False leaves further availability or
// retry-exhaustion output to the caller.
func handleGroupSelectionBusinessError(c *gin.Context, err error, started bool, write func(int, string, string, bool)) bool {
	return gatewayhttp.WriteGroupSelectionBusinessError(c, err, started, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, write)
}

// ResolveOpenAIUpstreamEndpoint chooses endpoint metadata for compatible
// execution completion. It prefers a nonblank result endpoint, then recorded
// actual attempt context, then the selected provider's platform default.
// Callers supply a provider for that last path.
func ResolveOpenAIUpstreamEndpoint(c *gin.Context, provider *gatewayprovider.ExecutionProvider, result *forwardcore.OpenAIResult) string {
	if result != nil {
		if endpoint := strings.TrimSpace(result.UpstreamEndpoint); endpoint != "" {
			return endpoint
		}
	}
	if endpoint := gatewayhttp.GetActualOpenAIUpstreamEndpoint(c); endpoint != "" {
		return endpoint
	}
	return gatewayhttp.GetUpstreamEndpoint(c, provider.Record.Platform)
}
