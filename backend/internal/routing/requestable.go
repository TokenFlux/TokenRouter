package routing

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// requestable.go resolves configured model candidates into requestable routes
// and public pricing identities.
// A visible request model is a finite configured candidate that survives group
// policy and provider execution rules. Saved model lists, concrete mapping
// names, and adapter-enumerated names supply candidates; metadata catalogue
// size does not expand a wildcard into new request identities.
//
// One query prepares group policy, billing source, and copied provider protocol
// selections before checking names. Each retained name keeps its group-mapped
// identity, permitted client protocols, and eligible upstream routes separate.
// A protocol is marked native only when every provider contributing that
// protocol uses it directly. Upstream-based public pricing needs one common
// identity; conflicting final names are marked ambiguous so display callers can
// report unknown pricing rather than choose a provider arbitrarily.

// RequestableModel records an eligible client identity, its execution routes,
// and its public pricing identity.
type RequestableModel struct {
	// AttributeTargets retains service, authentication, endpoint, and
	// client/upstream protocol identity for attribute resolution. Compact adapters
	// can select a different final model from UpstreamModels.
	AttributeTargets []modelrules.Target
	UpstreamModels   []string                // eligible final upstream names
	Protocols        []capability.ProtocolID // supported client entry protocols
	// NativeProtocols contains a client protocol when every provider contributing
	// that protocol executes it directly. A converted contributing route removes
	// that marker while Protocols still includes the entry.
	NativeProtocols []capability.ProtocolID
	ID              string // client request model identity
	// PricingModel follows the group's billing source. Upstream billing leaves it
	// empty when eligible final names disagree, with PricingAmbiguous recording
	// that display decision.
	PricingModel     string
	PricingAmbiguous bool // conflicting upstream pricing identities
}

// RequestableModelsResult contains resolved group models and evidence about
// restriction or configured candidates.
type RequestableModelsResult struct {
	// Err reports failed attribute or policy loading. Callers return this error
	// instead of presenting a partially resolved set of model capabilities.
	Err    error
	Models []RequestableModel // resolved client model catalogue
	// Restricted records configured group model restriction or a failed
	// lookup/cancelled resolution. Callers use it to distinguish an empty blocked
	// result from an unconfigured catalogue.
	Restricted bool
	// HadExplicitProviderModels records nonempty supplied base lists or provider
	// configured lists after platform filtering. Candidates contributed only by
	// group policy or mapping enumeration do not independently set it.
	HadExplicitProviderModels bool
}

// CatalogueRules provides prepared provider model configuration and eligibility
// checks for a query.
type CatalogueRules interface {
	// ConfiguredModels returns request candidates supplied by the provider's
	// prepared model rules. Callers keep the underlying provider configuration
	// stable while one query reuses those rules and route checks.
	ConfiguredModels() []string
	// Mapping returns the prepared provider source-to-target model table for
	// candidate enumeration. Concrete mapping sources can become request IDs;
	// wildcard patterns constrain matching and remain outside the displayed
	// candidate list.
	Mapping() map[string]string
	// Supports reports whether a group-mapped request name passes the provider's
	// prepared model eligibility. The implementation supplies platform and
	// authentication rules while the resolver separately applies group
	// restrictions and protocols.
	Supports(context.Context, string) bool
	// UpstreamModels returns eligible final upstream names for a group-mapped
	// input under prepared provider rules. The resolver applies any upstream-stage
	// group restriction to each returned name before retaining routes.
	UpstreamModels(context.Context, string) []string
}

// CatalogueProvider combines copied candidate fields with prepared rules for a
// stable provider query. Rule adapters can retain the original provider record,
// so callers keep its configuration unchanged during resolution.
type CatalogueProvider struct {
	provider.ProviderSnapshot // captured provider candidate fields
	// GroupIDs and ProviderGroupIDs retain the provider's two membership forms for
	// catalogue readers and prefetch filtering. Resolver methods assume supplied
	// providers have already been scoped by their caller.
	GroupIDs         []int64 // direct provider membership identifiers
	ProviderGroupIDs []int64 // relationship-derived membership identifiers
	Passthrough      bool    // provider passthrough mode hint
	// Rules is the required prepared model-rule implementation for this provider.
	// Its adapter can retain the original record, so callers keep that
	// configuration stable while querying it.
	Rules CatalogueRules // prepared provider model rules
}

