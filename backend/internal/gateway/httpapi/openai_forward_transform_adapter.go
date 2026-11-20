package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_forward_transform_adapter.go binds Responses transformations to model
// capabilities and request identity policies.
// Responses conversion resolves model attributes for the selected endpoint
// before applying image, Codex and fast-tier policies. Compact changes the
// attribute target endpoint and can reuse the request's attribute session, so
// the final resolution must be installed before subsequent conversion options
// inspect provider capabilities.
//
// The adapter borrows writable decoded request maps for these transformations.
// Credential-derived client metadata and fingerprint IDs use the current
// request headers, with the resulting IDs returned for the caller's header
// stage. Image policy distinguishes explicit tools from automatic bridge
// injection, and namespace/tool reverse mappings remain request-local.
// Transformation errors return before the shared runner serializes and sends
// the prepared request.

// openAIForwardTransformAdapter extends Responses prelude bindings with
// request transformation capabilities.
type openAIForwardTransformAdapter struct {
	// openAIForwardPreludeAdapter shares the selected provider, live request and
	// execution policy context with the transformation phase for this attempt.
	openAIForwardPreludeAdapter
}

// ModelAttributes resolves the selected provider's Responses capabilities for
// model and request bytes. Compact changes the target endpoint to compact and
// resolves through an existing context attribute session or a newly created
// service session, using the current group ID when available.
//
// Successful resolution is installed in provider.Capabilities before
// request-sensitive ForRequest adjustment is returned. Later transformations
// therefore read the endpoint resolution while the returned value includes
// this body's request state. Lookup failures leave this method's capability
// publication untouched; callers use the returned resolution to drive
// conversion before sending.
func (p openAIForwardTransformAdapter) ModelAttributes(ctx context.Context, model string, body []byte, compact bool) (modelrules.Resolution, error) {
	result, err := gatewayadapter.ResolveModelAttributes(ctx, p.provider, model, p.s.Text.modelAttributes(), "openai_responses")
	if err != nil {
		WriteOpenAIModelAttributeError(p.c, err, p.c.Writer.Written())
		return modelrules.Resolution{}, err
	}
	// Compact capabilities use a distinct endpoint target. Resolve that target
	// under the current group/session before publishing attributes for later transforms.
	if compact {
		target := result.Target
		target.Endpoint = "compact"
		session := routing.ModelAttributesFromContext(ctx)
		// Direct conversion callers may lack the request's attribute session;
		// create one so Compact still resolves through the same model service policy.
		if session == nil {
			session = p.s.Text.modelAttributes().NewSession()
		}
		var groupID int64
		if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
			groupID = group.ID
		}
		result, err = session.Resolve(ctx, groupID, target)
		if err != nil {
			WriteOpenAIModelAttributeError(p.c, err, p.c.Writer.Written())
			return modelrules.Resolution{}, err
		}
	}
	if p.provider != nil {
		p.provider.Capabilities = &result
	}
	return result.ForRequest(gatewayadapter.ModelRequestState(body)), nil
}

// GroupImagePolicy resolves explicit image-tool handling from the current
// effective key group and admitted client protocol. The inherited provider
// policy survives when no relevant group override applies;
// ResponsesPolicyGroup excludes unrelated declared client protocols.
func (p openAIForwardTransformAdapter) GroupImagePolicy(inherited string) string {
	key := GetExecutionAPIKey(p.c)
	return gatewayadapter.GroupResponsesExplicitToolPolicy(gatewayadapter.ResponsesPolicyGroup(p.ctx, gatewayadapter.APIKeyGroup(key)), inherited)
}

// ImageAllowed reports group image admission from the current effective API
// key. Missing keys use routing's nil-group policy, while a present key
// supplies its admitted group to the same policy helper.
func (p openAIForwardTransformAdapter) ImageAllowed() bool {
	key := GetExecutionAPIKey(p.c)
	if key == nil {
		return routing.GroupAllowsResponsesImages(nil)
	}
	return routing.GroupAllowsResponsesImages(key.Group)
}

// LiteHeader delegates the inbound Responses Lite header check to the prelude.
func (p openAIForwardTransformAdapter) LiteHeader() bool {
	return p.openAIForwardPreludeAdapter.LiteHeader()
}

// BridgeEnabled resolves automatic image bridge injection using group,
// provider and process settings.
func (p openAIForwardTransformAdapter) BridgeEnabled(ctx context.Context) bool {
	return p.s.ImageBridge.Enabled(ctx, p.provider, GetExecutionAPIKey(p.c))
}

// ImageIntentHint resolves this request's canonical or mapped image-intent
// hint for transformation.
func (p openAIForwardTransformAdapter) ImageIntentHint(model string, body []byte) bool {
	return ResolveOpenAIImageIntentHint(p.c, model, body, gatewayadapter.ImageIntent().IsImageGenerationIntent)
}

