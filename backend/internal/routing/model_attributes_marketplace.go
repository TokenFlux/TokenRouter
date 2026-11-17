package routing

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// model_attributes_marketplace.go resolves display ranges for public models and
// their client protocols. Marketplace readers need to discover every available
// capability, including modalities that differ between text and media routes.
// Their summaries therefore use unions of resolved route attributes, while
// client configuration exports keep the conservative aggregation implemented in
// model_attributes.go. Both readers resolve the same final model identities and
// administrator overrides before choosing how to summarize them.
//
// One query retains a catalogue publication and batch-loads group profiles.
// Each group caches equivalent attribute identities, and protocol buckets reuse
// the localized values collected for the overall model. Public output carries
// attribute values and protocol identifiers, leaving internal service and
// provider route details inside the resolver.

// MarketplaceModelAttributes contains overall and per-protocol available ranges
// for one publicly requestable model.
type MarketplaceModelAttributes struct {
	Presentation modelcatalog.Presentation                           // overall available attribute range
	Protocols    map[capability.ProtocolID]modelcatalog.Presentation // client protocol attribute ranges
}

// ResolveMarketplaceGroups batch-loads visible group profiles and resolves both
// overall and per-protocol unions from one retained catalogue View. Empty groups
// are omitted, absent profiles inherit defaults, and repository failures return
// an error before any public summaries are produced.
//
// A group's route cache is shared across its models but never across different
// profiles. Each model is resolved once before its values are divided into client
// protocol buckets. Name-only legacy targets contribute to the overall range
// without inventing a client protocol or publishing internal target information.
func (s *ModelAttributeService) ResolveMarketplaceGroups(ctx context.Context, groups map[int64][]RequestableModel) (map[int64]map[string]MarketplaceModelAttributes, error) {
	result := make(map[int64]map[string]MarketplaceModelAttributes, len(groups))
	ids := make([]int64, 0, len(groups))
	for id, models := range groups {
		if len(models) > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	configs := map[int64]*ModelAttributeConfig{}
	if s.Repo != nil {
		var err error
		configs, err = s.Repo.ForGroups(ctx, ids)
		if err != nil {
			return nil, err
		}
	}
	view := s.viewForContext(ctx)
	candidates := s.identityCandidates()
	language := locale.FromContext(ctx)
	for _, id := range ids {
		models := make(map[string]MarketplaceModelAttributes, len(groups[id]))
		cache := make(map[modelrules.TargetKey]modelcatalog.Attributes)
		for _, model := range groups[id] {
			resolved := s.resolveModelRoutes(view, configs[id], model, language, candidates, cache)
			presentation := modelcatalog.Union(resolved.values)
			presentation.SearchTerms = resolved.searchTerms
			presentation.LocalizationResolution = resolved.resolution
			protocols := make(map[capability.ProtocolID]modelcatalog.Presentation, len(resolved.byProtocol))
			for protocol, values := range resolved.byProtocol {
				protocols[protocol] = modelcatalog.Union(values)
			}
			models[model.ID] = MarketplaceModelAttributes{Presentation: presentation, Protocols: protocols}
		}
		result[id] = models
	}
	return result, nil
}
