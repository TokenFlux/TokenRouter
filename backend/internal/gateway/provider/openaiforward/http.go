package openaiforward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// http.go executes Responses HTTP exchanges and manages repairs within the
// selected provider attempt.
// An OpenAI HTTP attempt can recover an invalid agent task, rejected
// ciphertext, unsupported fields, or a Compact-model failure before final error
// output. Those mechanisms have separate retry state so one repair does not
// consume another repair's opportunity, and rejected-field state tracks adopted
// payloads to prevent repeated normalization of the same body.
//
// Error responses are read and their network bodies closed before replay or
// presentation. Successful response bodies stay owned by the runner while
// stream or buffered readers use them; Compact signals can close a failed
// reader body before selecting another exchange. Completion metadata is
// assembled after reading succeeds, using the final exchanged model and any
// observed image usage.

// HTTPInput contains the request snapshot after group and provider processing.
type HTTPInput struct {
	// Body and LineageEntryBody refer to distinct preparation stages: exchange
	// repairs replace Body, while ciphertext rejection marks the entry bytes
	// retained in LineageEntryBody.
	Body, LineageEntryBody []byte // Body is exchange bytes; LineageEntryBody is rejected-lineage evidence
	ProviderID             int64  // selected provider identifier
	// ProviderName is the configured name; Platform is the provider platform.
	ProviderName, Platform string
	// RequestedModel is the pre-mapping request model; OriginalModel is the
	// response model; BillingModel is the charge model; UpstreamModel is the
	// exchange model.
	RequestedModel, OriginalModel, BillingModel, UpstreamModel string
	ReasoningEffort                                            *string // reported reasoning effort
	ReasoningEffortValue                                       string  // response-reader effort hint
	// ImageBillingModel is the image charge model; ImageSizeTier is the output
	// tier; ImageInputSize is the input-image tier.
	ImageBillingModel, ImageSizeTier, ImageInputSize string
	// ImageMainModelAdapted permits terminal configuration errors for an automatically
	// selected image driver; explicit client text models retain ordinary failover.
	ImageMainModelAdapted bool
	// Stream chooses streaming output; OAuth enables usage snapshots; Shadow
	// suppresses OAuth snapshots; Grok identifies the platform branch.
	Stream, OAuth, Shadow, Grok bool
	StartedAt                   time.Time // forwarding duration origin
}

// Result contains output observations passed to gateway completion processing.
// Pointer, header-map, map, and slice fields retain the storage supplied by
// readers or adapters; conversion callers copy mutable data before changing it.
type Result struct {
	// UpstreamResponseModel records the upstream declaration before client model
	// restoration; it stays empty when the reader observes no vendor model.
	UpstreamResponseModel    string
	UpstreamEndpoint         string      // actual request endpoint
	RequestedReasoningEffort *string     // captured requested effort
	OpenAIWSMode             bool        // WebSocket execution indicator
	UpstreamTerminalEvent    string      // observed terminal event
	ResponseHeaders          http.Header // completion response headers
	// ImageOutputSize is the observed output tier; ImageSizeSource is its
	// resolution source.
	ImageOutputSize, ImageSizeSource string
	ImageSizeBreakdown               map[string]int               // image counts by size
	UpstreamWarning                  *forwardcore.UpstreamWarning // observed upstream warning
	VideoCount                       int                          // observed generated videos
	VideoResolution                  string                       // generated video resolution
	// VideoDurationSeconds is generated video duration in seconds; WebSearchCalls
	// is the billable search-call count.
	VideoDurationSeconds, WebSearchCalls int
	AudioUsage                           *protocol.AudioUsage // protocol audio usage

	ClientDisconnect bool // observed client disconnect
	SearchCount      int  // observed search operations
	// RequestID is the vendor request identifier; ResponseID is the generated
	// response identifier.
	RequestID, ResponseID string
	Headers               http.Header       // borrowed upstream response headers
	Usage                 wire.ForwardUsage // inclusive OpenAI token quantities
	// NativeUsage holds normalized uncached input and cache buckets from Messages.
	// Completion prefers these complete observations, including cache TTL and speed.
	NativeUsage *protocol.TokenUsage
	// Model is the client response model; BillingModel is the charge model;
	// UpstreamModel is the request model; UpstreamResponseServiceTier is the
	// vendor-observed tier.
	Model, BillingModel, UpstreamModel, UpstreamResponseServiceTier string
	// ServiceTier is the resolved charge tier; ReasoningEffort is the effective
	// reasoning effort.
	ServiceTier, ReasoningEffort *string
	Stream                       bool          // client streaming choice
	Duration                     time.Duration // elapsed request duration
	// Timings is captured from the shared observer after response processing.
	// Callers retain this immutable exchange snapshot until completion copies it for persistence.
	Timings         *timing.Performance
	ImageCount      int // observed generated images
	ImageFrameCount int // encoded output frame billing units
	// ImageSize is the generated-image tier; ImageInputSize is the input-image
	// tier.
	ImageSize, ImageInputSize string
	ImageOutputSizes          []string // observed image size list
}

