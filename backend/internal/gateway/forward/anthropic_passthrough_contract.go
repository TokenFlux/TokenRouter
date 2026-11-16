package forward

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// anthropic_passthrough_contract.go defines the inputs and adapter operations
// for Anthropic API-key passthrough. API-key passthrough retains the input
// model for result metadata while the selected provider receives its mapped
// model and filtered request body. A shared parsed request can receive those
// body changes before exchange so later body views and request hashes consult
// the forwarding representation.

// APIKeyInput supplies body, model, timing, and parsed-state values for passthrough.
type APIKeyInput struct {
	// Body retains the current passthrough bytes without an input-owned copy.
	// Filtering callbacks decide allocation, and the caller keeps the returned
	// representation available through the synchronous exchange.
	Body []byte
	// Parsed optionally receives body replacement before execution. It remains
	// shared with the request caller, whose field/range views are reparsed by ReplaceBody.
	Parsed *requeststate.ParsedRequest
	// RequestModel names the mapped provider request; OriginalModel retains the
	// logical input model before that passthrough mapping. Both are carried into
	// the result for later output and completion decisions.
	RequestModel, OriginalModel string    // provider request model; original input model
	RequestStream               bool      // requested streaming output mode
	StartTime                   time.Time // forwarding interval start time
}

// PassthroughPorts supplies operations for one selected provider exchange.
type PassthroughPorts interface {
	// Begin enters attempt activity and returns a release function after
	// admission succeeds. The forwarding caller defers that usable function
	// until credential, response, and error handling finish; admission errors
	// return before exchange work.
	Begin() (func(), error)
	// Credential acquires the selected provider's credentials and retains their
	// kind for this attempt. Errors stop preparation before proxy and transport
	// work, and API-key passthrough subsequently requires the exact apikey
	// kind.
	Credential(context.Context) error
	// TokenKind returns the credential kind retained by successful attempt
	// acquisition.
	TokenKind() string
	// ResolveProxy selects the configured provider proxy for the prepared
	// passthrough exchange.
	ResolveProxy()
	// MarkPassthrough records that this request uses the selected API-key
	// passthrough path.
	MarkPassthrough()
	// FilterSearchHistory removes server-tool history unsupported by the mapped
	// model. The caller passes the already resolved request model and retains
	// the returned bytes before updating the shared parsed body.
	FilterSearchHistory([]byte, string) []byte
	// InjectTTL reports whether per-Key or provider settings enable one-hour
	// cache-control TTL rewriting for the resolved passthrough model.
	InjectTTL(context.Context, string) bool
	// CacheTTL rewrites existing ephemeral cache-control markers in the supplied
	// passthrough body to use a one-hour TTL before parsed-body publication.
	CacheTTL([]byte) []byte
	// ReadErrorBody captures bytes from the current failed upstream response.
	// It can return partial bytes with a read error; the caller resets that
	// evidence for health policy before choosing final error handling.
	ReadErrorBody() ([]byte, error)
	// ResetErrorBody replaces a consumed response reader with captured bytes.
	// Health policy can reread this body, while the caller retains the byte
	// slice separately for observations and failover construction.
	ResetErrorBody([]byte)
	// Health classifies captured response evidence in retry or failover mode.
	// Mode can select a reader-consuming policy path; Generic delegates to
	// error handling, and RetrySameProvider is carried into the
	// status-qualified failover signal.
	Health(context.Context, string, int, map[string][]string, []byte, string) ErrorDecision
	// HandleError invokes direct or retry-exhausted Anthropic error handling
	// for the captured response. The boolean selects retry handling; the
	// returned result/error is propagated as the early stopped-exchange
	// outcome.
	HandleError(context.Context, string, bool) (*Result, error)
	// Observe submits passthrough failure metadata to the request observation
	// adapter.
	Observe(Notice)
	// FailoverError constructs a signal from source status, captured body, and
	// same-provider retry permission. It leaves resource handling with the
	// exchange executor and actual replay eligibility with the surrounding
	// request loop.
	FailoverError(int, []byte, bool) error
	// IsFailover reports whether the error chain contains a provider-failover
	// signal. Partial stream usage is omitted for such errors so a replacement
	// provider attempt owns the subsequent successful completion result.
	IsFailover(error) bool
	// Log writes prepared passthrough diagnostic text through the platform
	// logger.
	Log(string)
	// Truncate bounds valid UTF-8 diagnostic text by source bytes while
	// retaining complete characters. A nonpositive limit returns empty; the
	// caller supplies configured limits or a fixed routine excerpt budget.
	Truncate(string, int) string
	// ExecutePassthrough performs the selected exchange and output through
	// MessageHooks. Hooks run serially before return, and a successful response
	// invokes Before with nonnil metadata; the executor owns response closure
	// and stream reading.
	ExecutePassthrough(context.Context, *APIKeyInput, MessageHooks) (upstream.AttemptResult, error)
	// ServiceTier returns the request observer's selected upstream
	// execution-tier evidence. Empty means no usable tier was retained, and
	// completion reconciles a nonempty result with captured request policy and
	// credential-specific billing rules.
	ServiceTier() string
}