// CataloguePolicies supplies one query's group model policy and independent
// billing configuration.
type CataloguePolicies interface {
	// GetGroupPolicy returns the group's copied routing and protocol policy, or
	// nil when no readable policy exists. Read failures stop candidate preparation
	// so an unavailable restriction cannot admit a model.
	GetGroupPolicy(context.Context, int64) (*GroupPolicyView, error)
	// GetPricingConfigForGroup returns the group's independent pricing
	// configuration for billing identity selection. Absence or read failure leaves
	// preparation's default billing source; model permission remains governed by
	// the group policy.
	GetPricingConfigForGroup(context.Context, int64) (*PricingConfig, error)
}

// RequestableResolver checks finite configured names against one prepared group
// and provider query.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_resolution
type RequestableResolver struct {
	Attributes    *ModelAttributeService // shared route attribute resolver
	GroupPolicies CataloguePolicies      // optional group policy reader
	Warn          func(string, ...any)   // optional policy read warning
}

// requestableQuery retains the inputs and initial result shared by one group
// catalogue resolution.
type requestableQuery struct {
	attributes    *ModelAttributeSession  // query-local catalog and profile snapshot
	groupID       int64                   // selected attribute configuration group
	preview       bool                    // defer capability filtering to preview draft
	err           error                   // first attribute resolution failure
	providers     []CatalogueProvider     // platform-filtered stable query providers
	policy        *GroupPolicyView        // captured group request policy
	billingSource string                  // chosen public billing stage
	candidates    []string                // finite ordered concrete identities
	result        RequestableModelsResult // initial query restriction evidence
}

// ResolveWithProviders returns the eligible catalogue from finite configured
// candidates and the supplied provider pool. Callers scope that pool to the
// authorized group and keep its borrowed rules stable until resolution ends.
// Use an empty platform for a mixed pool or an already trimmed platform for a
// forced query.
//
// Preparation fixes policy, billing source, and protocol selections for the
// query, and results retain candidate order. Cancellation observed before a
// candidate returns Restricted true and discards accumulated models; a
// cancellation after the final check can leave the accumulated result
// available.
func (s *RequestableResolver) ResolveWithProviders(ctx context.Context, groupID *int64, platform string, baseModels []string, providers []CatalogueProvider) RequestableModelsResult {
	query := s.prepare(ctx, groupID, platform, baseModels, providers)
	result := query.result
	for _, requestedModel := range query.candidates {
		if ctx.Err() != nil {
			// A cancelled catalogue exposes incomplete eligibility evidence. Discard
			// accumulated models so display consumers do not publish a partial visible
			// catalogue.
			return RequestableModelsResult{Restricted: true}
		}
		if resolved, ok := query.resolve(ctx, requestedModel); ok {
			result.Models = append(result.Models, resolved)
		}
	}
	if query.err != nil {
		return RequestableModelsResult{Restricted: true, Err: query.err}
	}
	return result
}

// ResolveSelectedWithProviders resolves selected names that are exact members
// of the prepared candidate set, retaining selection order and skipping
// repetitions. Names are compared as supplied, so alternate casing or outer
// whitespace cannot introduce another spelling of a prepared candidate.
//
// Callers scope providers to the authorized group and keep borrowed rules
// stable for the query. Platform is empty for a mixed pool or already trimmed
// for a forced query. Cancellation observed before a selected name discards
// accumulated results and returns Restricted true; cancellation after the
// final check can leave the accumulated result available.
func (s *RequestableResolver) ResolveSelectedWithProviders(ctx context.Context, groupID *int64, platform string, selected []string, providers []CatalogueProvider) RequestableModelsResult {
	query := s.prepare(ctx, groupID, platform, nil, providers)
	result := query.result
	seen := make(map[string]bool, len(selected))
	for _, model := range selected {
		if ctx.Err() != nil {
			// Cancellation prevents completing the selected-name checks. Return an empty
			// restricted result so callers do not expose only the completed subset.
			return RequestableModelsResult{Restricted: true}
		}
		// Selection validates saved candidate identities; a requested wildcard or
		// undeclared name cannot expand the finite provider configuration into
		// additional entries.
		if seen[model] || !slices.Contains(query.candidates, model) {
			continue
		}
		seen[model] = true
		if resolved, ok := query.resolve(ctx, model); ok {
			result.Models = append(result.Models, resolved)
		}
	}
	if query.err != nil {
		return RequestableModelsResult{Restricted: true, Err: query.err}
	}
	return result
}

