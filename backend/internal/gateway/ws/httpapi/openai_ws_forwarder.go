package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
)

// openai_ws_forwarder.go resolves WebSocket turn models, transport budgets,
// response ownership, and upstream warning data. A supplied routing hook checks
// group eligibility before provider mapping, while response restoration and
// retry payloads retain the client model. The same execution adapter supplies
// transport budgets and provider warning observations to the pooled and
// dedicated paths.
//
// Missing option values select adapter defaults rather than disabling
// connection operations. Response-ID affinity uses a separate lifetime from
// session affinity. Warning bodies are copied for later observation, and
// ownership binding records the response ID under the authenticated user's
// group so later isolated-group requests can detect its first owner.

// Handshake state and diagnostic limits identify retained turn metadata and bound
// payload summaries; idle and store mode values name session transport policies.
const (
	openAIWSTurnStateHeader               = "x-codex-turn-state" // opaque continuation state header
	openAIWSPayloadKeySizeTopN            = 6                    // largest payload fields reported
	openAIWSPassthroughIdleTimeoutDefault = time.Hour            // fallback relay idle duration

	openAIWSStoreDisabledConnModeStrict   = "strict"   // require saved connection affinity
	openAIWSStoreDisabledConnModeAdaptive = "adaptive" // adapt after selected transport failures
	openAIWSStoreDisabledConnModeOff      = "off"      // disable forced connection replacement
)

// openAIWSIngressPreflightPingIdle delays pooled health probes after a clean turn
// so immediately following requests reuse the connection without another ping.
var openAIWSIngressPreflightPingIdle = 20 * time.Second

// openAIWSFastModePolicyContext binds the refreshed Key Fast policy for the
// supplied turn. Absent hooks leave the context unchanged; the hook's returned
// policy overrides the Key value carried by the existing context.
func openAIWSFastModePolicyContext(ctx context.Context, hooks *gatewayws.OpenAIIngressHooks, turn int) context.Context {
	if hooks == nil || hooks.ResolveFastModePolicy == nil {
		return ctx
	}
	return gatewayprovider.
		WithAPIKeyFastModePolicy(ctx, hooks.ResolveFastModePolicy(turn))
}

// resolveOpenAIWSTurnModels returns routing and provider-normalized upstream
// models for a client request. When supplied, the routing hook runs first and
// can reject eligibility; otherwise the trimmed requested model supplies the
// routing name. A blank routing result produces a policy close. Provider
// mapping then selects the upstream spelling, with an empty normalized result
// retaining the routing model.
// The caller keeps the client model separately for response restoration and
// replacement-provider replay. Running provider mapping after the hook keeps
// its eligibility input at the routing stage. The hook receives both turn
// number and payload so later turns can recheck their actual intent.
func resolveOpenAIWSTurnModels(provider *gatewayprovider.ExecutionProvider, hooks *gatewayws.OpenAIIngressHooks, turn int, requestedModel string, payload []byte) (string, string, error) {
	routingModel := strings.TrimSpace(requestedModel)
	if hooks != nil && hooks.ResolveRoutingModel != nil {
		resolved, err := hooks.ResolveRoutingModel(turn, routingModel, payload)
		if err != nil {
			return "", "", err
		}
		routingModel = strings.TrimSpace(resolved)
	}
	if routingModel == "" {
		return "", "", gatewayhttp.NewOpenAIWSClientCloseError(
			coderws.StatusPolicyViolation,
			"model is required in response.create payload",
			nil,
		)
	}

	effort := requeststate.ExtractRoutingReasoningEffort(payload, protocol.ProtocolResponsesWebSocket)
	upstreamModel := gatewayprovider.ExecutionModelPolicy(provider).WithEffort(effort).NormalizeOpenAI(
		providercore.ResolveForwardMappedModelForEffort(gatewayprovider.ExecutionRecord(provider), routingModel, effort, provideradapter.ModelDefaults()),
	)
	// A provider mapping can normalize to no model; retain the eligible routing
	// name so upstream execution does not lose the client's accepted model choice.
	if upstreamModel == "" {
		upstreamModel = routingModel
	}
	return routingModel, upstreamModel, nil
}

// SnapshotOpenAIWSPoolMetrics returns current counters from the shared
// connection pool. An unavailable pool returns a zero snapshot; this accessor
// asks Connections for the pool and may trigger its lazy creation.
func (s *OpenAIWebSocketExecutor) SnapshotOpenAIWSPoolMetrics() openaiws.WSPoolMetricsSnapshot {
	pool := s.Connections.Pool()
	if pool == nil {
		return openaiws.WSPoolMetricsSnapshot{}
	}
	return pool.SnapshotMetrics()
}

