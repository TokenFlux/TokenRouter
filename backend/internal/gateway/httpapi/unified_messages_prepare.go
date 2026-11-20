package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// unified_messages_prepare.go prepares native Messages requests with client
// detection, local probe interception and queue ownership.
// Native Messages execution needs client identity before choosing warmup
// interception or serial queue policy. Intercepted probes are answered locally
// and return before queue admission. Real user messages on eligible Anthropic
// targets use the provider's queue mode when present, otherwise the executor's
// default, with ping state shared back to the outer attempt.
//
// A serial queue lease ends when upstream accepts the message, or when
// cancellation or outer cleanup arrives first. Preparation installs the same
// wrapped callback for those paths and clears the parsed acceptance callback
// during cleanup. Queue errors are diagnostic, while body replacement errors
// still invoke available cleanup before forwarding is abandoned. Bedrock
// compatibility runs after that ownership has been established.

// These Gin keys retain the outer queue-ping marker and current local probe
// interception result across native Messages preparation and its caller.
const (
	nativeMessageStreamKey      = "native_message_stream_started"
	nativeMessageInterceptedKey = "native_message_intercepted"
)

// NativeMessageQueue connects the unified executor to the shared message queue service.
type NativeMessageQueue interface {
	// AcquireWithWait obtains serial message ownership using the request context
	// and supplied timeout. Preparation retains any returned release, wraps it for
	// cancellation and acceptance, and logs a returned error before continuing.
	AcquireWithWait(*gin.Context, int64, int, bool, *bool, time.Duration, *zap.Logger) (func(), error)
	// ThrottleWithPing applies message spacing with optional wait heartbeats and
	// the supplied request budget. Preparation reports errors as warnings and
	// continues, so implementations leave their returned error independent from
	// request rejection.
	ThrottleWithPing(*gin.Context, int64, int, bool, *bool, time.Duration, *zap.Logger) error
}

// BindNativeMessageStreamState retains the caller's shared queue-ping marker in
// Gin context. A valid context is required; nil pointers may be stored but
// preparation ignores them and uses its local writer-commitment marker.
func BindNativeMessageStreamState(c *gin.Context, started *bool) {
	c.Set(nativeMessageStreamKey, started)
}

// NativeMessageIntercepted reads the current native probe interception marker
// from Gin. The context is required, and absent or mistyped values yield false;
// Messages clears this marker before preparing each selected exchange.
func NativeMessageIntercepted(c *gin.Context) bool {
	value, _ := c.Get(nativeMessageInterceptedKey)
	intercepted, _ := value.(bool)
	return intercepted
}

