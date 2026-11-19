package openaiforward

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// transform_ports.go defines the policy and transformation callbacks used to
// prepare a Responses request.
// Responses transformations need model capability observations before they edit
// image tools, OAuth metadata, and billing controls. The bindings expose a
// number-aware decoder and marshaler for writable object changes, while the
// runner keeps byte patches, cached views, restoration names, and final header
// values synchronized for one provider attempt.

// TransformPorts supplies stateful model policy, provider identity, and fast-tier callbacks for one attempt.
type TransformPorts interface {
	// ModelAttributes resolves selected-model policy using request bytes and
	// Compact mode. The installed adapter publishes endpoint capabilities before
	// returning request-adjusted resolution, and errors stop transformation before
	// further policy edits.
	ModelAttributes(context.Context, string, []byte, bool) (modelrules.Resolution, error)
	// Reject records requested diagnostics and writes the selected local request
	// rejection.
	Reject(Rejection)
	// GroupImagePolicy resolves admitted group image handling over the inherited
	// provider value. The inherited policy remains when no relevant group override
	// applies, including a different declared client protocol.
	GroupImagePolicy(inherited string) string
	// ImageAllowed reports whether the admitted group permits Responses image
	// generation.
	ImageAllowed() bool
	// LiteHeader reports whether inbound HTTP headers request Responses Lite
	// execution.
	LiteHeader() bool
	// BridgeEnabled resolves automatic image bridge insertion from group,
	// provider, and process settings.
	BridgeEnabled(ctx context.Context) bool
	// ImageIntentHint resolves canonical image intent from retained client bytes
	// and model identity.
	ImageIntentHint(model string, body []byte) bool
	// Models resolves the billing model and upstream model for a supplied request
	// identity.
	Models(model string, compact bool) (string, string)
	// CompactModel reads the selected provider's configured Compact recovery model
	// for the request.
	CompactModel(model string) string
	// CodexTransform applies OAuth-compatible preparation to the supplied writable
	// request map. Callers inspect its Error before adopting serialized bytes and
	// tool mappings; failed preparation can leave map changes visible.
	CodexTransform(body map[string]any, options openai.CodexOAuthTransformOptions) openai.CodexTransformResult
	// ToolNameReverse publishes copied active-turn names and clears future-session
	// names for this request. Keep the supplied map stable while the setter copies
	// it; output readers later borrow the published copy.
	ToolNameReverse(mapping map[string]string)
	// ClientMetadata fills supported installation metadata on the writable
	// selected-provider body. Existing values follow the shared helper's retention
	// rules, and its boolean tells the runner whether object serialization is
	// needed.
	ClientMetadata(body map[string]any) bool
	// ProviderIdentity rewrites writable metadata using the selected Codex
	// namespace and key. The runner saves the caller cache seed first, and a
	// changed result marks the object for serialization.
	ProviderIdentity(body map[string]any) bool
	// ClearFingerprint clears any staged fingerprint IDs left by an earlier
	// request attempt.
	ClearFingerprint()
	// Fingerprint resolves credential identity and updates writable metadata
	// before deriving header IDs. It returns those same IDs and a change flag, and
	// the caller stages them for subsequent construction on this selected attempt.
	Fingerprint(ctx context.Context, body map[string]any) (*openai.FingerprintIDs, bool, error)
	// FastDecision returns outgoing tier edits or a policy denial for the selected
	// model.
	FastDecision(ctx context.Context, model, tier string, hasTier bool) FastDecision
	// FastBlocked writes the dedicated response for a recognized fast-tier policy
	// denial.
	FastBlocked(err error)
}
