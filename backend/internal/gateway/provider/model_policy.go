package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// model_policy.go resolves provider model identities for execution, listing,
// billing, and scheduling.
// A model request can have a group-mapped billing name and a different
// provider-specific outbound name. Provider mapping executes one hop, while
// Bedrock regions, Anthropic Vertex IDs, Qoder account aliases, and Antigravity
// thinking variants determine the final wire identity. Compact can select a
// dedicated outbound mapping without replacing the ordinary billing name.
//
// Prepared rules capture model configuration for catalogue queries; unplanned
// execution policies read provider rules when used. An admitted attempt retains
// its selected protocol and mapped model together, keeping request identity
// separate from shared provider configuration. Final
// scheduling names and shared image, Fable, or Gemini scopes then determine
// model-window eligibility. Successful outbound resolution records the final
// stage in request model tracing for response and usage correlation.

// ModelPolicy combines provider configuration, a request attempt route,
// optional prepared model rules, and an optional reasoning effort for mapping.
type ModelPolicy struct {
	// Record supplies the borrowed provider configuration. Keep it stable while
	// this policy resolves mappings, protocols, support, or model-limit observations.
	Record *provider.Record
	Route  requeststate.AttemptRoute // current request attempt route
	// models captures mapping and support rules for repeated catalogue checks;
	// an unset snapshot causes execution helpers to read the current provider configuration.
	models *provider.ModelRulesSnapshot
	// Effort carries the normalized effective reasoning effort used when
	// resolving effort_model_mapping outside an already planned attempt route.
	Effort string // optional canonical reasoning effort
}

// WithEffort returns a policy copy carrying the normalized reasoning effort for
// subsequent effort-aware mapping and admission calls.
func (p ModelPolicy) WithEffort(effort string) ModelPolicy {
	p.Effort = protocol.NormalizeReasoningEffort(effort)
	return p
}

// WithContext returns a policy copy populated with the request's routing
// reasoning effort when p.Effort is unset. A policy with an explicit Effort
// keeps its own selection so variant enumeration can override context state.
func (p ModelPolicy) WithContext(ctx context.Context) ModelPolicy {
	if p.Effort == "" {
		p.Effort = requeststate.RoutingReasoningEffortFromContext(ctx)
	}
	return p
}

// effectiveEffort returns p.Effort when set, or falls back to the routing
// reasoning effort captured in ctx.
func (p ModelPolicy) effectiveEffort(ctx context.Context) string {
	if p.Effort != "" {
		return p.Effort
	}
	return requeststate.RoutingReasoningEffortFromContext(ctx)
}

// Prepared captures reusable provider model rules when the policy has no
// snapshot yet. An existing snapshot is reused, while the record and attempt
// route remain the values supplied by the caller.
func (p ModelPolicy) Prepared() ModelPolicy {
	if p.models == nil {
		p.models = provider.PrepareModelRules(p.Record, provideradapter.ModelDefaults(), provideradapter.ModelRules(p.Record))
	}
	return p
}

// configuredModels returns concrete candidates from the prepared model-rule
// snapshot. Callers prepare the policy before enumeration so finite configured
// candidates are available without consulting the full metadata catalogue.
func (p ModelPolicy) configuredModels() []string {
	return p.models.ConfiguredModels()
}

// matchedEfforts returns canonical effort labels that match requested in
// configured effort_model_mapping rules, using prepared rules when available.
func (p ModelPolicy) matchedEfforts(requested string) []string {
	if p.Record == nil || requested == "" {
		return nil
	}
	if p.models != nil {
		return p.models.MatchedEfforts(requested)
	}
	return provider.EffortMappingMatchedEfforts(provider.ResolveEffortModelMapping(p.Record), requested)
}

// supportsConfiguredModel checks model support through prepared rules when
// available or current provider configuration otherwise. The uncaptured path
// reads shared platform defaults and rules each time the caller validates a
// model.
func (p ModelPolicy) supportsConfiguredModel(model string) bool {
	if p.models != nil {
		return p.models.SupportsForEffort(model, p.Effort)
	}
	return p.Record.IsModelSupportedForEffort(model, p.Effort, provideradapter.ModelDefaults(), provideradapter.ModelRules(p.Record))
}

