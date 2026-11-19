package usageprovider

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// zivv.go queries and normalizes Zivv wallet balances and key quota limits.
// Zivv responses carry cumulative wallet money alongside per-key allowance.
// Normalization keeps wallet debt in Balance while representing zero key limits
// as unlimited subscriptions and positive key limits as floored remainders.

// ZivvUsageAdapter queries the fixed Zivv wallet endpoint and retains key quota
// separately.
type ZivvUsageAdapter struct{}

// ZivvUsageResponse holds wallet amounts, key allowance, and availability from
// the response.
type ZivvUsageResponse struct {
	// Balance is the finite wallet remainder, including negative debt. The
	// normalizer combines it with TotalUsed and requires a nonnegative wallet total.
	Balance *float64 `json:"balance"`
	// Currency selects USD, CNY, or TOKENS after trimming and uppercasing. Other
	// spellings reject normalization before any amount is published.
	Currency string `json:"currency"`
	// IsAvailable is required to distinguish missing response data from an
	// explicit unavailable key; false becomes an authentication failure.
	IsAvailable *bool `json:"is_available"`
	// KeyLimit and KeyUsed are finite nonnegative key observations. Zero KeyLimit
	// becomes unlimited; positive limits derive a floored remainder separate from
	// the reported wallet amount.
	KeyLimit *float64 `json:"key_limit"` // reported total key allowance
	KeyUsed  *float64 `json:"key_used"`  // reported consumed key allowance
	// PlanName labels the key subscription after trimming. An empty name selects
	// Zivv, ensuring the returned subscription has the display label required by
	// shared validation.
	PlanName string `json:"plan_name"`
	// TotalUsed is required as finite nonnegative cumulative wallet usage. Adding
	// it to Balance must produce a finite nonnegative wallet total.
	TotalUsed *float64 `json:"total_used"`
}

// Name returns the registry identifier for Zivv wallet and quota queries.
func (*ZivvUsageAdapter) Name() string { return usageview.UpstreamUsageAdapterZivv }

// Query reads /v1/user/balance with the API key and normalizes wallet and key
// allowance. It requires a stable request snapshot with usable callbacks;
// shared endpoint construction handles versioned bases, and unsuccessful
// statuses return usage errors.
func (a *ZivvUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	body, status, err := client.Get(ctx, "/v1/user/balance", true)
	if err != nil {
		return nil, err
	}
	if httpErr := usageclient.UpstreamUsageHTTPError(status, true); httpErr != nil {
		return nil, httpErr
	}
	return ParseZivvUsage(body)
}

// ParseZivvUsage returns wallet amounts alongside key quota from a complete
// available response. Wallet remaining can be finite and negative, while used
// amounts and key counters require finite nonnegative values; wallet total is
// the remaining amount plus cumulative usage and must be nonnegative.
//
// Zero key limit becomes an unlimited subscription flag. Positive key limits
// produce a separately floored remainder for Limits and Subscription, keeping
// key capacity distinct from wallet money. Currency must identify USD, CNY, or
// TOKENS, and blank plan names use Zivv.
func ParseZivvUsage(body []byte) (*usageview.UpstreamUsageInfo, error) {
	var response ZivvUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.IsAvailable == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if !*response.IsAvailable {
		return nil, usageview.ErrUpstreamUsageAuthFailed
	}
	if response.Balance == nil || response.TotalUsed == nil || response.KeyLimit == nil || response.KeyUsed == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if !usageview.ValidFiniteNumber(*response.Balance) || !usageview.ValidNonNegativeNumber(*response.TotalUsed) ||
		!usageview.ValidNonNegativeNumber(*response.KeyLimit) || !usageview.ValidNonNegativeNumber(*response.KeyUsed) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	unit := strings.ToUpper(strings.TrimSpace(response.Currency))
	if unit != "USD" && unit != "CNY" && unit != "TOKENS" {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	planName := strings.TrimSpace(response.PlanName)
	if planName == "" {
		planName = "Zivv"
	}
	total := *response.Balance + *response.TotalUsed
	if !usageview.ValidNonNegativeNumber(total) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	usage := &usageview.UpstreamUsageInfo{
		Provider: usageview.UpstreamUsageAdapterZivv,
		Mode:     "balance",
		Unit:     unit,
		Balance:  &usageview.UpstreamUsageAmount{Used: response.TotalUsed, Total: &total, Remaining: response.Balance},
	}
	if *response.KeyLimit <= 0 {
		// Zero denotes unlimited key allowance. Use the subscription flag so
		// display consumers do not interpret an unlimited key as a zero-capacity
		// quota window.
		usage.Subscription = &usageview.UpstreamUsageSubscription{PlanName: planName, Unlimited: true}
		return usage, nil
	}
	// Key allowance exhaustion is separate from wallet debt. Floor the derived
	// key remainder while retaining the finite wallet amount already recorded above.
	keyRemaining := math.Max(0, *response.KeyLimit-*response.KeyUsed)
	usage.Limits = []usageview.UpstreamUsageLimit{{
		Name: "key_quota", Used: response.KeyUsed, Limit: response.KeyLimit, Remaining: &keyRemaining,
	}}
	usage.Subscription = &usageview.UpstreamUsageSubscription{PlanName: planName, Remaining: &keyRemaining}
	return usage, nil
}
