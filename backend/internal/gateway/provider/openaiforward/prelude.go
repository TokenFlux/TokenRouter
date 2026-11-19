package openaiforward

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// prelude.go prepares Responses request bytes and selects the protocol and
// transport path for a provider attempt.
// Responses preparation can lower tool declarations and historical calls
// differently according to provider protocol, Compact mode, and passthrough
// policy. Admission runs against the request after any group image blocking,
// while canonical image-intent bytes are retained before later compatibility
// normalization can remove evidence used by image policy. Request-local
// restoration mappings are cleared before a new provider attempt.
//
// The prepared body, its decoded view, and protocol route advance together
// through provider-specific checks. Grok, Anthropic, and raw Chat can return
// before the remaining OpenAI replay and transport policy steps. OpenAI paths
// rebuild the view after reasoning replay or item-ID repairs; passthrough image
// stripping marks intent invalidation so later processing knows to reconsider
// the retained canonical evidence.

// Dispatch values select one provider-attempt executor after common preparation;
// the zero value continues through the ordinary Responses execution path.
const (
	DispatchResponses   Dispatch = iota // ordinary Responses execution
	DispatchRawChat                     // raw Chat execution
	DispatchGrok                        // Grok Responses execution
	DispatchAnthropic                   // Anthropic protocol conversion
	DispatchPassthrough                 // Responses passthrough execution
)

// Dispatch identifies the selected executor for one prepared provider attempt.
type Dispatch uint8

// TransportDecision records the chosen upstream transport and its diagnostic reason.
type TransportDecision struct {
	// Transport is the selected transport mode; Reason is the selection explanation.
	Transport, Reason string
}

// Profile contains the selected provider's resolved protocol capabilities.
type Profile struct {
	// Platform is the upstream platform; Name is the configured provider name;
	// Type is the credential type.
	Platform, Name, Type string
	// ImageResponsesModel supplies the text driver only when adapting an image-only
	// top-level model. Explicit text models keep their separately resolved identity.
	ImageResponsesModel string
	UsesCodex           bool // Codex protocol selection flag
	// HTTPContinuation retains upstream response references on admitted native
	// HTTP Responses routes after the caller has validated ownership and affinity.
	HTTPContinuation bool
	// OpenAI identifies the platform; OAuth identifies OAuth credentials;
	// OAuthLike includes compatible credential types; APIKey identifies API Key
	// credentials.
	OpenAI, OAuth, OAuthLike, APIKey bool
	// Grok identifies the xAI platform; EncodeResponsesClientTools selects native
	// client-tool envelopes;
	// StatelessResponses selects removal of stored Responses continuation;
	// Anthropic selects Anthropic conversion; RawChat selects raw Chat
	// forwarding; ResolvedChat identifies the resolved Chat route; Passthrough
	// enables body passthrough.
	Grok, EncodeResponsesClientTools, StatelessResponses, Anthropic, RawChat, ResolvedChat, Passthrough bool
}

// Rejection describes the client response and diagnostic markers for local denial.
type Rejection struct {
	Status int // client HTTP status
	// Type is the protocol error category; Message is the client explanation;
	// Param is the offending parameter.
	Type, Message, Param string
	// PolicyDenied records policy rejection; FeatureDenied records feature
	// rejection; ObserveUpstream records upstream-facing error diagnostics.
	PolicyDenied, FeatureDenied, ObserveUpstream bool
}

