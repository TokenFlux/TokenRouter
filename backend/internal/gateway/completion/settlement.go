package completion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// settlement.go builds usage billing commands and applies committed allocations
// and effects to completion records. User funding and provider quota use
// different cost bases. The transaction command carries the financial key,
// payer and actor identities, funding policy, and measured usage together so
// billing can deduplicate and allocate one request. An explicitly present
// provider-statistics cost, including zero, replaces total model cost when
// calculating upstream quota consumption.
//
// Apply detaches settlement from request cancellation, then waits for the store's
// result before changing the analysis row or running committed effects. A newly
// applied result supplies final subscription/balance allocations and can trigger
// auth invalidation. Duplicate results only mark provider activity here. Analysis
// persistence happens later in the recorder, so its failure does not repeat the
// financial transaction.

// SettlementInput contains calculated charges and identities for settlement
// and its committed effects.
type SettlementInput struct {
	Cost               *CostBreakdown            // calculated model and user costs
	User               *PayerSnapshot            // captured funding payer
	APIKey             *KeySnapshot              // captured actor and funding policy
	Provider           *ProviderSnapshot         // executing provider identity
	Subscription       *billing.UserSubscription // preliminary subscription price evidence
	RequestPayloadHash string                    // semantic or identity fingerprint evidence
	// ProviderRateMultiplier: upstream quota cost factor.
	// SubscriptionRateMultiplier: preliminary subscription charge factor.
	// SubscriptionRateMultiplierScale: peak factor for reloaded plan rates.
	// BalanceRateMultiplier: excess balance charge factor. Subscription and balance
	// rates accompany the same base amount so the transaction can split funding;
	// provider rates apply separately to its statistics cost basis.
	ProviderRateMultiplier, SubscriptionRateMultiplier, SubscriptionRateMultiplierScale, BalanceRateMultiplier float64
	// QuotaUpdates captures the entrypoint's key quota capability; it gates
	// key total/window costs and exhausted-key invalidation, while provider quota
	// eligibility is decided independently from the provider snapshot.
	QuotaUpdates bool
	// BillingBaseAmountUSD overrides TotalCost for user allocation, including
	// an explicitly zero standard-price amount when free Fast retains premium costs.
	BillingBaseAmountUSD *float64
}

// usageBillingParams names the settlement inputs used by recorder implementation
// helpers while sharing SettlementInput's identity and allocation rules.
type usageBillingParams = SettlementInput

// shouldDeductAPIKeyQuota reports whether positive user cost updates key quota.
// The caller supplies nonnil Cost and APIKey; a positive configured quota and
// captured QuotaUpdates capability are both required for command inclusion.
func (p *SettlementInput) shouldDeductAPIKeyQuota() bool {
	return p.Cost.ActualCost > 0 && p.APIKey.Quota > 0 && p.QuotaUpdates
}

// shouldUpdateRateLimits reports whether user cost updates key rolling windows.
// The caller supplies nonnil Cost and APIKey; positive ActualCost, configured
// rate limits, and captured QuotaUpdates capability are required for command inclusion.
func (p *SettlementInput) shouldUpdateRateLimits() bool {
	return p.Cost.ActualCost > 0 && p.APIKey.HasRateLimits && p.QuotaUpdates
}

// shouldUpdateProviderQuota reports whether a positive provider cost updates
// upstream quota. The caller supplies a nonnil Provider; its eligibility and
// configured limit flags govern this independently of the key quota capability.
func (p *SettlementInput) shouldUpdateProviderQuota(cost float64) bool {
	return cost > 0 && p.Provider.QuotaEligible && p.Provider.HasQuotaLimit
}