// prepareNativeMessages parses native Messages input, identifies its client and
// handles eligible local probes before queueing. Parse failure writes a local
// error; intercepted probes return parsed input with true and no cleanup
// because forwarding will not occur.
//
// Real user messages on Anthropic OAuth/setup targets can acquire serialization
// or throttle using provider mode before the binding default. Queue errors
// remain warnings and preparation proceeds with any returned release. That
// callback is wrapped for cancellation, installed at upstream acceptance and
// also used by outer cleanup. When the Anthropic adapter is bound, Bedrock
// compatibility can replace the body; replacement failure runs cleanup before
// returning. Successful preparation publishes the parsed request for
// synchronous execution.
func (e *UnifiedTextExecutor) prepareNativeMessages(c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte) (*requeststate.ParsedRequest, func(), bool, error) {
	var parsed *requeststate.ParsedRequest
	var err error
	// Reuse the already parsed request via CloneForBody when admission published
	// one on Gin context so unchanged multi-megabyte bodies skip a second parse.
	if rawParsed, ok := c.Get("parsed_request"); ok {
		if existing, ok := rawParsed.(*requeststate.ParsedRequest); ok && existing != nil {
			parsed, err = existing.CloneForBody(body)
		}
	}
	if parsed == nil && err == nil {
		parsed, err = requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), "anthropic")
	}
	if err != nil {
		MarkResponseCommitted(c)
		WriteAnthropicStreamError(c, http.StatusBadRequest, "invalid_request_error", "", "Failed to parse request body", CommittedSSE(c), MarkOpsStreamError)
		return nil, nil, false, err
	}
	key, ok := EffectiveAPIKey(c)
	if !ok {
		key, _ = keyhttp.GetAPIKeyFromContext(c)
	}
	if key != nil {
		parsed.GroupID = key.GroupID
	}
	requestedModel := ErrorRequestModel(c)
	if requestedModel == "" {
		requestedModel = parsed.Model
	}
	detection := DetectClaudeCodeRequest(c, body, parsed, clientmeta.IsHaikuProbe(requestedModel, parsed.MaxTokens))
	ctx := c.Request.Context()
	if detection.ClaudeCode {
		ctx = requeststate.SetClaudeCodeClient(ctx, true)
		if detection.Version != "" {
			ctx = requeststate.SetClaudeCodeVersion(ctx, detection.Version)
		}
	}
	c.Request = c.Request.WithContext(ctx)
	// Answer recognized probes before queue admission and provider execution,
	// leaving this attempt with no queue lease or upstream usage result to
	// complete.
	if target.View().IsInterceptWarmupEnabled() {
		intercepted := clientmeta.DetectInterceptType(body, requestedModel, parsed.MaxTokens, requeststate.IsClaudeCodeClient(ctx))
		if intercepted != clientmeta.InterceptTypeNone {
			c.Set(nativeMessageInterceptedKey, true)
			if parsed.Stream {
				WriteInterceptStream(c, parsed.Model, intercepted)
			} else {
				WriteInterceptResponse(c, parsed.Model, intercepted)
			}
			return parsed, nil, true, nil
		}
	}
	var release func()
	if e.MessageQueue != nil && target.View().IsAnthropicOAuthOrSetupToken() && requeststate.IsRealUserMessage(parsed) {
		mode := gatewayadapter.ExecutionRuntimeConfig(target).GetUserMsgQueueMode()
		if mode == "" {
			mode = e.MessageQueueMode
		}
		baseRPM := gatewayadapter.ExecutionRuntimeConfig(target).GetBaseRPM()
		started := c.Writer.Written()
		streamState := &started
		if value, ok := c.Get(nativeMessageStreamKey); ok {
			if pointer, ok := value.(*bool); ok && pointer != nil {
				streamState = pointer
			}
		}
		log := RequestLogger(c, "handler.gateway.messages", zap.Int64("provider_id", target.Record.ID))
		switch mode {
		case policy.MessageQueueSerialize:
			var queueErr error
			release, queueErr = e.MessageQueue.AcquireWithWait(c, target.Record.ID, baseRPM, parsed.Stream, streamState, e.MessageQueueWait, log)
			// Queue failure is diagnostic here; retain any returned cleanup and continue
			// so serialization availability does not become a local request denial.
			if queueErr != nil {
				log.Warn("gateway.umq_acquire_failed", zap.Error(queueErr))
			}
		case policy.MessageQueueThrottle:
			// Throttle failure is reported as a warning and preparation continues; this
			// spacing path supplies no acquired serial lease to release later.
			if queueErr := e.MessageQueue.ThrottleWithPing(c, target.Record.ID, baseRPM, parsed.Stream, streamState, e.MessageQueueWait, log); queueErr != nil {
				log.Warn("gateway.umq_throttle_failed", zap.Error(queueErr))
			}
		}
	}
	// Share one lease-managed callback between cancellation, upstream acceptance
	// and outer exit, keeping serial ownership from being released repeatedly by
	// overlapping teardown paths.
	release = scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, release)
	parsed.OnUpstreamAccepted = release
	// Outer cleanup invokes the same accepted-release callback and removes it from
	// parsed input, so completed attempts cannot retain a callable queue release
	// in that request.
	cleanup := func() {
		if release != nil {
			release()
		}
		parsed.OnUpstreamAccepted = nil
	}
	if e.Anthropic != nil {
		// Compatibility replacement runs after possible queue admission; release that
		// ownership if the parsed body cannot be updated before platform forwarding
		// starts.
		if err = parsed.ReplaceBody(e.Anthropic.ApplyBedrockCCCompat(c, parsed.Body.Bytes(), parsed.Model, target, parsed.GroupID)); err != nil {
			cleanup()
			return nil, nil, false, err
		}
	}
	c.Set("parsed_request", parsed)
	return parsed, cleanup, false, nil
}