// finalModelWhitelisted checks final-model permission through the prepared
// snapshot or current provider rules. Both paths evaluate the provider
// whitelist after model resolution, keeping configured aliases separate from
// final-model admission.
func (p ModelPolicy) finalModelWhitelisted(model string) bool {
	if p.models != nil {
		return p.models.FinalModelWhitelisted(model)
	}
	return p.Record.FinalModelWhitelisted(model, provideradapter.ModelDefaults(), provideradapter.ModelRules(p.Record))
}

// forwardMappedModel resolves the outbound model through ResolveMapped, so an
// admitted attempt forwards its captured model and other calls use the current
// mapping. Nil records return empty, and a blank configured target keeps the
// request spelling for ordinary platform normalization.
func (p ModelPolicy) forwardMappedModel(model string) string {
	if p.Record == nil {
		return ""
	}
	mapped, matched := p.ResolveMapped(model)
	return provider.ForwardMappedTarget(model, mapped, matched)
}

// antigravityModel resolves Antigravity model, effort, and thinking variants
// through prepared rules or current provider configuration. The optional
// thinking pointer retains absent versus explicit state for the platform
// resolver.
func (p ModelPolicy) antigravityModel(model string, thinking *bool) string {
	if p.models != nil {
		return provideradapter.FinalAntigravityModelWithRulesForEffort(p.models, model, p.Effort, thinking)
	}
	return provideradapter.FinalAntigravityModelForEffort(p.Record, model, p.Effort, thinking)
}

// protocolTarget combines the policy record with the current attempt upstream
// protocol.
func (p ModelPolicy) protocolTarget() provider.ProtocolTarget {
	return provider.ProtocolTarget{Record: p.Record, Protocol: p.Route.Protocol()}
}

// Mapped returns one provider-mapping hop for model, leaving it unchanged when
// Record is nil. The attempt route, optional Effort, and optional prepared
// rules determine the mapping used by ResolveMapped.
func (p ModelPolicy) Mapped(model string) string {
	if p.Record == nil {
		return model
	}
	mapped, _ := p.ResolveMapped(model)
	return mapped
}

// ResolveMapped returns the admitted model or one effort-aware provider mapping
// hop, discarding whether an effort-specific rule produced the match.
func (p ModelPolicy) ResolveMapped(model string) (string, bool) {
	mapped, matched, _ := p.ResolveMappedWithEffort(model)
	return mapped, matched
}

// ResolveMappedWithEffort returns the admitted model or one effort-aware
// provider mapping hop together with ordinary and effort-specific match flags.
// A prepared policy with no candidate uses its captured rules directly;
// candidate-bound policies reuse their captured model for the admitted request.
func (p ModelPolicy) ResolveMappedWithEffort(model string) (string, bool, bool) {
	if p.Record == nil {
		return model, false, false
	}
	// Prepared catalogue queries use their captured resolver until an attempt
	// binds a final identity; that retained identity prevents another provider alias hop.
	if p.models != nil {
		if _, planned := p.Route.Candidate(); !planned {
			return p.models.ResolveMappedModelForEffort(model, p.Effort)
		}
		return p.Route.ResolveModelForEffort(p.Record.ID, p.models.RoutingSnapshot(), model, p.Effort)
	}
	snapshot := provider.NewModelRoutingSnapshotWithEffort(
		provider.ResolveModelMapping(p.Record, provideradapter.ModelDefaults()),
		provider.ResolveEffortModelMapping(p.Record),
	)
	return p.Route.ResolveModelForEffort(p.Record.ID, snapshot, model, p.Effort)
}

// ShouldStripWireEffort reports whether outbound wire effort fields should be
// removed for this provider and request. Stripping runs only when the provider
// enables effort_model_mapping_strip_effort and an effort_model_mapping rule
// matched the request model under its effective routing effort.
func (p ModelPolicy) ShouldStripWireEffort(ctx context.Context, model string) bool {
	if p.Record == nil || !provider.StripEffortOnEffortModelMapping(p.Record) {
		return false
	}
	if p.Route.EffortMappingMatched() {
		return true
	}
	_, _, effortMatched := p.WithContext(ctx).ResolveMappedWithEffort(model)
	return effortMatched
}