// prepare captures query policy, pricing source, finite candidates, and
// provider protocols after optional platform filtering. No providers produces
// an empty query before policy reads, while failed group policy reads return a
// restricted result with no candidates.
//
// A group query defaults billing identity to group-mapped once policy lookup
// succeeds; an available pricing setting can choose another source. Pricing
// read errors retain that default because catalogue permission uses the
// independent group policy. Protocol selections are copied per provider and
// reused, while model rule adapters and provider records remain borrowed for
// the query.
func (s *RequestableResolver) prepare(ctx context.Context, groupID *int64, platform string, baseModels []string, providers []CatalogueProvider) requestableQuery {
	providers = filterRequestableModelProviders(providers, platform)
	query := requestableQuery{providers: providers, billingSource: BillingModelSourceRequested}
	query.attributes = ModelAttributesFromContext(ctx)
	if query.attributes == nil {
		query.attributes = s.Attributes.NewSession()
	}
	if groupID != nil {
		query.groupID = *groupID
	}
	if len(providers) == 0 {
		return query
	}
	currentModels := ConfiguredRequestModelsFromProviders(providers, platform)
	query.result.HadExplicitProviderModels = len(baseModels) > 0 || len(currentModels) > 0
	if groupID != nil && s.GroupPolicies != nil {
		policy, err := s.GroupPolicies.GetGroupPolicy(ctx, *groupID)
		if err != nil {
			// A missing policy read can hide a model restriction. Stop candidate
			// preparation and mark it restricted so listing cannot widen permissions on
			// failure.
			if s.Warn != nil {
				s.Warn("failed to load group policy for requestable model resolution", "group_id", *groupID, "platform", platform, "error", err)
			}
			query.result.Restricted = true
			query.err = err
			return query
		}
		query.policy = policy
		query.billingSource = BillingModelSourceGroupMapped
		// Price configuration controls display identity independently of permission.
		// If its read fails, retain group-mapped billing while the loaded request
		// policy still applies.
		if config, err := s.GroupPolicies.GetPricingConfigForGroup(ctx, *groupID); err == nil && config != nil && config.BillingModelSource != "" {
			query.billingSource = config.BillingModelSource
		}
	}
	query.result.Restricted = query.policy != nil && query.policy.RestrictModels
	configured := make([]string, 0, len(baseModels)+len(currentModels))
	configured = append(configured, baseModels...)
	configured = append(configured, currentModels...)
	query.candidates = mergeRequestableModelCandidates(configured, providers, query.policy)
	return query
}

// ConfiguredRequestModelsFromProviders returns sorted distinct names supplied
// by same-platform provider rules. It retains each supplied spelling, returns
// nil when all lists are empty, and leaves name trimming and wildcard exclusion
// to later candidate merging. The platform comparison uses the supplied string
// exactly, so callers trim a forced platform before collecting names.
func ConfiguredRequestModelsFromProviders(providers []CatalogueProvider, platform string) []string {
	modelSet := make(map[string]struct{})
	hasConfiguredModels := false
	for i := range providers {
		provider := &providers[i]
		if platform != "" && provider.Platform != platform {
			continue
		}
		requestModels := provider.Rules.ConfiguredModels()
		if len(requestModels) == 0 {
			continue
		}
		hasConfiguredModels = true
		for _, model := range requestModels {
			modelSet[model] = struct{}{}
		}
	}
	if !hasConfiguredModels {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// filterRequestableModelProviders trims the optional platform and retains
// matching provider values. A blank platform returns the supplied slice
// directly; a filter allocates a new slice whose rule adapters remain shared.
func filterRequestableModelProviders(providers []CatalogueProvider, platform string) []CatalogueProvider {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return providers
	}
	filtered := make([]CatalogueProvider, 0, len(providers))
	for i := range providers {
		if matchesCataloguePlatform(&providers[i], platform) {
			filtered = append(filtered, providers[i])
		}
	}
	return filtered
}

// mergeRequestableModelCandidates gathers concrete request identities in stable
// source order from base names, group policy, and provider mapping sources.
// Names are trimmed, wildcard-bearing names are excluded, and case-folded
// duplicates retain the first supplied spelling.
//
// Group mapping sources and concrete targets both contribute, allowing a target
// to be validated even when a wildcard source cannot be enumerated. Provider
// mapping sources are sorted before addition; provider rules supply other
// configured targets through the base list. The resulting finite list defines
// which selected models later qualify for eligibility checks.
func mergeRequestableModelCandidates(baseModels []string, providers []CatalogueProvider, policy *GroupPolicyView) []string {
	candidates := make([]string, 0, len(baseModels)+16)
	seen := make(map[string]struct{}, len(baseModels)+16)
	appendModels := func(models ...string) {
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			key := strings.ToLower(model)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, model)
		}
	}

	appendModels(baseModels...)
	if policy != nil {
		appendModels(policy.AllowedModels...)
		appendModels(policy.ModelsList...)
		if mapping := policy.ModelMapping; len(mapping) > 0 {
			sources := sortedModelMappingSources(mapping)
			appendModels(sources...)
			// Wildcard sources cannot be enumerated, but concrete group targets can
			// still pass request eligibility. Include them so configured redirects can
			// offer usable names.
			for _, source := range sources {
				appendModels(mapping[source])
			}
		}
	}

	for i := range providers {
		appendModels(sortedModelMappingSources(providers[i].Rules.Mapping())...)
	}

	return candidates
}

