package execution

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// types.go defines synchronous text execution requests, results, and the
// executor interface. Text execution carries authenticated access, routing
// choices, and protocol-specific input from an adapter into a synchronous
// request call. These value structs retain their nested pointers, maps, and
// byte slices; adapters prepare those inputs before execution and choose when
// to snapshot results for asynchronous completion.

// Text kinds select the protocol-specific request bridge and provider-loop
// options; ordinary Messages is the zero value used by default request state.
const (
	TextMessages        TextKind = iota // ordinary Messages request execution
	TextGeminiMessages                  // Messages converted to Gemini execution
	TextNativeGemini                    // Gemini native request execution
	TextOpenAIResponses                 // OpenAI Responses request execution
	TextOpenAIChat                      // OpenAI Chat request execution
	TextOpenAIMessages                  // Messages using OpenAI execution
)

// Executor binds shared dependencies at construction and receives request
// state and synchronous output per call.
type Executor interface {
	// Execute runs one request using bound dependencies and a synchronous output
	// sink. Callers keep retained request inputs stable during the call; results
	// can include attempt observations together with an execution error.
	Execute(context.Context, Request, upstream.OutputSink) (ExecutionResult, error)
}

// TextKind selects the protocol-specific text execution and attempt bridge.
type TextKind uint8

// TextState retains adapter-prepared text inputs for its selected execution
// kind.
type TextState struct {
	AlternateBudget      bool            // use alternate provider switch budget
	SelectionContext     context.Context // adapter-provided provider selection context
	SelectionSessionHash string          // sticky hash used for selection
	Action               string          // Gemini native request action name
	UseDigestFallback    bool            // Gemini digest recovery is enabled
	// DigestChain, PrefixHash, SessionUUID, and MatchedDigestChain describe one
	// Gemini digest recovery lookup. The adapter captures these together after
	// sticky lookup misses, and the native attempt uses that prepared evidence.
	// DigestChain is the ordered current content digest chain.
	// PrefixHash identifies the caller/model lookup partition.
	// SessionUUID is the recovered provider-session UUID.
	// MatchedDigestChain is the matching prior digest chain.
	DigestChain, PrefixHash, SessionUUID, MatchedDigestChain string
	SignatureState                                           requeststate.GeminiSignatureState // Gemini thought-signature routing state
	SessionHashBody                                          []byte                            // payload used for session hashing
	// ForwardModel, PreviousResponseID, ProviderLayerModel, and PromptCacheKey
	// carry distinct prepared request roles. Attempt adapters retain these values
	// while resolving the chosen provider, avoiding another interpretation of
	// client input.
	// ForwardModel is the model prepared for forwarding.
	// PreviousResponseID identifies the requested response continuation.
	// ProviderLayerModel is the model entering provider policy.
	// PromptCacheKey identifies the client prompt cache scope.
	ForwardModel, PreviousResponseID, ProviderLayerModel, PromptCacheKey string
	// NativeCompactionV2, LegacyCompact, and RequireCompact carry independently
	// prepared compaction choices into OpenAI attempts. The adapter resolves these
	// flags before execution, and provider capability checks use the retained
	// requirement.
	// NativeCompactionV2 selects the native compaction request form.
	// LegacyCompact marks the legacy compact request form.
	// RequireCompact requests a compact-capable provider target.
	NativeCompactionV2, LegacyCompact, RequireCompact bool
	RequiredCapability                                provider.OpenAIEndpointCapability // required OpenAI endpoint capability
	RoutingStart                                      time.Time                         // routing phase start wall-clock time
	Mapping                                           routing.GroupMappingResult        // resolved group model mapping result

	Kind            TextKind                    // selected text execution kind
	Parsed          *requeststate.ParsedRequest // retained parsed client request pointer
	Platform        string                      // adapter-selected request platform label
	BoundProviderID int64                       // previously bound session provider identifier
	HasBoundSession bool                        // sticky provider binding was found
	GeminiBody      []byte                      // mapped Gemini request payload bytes
	GeminiModel     string                      // prepared Gemini forward model name
}

// Request carries authenticated, routed input for one synchronous gateway
// execution.
type Request struct {
	Hints       requeststate.ExecutionHints // prepared request execution policy hints
	Routing     requeststate.RoutingState   // current request routing state
	Access      *apikey.AccessSnapshot      // authenticated request access snapshot pointer
	Route       routing.RoutePlan           // resolved client request route plan
	UserID      int64                       // authenticated funding subject user identifier
	Concurrency int                         // request user concurrency slot allowance
	Stream      bool                        // client requested streamed response output
	Body        []byte                      // prepared client request payload bytes
	Model       string                      // request model used by execution
	Metadata    RequestMetadata             // captured request adapter observations
	Funding     FundingState                // resolved request funding input references
	SessionHash string                      // request sticky selection hash value
	AttemptBody []byte                      // prepared protocol attempt payload bytes

	Text TextState // protocol-specific text execution input state
}

// RequestMetadata retains adapter-observed headers, client identity hints,
// endpoints, and arrival time.
type RequestMetadata struct {
	Headers          map[string][]string // captured inbound HTTP header values
	UserAgent        string              // inbound client User-Agent value
	ClientIP         string              // resolved inbound client address
	InboundEndpoint  string              // client protocol endpoint label
	UpstreamEndpoint string              // observed upstream endpoint label

	ClaudeCode bool      // adapter recognized Claude Code client
	StartedAt  time.Time // request arrival wall-clock time
}

// FundingState retains the resolved API Key and subscription used for
// admission.
type FundingState struct {
	// Key and Subscription retain pointers from resolved admission. These structs
	// copy the references by value; callers keep their funding evidence stable
	// while execution or capture consumes it, or prepare a separate snapshot.
	Key          *apikey.APIKey            // resolved API Key funding pointer
	Subscription *billing.UserSubscription // resolved subscription funding pointer
}

// ExecutionResult carries the final observed attempt and its selected provider
// evidence.
type ExecutionResult struct {
	// PlanProvided reports whether Plan carries adapter-obtained candidate evidence.
	PlanProvided bool

	Attempt  upstream.AttemptResult    // final executed attempt observations
	Provider provider.ProviderSnapshot // final selected provider snapshot
	Plan     routing.CandidatePlan     // final selected candidate route plan
	Attempts int                       // number of executed upstream attempts
}
