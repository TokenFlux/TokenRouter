package batchimage

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// public_catalog.go discovers batch-image catalogue models, evaluates group
// model routing and restrictions, and ranks eligible Gemini API and Vertex
// submission candidates.
// Catalogue queries read group policy and pricing-model sources once per call,
// resolve group and provider model mappings, require declared image output
// modalities, and verify per-image pricing before exposing a model to callers.
//
// Candidate selection shares protocol, credential, scheduling, and managed-GCS
// readiness checks between ListModels and Submit, sorting eligible provider
// backends by ascending numeric priority and provider ID so discovery and job
// submission agree on model availability and backend order.

// mappedCandidateModel returns the trimmed mapped target selected by this
// candidate. A nil candidate returns empty text, and an unmatched or blank
// mapping leaves the supplied routing model unchanged.
func mappedCandidateModel(c *Candidate, m string) string {
	if c == nil {
		return ""
	}
	value, matched := c.ResolveMappedModel(m)
	if !matched || strings.TrimSpace(value) == "" {
		// A missing or blank mapped target cannot supply a callable model; retain
		// the routing spelling for catalogue restrictions and pricing-stage
		// selection.
		return m
	}
	return strings.TrimSpace(value)
}

// ListModels returns priced concrete request names supported by the owner's
// group and available batch backends. Its configured reader fetches group policy
// and billing source at most once per catalogue call. Each candidate passes
// group mapping, provider mapping, stage-specific restrictions and per-image
// pricing.
//
// Final output modalities must explicitly include image. Unknown modalities
// exclude the route even when configuration and image prices are present;
// attribute-read errors stop listing. Results are deduplicated within
// each backend and sorted by request name. Catalogue and submission share
// protocol, credential, scheduling and storage eligibility before model checks.
func (s *Public) ListModels(ctx context.Context, owner BatchImageOwner) (*BatchImagePublicModelsResponse, error) {
	if s == nil || s.Repo == nil || s.ProviderRepo == nil {
		return nil, ErrBatchImageDisabled
	}
	if s.Pricing == nil {
		return nil, ErrBatchImageSettlementPricingMissing
	}
	if err := s.EnsureGroupAllowsBatchImage(ctx, owner.GroupID); err != nil {
		return nil, err
	}

	var candidates []string
	var policy *routing.GroupPolicyView
	billingMapping := GroupMappingResult{BillingModelSource: routing.BillingModelSourceGroupMapped}
	if s.PricingConfigService != nil && owner.GroupID != nil {
		var err error
		policy, err = s.PricingConfigService.GetGroupPolicy(ctx, *owner.GroupID)
		if err != nil {
			return nil, err
		}
		// Keep group-mapped pricing as the default when optional config lookup
		// fails; each catalogue entry still needs its resolved per-image price
		// before admission.
		if config, err := s.PricingConfigService.GetPricingConfigForGroup(ctx, *owner.GroupID); err == nil && config != nil {
			billingMapping.PricingConfigID = config.ID
			billingMapping.BillingModelSource = config.BillingModelSource
		}
	}
	if policy != nil {
		candidates = append(candidates, policy.AllowedModels...)
		candidates = append(candidates, policy.ModelsList...)
		for source, target := range policy.ModelMapping {
			candidates = append(candidates, source, target)
		}
	}
	// Several names can map to one upstream model; reuse its modalities within this query.
	outputs := make(map[string]*[]string)
	attributeSession := s.Attributes.NewSession()
	modelsByProvider := make(map[string]map[string]struct{})
	providers, err := s.submissionCandidates(ctx, owner.GroupID, "")
	if err != nil {
		return nil, err
	}
	for _, provider := range providers {
		providerName := provider.backend
		for _, model := range BatchImageModelsFromProviderMapping(&provider.Candidate, candidates...) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			routingModel := strings.TrimSpace(policy.ResolveModel(model))
			if routingModel == "" {
				routingModel = model
			}
			upstreamModel := mappedCandidateModel(&provider.Candidate, routingModel)
			restrictionModel := routing.ModelForRestriction(policy.RestrictionSource(), model, routingModel)
			if policy.RestrictionSource() == routing.BillingModelSourceUpstream {
				restrictionModel = upstreamModel
			}
			if policy.IsModelRestricted(restrictionModel) || !provider.IsModelSupported(routingModel) {
				continue
			}
			modalities, found := outputs[upstreamModel]
			if s.Attributes != nil {
				target := provider.modelAttributeTarget(upstreamModel)
				var groupID int64
				if owner.GroupID != nil {
					groupID = *owner.GroupID
				}
				resolved, err := attributeSession.Resolve(ctx, groupID, target)
				if err == nil {
					modalities = resolved.Attributes.OutputModalities
				} else {
					return nil, err
				}
			} else if !found && s.ModelOutputModalities != nil {
				modalities = s.ModelOutputModalities(upstreamModel)
				outputs[upstreamModel] = modalities
			}
			if modalities == nil || !slices.Contains(*modalities, "image") {
				continue
			}
			mapping := billingMapping
			mapping.MappedModel = routingModel
			pricingModel := BatchImagePricingModel(mapping, model, routingModel, upstreamModel)
			// An image-capable mapping can still lack an applicable image price; omit
			// that catalogue entry before clients choose an unpriceable batch model.
			if _, err := s.Pricing.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: pricingModel, GroupID: owner.GroupID, ImageSize: "1K"}); err != nil {
				continue
			}
			if modelsByProvider[providerName] == nil {
				modelsByProvider[providerName] = make(map[string]struct{})
			}
			modelsByProvider[providerName][model] = struct{}{}
		}
	}

	out := make([]BatchImagePublicModel, 0)
	for _, providerName := range BatchImageProviderSelectionOrder("") {
		models := make([]string, 0, len(modelsByProvider[providerName]))
		for model := range modelsByProvider[providerName] {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			out = append(out, BatchImagePublicModel{
				ID:       model,
				Object:   "image.batch.model",
				Platform: providerName,
			})
		}
	}
	return &BatchImagePublicModelsResponse{Object: "list", Data: out}, nil
}

