package usage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// system_usage.go prices internal model observations and submits immutable
// records to provider-only settlement. System requests retain measured usage
// even when execution fails, but missing usage must not become a zero-token
// free request. Successful operation-priced responses can establish their
// units without token counters, while token pricing requires observed usage.
//
// Pricing runs once before persistence retries, so a changing catalog cannot
// change the fingerprint of a previously submitted attempt. Persistence uses
// a bounded context independent of client cancellation and retries the same
// row. It never repeats model execution. The injected writer owns atomic
// ledger, provider quota, and analytical-row persistence.

// SystemRecorder resolves system costs through shared billing services and
// writes provider-only settlement with no customer funding identity.
type SystemRecorder struct {
	// RequiresExplicitPricing identifies provider aliases that cannot borrow a
	// different vendor's catalog price. Nil preserves unrestricted catalog lookup.
	RequiresExplicitPricing func(platform, model string) bool

	Calculator *billing.Calculator    // shared cost arithmetic service
	Resolver   *billing.PriceResolver // shared pricing rule resolver
	// Save must atomically claim the attempt, update provider spending, and
	// persist the row. Repeated calls receive the same immutable financial data.
	Save func(context.Context, *UsageLog, string) error
}

// Record prices and persists a complete, exclusively owned system observation.
// The caller supplies source, provider, usage state, and immutable attribution;
// no prompt or response body is accepted. Returned errors describe recording,
// not upstream execution, and callers report them without repeating inference.
//
// Already observed work survives caller cancellation through a five-second
// persistence budget. Three attempts share one frozen calculation. Unknown
// costs remain explicitly unknown, while partial measured costs can increment
// provider spending without implying the entire request cost is known.
// @project-doc docs/domains/routing_and_billing.md#system_model_cost
func (r *SystemRecorder) Record(ctx context.Context, row *UsageLog, providerType string) error {
	if row == nil || row.SystemUsage == nil || !ValidRequestSource(row.RequestSource) || row.RequestSource == "" || row.RequestSource == "system" || row.RequestSource == "all" || row.RequestSource == SourceUser {
		return errors.New("invalid system usage observation")
	}
	if r == nil || r.Save == nil {
		return errors.New("system usage recorder unavailable")
	}
	// Caller cancellation must not erase completed upstream work. The independent
	// deadline bounds pricing and retries while keeping all attribution values.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	row.UserID, row.BillingUserID, row.APIKeyID = 0, 0, 0
	row.TeamID, row.SubscriptionID = nil, nil
	row.ActualCost, row.SubscriptionAmountUSD, row.BalanceAmountUSD = 0, 0, 0
	row.BillingAllocations = nil
	row.RateMultiplier = 1
	row.SystemUsage.CostState = "unknown"
	r.price(writeCtx, row)
	var err error
	for attempt := range 3 {
		if err = r.Save(writeCtx, row, providerType); err == nil {
			return nil
		}
		if attempt == 2 {
			break
		}
		// Retrying only persistence reuses the stable request identity and frozen
		// price; an unknown commit outcome therefore cannot charge the provider twice.
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-writeCtx.Done():
			timer.Stop()
			return errors.Join(err, writeCtx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("record system usage %s: %w", row.RequestID, err)
}

// price resolves a base price and an optional independent provider cost.
// Missing measurements or unavailable prices retain unknown cost; explicit
// zero prices remain calculated and no user charge is assigned.
//
// Provider cards use the same model ordering and multiplier as gateway
// reporting. Base pricing remains available when no separate provider card
// resolves, including catalog-only requests with no selected group. Partial
// counters permit only token costs, since failed operation counts do not
// establish completed image or per-request units.
func (r *SystemRecorder) price(ctx context.Context, row *UsageLog) {
	if r.Calculator == nil {
		return
	}
	tokens := billing.UsageTokens{
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
		CacheCreationTokens: row.CacheCreationTokens, CacheReadTokens: row.CacheReadTokens,
		CacheCreation5mTokens: row.CacheCreation5mTokens, CacheCreation1hTokens: row.CacheCreation1hTokens,
		ImageInputTokens:     row.ImageInputTokens,
		ImageCacheReadTokens: row.ImageCacheReadTokens, ImageOutputTokens: row.ImageOutputTokens,
	}
	model := row.Model
	if row.UpstreamModel != nil && *row.UpstreamModel != "" {
		model = *row.UpstreamModel
	}
	if row.SystemUsage.PricingModel != "" {
		model = row.SystemUsage.PricingModel
	}
	count := max(1, row.ImageCount)
	input := billing.CostInput{
		Ctx: ctx, Model: model, GroupID: row.GroupID,
		Tokens: tokens, RequestCount: count, SizeTier: systemString(row.ImageSize),
		RateMultiplier: 1, PricingAt: row.CreatedAt,
		ServiceTier:     systemString(row.ServiceTier),
		ReasoningEffort: systemString(row.ReasoningEffort), Resolver: r.Resolver,
	}
	cost, err := r.calculate(ctx, row, input)
	// Configured prices can name the requested or group-mapped alias rather
	// than the final provider model. Only unavailable earlier candidates fall
	// through, preserving explicit zero prices and each candidate's alias policy.
	for _, candidate := range []string{row.RequestedModel, row.SystemUsage.MappedModel} {
		if err == nil {
			break
		}
		if candidate == "" || candidate == model || candidate == input.Model {
			continue
		}
		input.Model = candidate
		cost, err = r.calculate(ctx, row, input)
	}
	if cost != nil && cost.BillingMode == "" {
		cost.BillingMode = "token"
	}
	costMode := "token"
	if cost != nil {
		costMode = cost.BillingMode
	}
	measured := row.SystemUsage.UsageState != "missing"
	if err == nil && cost != nil && ((cost.BillingMode == "token" && measured) || (cost.BillingMode != "token" && row.SystemUsage.Outcome == "success" && (cost.BillingMode != "image" || row.ImageCount > 0))) {
		row.TotalCost = cost.TotalCost
		row.InputCost, row.OutputCost = cost.InputCost, cost.OutputCost
		row.CacheCreationCost, row.CacheReadCost = cost.CacheCreationCost, cost.CacheReadCost
		row.ImageInputCost, row.ImageOutputCost = cost.ImageInputCost, cost.ImageOutputCost
		row.BillingMode = &cost.BillingMode
		row.ProviderStatsCost = &cost.TotalCost
		row.SystemUsage.PricingSource = "base"
		row.SystemUsage.CostState = "calculated"
	}
	if (measured || row.SystemUsage.Outcome == "success") && r.Resolver != nil {
		providerTokens := tokens
		if !measured {
			providerTokens = billing.UsageTokens{}
		}
		groupID := int64(0)
		if row.GroupID != nil {
			groupID = *row.GroupID
		}
		providerCost, providerMode := r.Resolver.ResolveProviderStatsWithMode(ctx, billing.ProviderStatsCostInput{
			ProviderID: row.ProviderID, GroupID: groupID,
			DisableCatalogFallback: r.RequiresExplicitPricing != nil && r.RequiresExplicitPricing(row.Platform, model),
			UpstreamModel:          model, RequestedModel: row.RequestedModel, MappedModel: row.SystemUsage.MappedModel,
			PreferRequestedModel: row.Platform == "qoder",
			Tokens:               providerTokens, RequestCount: count,
			ServiceTier:     systemString(row.ServiceTier),
			ReasoningEffort: systemString(row.ReasoningEffort),
		})
		if providerCost != nil && providerMode != "token" && (row.SystemUsage.Outcome != "success" || (providerMode == "image" && row.ImageCount == 0)) {
			// Partial tokens do not prove that an operation-priced model completed.
			// Retain its usage evidence, but do not substitute customer base prices
			// for a provider rule whose billing unit cannot yet be established.
			row.ProviderStatsCost = nil
			row.TotalCost, row.InputCost, row.OutputCost = 0, 0, 0
			row.CacheCreationCost, row.CacheReadCost = 0, 0
			row.ImageInputCost, row.ImageOutputCost = 0, 0
			row.SystemUsage.CostState = "unknown"
			return
		}
		if providerCost != nil {
			costMode = string(providerMode)
			row.ProviderStatsCost = providerCost
			row.SystemUsage.PricingSource = "provider"
			row.SystemUsage.CostState = "calculated"
		}
	}
	if row.SystemUsage.CostState == "calculated" && row.SystemUsage.UsageState == "partial" && costMode == "token" {
		row.SystemUsage.CostState = "partial"
	}
}

// systemString reads optional recorded text for shared pricing inputs. Nil
// returns empty text, while supplied values remain unchanged, leaving
// normalization and default interpretation to the pricing resolver.
func systemString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// calculate rejects unconfigured cross-vendor aliases before shared arithmetic.
// A real override or priced profile reference permits the alias; missing group
// context and catalog-only matches remain unavailable rather than borrowing a
// Claude price for a compatible provider's unrelated model. Resolved references
// retain their target identity in input.Resolved, so the subsequent calculator
// uses the same profile snapshot instead of rereading a changing mapping.
func (r *SystemRecorder) calculate(ctx context.Context, row *UsageLog, input billing.CostInput) (*billing.CostBreakdown, error) {
	if r.RequiresExplicitPricing != nil && r.RequiresExplicitPricing(row.Platform, input.Model) {
		if r.Resolver == nil || row.GroupID == nil {
			return nil, errors.New("system model requires explicit pricing")
		}
		resolved := r.Resolver.Resolve(ctx, billing.PricingInput{Model: input.Model, GroupID: row.GroupID})
		referencePriced := resolved.ConfiguredReference && r.Calculator.DisplayPricingWithResolvedMultipliers(input.Model, 1, resolved).PriceStatus == "priced"
		if !resolved.HasEffectiveOverridePricing() && !referencePriced {
			return nil, errors.New("system model requires explicit pricing")
		}
		input.Resolved = resolved
	}
	return r.Calculator.CalculateCostUnified(input)
}