// Models delegates requested/billing upstream model selection using the
// selected provider, active request context effort, and Compact flag.
func (p openAIForwardTransformAdapter) Models(model string, compact bool) (string, string) {
	policy := gatewayadapter.ExecutionModelPolicy(p.provider)
	if p.c != nil && p.c.Request != nil {
		policy = policy.WithContext(p.c.Request.Context())
	}
	return policy.ForwardMappedModels(model, compact)
}

// CompactModel resolves the selected provider's configured Compact model
// through shared recovery policy.
func (p openAIForwardTransformAdapter) CompactModel(model string) string {
	return p.s.Text.Compact.ResolveModel(p.provider, model)
}

// CodexTransform delegates OAuth request normalization with installed provider
// attributes and rules when available. The supplied writable map can change
// during that operation; callers inspect the returned Error before adopting
// serialization and restoration state.
func (p openAIForwardTransformAdapter) CodexTransform(body map[string]any, options openai.CodexOAuthTransformOptions) openai.CodexTransformResult {
	if p.provider != nil && p.provider.Capabilities != nil {
		options.Attributes = &p.provider.Capabilities.Attributes
		options.Rules = &p.provider.Capabilities.Rules
	}
	return gatewayadapter.ApplyCodexOAuthTransformWithOptions(body, options)
}

// ToolNameReverse publishes the request-local reverse names returned by Codex
// transformation.
func (p openAIForwardTransformAdapter) ToolNameReverse(mapping map[string]string) {
	SetCodexToolNameReverse(p.c, mapping)
}

// ClientMetadata delegates installation metadata filling for this selected
// provider and writable body. Existing nonblank IDs and unsupported metadata
// shapes follow the shared helper's retention rules; the result indicates
// whether it changed request metadata.
func (p openAIForwardTransformAdapter) ClientMetadata(body map[string]any) bool {
	return gatewayadapter.ApplyCodexClientMetadata(body, p.provider)
}

// ProviderIdentity applies the selected Codex identity namespace and API-key
// ID to client metadata. Callers supply the writable request map and retain
// current request identity state while the upstream helper updates that
// representation.
func (p openAIForwardTransformAdapter) ProviderIdentity(body map[string]any) bool {
	return openai.ApplyCodexProviderIdentityClientMetadataMap(body, provideradapter.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.provider.View())), APIKeyIDFromContext(p.c))
}

// ClearFingerprint replaces request-local staged fingerprint IDs with an
// absent value before transformation.
func (p openAIForwardTransformAdapter) ClearFingerprint() {
	StageCodexFingerprintIDs(p.c, nil)
}

// Fingerprint resolves the credential provider and applies installation
// metadata before deriving request-header fingerprint IDs. Credential
// resolution errors return before mutation; later body edits combine their
// change flags, and the returned IDs belong to the caller's subsequent header
// stage.
//
// Request headers may be absent, in which case the provider fingerprint helper
// receives nil. This method returns the same derived IDs used to update body
// metadata, allowing the caller to apply that identity to the outgoing
// headers. The writable map is borrowed for synchronous edits, and caller
// staging keeps those IDs attached to this attempt.
func (p openAIForwardTransformAdapter) Fingerprint(ctx context.Context, body map[string]any) (*openai.FingerprintIDs, bool, error) {
	provider, err := gatewayadapter.CredentialProvider(ctx, p.s.Requests.Providers, p.provider)
	if err != nil {
		return nil, false, fmt.Errorf("resolve Codex fingerprint provider: %w", err)
	}
	// Resolve credential identity before editing metadata, then use one derived
	// fingerprint for body metadata and the caller's later outgoing header stage.
	changed := gatewayadapter.ApplyCodexClientMetadata(body, provider)
	var headers http.Header
	if p.c != nil && p.c.Request != nil {
		headers = p.c.Request.Header
	}
	ids := provideradapter.CodexFingerprintIDsFromRequest(provider.View(), headers)
	if openai.ApplyCodexFingerprintClientMetadata(body, ids) {
		changed = true
	}
	return ids, changed, nil
}

// FastDecision delegates fast-tier resolution and translates its deletion,
// tier and optional rejection values for the shared transform. The blocked
// error pointer is retained as the returned error value; callers handle it
// before serializing or sending the body.
func (p openAIForwardTransformAdapter) FastDecision(ctx context.Context, model, tier string, hasTier bool) forward.FastDecision {
	decision := tierpolicy.Resolve(p.s.Text.FastPolicy.Input(ctx, p.provider, model), tier, hasTier)
	value := forward.FastDecision{DeleteField: decision.DeleteField, Tier: decision.Tier}
	if decision.Blocked != nil {
		value.Blocked = decision.Blocked
	}
	return value
}

// FastBlocked writes the heartbeat-aware rejection when err contains a
// tierpolicy BlockedError. Other errors produce no output here; the transform
// caller returns the preparation failure and controls further error handling.
func (p openAIForwardTransformAdapter) FastBlocked(err error) {
	if blocked, ok := errors.AsType[*tierpolicy.BlockedError](err); ok {
		WriteFastPolicyBlockedResponse(p.c, blocked)
	}
}