// ForwardModel prefers a nonblank trimmed dispatchMapped value over requested,
// then applies provider mapping. An absent record leaves that selected model
// unchanged through Mapped, supporting callers with already resolved group
// names.
func (p ModelPolicy) ForwardModel(requested, dispatchMapped string) string {
	model := requested
	if dispatchMapped = strings.TrimSpace(dispatchMapped); dispatchMapped != "" {
		model = dispatchMapped
	}
	return p.Mapped(model)
}

// NormalizeOpenAI trims ordinary model text and delegates Grok normalization
// for Grok records. The Grok helper supplies its default for empty input;
// ordinary IDs retain their spelling beyond surrounding whitespace.
func (p ModelPolicy) NormalizeOpenAI(model string) string {
	if p.Record != nil && p.Record.IsGrok() {
		return grok.NormalizeModelID(model)
	}
	return strings.TrimSpace(model)
}

// RawChat chooses Chat forwarding from a resolved attempt protocol before
// consulting provider defaults. With no route, only API Key targets qualify;
// Kimi, Zhipu, and DeepSeek adaptive routing uses native Responses capability,
// while other targets resolve their text-protocol extras.
func (p ModelPolicy) RawChat() bool {
	// A resolved attempt already chose its upstream protocol. Honor it before
	// reading provider defaults so failover candidates keep their own admitted route.
	if p.Record != nil && p.Route.Protocol() != "" {
		return p.Route.Protocol() == protocol.ProtocolOpenAIChatCompletions
	}
	if p.Record == nil || p.Record.Type != capability.ProviderTypeAPIKey {
		return false
	}
	if p.Record.HasProtocolEndpointDefaults() {
		switch p.protocolTarget().GetAPIProtocol() {
		case provider.APIProtocolChatCompletions:
			return true
		case provider.APIProtocolAdaptive:
			return !p.Record.SupportsAPIKeyResponses()
		default:
			return false
		}
	}
	return provider.ResolveUpstreamTextProtocol(p.Record.Extra, provider.TextProtocolResponses) == provider.TextProtocolChatCompletions
}

// OpenAIUpstream resolves the outbound model for ordinary or Compact
// execution. Empty requested text returns empty, and raw Chat uses ordinary
// mapping before normalization.
//
// Compact first checks its dedicated requested-model mapping; a nonblank match
// wins directly. Otherwise ordinary mapping supplies the model, after which
// Compact forwarding rules can choose a different outbound model. Model
// mapping still applies to passthrough targets. This ordering keeps dedicated
// Compact aliases distinct from the ordinary billing name returned by
// ForwardMappedModels.
func (p ModelPolicy) OpenAIUpstream(requested string, compact bool) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	if p.RawChat() {
		return p.NormalizeOpenAI(p.ForwardModel(requested, ""))
	}
	// Dedicated Compact mapping can name a different outbound model from ordinary
	// provider mapping; check it before deriving the model used by the generic Compact resolver.
	if compact && p.Record != nil {
		if model, matched := p.Record.ResolveCompactMappedModel(requested); matched {
			if model = strings.TrimSpace(model); model != "" {
				return model
			}
		}
	}
	model := strings.TrimSpace(p.ForwardModel(requested, ""))
	if model == "" {
		return ""
	}
	// No dedicated mapping selected a model. Apply platform Compact forwarding
	// after ordinary mapping so this second route choice uses the final ordinary name.
	if compact {
		compactModel := strings.TrimSpace(provider.ResolveCompactForwardModel(p.Record, model))
		if compactModel != "" && compactModel != model {
			return compactModel
		}
	}
	return p.NormalizeOpenAI(model)
}

// CanonicalSchedulingModel resolves the scheduling key from trimmed requested
// text and provider mapping. OpenAI uses ordinary outbound resolution,
// compatible platforms additionally normalize that mapped name, and absent
// records retain the trimmed input.
func (p ModelPolicy) CanonicalSchedulingModel(requested string) string {
	model := strings.TrimSpace(requested)
	if p.Record == nil || model == "" {
		return model
	}
	if p.Record.IsOpenAI() {
		return p.OpenAIUpstream(model, false)
	}
	if mapped := strings.TrimSpace(p.Mapped(model)); mapped != "" {
		model = mapped
	}
	if p.Record.IsOpenAICompatible() {
		return p.NormalizeOpenAI(model)
	}
	return model
}

