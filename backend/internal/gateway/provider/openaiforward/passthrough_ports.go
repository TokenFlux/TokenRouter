package openaiforward

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// passthrough_ports.go defines the callbacks for Responses passthrough
// preparation, transport, recovery, and output observations.
// Responses passthrough prepares body bytes with provider credentials, client
// tools, fingerprints, and image controls while retaining the client's output
// protocol. Request-bound callbacks supply platform policy and response
// observations; the runner decides when to replay rejected fields, recover
// agent tasks, or adopt a Compact model repair before final client output.

// PassthroughPorts binds stateful provider policy, request preparation, recovery, and output for one attempt.
type PassthroughPorts interface {
	// IsAgentIdentity reports whether the selected provider uses agent-task
	// execution credentials.
	IsAgentIdentity(ctx context.Context) bool
	// ReadErrorBody captures bounded available failure bytes and suppresses read
	// errors. The runner closes the consumed body and installs a replay copy
	// before recovery checks or final client presentation.
	ReadErrorBody(r *http.Response) []byte
	// RecoverAgentTask delegates task recovery using the selected provider's
	// current credential state.
	RecoverAgentTask(ctx context.Context) error
	// ResolvedServiceTier resolves the completion tier using observed output and
	// outgoing intent.
	ResolvedServiceTier(tier *string) *string
	// Sink returns the synchronous client output destination for this provider
	// attempt.
	Sink() upstream.OutputSink
	// UpdateCodexUsage observes Codex rate-limit headers for the selected provider
	// record.
	UpdateCodexUsage(ctx context.Context, h http.Header)
	// Profile returns the selected provider's platform, credential, and protocol
	// capability snapshot.
	Profile() MessagesProfile
	// CompactPath reports whether this passthrough request targets legacy
	// Responses Compact.
	CompactPath() bool
	// ForwardModel selects the provider-mapped vendor identity for the incoming
	// model and Compact mode.
	ForwardModel(model string, compact bool) string
	// PolicyDenied records local client policy rejection in the request's
	// diagnostics.
	PolicyDenied()
	// LogInstructionsRejected records request evidence for the selected Codex
	// instruction rejection.
	LogInstructionsRejected(ctx context.Context, model, reason string, body []byte)
	// Reject writes the client rejection with its status, category, message, and
	// offending parameter.
	Reject(status int, kind, message, param string)
	// ProviderIdentityRaw applies the selected provider namespace to raw client
	// metadata. A changed result replaces the request bytes before fingerprint
	// preparation; errors stop the runner before transport starts.
	ProviderIdentityRaw(body []byte) ([]byte, bool, error)
	// StageFingerprint replaces request-local header IDs with the supplied value,
	// including nil. The runner clears prior-attempt state before deriving this
	// provider's fingerprint and stages the result before request construction.
	StageFingerprint(ids *openai.FingerprintIDs)
	// Fingerprint derives the selected attempt's header fingerprint from provider
	// and incoming request identity.
	Fingerprint() *openai.FingerprintIDs
	// HasContext reports whether the passthrough adapter has a live inbound HTTP
	// context.
	HasContext() bool
	// LiteHeader reports whether inbound headers request Responses Lite
	// preparation for this attempt.
	LiteHeader() bool
	// CompatibilityBodyWithSkip applies Responses compatibility preparation with
	// the resolved Lite flag while skipping stages already completed on identical
	// prelude bytes.
	CompatibilityBodyWithSkip(body []byte, lite bool, skipToolSchemasPlatform string, skipReasoningReplay, skipInputItemIDs bool) ([]byte, bool, error)
	// MergeToolNames merges supplied reverse names over active-turn names and
	// publishes a copied map. Keep supplied entries stable during copying; later
	// restoration reads the newly published map for this request.
	MergeToolNames(value map[string]string)
	// AdaptClientTools lowers API Key client-tool shapes and records the output
	// restoration mapping.
	AdaptClientTools(body []byte) ([]byte, error)
	// NormalizeLite reapplies raw Lite rules after earlier OAuth or API Key
	// preparation. Its changed result replaces outgoing bytes so late
	// compatibility cleanup cannot lose the Lite-required controls.
	NormalizeLite(body []byte) ([]byte, bool, error)
	// ModelAttributes resolves the final passthrough route and retains its
	// capabilities for image billing. Lookup failures stop this attempt before
	// permission checks or transport can use incomplete model information.
	ModelAttributes(context.Context, string) (modelrules.Resolution, error)
	// ApplyFastPass applies provider fast-tier policy to the prepared outgoing
	// body and model.
	ApplyFastPass(ctx context.Context, model string, body []byte) ([]byte, error)
	// ImageIntent resolves broad image intent from canonical evidence and the
	// currently prepared payload.
	ImageIntent(model string, canonical []byte, policy string, body []byte, invalidated bool) bool
	// ImageAllowed reports whether the current group allows declared image-
	// generation requests.
	ImageAllowed() bool
	// FeatureDenied records a local feature-policy rejection for this request's
	// diagnostics.
	FeatureDenied()
	// ImageBilling resolves image charge identity and size tiers from request
	// bytes and model. Known capability errors are presented locally before the
	// callback returns, so the runner can stop without upstream error reporting.
	ImageBilling(body []byte, model string) (ImageBilling, error)
	// WarnTimeoutHeaders records streaming timeout-header diagnostics before the
	// authenticated exchange begins.
	WarnTimeoutHeaders(ctx context.Context)
	// AccessToken resolves the current selected provider's credential for upstream
	// request construction.
	AccessToken(ctx context.Context) (string, error)
	// PrepareTransportPass resolves provider transport state for the upcoming
	// passthrough exchange.
	PrepareTransportPass()
	// MarkPassthrough records selected passthrough execution on this request
	// attempt's diagnostics.
	MarkPassthrough()
	// RetryState seeds rejected-field recovery tracking with the prepared initial
	// request bytes.
	RetryState(body []byte) *openai.ResponsesRejectedFieldRetryState
	// UpstreamModelObserved records the body's actual model before each
	// passthrough exchange.
	UpstreamModelObserved(model string)
	// BuildPass constructs an authenticated passthrough request using this
	// selected provider and body. Callers release the returned upstream context
	// handle after construction and retain request state until sending finishes.
	BuildPass(ctx context.Context, body []byte, token string) (*http.Request, error)
	// SendPass exchanges a prepared request through the selected provider
	// transport. Successful response bodies stay owned by the runner through
	// reading, or are closed by adopted Compact signal recovery before replay.
	SendPass(r *http.Request) (*http.Response, error)
	// Latency records the upstream exchange duration before body reading starts
	// for diagnostics.
	Latency(d time.Duration)
	// TransportErrorPass presents exchange failure and returns recovery guidance
	// for the outer provider loop.
	TransportErrorPass(ctx context.Context, err error) error
	// CompactRetry selects eligible repaired bytes and a model using captured HTTP
	// failure evidence. The runner supplies whether its Compact budget is spent
	// and adopts the result only when retry is true.
	CompactRetry(model string, body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool)
	// CompactObserved records the selected provider's captured Compact rejection
	// before model replay.
	CompactObserved(r *http.Response, body []byte, message string)
	// ShouldFailover classifies passthrough failure evidence before final client
	// error presentation.
	ShouldFailover(status int, body []byte) bool
	// FailoverError applies health handling and builds typed provider-loop
	// recovery evidence from failure bytes.
	FailoverError(ctx context.Context, r *http.Response, body, payload []byte) error
	// ErrorResponsePass writes final passthrough failure using the current body
	// and captured payload.
	ErrorResponsePass(ctx context.Context, r *http.Response, body, payload []byte) error
	// WrapResponseBody installs response restoration when the request lowered
	// client tools. Its wrapper retains closure of the underlying response body
	// for the runner after successful reading or failure handling.
	WrapResponseBody(r *http.Response)
	// ResponseOptions returns request-bound passthrough reading, health, and
	// synchronous output callbacks.
	ResponseOptions(ctx context.Context) openai.PassthroughOptions
	// CompactFromSignal selects recovery for a recognized reader failure using the
	// prior-retry guard. An adopted repair closes the rejected response through
	// the installed recovery helper before returning bytes for another exchange.
	CompactFromSignal(model string, body []byte, err error, tried bool, r *http.Response) ([]byte, string, bool)
	// CompactErrorResponse constructs HTTP-like failure evidence from a captured
	// Compact reader signal.
	CompactErrorResponse(r *http.Response, v CompactFailure) (*http.Response, []byte)
	// BindOwner records completed response ownership for this selected provider
	// and authenticated request.
	BindOwner(ctx context.Context, id string)
	// ObservedServiceTier reads the vendor-declared service tier recorded during
	// response processing.
	ObservedServiceTier() string
}