// OpenAIHTTPResponseStickyTTL returns response-ID affinity lifetime for the
// captured turn options. A nil executor, nil Options, or nonpositive seconds
// selects one hour; session-hash records use Selection.SessionStickyTTL
// separately.
func (s *OpenAIWebSocketExecutor) OpenAIHTTPResponseStickyTTL() time.Duration {
	if s != nil && s.Options != nil {
		seconds := s.Options.StickyResponseIDTTLSeconds
		if seconds > 0 {
			// Convert configured seconds for response affinity expiry timestamps.
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Hour
}

// openAIWSIngressPreviousResponseRecoveryEnabled reports permission for
// missing-response recovery. Nil executor or Options selects enabled;
// configured false remains false and does not govern the core's separate idle
// ping or transport retry rules.
func (s *OpenAIWebSocketExecutor) openAIWSIngressPreviousResponseRecoveryEnabled() bool {
	if s != nil && s.Options != nil {
		return s.Options.IngressPreviousResponseRecoveryEnabled
	}
	return true
}

// openAIWSReadTimeout returns the captured upstream read budget for the turn.
// Positive configured seconds take effect; nil executor, nil Options, or
// nonpositive seconds selects fifteen minutes.
func (s *OpenAIWebSocketExecutor) openAIWSReadTimeout() time.Duration {
	if s != nil && s.Options != nil && s.Options.ReadTimeoutSeconds > 0 {
		// Convert configured seconds for pooled event read deadlines.
		return time.Duration(s.Options.ReadTimeoutSeconds) * time.Second
	}
	return 15 * time.Minute
}

// openAIWSPassthroughIdleTimeout returns the dedicated relay's general idle
// budget. It uses openAIWSReadTimeout, including that accessor's fifteen minute
// default; the one-hour fallback applies only if that accessor returns a
// nonpositive duration.
func (s *OpenAIWebSocketExecutor) openAIWSPassthroughIdleTimeout() time.Duration {
	if timeout := s.openAIWSReadTimeout(); timeout > 0 {
		return timeout
	}
	return openAIWSPassthroughIdleTimeoutDefault
}

// openAIWSWriteTimeout returns the captured frame write budget for the turn.
// Positive configured seconds take effect; nil executor, nil Options, or
// nonpositive seconds selects two minutes.
func (s *OpenAIWebSocketExecutor) openAIWSWriteTimeout() time.Duration {
	if s != nil && s.Options != nil && s.Options.WriteTimeoutSeconds > 0 {
		// Convert configured seconds for downstream and upstream write deadlines.
		return time.Duration(s.Options.WriteTimeoutSeconds) * time.Second
	}
	return 2 * time.Minute
}

// openAIWSDialTimeout returns the captured upstream handshake budget for the
// turn. Positive configured seconds take effect; nil executor, nil Options, or
// nonpositive seconds selects ten seconds.
func (s *OpenAIWebSocketExecutor) openAIWSDialTimeout() time.Duration {
	if s != nil && s.Options != nil && s.Options.DialTimeoutSeconds > 0 {
		// Convert configured seconds for the upstream handshake context deadline.
		return time.Duration(s.Options.DialTimeoutSeconds) * time.Second
	}
	return 10 * time.Second
}

// openAIWSAcquireTimeout returns the pooled acquisition budget, including
// queueing and dialing. It adds two seconds to the dial budget, which lets
// pool acquisition include selection work while retaining the current turn's
// fixed deadline.
func (s *OpenAIWebSocketExecutor) openAIWSAcquireTimeout() time.Duration {
	// Reserve selection overhead beyond the handshake budget while retaining one
	// deadline across pool reuse, queue wakes, and any new dial for this acquisition.
	dial := s.openAIWSDialTimeout()
	if dial <= 0 {
		dial = 10 * time.Second
	}
	return dial + 2*time.Second
}

// bindOpenAIWSResponseSessionOwner records the response ID's first group under
// the authenticated user. Missing executor, request, Key user, or response
// hash skips binding; an isolation or cache error is ignored because this
// observation follows an already completed pooled turn.
func (s *OpenAIWebSocketExecutor) bindOpenAIWSResponseSessionOwner(ctx context.Context, c *gin.Context, responseID string) {
	if s == nil || c == nil {
		return
	}
	apiKey := gatewayhttp.GetExecutionAPIKey(c)
	if apiKey == nil || apiKey.UserID <= 0 {
		return
	}
	responseHash, _ := scheduler.DeriveSessionHashes(responseID)
	if responseHash == "" {
		return
	}
	// The relay has already returned its turn result. Ignore storage failures here
	// so ownership observation cannot create another provider attempt after completion.
	_ = s.EnsureSessionIsolation(ctx, apiKey, apiKey.UserID, session.SessionIsolationSourceOpenAIPreviousResponse, responseHash)
}

// buildOpenAIWSUpstreamWarning returns an observation for error, failed, or
// incomplete events with nonempty bytes. Other events return nil. The body is
// copied; standalone errors derive their HTTP status while failed and
// incomplete observations use 502.
func buildOpenAIWSUpstreamWarning(eventType string, message []byte) *forwardcore.UpstreamWarning {
	if !openAIWSEventMayCarryUpstreamWarning(eventType) || len(message) == 0 {
		return nil
	}
	statusCode := http.StatusBadGateway
	if strings.TrimSpace(eventType) == "error" {
		statusCode = openai.WSErrorHTTPStatus(message)
	}
	return &forwardcore.UpstreamWarning{
		StatusCode:   statusCode,
		ResponseBody: append([]byte(nil), message...),
		Message:      extractOpenAIWSUpstreamWarningMessage(message),
	}
}

// extractOpenAIWSUpstreamWarningMessage returns the first trimmed message
// across top-level error, response error, status-details error, and incomplete
// reason paths. Empty bytes or absent nonblank values return an empty string
// for the caller's warning observation.
func extractOpenAIWSUpstreamWarningMessage(message []byte) string {
	if len(message) == 0 {
		return ""
	}
	paths := []string{
		"error.message",
		"response.error.message",
		"response.status_details.error.message",
		"response.incomplete_details.reason",
	}
	for _, path := range paths {
		if value := strings.TrimSpace(gjson.GetBytes(message, path).String()); value != "" {
			return value
		}
	}
	return ""
}

// openAIWSEventMayCarryUpstreamWarning reports whether a trimmed event label
// denotes an upstream error, failure, or incomplete response.
func openAIWSEventMayCarryUpstreamWarning(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "error", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}