// Prelude retains prepared bytes, routing observations, and the selected execution path.
type Prelude struct {
	// Body and OriginalBody retain adopted prepared bytes;
	// CanonicalImageIntentBody retains the post-group-blocking payload before tool
	// and replay normalization so later image checks can revisit intent.
	Body, OriginalBody, CanonicalImageIntentBody []byte                                 // Body is prepared bytes; OriginalBody is adopted attempt bytes; CanonicalImageIntentBody is image-intent input
	StartedAt                                    time.Time                              // attempt preparation start
	TLS                                          egress.TLSFingerprintRouterMatchResult // selected TLS routing match
	Transport                                    TransportDecision                      // resolved upstream transport decision
	// Compact selects the Compact path; MessagesBridge identifies converted
	// Messages input; CodexCLI identifies the client; ImageIntentInvalidated marks
	// later image-declaration stripping.
	Compact, MessagesBridge, CodexCLI, ImageIntentInvalidated bool
	ImageToolPolicy                                           string  // Codex image tool policy
	ReasoningEffort                                           *string // passthrough effective reasoning effort
	// View caches model and stream metadata after adopted replay and item-ID
	// repairs; final passthrough image stripping retains that already-decoded
	// view.
	View  requeststate.OpenAIRequestView
	Route Dispatch // selected provider-attempt executor
	// SanitizedToolSchemasPlatform, SanitizedReasoningReplay, and SanitizedInputItemIDs
	// record compatibility passes already completed on Body so HTTP passthrough
	// skips duplicate multi-megabyte scans when those sections stay unchanged.
	SanitizedToolSchemasPlatform string // platform whose tool schemas were sanitized
	SanitizedReasoningReplay     bool   // reasoning.content replay already sanitized
	SanitizedInputItemIDs        bool   // input item IDs already sanitized
}

// PreludePorts supplies stateful policy reads, identity setup, and attempt observations during request preparation.
type PreludePorts interface {
	// BlockGroupImages reports whether the admitted Responses group blocks image
	// generation.
	BlockGroupImages() bool
	// Begin resets model and endpoint observations for the selected provider
	// attempt.
	Begin()
	// ClearMappings clears response restoration mappings before another provider
	// attempt is prepared.
	ClearMappings()
	// PrepareIdentity prepares the selected provider's Codex identity before
	// request conversion.
	PrepareIdentity(context.Context) error
	// MatchTLS returns the TLS routing match for the current provider request.
	MatchTLS() egress.TLSFingerprintRouterMatchResult
	// ClientAllowed reports client admission using TLS observations and request
	// bytes.
	ClientAllowed(context.Context, egress.TLSFingerprintRouterMatchResult, []byte) (bool, string)
	// Reject publishes requested diagnostics and writes the local protocol error
	// response.
	Reject(Rejection)
	// LiteHeader reports whether the incoming header requests Responses Lite
	// execution.
	LiteHeader() bool
	// LitePayload normalizes Responses Lite bytes and returns a rejection
	// parameter on error. The changed result identifies adopted bytes; preparation
	// writes the supplied parameter when rejecting invalid Lite input.
	LitePayload([]byte) ([]byte, bool, string, error)
	// Transport returns the upstream transport choice and its diagnostic selection
	// reason.
	Transport() TransportDecision
	// CompactPath reports whether the incoming request targets legacy Responses
	// Compact.
	CompactPath() bool
	// FlattenRequired reports whether declarations need lowering for the resolved
	// transport policy.
	FlattenRequired(TransportDecision, bool, bool) bool
	// Flatten lowers namespace declarations and binds their response restoration
	// names. Call it after ClearMappings so another provider's names are absent;
	// errors produce the tools rejection before forwarding.
	Flatten([]byte) ([]byte, error)
	// StripNamespacesRequired reports whether historical calls need namespace
	// cleanup for this route.
	StripNamespacesRequired(TransportDecision, bool) bool
	// KeepNamespaces selects retention from the chosen credential and transport.
	// Compact and explicitly flattened routes use their endpoint rules; ordinary
	// Responses retains historical tool identities across earlier turns.
	KeepNamespaces(TransportDecision, bool, bool) bool
	// AdaptClientTools lowers client tools and installs their response restoration
	// mapping on success. Call after clearing attempt mappings; an error stops
	// preparation before the body and view are published.
	AdaptClientTools([]byte) ([]byte, error)
	// BindMessagesBridge records the attempt's bridge classification for later
	// request construction.
	BindMessagesBridge(bool)
	// CodexClient reports whether the inbound client uses the Codex CLI identity.
	CodexClient() bool
	// ImageToolPolicy returns the Codex client's configured image-generation tool
	// handling rule.
	ImageToolPolicy() string
	// ObserveTransport records the resolved mode, model, and stream choice for
	// diagnostics.
	ObserveTransport(TransportDecision, string, bool)
	// MappedModel resolves the selected provider's upstream identity for a caller
	// model.
	MappedModel(string) string
}

