package app

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// routing_marketplace.go assembles marketplace model display with shared
// routing, capacity, pricing, and public statistics. Model presentation also
// receives current attributes and availability probes. Its statistics adapter
// exposes dashboard token/user totals, while price and modality calls delegate
// to shared billing components so public presentation uses the assembled
// pricing readers. Personal browsing reads explicit grants from current user
// storage without passing identity records into routing or changing public visibility.

// marketplacePrices adapts billing quotes and model modalities for marketplace
// presentation.
type marketplacePrices struct {
	resolver   *billing.PriceResolver // group model quotes
	calculator *billing.Calculator    // catalog model modalities
}

// marketplaceStats narrows public dashboard counts for the marketplace
// homepage.
type marketplaceStats struct {
	source *usage.DashboardService // public dashboard totals
}

// marketplaceGroupAccess narrows current user storage to exclusive group grants.
type marketplaceGroupAccess struct {
	users identity.UserRepository // current identity storage reader
}

// AllowedGroupIDs reads explicit exclusive grants without administrator bypass.
// Missing or inactive accounts fail closed. Returned IDs are copied so callers
// cannot mutate the identity repository's loaded permission slice.
func (a marketplaceGroupAccess) AllowedGroupIDs(ctx context.Context, userID int64) ([]int64, error) {
	user, err := a.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive() {
		return nil, apperror.Unauthorized("UNAUTHORIZED", "User not active")
	}
	return slices.Clone(user.AllowedGroups), nil
}

// provideMarketplace binds groups, settings, requestable models, and
// price/capacity presentation. cfg, catalogue, and attributes are required by
// construction; the marketplace retains the supplied timezone and current
// model-attribute resolver for later queries.
func provideMarketplace(groups *routingpostgres.GroupStore, store *settings.Store, catalogue *routing.RequestableCatalogue, prices *billing.PriceResolver, calculator *billing.Calculator, capacity *routing.CapacityService, availability routing.GroupAvailabilityProbeRepository, cfg *config.Config, attributes *routing.ModelAttributeService) *routing.Marketplace {
	options := routing.MarketplaceOptions{Attributes: attributes.ResolveMarketplaceGroups, Timezone: cfg.Timezone, Now: time.Now, Warn: slog.Warn}
	return routing.NewMarketplace(groups, store, catalogue, catalogue.Resolver, marketplacePrices{prices, calculator}, capacity, availability, options)
}

// PublicStats returns dashboard token and user counts for public marketplace
// display.
func (s marketplaceStats) PublicStats(ctx context.Context) (routingdto.ModelMarketplaceStats, error) {
	value, err := s.source.GetPublicDashboardStats(ctx)
	if err != nil {
		return routingdto.ModelMarketplaceStats{}, err
	}
	return routingdto.ModelMarketplaceStats{TodayTokens: value.TodayTokens, TotalTokens: value.TotalTokens, TotalUsers: value.TotalUsers}, nil
}

// Quote delegates the requested group/model and display multipliers to billing
// public pricing.
func (p marketplacePrices) Quote(ctx context.Context, request routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	return p.resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{
		Model: request.Model, GroupID: &request.GroupID,
	}, RateMultiplier: request.RateMultiplier, FreeFastApplicable: request.FreeFastApplicable})
}

// GetModelModalities delegates input and output modality reads to the shared
// calculator.
func (p marketplacePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}

// provideMarketplaceHTTP combines the marketplace with public statistics and grants.
// The identity store supplies fresh personal permissions independently of the
// homepage reader; the same handler serves separately registered visibility scopes.
func provideMarketplaceHTTP(core *routing.Marketplace, dashboard *usage.DashboardService, users *identitypostgres.UserStore) *routinghttp.MarketplaceHandler {
	return routinghttp.NewMarketplaceHandler(core, marketplaceStats{source: dashboard}, marketplaceGroupAccess{users: users})
}
