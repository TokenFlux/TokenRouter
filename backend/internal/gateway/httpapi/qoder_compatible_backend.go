package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// qoder_compatible_backend.go binds compatible Qoder admission and captures
// completion input before asynchronous recording.
// Qoder compatible execution forwards a group-mapped body but records the
// original client input and requested model. Route planning installs that
// mapping on request context, while unconfigured execution retains the
// original body and a zero plan. An absent funding service permits admission;
// nil error-rule input retains any service already bound to the HTTP context.
//
// Completion capture happens synchronously before worker submission: body
// bytes are copied and provider-specific input is built from request metadata,
// retained funding and pricing fields. The worker receives that capture rather
// than reading Gin later. A partial result is recorded before cancellation or
// terminal error handling and then ends the loop, so served usage survives an
// error without taking the success sticky-binding path.

// Enter joins the configured request barrier and returns its cleanup callback.
// With no barrier binding it returns a no-op cleanup and nil error, so
// admission can defer cleanup uniformly.
func (h *QoderCompatibleRuntime) Enter() (func(), error) {
	if h.options.Enter != nil {
		return h.options.Enter()
	}
	return func() {}, nil
}

// Access prefers the effective request Key already installed in context.
// Otherwise it reads configured authentication and copies that Key with its
// presence flag; the HTTP handler rejects missing authentication.
// A typed nil stored key can retain a true flag, so admission requires usable
// middleware/effective key state before reading identity and group.
func (h *QoderCompatibleRuntime) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	// Authentication remains the fallback when no effective route Key is stored;
	// copy that result before compatible admission passes it to routing and funding.
	key, ok := h.options.ReadAccess(c)
	return apikey.CopyAPIKey(key), ok
}

// BindErrors installs configured passthrough rules on the active HTTP request.
// Nil rules leave current bindings unchanged; protocol defaults apply when no
// service is bound, while the separate presenter retains its configured rules.
func (h *QoderCompatibleRuntime) BindErrors(c *gin.Context) {
	if h.options.Rules != nil {
		BindErrorPassthroughService(c, h.options.Rules)
	}
}

// PrepareClient delegates protocol-specific Qoder client identity setup for
// this request.
func (h *QoderCompatibleRuntime) PrepareClient(c *gin.Context, body []byte, endpoint QoderEndpoint) {
	prepareQoderRequestContext(c, body, endpoint)
}

// ObserveRequest stores original client model and stream intent for Ops.
func (h *QoderCompatibleRuntime) ObserveRequest(c *gin.Context, model string, stream bool) {
	SetOpsRequestContext(c, model, stream)
}

// ObserveEndpoint stores normalized endpoint and legacy request category for
// Ops.
func (h *QoderCompatibleRuntime) ObserveEndpoint(c *gin.Context, stream bool) {
	SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}

// ObserveAuthLatency records elapsed admission duration as integer
// milliseconds in Ops.
func (h *QoderCompatibleRuntime) ObserveAuthLatency(c *gin.Context, d time.Duration) {
	// Ops stores whole milliseconds, discarding submillisecond admission timing.
	SetOpsLatencyMs(c, OpsAuthLatencyMsKey, d.Milliseconds())
}

// Plan resolves routing using a Key copy and installs the returned route plan
// on request context. An absent execution service returns a zero plan without
// installation; configured planning retains original and mapped identities for
// later completion.
func (h *QoderCompatibleRuntime) Plan(c *gin.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	if h.options.Execution == nil {
		return routing.RoutePlan{}
	}
	old := apikey.CopyAPIKey(key)
	plan := h.options.Execution.Plan(c.Request.Context(), old, model)
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
	return plan
}

// AttemptBody applies group model mapping only when execution exists and the
// plan is mapped. Otherwise it returns the original slice unchanged; callers
// retain original body/model separately for completion and request hashing.
func (h *QoderCompatibleRuntime) AttemptBody(body []byte, plan routing.RoutePlan) []byte {
	mapping := plan.Mapping()
	// Change only the attempt representation so client payload hashing and
	// completion still use the original body and requested model captured by admission.
	if h.options.Execution != nil && mapping.Mapped {
		return openaiprotocol.ReplaceModelInBody(body, mapping.MappedModel)
	}
	return body
}