// BuildCommand combines captured ownership, measured usage, and calculated costs
// into a normalized financial command. Missing cost, key, payer, or provider data
// returns nil; requestID is the financial operation key supplied by SettlementKey.
//
// Actor evidence can override the payer even when the captured actor is zero.
// Optional team and positive group IDs are copied into the command. Normalization
// retains PreferredSubscriptionID for locked subscription funding and clears it for
// other modes. User quota/window costs use positive ActualCost and captured
// capability, while provider quota uses its separate statistics basis and rate.
// A present billing-base override drives funding allocation independently of the
// provider cost retained in the analysis row.
func BuildCommand(requestID string, usageLog *UsageLog, p *usageBillingParams) *billing.UsageBillingCommand {
	if p == nil || p.Cost == nil || p.APIKey == nil || p.User == nil || p.Provider == nil {
		return nil
	}

	cmd := &billing.UsageBillingCommand{
		RequestID:          requestID,
		APIKeyID:           p.APIKey.ID,
		APIKeyBillingMode:  p.APIKey.BillingMode,
		UserID:             p.User.ID,
		ActorUserID:        p.User.ID,
		ProviderID:         p.Provider.ID,
		ProviderType:       p.Provider.Type,
		RequestPayloadHash: strings.TrimSpace(p.RequestPayloadHash),
	}
	if p.APIKey.PreferredSubscriptionID != nil {
		preferredSubscriptionID := *p.APIKey.PreferredSubscriptionID
		cmd.PreferredSubscriptionID = &preferredSubscriptionID
	}
	// The actor can be present with identifier zero; honor that captured evidence
	// so payer identity is used only when the request carried no actor identity.
	if p.APIKey.ActorUserPresent || p.APIKey.ActorUserID != 0 {
		cmd.ActorUserID = p.APIKey.ActorUserID
	}
	if p.APIKey.TeamID != nil {
		teamID := *p.APIKey.TeamID
		cmd.TeamID = &teamID
	}
	if p.APIKey.GroupID != nil && *p.APIKey.GroupID > 0 {
		groupID := *p.APIKey.GroupID
		cmd.GroupID = &groupID
	}
	if usageLog != nil {
		cmd.Model = usageLog.Model
		cmd.BillingType = usageLog.BillingType
		cmd.InputTokens = usageLog.InputTokens
		cmd.OutputTokens = usageLog.OutputTokens
		cmd.CacheCreationTokens = usageLog.CacheCreationTokens
		cmd.CacheReadTokens = usageLog.CacheReadTokens
		cmd.ImageCount = usageLog.ImageCount
		cmd.ImageFrameCount = usageLog.ImageFrameCount
		cmd.ImageCacheReadTokens = usageLog.ImageCacheReadTokens
		if usageLog.ServiceTier != nil {
			cmd.ServiceTier = *usageLog.ServiceTier
		}
		if usageLog.ReasoningEffort != nil {
			cmd.ReasoningEffort = *usageLog.ReasoningEffort
		}
	}

	if p.Cost.ActualCost > 0 {
		cmd.BillableAmountUSD = p.Cost.ActualCost
	}
	baseAmount := p.Cost.TotalCost
	if p.BillingBaseAmountUSD != nil {
		baseAmount = *p.BillingBaseAmountUSD
	}
	// The chosen user base can be standard-priced Fast while TotalCost retains
	// premium provider cost; allocate funding from this base and its separate factors.
	if baseAmount > 0 {
		cmd.BaseAmountUSD = baseAmount
		ApplyRateMultipliers(cmd, p)
	}
	if p.shouldDeductAPIKeyQuota() {
		cmd.APIKeyQuotaCost = p.Cost.ActualCost
	}
	if p.shouldUpdateRateLimits() {
		cmd.APIKeyRateLimitCost = p.Cost.ActualCost
	}
	providerQuotaCost := ProviderQuotaCost(usageLog, p)
	if p.shouldUpdateProviderQuota(providerQuotaCost) {
		cmd.ProviderQuotaCost = providerQuotaCost
	}

	cmd.Normalize()
	return cmd
}