// bedrockInput supplies source region, force-global flag, and one mapped model
// for Bedrock resolution. Nil records return nil, allowing the Bedrock helper
// to handle the missing configuration using its own route rules.
func (p ModelPolicy) bedrockInput(model string) *bedrock.RouteInput {
	if p.Record == nil {
		return nil
	}
	return &bedrock.RouteInput{Region: p.Record.GetCredential("aws_region"), ForceGlobal: p.Record.GetCredential("aws_force_global") == "true", Model: p.Mapped(model)}
}

// Bedrock resolves the mapped model to a Bedrock ID and reports whether a
// supported route exists. Provider credentials supply source-region and
// force-global settings through bedrockInput before resolution.
func (p ModelPolicy) Bedrock(model string) (string, bool) {
	return bedrock.ResolveBedrockModelID(p.bedrockInput(model), model)
}

// BedrockRoute resolves a Bedrock route using one provider mapping and the
// configured source region. The upstream resolver returns the route or its
// validation error, including unsupported routing choices.
func (p ModelPolicy) BedrockRoute(model string) (bedrock.BedrockModelRoute, error) {
	return bedrock.ResolveBedrockModelRoute(p.bedrockInput(model), model)
}

// AnthropicUpstream normalizes service-account Anthropic model IDs for Vertex
// after trimming. Nil records return empty; other Anthropic credential modes
// and other platforms return the supplied trimmed model.
func (p ModelPolicy) AnthropicUpstream(mapped string) string {
	if p.Record == nil {
		return ""
	}
	mapped = strings.TrimSpace(mapped)
	if mapped == "" || p.Record.Platform != capability.PlatformAnthropic || p.Record.Type == capability.ProviderTypeAPIKey || p.Record.IsBedrock() {
		return mapped
	}
	if p.Record.Type == capability.ProviderTypeServiceAccount {
		return vertex.NormalizeVertexAnthropicModelID(mapped)
	}
	return mapped
}

// modelThinking returns a pointer to the request thinking hint when present.
// Missing hints return nil, keeping an omitted mode distinct from an explicit
// false value passed to model resolution.
func modelThinking(ctx context.Context) *bool {
	if value, ok := requeststate.ThinkingEnabledFromContext(ctx); ok {
		return &value
	}
	return nil
}

// Supports checks execution support for a post-group-mapping model under
// platform rules and the active reasoning effort from p or ctx. Nil records
// fail; Antigravity resolves effort and thinking variants, Bedrock requires
// configured support and a valid route, and non-API-Key Anthropic applies
// final-model whitelist checks after normalization.
func (p ModelPolicy) Supports(ctx context.Context, model string) bool {
	p = p.WithContext(ctx)
	value := p.Record
	if value == nil {
		return false
	}
	if value.Platform == capability.PlatformAntigravity {
		if strings.TrimSpace(model) == "" {
			return true
		}
		return p.antigravityModel(model, modelThinking(ctx)) != ""
	}
	if value.IsBedrock() {
		if !p.supportsConfiguredModel(model) {
			return false
		}
		_, ok := p.Bedrock(model)
		return ok
	}
	if value.Platform == capability.PlatformAnthropic && value.Type != capability.ProviderTypeAPIKey {
		mapped := p.forwardMappedModel(model)
		return p.finalModelWhitelisted(p.AnthropicUpstream(mapped))
	}
	return p.supportsConfiguredModel(model)
}

// SupportsAnyEffort reports whether model is supported for catalogue discovery
// either directly under ctx or through at least one matching effort_model_mapping
// rule. Runtime request selection uses Supports instead so effort-free requests
// cannot borrow effort-only routes.
func (p ModelPolicy) SupportsAnyEffort(ctx context.Context, model string) bool {
	if p.Supports(ctx, model) {
		return true
	}
	for _, effort := range p.matchedEfforts(model) {
		variantCtx := requeststate.WithRoutingReasoningEffort(ctx, effort)
		if p.WithEffort(effort).Supports(variantCtx, model) {
			return true
		}
	}
	return false
}