// CompactFailure holds a compaction recovery signal from a response reader.
type CompactFailure struct {
	Message string // reported Compact failure
	Payload []byte // captured Compact error bytes
}

// HTTPOptions supplies exchange and response operations; RunHTTP owns retry order and recovery budgets.
type HTTPOptions struct {
	Exchange openai.HTTPExchangeOptions // authenticated HTTP exchange callbacks
	Sink     upstream.OutputSink        // synchronous client output destination
	// StreamOptions supplies reader policy when Stream is true; the runner calls
	// it for the response accepted by each iteration.
	StreamOptions func() openai.StreamOptions
	// NonStreamOptions supplies buffered-reader policy when Stream is false; the
	// runner calls it for the response accepted by each iteration.
	NonStreamOptions func() openai.NonStreamOptions
	// ReadErrorBody retains bounded failure bytes, including partial captures when
	// reading fails; RunHTTP closes and replaces the consumed body before recovery
	// checks.
	ReadErrorBody    func(*http.Response) []byte
	IsAgentIdentity  func(context.Context) bool           // agent credential classification callback
	InvalidAgentTask func(int, []byte) bool               // invalid agent-task classifier
	RecoverAgentTask func(context.Context) error          // provider task-recovery callback
	RedactErrorBody  func(context.Context, []byte) []byte // provider-specific error-body redactor
	ErrorDetails     func([]byte) (string, string)        // vendor message and code extractor
	RetryEncrypted   func([]byte) ([]byte, bool, error)   // ciphertext-removal repair callback
	// MarkInvalidLineage receives LineageEntryBody after a ciphertext repair is
	// adopted; it records the rejected history for later requests.
	MarkInvalidLineage func([]byte)
	// CompactRetry receives the current body, rejection evidence, and spent
	// Compact budget; a true result adopts its body and model for another
	// exchange.
	CompactRetry         func([]byte, int, string, []byte, bool) ([]byte, string, bool)
	CompactRetryObserved func(*http.Response, []byte, string) // Compact recovery diagnostic callback
	CompactSignal        func(error) (CompactFailure, bool)   // reader-error Compact signal decoder
	// Compact error response constructor
	CompactErrorResponse func(*http.Response, CompactFailure) (*http.Response, []byte)
	ShouldFailover       func(int, string, []byte) bool // HTTP provider-switch classifier
	// HTTPFailover applies health handling for a candidate HTTP failure; nil
	// continues final presentation, while a nonnil error returns to the caller.
	HTTPFailover func(*http.Response, []byte, string, string) error
	// Compact provider-switch error constructor
	CompactFailover      func(*http.Response, []byte, string, string) error
	ErrorResponse        func(*http.Response, []byte, string) error // final HTTP failure presenter
	ErrorSchedulingModel func(string, string) string                // Compact error model selector
	// WrapResponseBody may replace a successful response body before reading; its
	// wrapper retains body closure for the runner's deferred cleanup.
	WrapResponseBody func(*http.Response)
	// ReleaseDecodedRequest drops the prepared decoder reference after successful
	// headers and before response reading; repair callbacks retain their own
	// required state.
	ReleaseDecodedRequest func()
	BindResponseOwner     func(context.Context, string)      // completed response ownership binder
	UpdateUsageSnapshot   func(context.Context, http.Header) // OAuth rate-limit snapshot observer
	ObserveUpstreamModel  func(string)                       // adopted recovery model observer
	ObservedServiceTier   func() string                      // vendor declared tier reader
	ResolvedServiceTier   func(*string) *string              // effective charge tier resolver
	ExtractServiceTier    func([]byte) *string               // outbound payload tier reader
	Log                   func(string, ...any)               // provider recovery diagnostic writer
}