// ProviderQuotaCost calculates upstream quota consumption from the provider's
// cost basis. Missing parameters or cost yields zero, and a present statistics
// amount replaces TotalCost even when that amount is zero.
//
// The provider multiplier applies after choosing that base. Nonpositive base
// amounts or multipliers produce no quota increment, independently of the user
// ActualCost and subscription/balance factors. This matches the provider-cost
// basis used by usage statistics and lets free user pricing retain a positive
// provider expense or a configured zero provider expense.
func ProviderQuotaCost(usageLog *UsageLog, p *usageBillingParams) float64 {
	if p == nil || p.Cost == nil {
		return 0
	}
	baseCost := p.Cost.TotalCost
	if usageLog != nil && usageLog.ProviderStatsCost != nil {
		baseCost = *usageLog.ProviderStatsCost
	}
	if baseCost <= 0 || p.ProviderRateMultiplier <= 0 {
		return 0
	}
	return baseCost * p.ProviderRateMultiplier
}

// ApplyRateMultipliers writes the factors billing uses when splitting funding.
// Missing command, parameters, cost, or a positive base leaves the command
// unchanged; a present billing-base override replaces TotalCost for this decision.
//
// ActualCost divided by the chosen base supplies an inferred factor when captured
// subscription or balance factors are unusable. RateOrFallback retains supplied
// values when that inferred factor is zero, including supplied zero. The
// subscription scale defaults to one when nonpositive, allowing the transaction
// to apply the chosen peak factor to a plan rate it reloads.
func ApplyRateMultipliers(cmd *billing.UsageBillingCommand, p *usageBillingParams) {
	if cmd == nil || p == nil || p.Cost == nil {
		return
	}
	baseAmount := p.Cost.TotalCost
	if p.BillingBaseAmountUSD != nil {
		baseAmount = *p.BillingBaseAmountUSD
	}
	if baseAmount <= 0 {
		return
	}

	effectiveRate := p.Cost.ActualCost / baseAmount

	cmd.SubscriptionRateMultiplier = RateOrFallback(p.SubscriptionRateMultiplier, effectiveRate)
	cmd.SubscriptionRateMultiplierScale = p.SubscriptionRateMultiplierScale
	if cmd.SubscriptionRateMultiplierScale <= 0 {
		cmd.SubscriptionRateMultiplierScale = 1
	}
	cmd.BalanceRateMultiplier = RateOrFallback(p.BalanceRateMultiplier, effectiveRate)
}

// Apply submits one financial command and updates the row from its committed
// result. Missing parameters or effects returns false with nil error before
// submission; missing storage or an invalid command produces an error.
//
// The store receives a context detached from caller cancellation with a fresh
// fifteen-second budget. Errors return before committed effects. A nil or
// unapplied result marks provider activity and returns false; a newly applied
// result replaces allocation metadata, optionally invalidates exhausted-key auth,
// and then runs Settled effects. Callers write analysis data afterward and retry
// its persistence independently of this transaction.
func (s *Recorder) Apply(ctx context.Context, requestID string, usageLog *UsageLog, p *usageBillingParams) (bool, error) {
	if p == nil || s.effects == nil {
		return false, nil
	}

	cmd := BuildCommand(requestID, usageLog, p)
	if s.store == nil {
		return false, fmt.Errorf("usage billing repository is required")
	}
	if cmd == nil || cmd.RequestID == "" {
		return false, fmt.Errorf("usage billing command is invalid")
	}

	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()

	// Financial deduplication and allocation commit before row mutation or cache
	// effects, so later log persistence can fail without repeating the funds command.
	result, err := s.store.Apply(billingCtx, cmd)
	if err != nil {
		return false, err
	}

	// A duplicate contributes no new allocation or cache deduction; record provider
	// activity for the observed request while leaving committed-effect dispatch skipped.
	if result == nil || !result.Applied {
		s.effects.ProviderUsed(p.Provider.ID)
		return false, nil
	}

	ApplyResultToLog(usageLog, result)
	// Exhausted admission data is invalidated before the remaining committed effects
	// so a configured key quota update reaches the next authentication lookup.
	if result.APIKeyQuotaExhausted {
		if p.QuotaUpdates && p.APIKey != nil && p.APIKey.Key != "" {
			s.effects.InvalidateAuth(billingCtx, p.APIKey.Key)
		}
	}

	s.effects.Settled(*p, result)
	return true, nil
}

