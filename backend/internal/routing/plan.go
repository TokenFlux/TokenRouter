package routing

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// plan.go captures request routing policy and resolves provider-specific
// protocol and model identities.
// A request's final group and client protocol remain fixed while provider
// attempts can choose different upstream protocols and model mappings. Group
// policy collections are copied when the plan is built, keeping later
// administrative edits from changing that request's retained conversion
// choices.
//
// The model chain retains client, key, group, and provider identities
// separately so restriction and billing consumers can select their configured
// stage. Candidate resolution binds the chosen provider, protocol, and final model
// from the same snapshot before execution. Each attempt receives
// the scalar chain by value, keeping its provider mapping in that candidate
// copy. Gateway admission authorizes the group and client entry before using
// the plan.

// ModelChain retains the request's model identities and the configured
// restriction and billing stages.
type ModelChain struct {
	// ClientModel, RequestedModel, GroupMappedModel, and ProviderMappedModel
	// retain successive routing identities. Each stage has its own restriction,
	// response, or billing consumer; resolving an attempt fills the provider
	// stage.
	//
	// ClientModel: original client model. RequestedModel: key-redirected request
	// model. GroupMappedModel: group policy model. ProviderMappedModel:
	// provider-mapped upstream model.
	ClientModel, RequestedModel, GroupMappedModel, ProviderMappedModel string
	// APIKeyRedirected and GroupMapped record whether their respective stages
	// rewrote the client identity. They are retained independently so response
	// consumers can recognize either rewrite in the captured model chain.
	//
	// APIKeyRedirected: key model rewritten. GroupMapped: group model rewritten.
	APIKeyRedirected, GroupMapped bool

	RestrictionModelSource string // selected whitelist identity stage
	RestrictModels         bool   // retained group model restriction
	PricingConfigID        int64  // associated pricing config ID
	BillingModelSource     string // selected billing identity stage
}

// PlanInput supplies the admitted group, completed group mapping, optional
// reasoning effort, and client entry for a request plan.
type PlanInput struct {
	// GroupID overrides Group.ID when supplied. Plan copies the pointed value, so
	// changing the input pointer after construction leaves the captured identifier
	// unchanged.
	GroupID        *int64
	RequestedModel string             // model after key redirection
	GroupMapping   GroupMappingResult // completed group mapping metadata
	// Group supplies scheduler and protocol settings for Plan. The caller
	// authorizes this group first; Plan copies its protocol collections for
	// retained request use.
	Group          *Group
	ClientProtocol capability.ProtocolID // admitted client entry protocol
	// ReasoningEffort carries the optional post-policy effort label used when
	// resolving provider effort_model_mapping rules; blank values use ordinary
	// model mappings only.
	ReasoningEffort string // optional normalized reasoning effort
}

// RoutePlan retains group policy, optional effort, and model metadata for
// provider-attempt resolution.
type RoutePlan struct {
	models          ModelChain            // retained request identity stages
	groupID         int64                 // captured final group identifier
	schedulerType   GroupSchedulerType    // captured group scheduler selection
	clientProtocol  capability.ProtocolID // current plan client source
	reasoningEffort string                // normalized effort for provider mapping
	// allowed and fallbacks retain copied group protocol policy. Plan copies the
	// admission selection and each conversion list; plan copies with a different
	// client protocol share these collections as read-only values.
	allowed []capability.ProtocolID // copied client admission selection
	// fallbacks is copied, including each target list, when Plan captures group
	// policy. Missing and present empty entries retain their distinct automatic
	// and native-only choices.
	fallbacks map[capability.ProtocolID][]capability.ProtocolID
}

// CandidatePlan records the provider and protocol selected for one request
// attempt.
type CandidatePlan struct {
	ModelMappingMatched  bool                  // one-hop provider mapping match
	EffortMappingMatched bool                  // effort-scoped provider mapping match
	Models               ModelChain            // attempt-specific model identity stages
	ProviderID           int64                 // selected provider row identifier
	GroupID              int64                 // authorized final group identifier
	ClientProtocol       capability.ProtocolID // resolved client entry identity
	UpstreamProtocol     capability.ProtocolID // selected provider execution protocol
}

// Mapping returns the retained group mapping, restriction settings, and billing
// metadata.
func (p RoutePlan) Mapping() GroupMappingResult {
	return GroupMappingResult{
		MappedModel:            p.models.GroupMappedModel,
		PricingConfigID:        p.models.PricingConfigID,
		Mapped:                 p.models.GroupMapped,
		BillingModelSource:     p.models.BillingModelSource,
		ClientModel:            p.models.ClientModel,
		APIKeyRedirected:       p.models.APIKeyRedirected,
		RestrictModels:         p.models.RestrictModels,
		RestrictionModelSource: p.models.RestrictionModelSource,
	}
}