// PreparePrelude applies admission and compatibility preparation before
// selecting the provider-attempt executor. It clears previous restoration
// mappings and prepares identity before inspecting client policy, then records
// canonical image-intent bytes before later transformations. Rejected client
// policy and protocol fields produce their local error response before
// returning.
//
// Grok, Anthropic, and raw Chat dispatch return at their respective preparation
// stages. Remaining OpenAI requests refresh the body and decoded view after
// reasoning and item-ID repairs, then bind bridge and client observations
// before transport validation. Passthrough can strip image declarations and
// mark intent invalidation while retaining the canonical bytes needed by later
// image policy. A zero Route selects ordinary Responses execution.
func PreparePrelude(ctx context.Context, body []byte, profile Profile, p PreludePorts) (*Prelude, error) {
	var err error
	// Apply admitted group image blocking before capturing canonical intent so
	// later provider transforms cannot reintroduce declarations the group policy
	// removed.
	if p.BlockGroupImages() {
		body, _, err = gatewayprovider.StripOpenAIImageGenerationToolsFromRawPayload(body)
		if err != nil {
			return nil, err
		}
	}
	// Reset attempt observations and tool restoration state before identity
	// preparation so a new provider cannot interpret responses using an earlier
	// attempt's mappings.
	p.Begin()
	p.ClearMappings()
	if err = p.PrepareIdentity(ctx); err != nil {
		return nil, err
	}
	value := &Prelude{StartedAt: time.Now(), CanonicalImageIntentBody: body, ImageToolPolicy: "allow"}
	value.TLS = p.MatchTLS()
	if allowed, message := p.ClientAllowed(ctx, value.TLS, body); !allowed {
		p.Reject(Rejection{Status: 403, Type: "forbidden_error", Message: message, PolicyDenied: true})
		return nil, errors.New("openai oauth client policy restriction: client is not allowed")
	}

	// Adopt platform-compatible tool schemas before route selection; later tool
	// adaptation and namespace policy must inspect the same declarations being
	// forwarded.
	if updated, changed, err := gatewayprovider.SanitizeOpenAIResponsesToolSchemasForPlatform(body, profile.Platform); err != nil {
		return nil, err
	} else if changed {
		body = updated
	}
	value.SanitizedToolSchemasPlatform = profile.Platform
	// OAuth Responses accepts the platform reasoning-mode normalization here
	// before Lite and Compact policy inspect the body for their own compatibility
	// requirements.
	if profile.OpenAI && profile.OAuth {
		if updated, changed, err := openai.NormalizeOpenAIResponsesReasoningMode(body); err != nil {
			return nil, fmt.Errorf("normalize OpenAI Responses reasoning.mode: %w", err)
		} else if changed {
			body = updated
		}
	}
	// Lite headers require credential-specific payload cleanup; report the
	// validator's offending parameter before any transport preparation can treat
	// the request as accepted.
	if profile.OpenAI && p.LiteHeader() {
		updated, changed, param, err := p.LitePayload(body)
		if err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: param, ObserveUpstream: true})
			return nil, err
		}
		if changed {
			body = updated
		}
	}
	value.Transport = p.Transport()
	value.Compact = p.CompactPath()
	// Compact preparation applies its field whitelist first, then repairs API Key
	// replay state; namespace handling uses the adopted result of both steps.
	if value.Compact {
		if updated, changed, err := openai.NormalizeOpenAICompactRequestBody(body); err != nil {
			return nil, err
		} else if changed {
			body = updated
		}
		if profile.OpenAI && profile.APIKey {
			if updated, changed, err := openai.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, true); err != nil {
				return nil, err
			} else if changed {
				body = updated
			}
		}
	}
	// Lower declarations when the selected transport requires it, binding
	// restoration state before historical tool calls are cleaned for that same
	// route.
	if p.FlattenRequired(value.Transport, profile.Passthrough, value.Compact) {
		body, err = p.Flatten(body)
		if err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "tools", ObserveUpstream: true})
			return nil, err
		}
		value.SanitizedToolSchemasPlatform = ""
	}
	// Retention follows the selected protocol policy. Earlier turns may own the
	// declaration, so the current body cannot establish that a call namespace is
	// disposable.
	if p.StripNamespacesRequired(value.Transport, profile.Passthrough) {
		keep := p.KeepNamespaces(value.Transport, profile.Passthrough, value.Compact)
		body, err = protocolbridge.StripOpenAIResponsesInputNamespaces(body, keep)
		if err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "input", ObserveUpstream: true})
			return nil, err
		}
	}
	// Native DeepSeek API Key Responses lowers client-tool shapes after namespace
	// preparation, allowing the response adapter to restore those tools for the
	// caller.
	if profile.EncodeResponsesClientTools && profile.APIKey && !value.Compact && protocolbridge.NeedsOpenAIResponsesClientToolAdaptation(body) {
		body, err = p.AdaptClientTools(body)
		if err != nil {
			return nil, fmt.Errorf("adapt DeepSeek Responses client tools: %w", err)
		}
		value.SanitizedToolSchemasPlatform = ""
	}
	value.Body = body
	value.OriginalBody = body
	value.View = requeststate.NewOpenAIRequestView(body)
	if profile.Grok {
		value.Route = DispatchGrok
		if profile.ResolvedChat {
			value.Route = DispatchRawChat
		}
		return value, nil
	}
	if err := requeststate.ValidateOpenAIReasoningEffort(body, value.View.Model); err != nil {
		p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "reasoning.effort"})
		return nil, err
	}
	if profile.Anthropic {
		value.Route = DispatchAnthropic
		return value, nil
	}
	if profile.RawChat {
		value.Route = DispatchRawChat
		return value, nil
	}
	// Normalize replay content and sanitize item identifiers for eligible OpenAI
	// credentials; every adopted repair refreshes both byte snapshots and the
	// decoded request view.
	if profile.OpenAI && (profile.APIKey || profile.OAuthLike) {
		if updated, changed, err := openai.NormalizeOpenAIResponsesReasoningContentReplay(body); err != nil {
			return nil, fmt.Errorf("normalize OpenAI Responses reasoning content replay: %w", err)
		} else if changed {
			body = updated
			value.Body = body
			value.OriginalBody = body
			value.View = requeststate.NewOpenAIRequestView(body)
		}
		value.SanitizedReasoningReplay = true
		if updated, changed, err := gatewayprovider.SanitizeOpenAIResponsesInputItemIDs(body); err != nil {
			return nil, fmt.Errorf("sanitize OpenAI Responses input item IDs: %w", err)
		} else if changed {
			body = updated
			value.Body = body
			value.OriginalBody = body
			value.View = requeststate.NewOpenAIRequestView(body)
		}
		value.SanitizedInputItemIDs = true
	}
	value.MessagesBridge = gatewayprovider.IsOpenAICompatMessagesBridgeBody(body)
	p.BindMessagesBridge(value.MessagesBridge)
	value.CodexCLI = p.CodexClient()
	if value.CodexCLI {
		value.ImageToolPolicy = p.ImageToolPolicy()
	}
	p.ObserveTransport(value.Transport, value.View.Model, value.View.Stream)
	if value.Transport.Transport == "responses_websockets" {
		p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: "OpenAI WSv1 is temporarily unsupported. Please enable responses_websockets_v2.", FeatureDenied: true})
		return nil, errors.New("openai ws v1 is temporarily unsupported; use ws v2")
	}
	if profile.Passthrough {
		// Provider image stripping changes passthrough intent after canonical
		// capture; mark that invalidation so later image policy reevaluates the
		// appropriate request evidence.
		if value.ImageToolPolicy == "strip" {
			if updated, changed, err := gatewayprovider.StripOpenAIImageGenerationToolsFromRawPayload(body); err != nil {
				return nil, err
			} else if changed {
				body = updated
				value.Body = body
				value.OriginalBody = body
				value.ImageIntentInvalidated = true
				logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Stripped /responses image_generation tool for Codex client by provider policy")
			}
		}
		mappedModel := p.MappedModel(value.View.Model)
		value.ReasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(requeststate.ExtractOpenAIReasoningEffortFromBody(body), body, mappedModel)
		value.Route = DispatchPassthrough
	}
	return value, nil
}