// detachedBillingContext retains source values while removing its cancellation
// and deadline, then supplies a fifteen-second timeout. Nil source starts from
// Background. Callers invoke the returned cancel function after storage work completes.
func detachedBillingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	// Funds work follows observed usage even after the client leaves; retain request
	// values for correlation while giving this storage attempt its own execution budget.
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, 15*time.Second)
}

// ApplyResultToLog replaces provisional funding metadata with transaction
// allocations. Nil row or result is ignored. Allocation slices and their optional
// identifiers are copied, and the first subscription allocation supplies the row's
// subscription ID.
//
// Positive subscription spending selects subscription BillingType even when
// balance also contributes. ActualCost and RateMultiplier change together only
// when the allocation total is nonnegative and EffectiveRateMultiplier is present;
// otherwise their calculated values remain. Committed subscription and balance
// amounts explain mixed funding while the row's primary category names the
// subscription share.
func ApplyResultToLog(usageLog *UsageLog, result *billing.UsageBillingApplyResult) {
	if usageLog == nil || result == nil {
		return
	}

	usageLog.SubscriptionAmountUSD = result.SubscriptionAmountUSD
	usageLog.BalanceAmountUSD = result.BalanceAmountUSD
	usageLog.BillingAllocations = cloneBillingAllocations(result.BillingAllocations)
	usageLog.SubscriptionID = firstAllocatedSubscriptionID(result.BillingAllocations)
	if billable := usageBillingResultBillableAmount(result); billable >= 0 && result.EffectiveRateMultiplier != nil {
		usageLog.ActualCost = billable
		usageLog.RateMultiplier = *result.EffectiveRateMultiplier
	}
	switch {
	case result.SubscriptionAmountUSD > 0:
		usageLog.BillingType = BillingTypeSubscription
	default:
		usageLog.BillingType = BillingTypeBalance
	}
}

// cloneBillingAllocations copies allocation entries and their optional identity
// pointers through billing.CloneBillingAllocation. Empty input returns nil, so
// later row mutations cannot change the transaction result's allocation storage.
func cloneBillingAllocations(allocations []billing.BillingAllocation) []billing.BillingAllocation {
	if len(allocations) == 0 {
		return nil
	}
	cloned := make([]billing.BillingAllocation, len(allocations))
	for i, allocation := range allocations {
		cloned[i] = billing.CloneBillingAllocation(allocation, allocation.AmountUSD)
	}
	return cloned
}

// usageBillingResultBillableAmount sums committed subscription and balance
// amounts. A nil result returns -1 as unavailable evidence, allowing callers to
// retain calculated charges instead of treating missing allocation data as zero.
func usageBillingResultBillableAmount(result *billing.UsageBillingApplyResult) float64 {
	if result == nil {
		return -1
	}
	return result.SubscriptionAmountUSD + result.BalanceAmountUSD
}

// firstAllocatedSubscriptionID copies the first identified subscription
// allocation's ID. Balance entries and subscription entries with nil IDs are
// skipped; no identified subscription returns nil for the usage row.
func firstAllocatedSubscriptionID(allocations []billing.BillingAllocation) *int64 {
	for i := range allocations {
		if allocations[i].Type != billing.BillingAllocationTypeSubscription || allocations[i].SubscriptionID == nil {
			continue
		}
		subscriptionID := *allocations[i].SubscriptionID
		return &subscriptionID
	}
	return nil
}