// UpstreamModel resolves and registers the final outbound identity for the
// request model trace. Nil records and rejected Bedrock routes
// return empty before registration.
//
// Platform handling uses Bedrock routing, Antigravity effort/thinking state,
// ordinary OpenAI/Grok outbound mapping, or effort-aware provider mapping
// followed by Qoder account aliases or Anthropic normalization. The result is
// trimmed before trace registration. Callers keep the record stable and use
// this final identity for execution while the earlier requested and billing
// names remain separate observations.
func (p ModelPolicy) UpstreamModel(ctx context.Context, requested string) string {
	p = p.WithContext(ctx)
	value := p.Record
	if value == nil {
		return ""
	}
	var model string
	if value.IsBedrock() {
		mapped, ok := p.Bedrock(requested)
		if !ok {
			return ""
		}
		model = mapped
	} else if value.Platform == capability.PlatformAntigravity {
		model = p.antigravityModel(requested, modelThinking(ctx))
	} else if value.Platform == capability.PlatformOpenAI || value.Platform == capability.PlatformGrok {
		model = p.OpenAIUpstream(requested, false)
	} else {
		mapped := p.forwardMappedModel(requested)
		if value.Platform == capability.PlatformQoder {
			model = value.NormalizeQoderModel(mapped)
		} else {
			model = p.AnthropicUpstream(mapped)
		}
	}
	model = strings.TrimSpace(model)
	// Register the final route identity after platform normalization so response
	// model restoration and usage mapping can correlate this attempt with earlier stages.
	modeltrace.RegisterStage(ctx, model)
	return model
}