// Models returns the plan's retained scalar model-chain value by copy.
func (p RoutePlan) Models() ModelChain { return p.models }

// Plan captures the caller's admitted group and completed model mapping for
// later attempts. It copies admitted protocol lists and fallback targets, and a
// supplied GroupID overrides the ID from Group.
func Plan(input PlanInput) RoutePlan {
	plan := RoutePlan{
		clientProtocol:  input.ClientProtocol,
		reasoningEffort: protocol.NormalizeReasoningEffort(input.ReasoningEffort),
		models: ModelChain{
			RequestedModel:         input.RequestedModel,
			ClientModel:            input.GroupMapping.ClientModel,
			APIKeyRedirected:       input.GroupMapping.APIKeyRedirected,
			GroupMappedModel:       input.GroupMapping.MappedModel,
			GroupMapped:            input.GroupMapping.Mapped,
			RestrictModels:         input.GroupMapping.RestrictModels,
			RestrictionModelSource: input.GroupMapping.RestrictionModelSource,
			PricingConfigID:        input.GroupMapping.PricingConfigID,
			BillingModelSource:     input.GroupMapping.BillingModelSource,
		},
	}
	if input.Group != nil {
		plan.groupID = input.Group.ID
		plan.schedulerType = input.Group.SchedulerType
		plan.allowed = slices.Clone(input.Group.AllowedProtocols)
		plan.fallbacks = protocol.CloneFallbacks(input.Group.ProtocolFallbacks)
	}
	if input.GroupID != nil {
		plan.groupID = *input.GroupID
	}
	return plan
}

// ResolveCandidate binds the final model and upstream protocol from one provider
// snapshot. An unusable route returns false; callers resolve again for each
// refreshed candidate and enforce client entry admission separately.
//
// The retained group model feeds exactly one provider mapping hop. Its result
// selects the model's protocol from the same snapshot, so consumers can send
// the captured model directly. Retrying with a
// different provider rebuilds the complete candidate while preserving request,
// group, restriction, and billing identities in the retained plan.
func (p RoutePlan) ResolveCandidate(candidate provider.ProviderSnapshot) (CandidatePlan, bool) {
	model := p.models.GroupMappedModel
	if model == "" {
		model = p.models.RequestedModel
	}
	resolved, ok := candidate.ResolveModelRouteForEffort(model, p.reasoningEffort, p.clientProtocol, p.fallbacks)
	if !ok {
		return CandidatePlan{}, false
	}
	models := p.models
	models.ProviderMappedModel = resolved.Model
	return CandidatePlan{
		Models:               models,
		ModelMappingMatched:  resolved.MappingMatched,
		EffortMappingMatched: resolved.EffortMappingMatched,
		ProviderID:           candidate.ID,
		GroupID:              p.groupID,
		ClientProtocol:       p.clientProtocol,
		UpstreamProtocol:     resolved.Protocol,
	}, true
}

// GroupID returns the captured final group identifier for this request.
func (p RoutePlan) GroupID() int64 { return p.groupID }

// SchedulerType returns the group selection strategy captured when planning
// began.
func (p RoutePlan) SchedulerType() GroupSchedulerType { return p.schedulerType }

// ReasoningEffort returns the normalized effort label captured for provider
// model mapping, or empty when no effort was supplied.
func (p RoutePlan) ReasoningEffort() string { return p.reasoningEffort }

// AllowedProtocols returns a copy of the captured client admission selection.
// Callers may modify the returned slice independently; a missing group produces
// nil because no selection was captured.
func (p RoutePlan) AllowedProtocols() []capability.ProtocolID { return slices.Clone(p.allowed) }

// WithClientProtocol returns a plan copy with the supplied client protocol.
// Retained group policy collections remain shared between copies as read-only
// plan data, and candidate routing uses the replacement source.
func (p RoutePlan) WithClientProtocol(source capability.ProtocolID) RoutePlan {
	p.clientProtocol = source
	return p
}

// WithReasoningEffort returns a plan copy carrying the normalized replacement
// reasoning effort while sharing read-only group policy collections. Callers
// use this after post-selection effort policy rewrites change the effective
// effort for candidate resolution.
func (p RoutePlan) WithReasoningEffort(effort string) RoutePlan {
	p.reasoningEffort = protocol.NormalizeReasoningEffort(effort)
	return p
}
