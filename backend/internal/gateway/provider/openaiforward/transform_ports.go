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

// TransformPorts supplies model policy, mutable request transformations, and attempt observations.
type TransformPorts interface {
	// ModelAttributes resolves selected-model policy using request bytes and
	// Compact mode. The installed adapter publishes endpoint capabilities before
	// returning request-adjusted resolution, and errors stop transformation before
	// further policy edits.
	ModelAttributes(context.Context, string, []byte, bool) (modelrules.Resolution, error)
	// Reject records requested diagnostics and writes the selected local request
	// rejection.
	Reject(Rejection)
	// Log writes the supplied formatting message through the bound gateway logger.
	Log(string, ...any)
	// Decode returns a number-aware writable Responses object for synchronous
	// transformation. Parsing failures stop preparation; callers keep returned
	// mutable values stable through object edits and final serialization.
	Decode(body []byte) (map[string]any, error)
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
	// ImagePermissionMessage returns the client explanation for rejected group
	// image admission.
	ImagePermissionMessage() string
	// IsImageGenerationIntent reports broad image intent from endpoint, model, and
	// raw request bytes.
	IsImageGenerationIntent(endpoint, model string, body []byte) bool
	// IsExplicitImageGenerationIntent reports declared image intent used by group
	// permission checks.
	IsExplicitImageGenerationIntent(endpoint, model string, body []byte) bool
	// IsImageGenerationIntentMap reports broad image intent from the supplied
	// decoded request object.
	IsImageGenerationIntentMap(endpoint, model string, body map[string]any) bool
	// IsExplicitImageGenerationIntentMap reports declared image intent in the
	// supplied decoded request object.
	IsExplicitImageGenerationIntentMap(endpoint, model string, body map[string]any) bool
	// OpenAIRequestBodyImageGenerationToolNeedsNormalization reports whether raw
	// image tools need compatibility preparation.
	OpenAIRequestBodyImageGenerationToolNeedsNormalization(body []byte) bool
	// OpenAIRequestBodyHasImageGenerationDeclaration reports whether raw input
	// declares an image-generation tool.
	OpenAIRequestBodyHasImageGenerationDeclaration(body []byte) bool
	// EnsureOpenAIResponsesImageGenerationTool inserts the automatic bridge tool
	// into a writable body. Its result reports a mutation for serialization; the
	// caller applies group and provider admission before invoking insertion.
	EnsureOpenAIResponsesImageGenerationTool(body map[string]any) bool
	// EnsureOpenAIResponsesImageGenerationToolChoiceAuto applies automatic image
	// choice to a writable body. Its result reports a mutation for serialization;
	// the caller selects this operation when automatic bridge policy permits it.
	EnsureOpenAIResponsesImageGenerationToolChoiceAuto(body map[string]any) bool
	// ValidateCodexSparkInput checks image input against selected Spark model
	// capability restrictions.
	ValidateCodexSparkInput(body map[string]any, model string) error
	// ApplyCodexImageGenerationBridgeInstructions adds automatic bridge
	// instructions to a writable request body. A true result reports changed text
	// for serialization; callers invoke it after image-tool preparation under
	// bridge-enabled policy.
	ApplyCodexImageGenerationBridgeInstructions(body map[string]any) bool
	// CodexTransform applies OAuth-compatible preparation to the supplied writable
	// request map. Callers inspect its Error before adopting serialized bytes and
	// tool mappings; failed preparation can leave map changes visible.
	CodexTransform(body map[string]any, options openai.CodexOAuthTransformOptions) openai.CodexTransformResult
	// EnsureCodexOAuthInstructionsField inserts required instructions into the
	// writable OAuth request map.
	EnsureCodexOAuthInstructionsField(body map[string]any)
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
	// SanitizeOpenAIResponsesOrphanToolOutputs removes unmatched outputs when
	// continuation cannot provide their call context. The body is writable,
	// retained nested input values stay shared, and a true result requests
	// serialization.
	SanitizeOpenAIResponsesOrphanToolOutputs(body map[string]any, input []any, hasPrevious bool) bool
	// FirstNonEmptyString returns the first trimmed, nonblank string among the
	// supplied values. Nonstrings are ignored and no usable value returns empty,
	// allowing orphan-output cleanup to recognize an absent continuation ID.
	FirstNonEmptyString(values ...any) string
	// Marshal serializes the stable prepared map using the number-aware JSON
	// wrapper. Unsupported values and invalid numeric literals return errors, and
	// the runner adopts bytes only after serialization succeeds.
	Marshal(body map[string]any) ([]byte, error)
	// NormalizeTrigger places one compaction trigger after other input items in
	// raw request bytes. Its changed result selects replacement bytes; errors stop
	// final preparation before the result is returned.
	NormalizeTrigger(body []byte) ([]byte, bool, error)
}