// sortedModelMappingSources returns mapping keys ordered by lowercase spelling,
// then original spelling for ties. Empty maps return nil; the map stays
// unchanged and target values do not influence ordering.
func sortedModelMappingSources(mapping map[string]string) []string {
	if len(mapping) == 0 {
		return nil
	}
	models := make([]string, 0, len(mapping))
	for model := range mapping {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		left := strings.ToLower(models[i])
		right := strings.ToLower(models[j])
		if left != right {
			return left < right
		}
		return models[i] < models[j]
	})
	return models
}

// resolve checks one request name through group mapping, protocol routes,
// provider support, and the configured restriction stage. It retains eligible
// upstream identities and full attribute targets only when at least one
// provider contributes an allowed result.
//
// A nonnil AllowedProtocols selection gates each candidate; an empty selection
// excludes it, while absent policy protocols retain configured identity
// resolution. Each concrete protocol route then resolves its final attributes
// before contributing output capability, pricing identity, and native status. Native status is combined across all providers contributing each
// client protocol, so one converted route removes that protocol's native
// marker. Billing source chooses requested, group-mapped, or a unique
// case-insensitive upstream identity; differing upstream identities set pricing
// ambiguity.
func (q *requestableQuery) resolve(ctx context.Context, requestedModel string) (RequestableModel, bool) {
	policy, providers, billingSource := q.policy, q.providers, q.billingSource
	groupMappedModel := requestedModel
	// A blank mapping supplies no usable identity. Retain the requested name so
	// downstream model support and price-source checks still evaluate a concrete
	// candidate.
	if mapped := strings.TrimSpace(policy.ResolveModel(requestedModel)); mapped != "" {
		groupMappedModel = mapped
	}

	if policy != nil && policy.RestrictModels && policy.RestrictionSource() != BillingModelSourceUpstream {
		pricingModel := ModelForRestriction(policy.RestrictionSource(), requestedModel, groupMappedModel)
		if policy.IsModelRestricted(pricingModel) {
			return RequestableModel{}, false
		}
	}

	upstreamModels := make([]string, 0, len(providers))
	var attributeTargets []modelrules.Target
	var protocols []capability.ProtocolID
	// Each client entry combines all contributing provider routes. One converted
	// route clears its native marker so display consumers do not infer universal
	// native execution.
	nativeByProtocol := map[capability.ProtocolID]bool{}
	for i := range providers {
		provider := &providers[i]
		var candidateProtocols []capability.ProtocolID
		var nativeCandidates []capability.ProtocolID
		targetProtocols := map[capability.ProtocolID]capability.ProtocolID{}
		if policy != nil && policy.AllowedProtocols != nil {
			if policy.RequireOAuthOnly && provider.Type == capability.ProviderTypeAPIKey {
				continue
			}
			for _, source := range policy.AllowedProtocols {
				finalModel, _ := provider.ModelPolicy.Resolve(groupMappedModel)
				target, ok := capability.ResolveRoute(provider.ProtocolsForModel(finalModel), source, policy.ProtocolFallbacks)
				if !ok {
					continue
				}
				candidateProtocols = append(candidateProtocols, source)
				targetProtocols[source] = target
				if target == source {
					nativeCandidates = append(nativeCandidates, source)
				}
			}
			if len(candidateProtocols) == 0 {
				continue
			}
		}
		if !provider.Rules.Supports(ctx, groupMappedModel) {
			continue
		}
		for _, upstreamModel := range provider.Rules.UpstreamModels(ctx, groupMappedModel) {
			if policy != nil && policy.RestrictModels && policy.RestrictionSource() == BillingModelSourceUpstream && policy.IsModelRestricted(upstreamModel) {
				continue
			}
			sources := candidateProtocols
			if len(sources) == 0 {
				sources = []capability.ProtocolID{""}
			}
			contributed := false
			for _, source := range sources {
				target := modelrules.Target{Platform: provider.Platform, ProviderType: provider.Type, Model: upstreamModel, ClientProtocol: string(source), UpstreamProtocol: string(targetProtocols[source])}
				if aware, ok := provider.Rules.(interface {
					// AttributeTarget supplies the full mapped route used for capability lookup.
					AttributeTarget(string, string, capability.ProtocolID, capability.ProtocolID) modelrules.Target
				}); ok {
					target = aware.AttributeTarget(groupMappedModel, upstreamModel, source, targetProtocols[source])
				}
				if !q.preview {
					resolution, err := q.attributes.Resolve(ctx, q.groupID, target)
					if err != nil {
						q.err = err
						return RequestableModel{}, false
					}
					if !modelSupportsClientProtocol(target, resolution.Attributes) {
						continue
					}
				}
				attributeTargets = append(attributeTargets, target)
				contributed = true
				if source == "" {
					continue
				}
				native := slices.Contains(nativeCandidates, source)
				if previous, seen := nativeByProtocol[source]; seen {
					nativeByProtocol[source] = previous && native
				} else {
					nativeByProtocol[source] = native
					protocols = append(protocols, source)
				}
			}
			if contributed {
				upstreamModels = append(upstreamModels, upstreamModel)
			}
		}
	}
	if len(upstreamModels) == 0 {
		return RequestableModel{}, false
	}

	var nativeProtocols []capability.ProtocolID
	for _, source := range protocols {
		if nativeByProtocol[source] {
			nativeProtocols = append(nativeProtocols, source)
		}
	}
	slices.Sort(upstreamModels)
	resolved := RequestableModel{ID: requestedModel, Protocols: protocols, NativeProtocols: nativeProtocols, UpstreamModels: slices.Compact(upstreamModels), AttributeTargets: attributeTargets}
	switch billingSource {
	case BillingModelSourceRequested:
		resolved.PricingModel = requestedModel
	case BillingModelSourceUpstream:
		resolved.PricingModel, resolved.PricingAmbiguous = uniquePricingModel(upstreamModels)
	default:
		resolved.PricingModel = groupMappedModel
	}
	return resolved, true
}