// ResolveBatchImageGroupModel applies group mapping and the selected model
// restriction stage while retaining trace information. Callers supply a nonnil
// service and WithModelTrace callback. An absent group or mapping service keeps
// the requested model; an applicable restriction rejects provider selection.
func (s *Public) ResolveBatchImageGroupModel(ctx context.Context, groupID *int64, requestedModel string) (GroupMappingResult, string, error) {
	mapping := s.WithModelTrace(ctx, GroupMappingResult{MappedModel: requestedModel}, requestedModel)
	if s == nil || s.PricingConfigService == nil || groupID == nil || *groupID <= 0 {
		return mapping, requestedModel, nil
	}
	mapping = s.WithModelTrace(ctx, s.PricingConfigService.ResolveGroupMapping(ctx, *groupID, requestedModel), requestedModel)
	routingModel := strings.TrimSpace(mapping.MappedModel)
	if routingModel == "" {
		routingModel = requestedModel
	}
	billingModel := routing.ModelForRestriction(mapping.RestrictionModelSource, requestedModel, routingModel)
	if billingModel != "" && s.PricingConfigService.IsModelRestricted(ctx, *groupID, billingModel) {
		return mapping, routingModel, ErrBatchImageNoProviderAvailable
	}
	return mapping, routingModel, nil
}

// BatchImagePricingModel selects the billing model stage through the shared
// routing pricing rule.
func BatchImagePricingModel(mapping GroupMappingResult, requestedModel, groupMappedModel, upstreamModel string) string {
	return routing.BillingModelForPrice(mapping, requestedModel, groupMappedModel, upstreamModel)
}

// SelectProviderAndProvider returns a bound execution backend, candidate and
// final upstream model for the requested route. An explicit backend narrows
// selection; otherwise both backends participate in one ascending priority
// and provider-ID order.
//
// Catalogue and submission share candidate admission before checking each
// routed model. ResolveUpstream supplies the final execution model, which is
// checked against upstream-stage restrictions and image-output attributes.
// The selected model is registered for tracing before binding the provider
// snapshot. Rejected models allow the next candidate to participate; absence
// of any acceptable route returns the capability or provider error.
func (s *Public) SelectProviderAndProvider(
	ctx context.Context,
	owner BatchImageOwner,
	requestedProvider, routingModel string,
	mapping GroupMappingResult,
) (ExecutionProvider, *Candidate, string, error) {
	session := routing.ModelAttributesFromContext(ctx)
	if session == nil {
		session = s.Attributes.NewSession()
	}
	providers, err := s.submissionCandidates(ctx, owner.GroupID, requestedProvider)
	if err != nil {
		return nil, nil, "", err
	}
	var capabilityErr error
	for _, provider := range providers {
		if !provider.IsModelSupported(routingModel) {
			continue
		}
		upstreamModel := strings.TrimSpace(provider.ResolveUpstream(ctx, routingModel))
		if upstreamModel == "" {
			continue
		}
		if owner.GroupID != nil && *owner.GroupID > 0 && s.PricingConfigService != nil &&
			mapping.RestrictionModelSource == BillingModelSourceUpstream &&
			s.PricingConfigService.IsModelRestricted(ctx, *owner.GroupID, upstreamModel) {
			continue
		}
		var groupID int64
		if owner.GroupID != nil {
			groupID = *owner.GroupID
		}
		attributes, err := session.Resolve(ctx, groupID, provider.modelAttributeTarget(upstreamModel))
		if err != nil {
			return nil, nil, "", err
		}
		if err := routing.RequireImageOutput(attributes.Attributes); err != nil {
			capabilityErr = err
			continue
		}
		s.RegisterModel(ctx, upstreamModel)
		return provider.Bind(provider.backend), &provider.Candidate, upstreamModel, nil
	}
	if capabilityErr != nil {
		return nil, nil, "", capabilityErr
	}
	return nil, nil, "", ErrBatchImageNoProviderAvailable
}