// Eligibility invokes optional funding admission with a copied Key and
// optional subscription. An absent service returns nil; configured checks pass
// empty model and skip-RPM=false, so this admission call carries no final-model
// price identity. Later execution and recording retain their own model inputs.
func (h *QoderCompatibleRuntime) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	if h.options.Funding == nil {
		return nil
	}
	old := apikey.CopyAPIKey(key)
	return h.options.Funding.CheckKey(ctx, old, sub, "", false)
}

// SessionHash delegates Qoder sticky identity generation for the selected
// client endpoint. Explicit seeds take precedence over parsed-body fallback,
// and an empty result lets execution proceed without a usable sticky key.
func (h *QoderCompatibleRuntime) SessionHash(c *gin.Context, endpoint QoderEndpoint, body []byte, id int64) string {
	return h.qoderSessionHash(c, endpoint, body, id)
}

// ConcurrencyError delegates slot-acquisition failure in the selected Qoder
// client format. The supplied started state preserves presentation after
// admission waits that may already have written response headers or keepalive
// bytes.
func (h *QoderCompatibleRuntime) ConcurrencyError(c *gin.Context, err error, kind string, started bool, endpoint QoderEndpoint) {
	h.handleConcurrencyError(c, err, kind, started, endpoint)
}

// Execution builds fresh serial attempt state from the admitted Qoder call. It
// copies the Key, retains original body and route mapping for completion, and
// forwards AttemptBody through the returned bridge. Callers keep request
// output and stream state usable until the text loop finishes.
//
// The completion callback captures body bytes, pricing and request metadata
// before submitting worker work. Partial-result handling invokes that same
// capture before checking cancellation or writing its error, allowing measured
// work to be recorded while the loop ends without normal success binding.
func (h *QoderCompatibleRuntime) Execution(c *gin.Context, call QoderCompatibleCall) textflow.QoderCompatiblePorts {
	apiKey := apikey.CopyAPIKey(call.Key)
	subscription := call.Subscription
	body := call.Body
	reqModel := call.Model
	reqLog := call.Log
	endpoint := call.Endpoint
	groupMapping := call.Plan.Mapping()

	recordUsage := func(provider QoderCompatibleTarget, result *forwardcore.MessagesResult) {
		userAgent := c.GetHeader("User-Agent")
		clientIP := clientip.GetClientIP(c)
		requestPayloadHash := billing.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, provider.Snapshot().Platform)

		// Build provider completion input before queueing and copy the original body
		// here, so asynchronous recording does not reread mutable HTTP request state.
		completionInput := provider.Completion(CompletionContext(c), QoderCompletionCapture{
			Result: result, Key: apiKey, Subscription: subscription,
			InboundEndpoint: inboundEndpoint, UpstreamEndpoint: upstreamEndpoint, UserAgent: userAgent, ClientIP: clientIP,
			PayloadHash: requestPayloadHash, Body: append([]byte(nil), body...), Pricing: groupMapping.ToUsageFields(reqModel, result.UpstreamModel),
		})
		completionRuntime := h.options.Recorder
		h.submitUsageRecordTask(c, func(ctx context.Context) {
			if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
				reqLog.Error("qoder.record_usage_failed", zap.Int64("provider_id", completionInput.Provider.ID), zap.Error(err))
			}
		})
	}

	// A served partial result carries completion data despite its error. Record
	// that data before checking cancellation, which can suppress only subsequent error output.
	finishPartial := func(provider QoderCompatibleTarget, result *forwardcore.MessagesResult, forwardErr error) bool {
		if forwardErr == nil || result == nil {
			return false
		}
		recordUsage(provider, result)
		if qoderRequestCanceled(c.Request.Context(), forwardErr) {
			return true
		}
		status, kind, message, ok := h.qoderGatewayErrorDetails(c, forwardErr)
		// Unclassified partial failures still need a terminal client message, while
		// the captured result keeps its measured usage for the already submitted completion.
		if !ok {
			status = http.StatusBadGateway
			kind = "upstream_error"
			message = "Upstream request failed"
		}
		h.streamingAwareError(c, status, kind, message, true, endpoint)
		return true
	}
	return &qoderCompatibleAttemptBridge{h: h, c: c, endpoint: endpoint, key: apiKey, subjectID: call.Subject.UserID, hash: call.SessionHash, model: reqModel, stream: call.Stream, streamStarted: call.StreamStarted, body: call.AttemptBody, log: reqLog, record: recordUsage, partial: finishPartial}
}
