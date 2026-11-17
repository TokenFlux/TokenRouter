package routing

import (
	"context"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// requestable_catalogue.go loads provider configuration and delegates
// requestable-model resolution.
// Catalogue queries load current provider configuration before validating
// finite saved request names. A missing reader represents absent catalogue
// wiring, while a provider read failure produces a restricted empty result so
// callers do not display models from incomplete eligibility data.

// RequestableCatalogue combines a current provider reader with finite
// configured-model resolution.
type RequestableCatalogue struct {
	// Read scopes providers to a group when given an identifier. A nil group is
	// the prefetch query; readers return provider records that remain stable
	// through resolution.
	Read     func(context.Context, *int64) ([]CatalogueProvider, error)
	Resolver RequestableResolver  // prepared candidate eligibility resolver
	Warn     func(string, ...any) // optional provider read warning
}

// Prefetch reads providers without a group filter for callers preparing
// multi-group listings. Its boolean marks a successful read, including an empty
// result; missing catalogue wiring returns false without an error.
func (c *RequestableCatalogue) Prefetch(ctx context.Context) ([]CatalogueProvider, bool, error) {
	if c == nil || c.Read == nil {
		return nil, false, nil
	}
	values, err := c.Read(ctx, nil)
	return values, err == nil, err
}

// ResolveRequestableModels reads current providers and validates their
// configured names against group policy. Missing wiring returns an unconfigured
// result; read failures optionally warn and return an empty result marked
// restricted. Callers use an empty platform for mixed groups or a trimmed
// forced-platform value for the resolver's exact configured-name comparison.
func (c *RequestableCatalogue) ResolveRequestableModels(ctx context.Context, groupID *int64, platform string) RequestableModelsResult {
	if c == nil || c.Read == nil {
		return RequestableModelsResult{}
	}
	providers, err := c.Read(ctx, groupID)
	if err != nil {
		// Provider read failure leaves eligibility unknown. Return a restricted empty
		// catalogue so display consumers cannot reuse unverified model candidates
		// from this query.
		if c.Warn != nil {
			var id int64
			if groupID != nil {
				id = *groupID
			}
			c.Warn("failed to load providers for requestable model resolution", "group_id", id, "platform", platform, "error", err)
		}
		return RequestableModelsResult{Restricted: true, Err: err}
	}
	return c.Resolver.ResolveWithProviders(ctx, groupID, platform, nil, providers)
}

// ResolveSelectedModels reads current providers and validates exact selected
// names from their prepared candidate set. Missing wiring returns an
// unconfigured result, and a read failure returns a restricted empty result.
// Callers pass an empty or trimmed platform, and selected names exactly match
// the prepared candidates retained by the resolver.
func (c *RequestableCatalogue) ResolveSelectedModels(ctx context.Context, groupID *int64, platform string, models []string) RequestableModelsResult {
	if c == nil || c.Read == nil {
		return RequestableModelsResult{}
	}
	providers, err := c.Read(ctx, groupID)
	if err != nil {
		// Selected-name validation needs the current provider set. Mark read failure
		// restricted rather than declaring any selected model eligible from missing
		// rules.
		return RequestableModelsResult{Restricted: true, Err: err}
	}
	return c.Resolver.ResolveSelectedWithProviders(ctx, groupID, platform, models, providers)
}

// AttributeTargets enumerates configured routes for an administrator preview.
// It applies provider, group, and protocol eligibility while leaving capability
// evaluation to the supplied preview draft, allowing unknown models to acquire
// capabilities before their first successful media request.
func (c *RequestableCatalogue) AttributeTargets(ctx context.Context, groupID int64, model, clientProtocol string) ([]modelrules.Target, error) {
	if c == nil || c.Read == nil {
		return nil, apperror.ServiceUnavailable("MODEL_ATTRIBUTES_UNAVAILABLE", "model catalogue is unavailable")
	}
	providers, err := c.Read(ctx, &groupID)
	if err != nil {
		return nil, err
	}
	query := c.Resolver.prepare(ctx, &groupID, "", nil, providers)
	if query.err != nil {
		return nil, query.err
	}
	query.attributes = nil
	query.preview = true
	if !slices.Contains(query.candidates, model) {
		return []modelrules.Target{}, nil
	}
	resolved, _ := query.resolve(ctx, model)
	if query.err != nil {
		return nil, query.err
	}
	targets := make([]modelrules.Target, 0, len(resolved.AttributeTargets))
	for _, target := range resolved.AttributeTargets {
		if clientProtocol == "" || target.ClientProtocol == clientProtocol {
			targets = append(targets, target)
		}
	}
	return targets, nil
}
