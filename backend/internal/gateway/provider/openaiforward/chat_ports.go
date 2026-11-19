package openaiforward

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// chat_ports.go defines the provider capabilities and callbacks used to
// prepare, forward, and read Chat requests.
// A Chat URL can receive either Chat messages or Responses input, and the
// resolved provider protocol determines which representation reaches the
// upstream. Chat bindings expose shape conversion, model-aware cache identity,
// OAuth normalization, and response reading while the runner owns route
// selection and recovery order.

// ChatProfile combines selected provider capabilities with resolved Chat dispatch observations.
type ChatProfile struct {
	MessagesProfile // selected provider capability snapshot
	// resolved upstream protocol identifier
	Protocol protocol.ProtocolID
	// Adaptive selects request-shape dispatch; NativeResponsesSupported permits
	// Responses-shaped input without Chat conversion. ProtocolSlotDefaults keeps
	// endpoint selection with configured slots instead of the generic Chat default.
	// OpenAIAPIKey identifies the OpenAI credential path used for cache isolation.
	Adaptive, NativeResponsesSupported, ProtocolSlotDefaults, OpenAIAPIKey bool
}

// ChatPorts supplies stateful request preparation, routing, and Responses-to-Chat output for one attempt.
type ChatPorts interface {
	// PrepareChat resolves the provider attempt and resets its request
	// observations before identity preparation. Later preparation can fail after
	// provider state is replaced; successful profiles determine the runner's
	// dispatch route.
	PrepareChat(context.Context) (ChatProfile, error)
	// DispatchChat delegates another selected protocol route with the current body
	// and cache identity.
	DispatchChat(context.Context, Dispatch, []byte, string, string) (*Result, error)
	// APIKeyID reads the authenticated downstream key identifier for request
	// isolation.
	APIKeyID() int64
	// ProviderIdentity rewrites client metadata using the selected provider
	// namespace and downstream key. The supplied map is writable and stays bound
	// to this attempt while later header construction uses that identity.
	ProviderIdentity(body map[string]any, key int64)
	// BillingModel selects the charge model using provider mapping and a caller
	// default.
	BillingModel(model, fallback string) string
	// CyberPolicy reports whether this attempt recorded an upstream cyber-policy
	// warning.
	CyberPolicy() bool
	// FailoverHTTP applies provider failure policy before final client error
	// presentation. A nonnil result returns recovery guidance to the outer
	// provider loop; nil keeps the current response for its protocol presenter.
	FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error
	// IsAgentIdentity reports whether the selected provider uses agent-task
	// execution credentials.
	IsAgentIdentity(ctx context.Context) bool
	// ReadUpstreamError captures bounded upstream error bytes and closes the
	// consumed network body. The installed adapter replaces the body for later
	// presentation and returns a query-sanitized explanation alongside captured
	// bytes.
	ReadUpstreamError(r *http.Response) ([]byte, string)
	// RecoverAgentTask delegates task recovery using the selected provider's
	// current credential state.
	RecoverAgentTask(ctx context.Context) error
	// RedactErrorBody removes selected provider credential evidence from captured
	// error bytes.
	RedactErrorBody(ctx context.Context, body []byte) []byte
	// ResolvedServiceTier resolves the completion tier using observed output and
	// outgoing intent.
	ResolvedServiceTier(tier *string) *string
	// PrepareTransport resolves the current provider proxy before its next request
	// exchange.
	PrepareTransport()
	// Send exchanges a prepared request after current provider transport
	// preparation. The runner owns a successful response body until reading or
	// HTTP error handling ends, including recursive recovery paths.
	Send(r *http.Request) (*http.Response, error)
	// Sink returns the synchronous client output destination for this provider
	// attempt.
	Sink() upstream.OutputSink
	// ToolNameReverse publishes copied active-turn names and clears future-session
	// names for this request. Keep the supplied map stable while the setter copies
	// it; output readers later borrow the published copy.
	ToolNameReverse(value map[string]string)
	// TransportError presents exchange failure and returns its request or provider
	// recovery signal.
	TransportError(ctx context.Context, err error) error
	// UpdateCodexUsage observes Codex rate-limit headers for the selected provider
	// record.
	UpdateCodexUsage(ctx context.Context, h http.Header)
	// UpstreamModel normalizes the charge model for the selected upstream
	// provider.
	UpstreamModel(model string) string
	// ClientAllowed reports client admission using request bytes and the provider
	// TLS policy.
	ClientAllowed(ctx context.Context, body []byte) bool
	// PolicyDenied records local client policy rejection in the request's
	// diagnostics.
	PolicyDenied()
	// Reject writes the local protocol failure response for the current request.
	Reject(status int, kind, message string)
	// ResponsesToChat converts the supplied request using bound reasoning-history
	// lookup for tool continuation.
	ResponsesToChat(r *protocolopenai.ResponsesRequest) (*protocolopenai.ChatCompletionsRequest, error)
	// ChatError commits response state and writes the dedicated Chat rejection for
	// preparation failure.
	ChatError(status int, kind, message string)
	// DefaultChat reports whether the current attempt policy selects ordinary Chat
	// execution.
	DefaultChat() bool
	// IsolateCacheKey namespaces the generated prompt seed with the authenticated
	// downstream key.
	IsolateCacheKey(key string) string
	// NormalizeBodyTier returns Responses bytes and their normalized service tier
	// for completion attribution.
	NormalizeBodyTier(body []byte) ([]byte, string, error)
	// ChatToResponses resolves model capabilities and converts Chat to Responses
	// using their options. Capability publication can precede a later conversion
	// failure; callers return errors before constructing the upstream request.
	ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error)
	// NormalizeRequestTier updates the writable Responses request's service-tier
	// value in place.
	NormalizeRequestTier(r *protocolopenai.ResponsesRequest)
	// ApplyChatFast applies provider fast-tier policy to the outgoing converted
	// request bytes.
	ApplyChatFast(ctx context.Context, model string, body []byte) ([]byte, error)
	// AccessToken resolves the current selected provider's credential for upstream
	// request construction.
	AccessToken(ctx context.Context) (string, error)
	// BuildChat constructs the selected provider's streaming Responses request
	// from prepared bytes. Callers supply the detached upstream context and
	// credential; the builder borrows request state for synchronous header
	// construction.
	BuildChat(ctx context.Context, body []byte, token, key string) (*http.Request, error)
	// UpstreamSessionKey namespaces a prompt seed by downstream key and current
	// Codex identity.
	UpstreamSessionKey(id int64, key string) string
	// ChatErrorResponse applies shared Chat failure rules and converts any
	// returned observation. The execution caller retains response-body cleanup and
	// honors returned recovery guidance before another provider attempt.
	ChatErrorResponse(r *http.Response, model string) (*Result, error)
	// ChatResponseOptions binds Chat response reading to request state, provider,
	// and model identities.
	ChatResponseOptions(r *http.Response, original, billing, model string) openai.ChatResponseOptions
}