// LimitKeys returns the final model key and applicable shared limit scopes for
// a request. Nil records and empty resolved keys return nil, so callers can
// distinguish absent model evidence from a window on a known key.
//
// OpenAI and Grok use canonical scheduling names, while Antigravity includes
// its effort and thinking-specific route. Gemini Antigravity traffic also
// receives the shared Gemini key, declared OpenAI image output or request
// intent receive the shared image key, and Anthropic Fable names receive the
// shared Fable key. These scopes supplement the model window, so either active
// window can restrict that request.
func (p ModelPolicy) LimitKeys(ctx context.Context, requested string) []string {
	p = p.WithContext(ctx)
	value := p.Record
	if value == nil {
		return nil
	}
	key := p.Mapped(requested)
	switch value.Platform {
	case capability.PlatformOpenAI, capability.PlatformGrok:
		key = p.CanonicalSchedulingModel(requested)
	case capability.PlatformAntigravity:
		key = p.antigravityModel(requested, modelThinking(ctx))
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	keys := []string{key}
	switch value.Platform {
	case capability.PlatformAntigravity:
		// Gemini quota on Antigravity can cover several concrete models, so include
		// its shared window alongside this attempt's model-specific scheduling key.
		if strings.HasPrefix(antigravity.NormalizeAntigravityModelName(key), "gemini-") && key != "antigravity:gemini" {
			keys = append(keys, "antigravity:gemini")
		}
	case capability.PlatformOpenAI:
		// A text model can invoke image generation through tools. Request intent
		// therefore joins declared output evidence when assigning the shared image cooldown scope.
		images := requeststate.OpenAIImageGenerationIntentFromContext(ctx)
		if !images {
			// Mixed-output models can serve ordinary text requests. Apply the shared
			// image cooldown only to an image-only target or explicit image intent.
			target, err := ProviderForProtocolAttempt(ctx, &ExecutionProvider{Record: *value, Route: p.Route})
			if err == nil {
				resolution, err := ResolveModelAttributes(ctx, target, ExecutionModelPolicy(target).UpstreamModel(ctx, requested), nil)
				images = err == nil && resolution.Attributes.OutputOnly("image")
			}
		}
		if images && key != provider.OpenAIImageGenerationRateLimitKey {
			keys = append(keys, provider.OpenAIImageGenerationRateLimitKey)
		}
	case capability.PlatformAnthropic:
		// Fable failures can cool the family as well as one model; checking both
		// keys carries that shared quota observation into later eligibility decisions.
		if anthropic.IsAnthropicFableModel(key) && key != provider.AnthropicFableRateLimitKey {
			keys = append(keys, provider.AnthropicFableRateLimitKey)
		}
	}
	return keys
}

// AllowsModel delegates model and shared-scope window permission to the
// provider record using LimitKeys. Provider policy owns overage handling and
// empty-key behavior; this method does not establish model support by itself.
func (p ModelPolicy) AllowsModel(ctx context.Context, model string) bool {
	return p.Record.ModelRateLimitAllows(p.LimitKeys(ctx, model))
}

// ListingModels returns usable upstream identities in encounter order,
// deduplicating names after case folding. A nil record or disallowed model
// window returns an allocated empty result.
//
// Antigravity evaluates plain and thinking contexts separately, checking both
// window permission and support before resolving each route. When
// effort_model_mapping matches requested, listing evaluates the ordinary route
// alongside each matched effort branch and includes only supported, permitted
// targets so effort-only aliases do not emit unwhitelisted base spellings.
func (p ModelPolicy) ListingModels(ctx context.Context, requested string) []string {
	models := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	if p.Record == nil {
		return models
	}
	addVariant := func(variantCtx context.Context, policy ModelPolicy, requireSupport bool) {
		if policy.Record.Platform == capability.PlatformAntigravity {
			// Plain and thinking routes can have different final names and limit windows.
			// Evaluate both contexts before deduplication so one rejected variant cannot hide the other.
			plain := requeststate.WithThinkingEnabled(variantCtx, false)
			if policy.AllowsModel(plain, requested) && policy.Supports(plain, requested) {
				add(policy.UpstreamModel(plain, requested))
			}
			thinking := requeststate.WithThinkingEnabled(variantCtx, true)
			if policy.AllowsModel(thinking, requested) && policy.Supports(thinking, requested) {
				add(policy.UpstreamModel(thinking, requested))
			}
			return
		}
		if !policy.AllowsModel(variantCtx, requested) {
			return
		}
		if requireSupport && !policy.Supports(variantCtx, requested) {
			return
		}
		add(policy.UpstreamModel(variantCtx, requested))
	}

	efforts := p.matchedEfforts(requested)
	if len(efforts) == 0 {
		addVariant(ctx, p, false)
		return models
	}
	// Effort-scoped rules can expose a base alias whose unmapped spelling is not
	// itself whitelisted. Require support on every branch so only eligible
	// ordinary and effort-mapped targets appear in the listing.
	plainCtx := requeststate.WithRoutingReasoningEffort(ctx, "")
	addVariant(plainCtx, p.WithEffort(""), true)
	for _, effort := range efforts {
		effortCtx := requeststate.WithRoutingReasoningEffort(ctx, effort)
		addVariant(effortCtx, p.WithEffort(effort), true)
	}
	return models
}

// ForwardMappedModels returns ordinary billing identity and the potentially
// different Compact outbound identity. Requested text is trimmed, and an empty
// billing mapping uses that requested name.
//
// OpenAIUpstream performs the outbound route selection, including dedicated
// Compact mappings, while billing keeps the ordinary provider-mapped name. If
// outbound resolution is empty, billing identity supplies the outgoing
// fallback. Callers retain both values for usage and error scheduling so a
// Compact rewrite does not silently replace the ordinary pricing identity.
func (p ModelPolicy) ForwardMappedModels(requested string, compact bool) (billingModel, upstreamModel string) {
	requested = strings.TrimSpace(requested)
	if p.Record != nil {
		billingModel = strings.TrimSpace(p.Mapped(requested))
	}
	// A missing provider billing mapping keeps the requested pricing identity;
	// outbound Compact resolution can still select its separate model afterward.
	if billingModel == "" {
		billingModel = requested
	}
	upstreamModel = p.OpenAIUpstream(requested, compact)
	// Outbound resolution supplied no usable identity. Retain the billing name
	// so callers still receive a consistent model pair for their attempt preparation.
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = billingModel
	}
	return billingModel, upstreamModel
}

// ErrorSchedulingModel returns the trimmed observed upstream name or, when
// absent, the billing name. Failure handlers use that selected identity to
// scope model scheduling feedback to the executed route.
func ErrorSchedulingModel(billingModel, upstreamModel string) string {
	if upstream := strings.TrimSpace(upstreamModel); upstream != "" {
		return upstream
	}
	return strings.TrimSpace(billingModel)
}