// RunHTTP executes HTTP exchanges and returns completion data after its
// response reader succeeds. Callbacks are required on the paths that use them,
// and each returned network body is closed by the runner. Agent recovery,
// ciphertext removal, rejected-field normalization, and Compact model recovery
// have separate state.
//
// Repairs adopt a new body before another exchange; ciphertext recovery also
// marks the entry lineage, and Compact recovery updates the effective upstream
// model. Rejected-field state prevents replay of a remembered payload. Health
// handling can decline an HTTP failover by returning nil, after which final
// presentation uses the current response. A reader error returns before
// ownership binding, usage snapshots, and successful result assembly unless its
// Compact signal selects a recovery exchange.
func RunHTTP(ctx context.Context, input HTTPInput, o HTTPOptions) (*Result, error) {
	body := input.Body
	upstreamModel := input.UpstreamModel
	encryptedRetried, compactRetried, agentRetried := false, false, false
	rejected := openai.NewOpenAIResponsesRejectedFieldRetryState(body)
	for {
		resp, err := openai.ExchangeHTTP(ctx, body, o.Exchange)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			payload := o.ReadErrorBody(resp)
			// Close the consumed network body before any recovery exchange; the
			// replacement reader retains captured bytes for final error presentation.
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			// Agent-task rejection spends its own one-recovery budget before replay, so
			// repeated task failures reach ordinary error handling on the next response.
			if !agentRetried && o.IsAgentIdentity(ctx) && o.InvalidAgentTask(resp.StatusCode, payload) {
				agentRetried = true
				if err := o.RecoverAgentTask(ctx); err != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
				}
				continue
			}
			payload = o.RedactErrorBody(ctx, payload)
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			message, code := o.ErrorDetails(payload)
			// An explicit ciphertext rejection permits one adopted removal; mark the
			// entry lineage only after changed bytes are available for replay.
			if !encryptedRetried && resp.StatusCode == http.StatusBadRequest && code == "invalid_encrypted_content" {
				retryBody, changed, retryErr := o.RetryEncrypted(body)
				if retryErr != nil {
					return nil, retryErr
				}
				if changed {
					body = retryBody
					o.MarkInvalidLineage(input.LineageEntryBody)
					encryptedRetried = true
					rejected.Remember(body)
					o.Log("[OpenAI] Retrying non-WSv2 request once after invalid_encrypted_content (provider: %s)", input.ProviderName)
					continue
				}
				o.Log("[OpenAI] Skip non-WSv2 invalid_encrypted_content retry because encrypted state items are missing (provider: %s)", input.ProviderName)
			}
			// Rejected-field repair uses remembered payloads to bound repeated
			// normalization while retaining the independent ciphertext and Compact
			// recovery opportunities.
			if retryBody, reason, changed, retryErr := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, payload); retryErr != nil {
				return nil, fmt.Errorf("normalize rejected Responses field retry body: %w", retryErr)
			} else if changed && rejected.Allow(retryBody) {
				body = retryBody
				o.Log("[OpenAI] Retrying non-WSv2 request after %s (provider: %s)", reason, input.ProviderName)
				continue
			}
			// A Compact HTTP rejection can select a different upstream model once;
			// observe the replacement model before the next exchange and its
			// diagnostics.
			if retryBody, model, retry := o.CompactRetry(body, resp.StatusCode, message, payload, compactRetried); retry {
				o.CompactRetryObserved(resp, payload, message)
				from := strings.TrimSpace(gjson.GetBytes(body, "model").String())
				body = retryBody
				upstreamModel = model
				compactRetried = true
				o.ObserveUpstreamModel(model)
				o.Log("[OpenAI] Retrying explicit compact request once with fallback model (provider: %s, from: %s, to: %s, upstream_code: %s)", input.ProviderName, from, model, code)
				continue
			}
			if o.ShouldFailover(resp.StatusCode, message, payload) {
				// Health may request generic presentation even when the status is a
				// failover candidate; a nil callback result keeps that response for final
				// output.
				if err := o.HTTPFailover(resp, payload, message, upstreamModel); err != nil {
					return nil, err
				}
			}
			return nil, o.ErrorResponse(resp, body, input.BillingModel)
		}
		// Capture this iteration's response for eventual cleanup; Compact recovery
		// also closes it before exchange, so wrappers can receive repeated closes.
		defer func() { _ = resp.Body.Close() }()
		o.WrapResponseBody(resp)
		tier := o.ExtractServiceTier(body)
		o.ReleaseDecodedRequest()
		var deliveryErr error
		var usage *wire.ForwardUsage
		var performance *timing.Performance
		responseID := ""
		imageCount := 0
		imageFrameCount := 0
		var imageSizes []string
		if input.Stream {
			result, readErr := openai.ReadStreamingResponse(ctx, resp, upstream.NewOutputContext(o.Sink), o.StreamOptions(), input.StartedAt, input.OriginalModel, upstreamModel, input.ReasoningEffortValue)
			if readErr != nil && !errors.Is(readErr, wire.ErrGIFResultInvalid) {
				// A withheld Compact stream failure carries HTTP-like evidence; close the
				// failed reader body before either model replay or final failure
				// presentation.
				if signal, ok := o.CompactSignal(readErr); ok {
					if retryBody, model, retry := o.CompactRetry(body, http.StatusBadRequest, signal.Message, signal.Payload, compactRetried); retry {
						if resp.Body != nil {
							_ = resp.Body.Close()
						}
						o.CompactRetryObserved(resp, signal.Payload, signal.Message)
						body = retryBody
						upstreamModel = model
						compactRetried = true
						o.ObserveUpstreamModel(model)
						continue
					}
					if resp.Body != nil {
						_ = resp.Body.Close()
					}
					compactResp, compactBody := o.CompactErrorResponse(resp, signal)
					if o.ShouldFailover(compactResp.StatusCode, signal.Message, compactBody) {
						return nil, o.CompactFailover(compactResp, compactBody, signal.Message, upstreamModel)
					}
					return nil, o.ErrorResponse(compactResp, body, o.ErrorSchedulingModel(input.BillingModel, upstreamModel))
				}
				return nil, readErr
			}
			deliveryErr = readErr
			usage = result.Usage
			performance = timing.PerformanceFromContext(ctx)
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageFrameCount = result.ImageFrameCount
			imageSizes = result.ImageOutputSizes
		} else {
			result, readErr := openai.ReadNonStreamingResponse(ctx, resp, upstream.NewOutputContext(o.Sink), o.NonStreamOptions(), input.OriginalModel, upstreamModel)
			if readErr != nil && !errors.Is(readErr, wire.ErrGIFResultInvalid) {
				// Buffered Compact decoding can request a replacement model; release the
				// current body before replay because its reader has already reported
				// failure.
				if signal, ok := o.CompactSignal(readErr); ok {
					if retryBody, model, retry := o.CompactRetry(body, http.StatusBadRequest, signal.Message, signal.Payload, compactRetried); retry {
						_ = resp.Body.Close()
						body = retryBody
						upstreamModel = model
						compactRetried = true
						o.ObserveUpstreamModel(model)
						continue
					}
				}
				return nil, readErr
			}
			deliveryErr = readErr
			usage = result.Usage
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageFrameCount = result.ImageFrameCount
			imageSizes = result.ImageOutputSizes
		}
		o.BindResponseOwner(ctx, responseID)
		if input.OAuth && !input.Shadow {
			o.UpdateUsageSnapshot(ctx, resp.Header)
		}
		// Readers may finish without token observations; supply an empty usage value
		// so completion receives a consistent result shape for that successful
		// response.
		if usage == nil {
			usage = &wire.ForwardUsage{}
		}
		result := &Result{
			RequestID: resp.Header.Get("x-request-id"), ResponseID: responseID, Headers: resp.Header,
			Usage: *usage, Model: input.OriginalModel, BillingModel: input.BillingModel, UpstreamModel: upstreamModel,
			UpstreamResponseServiceTier: o.ObservedServiceTier(), ServiceTier: o.ResolvedServiceTier(tier), ReasoningEffort: input.ReasoningEffort,
			Stream: input.Stream, Duration: time.Since(input.StartedAt), Timings: performance,
		}
		// Observed generated images switch billing to the image identity and carry
		// size evidence, while text-only results retain the text billing model.
		if imageCount > 0 {
			result.ImageCount = imageCount
			result.ImageFrameCount = imageFrameCount
			result.ImageSize = input.ImageSizeTier
			result.ImageInputSize = input.ImageInputSize
			result.ImageOutputSizes = imageSizes
			result.BillingModel = input.ImageBillingModel
		}
		return result, deliveryErr
	}
}