// uniquePricingModel returns one upstream pricing identity when every populated
// name agrees after trimming and case folding. The first nonblank spelling is
// retained so display consumers receive an existing complete identity.
//
// Two eligible routes with distinct normalized names cannot select one public
// upstream price, so the result is empty with ambiguity true. Repeated or
// differently cased spellings of the same name remain unambiguous. A list
// containing no populated name returns empty with false, letting callers
// distinguish missing identity from conflicting routes.
func uniquePricingModel(models []string) (string, bool) {
	var selected string
	selectedKey := ""
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if selectedKey == "" {
			selected = model
			selectedKey = key
			continue
		}
		if key != selectedKey {
			return "", true
		}
	}
	return selected, false
}

// RequestableModelIDs returns client IDs in resolution order using new slice
// storage. Empty input produces an allocated empty list, retaining a
// ready-to-serialize result for catalogue consumers.
func RequestableModelIDs(models []RequestableModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// matchesCataloguePlatform reports whether a provider satisfies the optional
// exact platform filter.
func matchesCataloguePlatform(provider *CatalogueProvider, platform string) bool {
	return platform == "" || provider.Platform == platform
}

// modelSupportsClientProtocol checks declared output against the selected
// entry. Image operations require positive evidence; unknown ordinary text
// models remain requestable, and mixed text/image outputs retain text routes.
func modelSupportsClientProtocol(target modelrules.Target, attrs modelcatalog.Attributes) bool {
	source := capability.ProtocolID(target.ClientProtocol)
	if source == "" {
		return true
	}
	embedding := strings.HasPrefix(strings.ToLower(target.Model), "text-embedding-")
	if embedding || source == capability.ProtocolEmbeddings {
		return embedding && source == capability.ProtocolEmbeddings
	}
	switch source {
	case capability.ProtocolImagesGenerations, capability.ProtocolImagesEdits, capability.ProtocolImageBatches:
		supported := attrs.OutputSupport("image")
		return supported != nil && *supported
	case capability.ProtocolOpenAIChatCompletions, capability.ProtocolAnthropicMessages:
		return !attrs.OutputOnly("image")
	case capability.ProtocolOpenAIResponses, capability.ProtocolResponsesWebSocket:
		return target.Platform != "grok" || !attrs.OutputOnly("image")
	case capability.ProtocolGeminiGenerateContent:
		return true
	default:
		return !attrs.OutputOnly("image")
	}
}