// submissionCandidates lists group-bound providers that can create batch jobs.
// Both adapters share the Gemini provider pool; requestedBackend narrows adapter
// admission, and an empty value considers every registered batch backend.
//
// Protocol, credential, scheduling and storage checks precede model discovery
// and submission side effects. Eligible adapters remain attached to each provider
// while sorting by ascending numeric priority and provider ID, so authentication
// type cannot outrank an administrator's configured priority. Model checks remain
// with the caller because a catalogue evaluates several model names per provider.
func (s *Public) submissionCandidates(ctx context.Context, groupID *int64, requestedBackend string) ([]submissionCandidate, error) {
	providers, err := s.ListCandidateProviders(ctx, groupID, PlatformGemini)
	if err != nil {
		return nil, err
	}
	backends := BatchImageProviderSelectionOrder(requestedBackend)
	candidates := make([]submissionCandidate, 0, len(providers))
	for _, provider := range providers {
		if !provider.IsSchedulable() {
			continue
		}
		for _, backend := range backends {
			if s.ProviderExists(backend) && provider.CanSubmit(backend) {
				candidates = append(candidates, submissionCandidate{Candidate: provider, backend: backend})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates, nil
}

// ListCandidateProviders requests provider candidates for a positive bound
// group and platform. A missing repository or group returns the
// unavailable-provider result, so batch submission never widens selection to
// a global provider pool.
func (s *Public) ListCandidateProviders(ctx context.Context, groupID *int64, platform string) ([]Candidate, error) {
	if s.ProviderRepo == nil {
		return nil, ErrBatchImageNoProviderAvailable
	}
	if groupID != nil && *groupID > 0 {
		return s.ProviderRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	return nil, ErrBatchImageNoProviderAvailable
}

// EnsureGroupAllowsBatchImage requires a positive group identity and its
// batch generation permission. Missing group data returns the pricing
// category; a disabled group returns the permission category before
// submission or model catalogue work.
func (s *Public) EnsureGroupAllowsBatchImage(ctx context.Context, groupID *int64) error {
	if groupID == nil || *groupID <= 0 {
		return ErrBatchImageGroupDisabled
	}
	if s.GroupRepo == nil {
		return ErrBatchImageSettlementPricingMissing
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, *groupID)
	if err != nil || group == nil {
		return ErrBatchImageSettlementPricingMissing
	}
	if !group.AllowBatchImageGeneration {
		return ErrBatchImageGroupDisabled
	}
	return nil
}

// BatchImageProviderPlatform returns the Gemini routing platform for batch
// backend lookup and usage reporting. Both supported backends and
// unrecognized labels select that same value; backend admission is checked
// separately by callers.
func BatchImageProviderPlatform(platform string) string {
	switch platform {
	case BatchImageProviderGeminiAPI, BatchImageProviderVertex:
		return PlatformGemini
	default:
		return PlatformGemini
	}
}

// BatchImageProviderSelectionOrder returns a trimmed explicit backend as the
// only backend when supplied. Blank input enumerates Gemini API and Vertex
// for candidate construction and stable public catalogue ordering; provider
// priority determines submission selection across those backends.
func BatchImageProviderSelectionOrder(requestedProvider string) []string {
	if strings.TrimSpace(requestedProvider) != "" {
		return []string{strings.TrimSpace(requestedProvider)}
	}
	return []string{BatchImageProviderGeminiAPI, BatchImageProviderVertex}
}

// BatchImageModelsFromProviderMapping merges supplied candidates with concrete
// provider names and mapping sources, then sorts distinct spellings. Nil
// providers return nil. Supplied candidates are filtered as written, while
// mapping source names are trimmed before wildcard screening; prepared target
// names arrive through the optional candidate interface.
func BatchImageModelsFromProviderMapping(provider *Candidate, candidates ...string) []string {
	if provider == nil {
		return nil
	}
	mapping := provider.GetModelMapping()
	models := make(map[string]struct{})
	if configured, ok := provider.CandidateRules.(interface {
		// GetConfiguredRequestModels supplies concrete request names prepared from
		// provider configuration.
		GetConfiguredRequestModels() []string
	}); ok {
		candidates = append(candidates, configured.GetConfiguredRequestModels()...)
	}
	for _, id := range candidates {
		if id != "" && !strings.ContainsAny(id, "*?") {
			models[id] = struct{}{}
		}
	}
	for model := range mapping {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if strings.ContainsAny(model, "*?") {
			continue
		}
		models[model] = struct{}{}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}
