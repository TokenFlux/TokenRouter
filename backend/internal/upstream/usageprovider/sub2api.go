package usageprovider

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// sub2api.go queries and normalizes Sub2API usage responses and supplies shared
// UTC time and finite numeric tolerance helpers.
// Sub2API usage responses can describe a wallet, a key quota, or subscription
// windows using superficially similar amounts. Normalization selects the
// reported mode and checks agreement between redundant quota values while
// keeping finite negative wallet remainders distinct from unlimited
// subscription sentinels.
//
// Window reset derivation and amount validation retain missing observations as
// distinct from reported zero values. CloseEnough rejects NaN and infinite
// operands before applying its absolute and relative tolerances so overflowed
// sums cannot pass quota consistency checks.

// Sub2APIUsageAdapter queries the fixed usage endpoint and normalizes its
// reported mode.
type Sub2APIUsageAdapter struct{}

// Sub2APIUsageResponse is the wire envelope for wallet, quota, and subscription
// observations.
type Sub2APIUsageResponse struct {
	Mode string `json:"mode"` // reported upstream usage mode
	// IsValid distinguishes a missing validity observation from an explicit
	// rejected key. ParseSub2APIUsage reports different errors for those cases.
	IsValid *bool `json:"isValid"`
	// Status selects accepted quota states in quota_limited mode: active,
	// quota_exhausted, or expired. Unrestricted normalization does not consume it.
	Status   string `json:"status"`
	PlanName string `json:"planName"` // reported subscription plan name
	// Unit labels the top-level amounts. Unrestricted responses require USD, and
	// a present Quota requires agreement with its own USD unit.
	Unit string `json:"unit"`
	// Remaining accompanies quota or unrestricted amounts. Limited quota requires
	// agreement with Quota.Remaining; unrestricted subscriptions can use minus one
	// as their unlimited representation.
	Remaining *float64 `json:"remaining"`
	// Balance selects the unrestricted wallet path when Subscription is absent.
	// Its finite amount must agree with Remaining, including a negative wallet remainder.
	Balance *float64 `json:"balance"`
	// Quota selects aggregate key-quota normalization. When it is absent,
	// quota_limited responses need rolling limits and omit top-level Remaining and Unit.
	Quota *Sub2APIQuota `json:"quota"`
	// RateLimits: reported rolling allowance windows
	RateLimits []Sub2APIRateLimit `json:"rate_limits"`
	// Subscription selects unrestricted plan handling when Balance is absent, and
	// can accompany quota-limited results. Normalized window amounts can retain
	// its numeric storage.
	Subscription *Sub2APISubscription `json:"subscription"`
	// ExpiresAt is optional for aggregate quota or wallet results. A supplied zero
	// instant rejects normalization, while accepted instants return as UTC copies.
	ExpiresAt *time.Time `json:"expires_at"`
}

// Sub2APIQuota holds one key's reported quota amounts and allowance unit.
type Sub2APIQuota struct {
	// Limit, Used, and Remaining are required finite nonnegative observations on
	// one USD scale. Limit is positive and Remaining agrees with floored Limit
	// minus Used; normalization retains these numeric pointers.
	Limit     *float64 `json:"limit"`     // reported total key allowance
	Used      *float64 `json:"used"`      // reported consumed key allowance
	Remaining *float64 `json:"remaining"` // reported remaining key allowance
	Unit      string   `json:"unit"`      // USD
}

// Sub2APIRateLimit holds a rolling allowance and its reported window metadata.
type Sub2APIRateLimit struct {
	// Window identifies 5h, 1d, or 7d after trimming. NormalizeSub2APIRateLimits
	// rejects duplicate trimmed names so one period has one reported allowance.
	Window string `json:"window"`
	// Limit, Used, and Remaining form one rolling allowance. Limit is positive,
	// all values are finite and nonnegative, and Remaining agrees with floored
	// Limit minus Used; normalized windows retain these pointers.
	Limit     *float64 `json:"limit"`     // reported rolling allowance total
	Used      *float64 `json:"used"`      // reported rolling allowance consumption
	Remaining *float64 `json:"remaining"` // reported rolling allowance remainder
	// WindowStart retains timestamp JSON or explicit null. Absence is invalid,
	// while null records unavailable start metadata without inventing an instant.
	WindowStart json.RawMessage `json:"window_start"`
	// ResetAt is an optional rolling-window reset. A supplied zero instant rejects
	// normalization, while accepted times become UTC; WindowStart is validated
	// separately and does not derive this reset.
	ResetAt *time.Time `json:"reset_at"`
}

// Sub2APISubscription holds USD usage windows and unlimited-plan evidence from
// the response.
type Sub2APISubscription struct {
	// DailyUsageUSD, WeeklyUsageUSD, and MonthlyUsageUSD are required finite
	// nonnegative observations when a subscription is supplied. Included limited
	// windows retain the corresponding usage pointers in their normalized data.
	DailyUsageUSD   *float64 `json:"daily_usage_usd"`   // reported daily USD usage
	WeeklyUsageUSD  *float64 `json:"weekly_usage_usd"`  // reported weekly USD usage
	MonthlyUsageUSD *float64 `json:"monthly_usage_usd"` // reported monthly USD usage
	// DailyLimitUSD, WeeklyLimitUSD, and MonthlyLimitUSD enable their respective
	// finite windows when positive. Missing or zero limits omit those windows;
	// negative or nonfinite supplied limits are rejected.
	DailyLimitUSD   *float64 `json:"daily_limit_usd"`   // reported daily USD allowance
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`  // reported weekly USD allowance
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"` // reported monthly USD allowance
	// DailyResetAt, WeeklyResetAt, and MonthlyResetAt take precedence over their
	// corresponding window starts for included allowance windows. A zero time
	// encountered on that path rejects normalization; omitted windows skip reset validation.
	DailyResetAt   *time.Time `json:"daily_reset_at"`   // daily reset instant
	WeeklyResetAt  *time.Time `json:"weekly_reset_at"`  // reported weekly reset timestamp
	MonthlyResetAt *time.Time `json:"monthly_reset_at"` // reported monthly reset timestamp
	// DailyWindowStart, WeeklyWindowStart, and MonthlyWindowStart derive resets
	// only when their corresponding reset is absent. The normalizer adds fixed
	// one-day, seven-day, or thirty-day durations to nonzero starts. A supplied
	// zero start rejects an included window when its explicit reset is absent.
	DailyWindowStart   *time.Time `json:"daily_window_start"`   // reported daily window start
	WeeklyWindowStart  *time.Time `json:"weekly_window_start"`  // reported weekly window start
	MonthlyWindowStart *time.Time `json:"monthly_window_start"` // reported monthly window start
	// Unlimited controls plan representation when supplied. A missing flag lets
	// the first legacy minus-one remainder infer unlimited status; supplied false
	// retains limited handling.
	Unlimited *bool `json:"unlimited"`
	// ExpiresAt is required and nonzero for a supplied subscription. The
	// normalizer returns its UTC copy for plan display alongside window observations.
	ExpiresAt *time.Time `json:"expires_at"`
}

// Name returns the registry identifier for Sub2API upstream usage queries.
func (*Sub2APIUsageAdapter) Name() string { return usageview.UpstreamUsageAdapterSub2API }

// Query reads /v1/usage with the API key and normalizes its reported mode. It
// requires a stable request snapshot with usable transport, context, header,
// and endpoint callbacks; unsuccessful statuses use shared usage errors.
func (a *Sub2APIUsageAdapter) Query(ctx context.Context, input *usagecontract.Request) (*usageview.UpstreamUsageInfo, error) {
	client := usageclient.New(input)
	body, status, err := client.Get(ctx, "/v1/usage", true)
	if err != nil {
		return nil, err
	}
	if httpErr := usageclient.UpstreamUsageHTTPError(status, true); httpErr != nil {
		return nil, httpErr
	}
	return ParseSub2APIUsage(body)
}

// ParseSub2APIUsage decodes and normalizes a Sub2API usage response. A missing
// isValid field is malformed, false is an authentication failure, and supported
// mode values select quota-limited or unrestricted handling; other modes are
// rejected.
func ParseSub2APIUsage(body []byte) (*usageview.UpstreamUsageInfo, error) {
	var response Sub2APIUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.IsValid == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if !*response.IsValid {
		return nil, usageview.ErrUpstreamUsageAuthFailed
	}
	switch response.Mode {
	case "quota_limited":
		return NormalizeSub2APIQuotaLimited(&response)
	case "unrestricted":
		return NormalizeSub2APIUnrestricted(&response)
	default:
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
}

// NormalizeSub2APIQuotaLimited returns key quota or rolling limits from a
// nonnil response with an accepted quota status. An absent Quota requires
// nonempty rolling limits and absent top-level remaining and unit; an optional
// subscription establishes USD for that limits result.
//
// A present Quota requires positive finite USD allowance and consistent
// nonnegative used and remaining values, including agreement with top-level
// Remaining after flooring overuse at zero. Normalized quota and rolling-limit
// amounts retain the response's numeric pointers, so callers keep that storage
// stable while the result is consumed. Expiry values are normalized to UTC.
func NormalizeSub2APIQuotaLimited(response *Sub2APIUsageResponse) (*usageview.UpstreamUsageInfo, error) {
	if response.Status != "active" && response.Status != "quota_exhausted" && response.Status != "expired" {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	limits, err := NormalizeSub2APIRateLimits(response.RateLimits)
	if err != nil {
		return nil, err
	}
	subscription, err := NormalizeSub2APISubscription(response.PlanName, response.Subscription)
	if err != nil {
		return nil, err
	}
	// A limits-only response has no total key quota to compare. Require rolling
	// windows and absent aggregate fields so that representation cannot mix
	// incompatible allowance sources.
	if response.Quota == nil {
		if len(limits) == 0 || response.Remaining != nil || strings.TrimSpace(response.Unit) != "" {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		expiresAt, err := NormalizeTime(response.ExpiresAt)
		if err != nil {
			return nil, err
		}
		unit := ""
		if subscription != nil {
			unit = "USD"
		}
		return &usageview.UpstreamUsageInfo{Provider: usageview.UpstreamUsageAdapterSub2API, Mode: "limits", Unit: unit, Limits: limits, Subscription: subscription, ExpiresAt: expiresAt}, nil
	}
	quota := response.Quota
	// Compare an overused allowance against zero remaining while retaining
	// reported Used. This lets the result describe exhaustion without treating
	// spent capacity as negative available quota.
	if quota.Limit == nil || quota.Used == nil || quota.Remaining == nil || response.Remaining == nil ||
		quota.Unit != "USD" || response.Unit != quota.Unit || *quota.Limit <= 0 ||
		!usageview.ValidNonNegativeNumber(*quota.Limit) || !usageview.ValidNonNegativeNumber(*quota.Used) || !usageview.ValidNonNegativeNumber(*quota.Remaining) ||
		!usageview.ValidNonNegativeNumber(*response.Remaining) || !CloseEnough(*quota.Remaining, math.Max(0, *quota.Limit-*quota.Used)) ||
		!CloseEnough(*quota.Remaining, *response.Remaining) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := NormalizeTime(response.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &usageview.UpstreamUsageInfo{
		Provider: usageview.UpstreamUsageAdapterSub2API,
		Mode:     "quota",
		Unit:     quota.Unit,
		Balance:  &usageview.UpstreamUsageAmount{Used: quota.Used, Total: quota.Limit, Remaining: quota.Remaining},
		Limits:   limits, Subscription: subscription, ExpiresAt: expiresAt,
	}, nil
}

// NormalizeSub2APIUnrestricted returns either a wallet or subscription from a
// nonnil USD response with a plan name and finite Remaining. Exactly one of
// Balance and Subscription must be supplied, distinguishing the meaning of that
// top-level remaining value.
//
// Wallet amounts retain finite negative debt when Balance agrees with
// Remaining, and the returned amount borrows Balance's numeric storage.
// Subscriptions convert the unlimited representation to a flag with absent
// numeric remaining; limited subscriptions require their smallest window
// remainder to agree with the top-level amount. This prevents a wallet amount
// and subscription sentinel from sharing one display representation.
func NormalizeSub2APIUnrestricted(response *Sub2APIUsageResponse) (*usageview.UpstreamUsageInfo, error) {
	if response.Unit != "USD" || strings.TrimSpace(response.PlanName) == "" || response.Remaining == nil || !usageview.ValidFiniteNumber(*response.Remaining) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := NormalizeTime(response.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if (response.Subscription == nil) == (response.Balance == nil) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	if response.Balance != nil {
		if !usageview.ValidFiniteNumber(*response.Balance) || !CloseEnough(*response.Balance, *response.Remaining) {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		return &usageview.UpstreamUsageInfo{
			Provider:  usageview.UpstreamUsageAdapterSub2API,
			Mode:      "balance",
			Unit:      response.Unit,
			Balance:   &usageview.UpstreamUsageAmount{Remaining: response.Balance},
			ExpiresAt: expiresAt,
		}, nil
	}
	subscription, err := NormalizeSub2APISubscription(response.PlanName, response.Subscription, response.Remaining)
	if err != nil || subscription == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	// Treat -1 as an unlimited subscription's wire sentinel, then return a
	// flag-based result so downstream amount readers cannot display that sentinel as funds.
	if subscription.Unlimited {
		if *response.Remaining != -1 {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		return &usageview.UpstreamUsageInfo{Provider: usageview.UpstreamUsageAdapterSub2API, Mode: "subscription", Unit: response.Unit, Subscription: subscription, ExpiresAt: expiresAt}, nil
	}
	if !usageview.ValidNonNegativeNumber(*response.Remaining) || subscription.Remaining == nil || !CloseEnough(*response.Remaining, *subscription.Remaining) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	return &usageview.UpstreamUsageInfo{Provider: usageview.UpstreamUsageAdapterSub2API, Mode: "subscription", Unit: response.Unit, Subscription: subscription, ExpiresAt: expiresAt}, nil
}

// NormalizeSub2APISubscription builds a plan from usage windows, or returns nil
// when raw is nil. Supplied plans require a name, all three nonnegative usage
// observations, and a nonzero expiry. Only the first optional legacyRemaining
// argument participates in unlimited inference.
//
// A supplied Unlimited flag takes precedence over the legacy -1 remainder;
// unlimited plans reject any finite window limits. Limited plans require at
// least one window and expose the smallest derived remainder across them.
// Window Used and Limit pointers borrow raw storage, while the selected
// Remaining and normalized times use newly computed values.
func NormalizeSub2APISubscription(planName string, raw *Sub2APISubscription, legacyRemaining ...*float64) (*usageview.UpstreamUsageSubscription, error) {
	if raw == nil {
		return nil, nil
	}
	if strings.TrimSpace(planName) == "" || raw.DailyUsageUSD == nil || raw.WeeklyUsageUSD == nil || raw.MonthlyUsageUSD == nil ||
		!usageview.ValidNonNegativeNumber(*raw.DailyUsageUSD) || !usageview.ValidNonNegativeNumber(*raw.WeeklyUsageUSD) || !usageview.ValidNonNegativeNumber(*raw.MonthlyUsageUSD) {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := NormalizeTime(raw.ExpiresAt)
	if err != nil || expiresAt == nil {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	var remainingSentinel *float64
	if len(legacyRemaining) > 0 {
		remainingSentinel = legacyRemaining[0]
	}
	unlimited := raw.Unlimited != nil && *raw.Unlimited
	// The legacy remainder can establish unlimited status when the dedicated
	// flag is absent. Keep an explicitly supplied false flag authoritative when
	// selecting this representation.
	if raw.Unlimited == nil && remainingSentinel != nil && *remainingSentinel == -1 {
		unlimited = true
	}
	limits, err := NormalizeSub2APISubscriptionLimits(raw)
	if err != nil {
		return nil, err
	}
	if unlimited {
		if len(limits) != 0 {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		return &usageview.UpstreamUsageSubscription{PlanName: strings.TrimSpace(planName), Unlimited: true, ExpiresAt: expiresAt}, nil
	}
	if len(limits) == 0 {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	// The smallest allowance remainder constrains the subscription display when
	// several windows apply, so a larger daily or monthly remainder cannot replace
	// an exhausted window.
	minimum := *limits[0].Remaining
	for _, limit := range limits[1:] {
		if limit.Remaining != nil && *limit.Remaining < minimum {
			minimum = *limit.Remaining
		}
	}
	return &usageview.UpstreamUsageSubscription{PlanName: strings.TrimSpace(planName), Remaining: &minimum, ExpiresAt: expiresAt, Limits: limits}, nil
}

// NormalizeSub2APISubscriptionLimits builds daily, weekly, and monthly limits
// from a nonnil subscription in that order. Missing or zero limits omit their
// windows; included windows require finite nonnegative usage and a positive
// finite limit, with remaining floored at zero.
//
// For an included window, a supplied reset takes precedence over its start.
// When reset is absent, a usable start adds the fixed daily, weekly, or
// thirty-day duration. Invalid times encountered on that included path reject
// normalization. Returned Used and Limit pointers retain the source fields, so
// callers keep that storage stable while consuming the derived windows.
func NormalizeSub2APISubscriptionLimits(raw *Sub2APISubscription) ([]usageview.UpstreamUsageLimit, error) {
	// subscriptionInput pairs one subscription window's allowance and reset
	// sources for normalization.
	type subscriptionInput struct {
		name string // normalized subscription window label
		// used and limit retain the subscription's numeric pointers for this
		// window. A missing or zero limit omits it; included windows require usable
		// finite nonnegative amounts and positive allowance.
		used  *float64 // reported window usage amount
		limit *float64 // reported window allowance total
		// resetAt is the preferred source for an included window. A zero instant
		// encountered there rejects normalization, while absence allows start and
		// duration to derive its reset.
		resetAt *time.Time
		// start supplies a reset when resetAt is absent for an included window. A
		// supplied zero time rejects that derivation.
		start *time.Time
		// duration is added to start only when resetAt is absent. Monthly windows
		// use thirty days as the fixed approximation for this derived reset.
		duration time.Duration
	}
	// Reset derivation uses fixed window durations, including thirty days for
	// monthly allowance, so a missing reset receives an approximation from its reported start.
	inputs := []subscriptionInput{
		{name: "daily", used: raw.DailyUsageUSD, limit: raw.DailyLimitUSD, resetAt: raw.DailyResetAt, start: raw.DailyWindowStart, duration: 24 * time.Hour},
		{name: "weekly", used: raw.WeeklyUsageUSD, limit: raw.WeeklyLimitUSD, resetAt: raw.WeeklyResetAt, start: raw.WeeklyWindowStart, duration: 7 * 24 * time.Hour},
		{name: "monthly", used: raw.MonthlyUsageUSD, limit: raw.MonthlyLimitUSD, resetAt: raw.MonthlyResetAt, start: raw.MonthlyWindowStart, duration: 30 * 24 * time.Hour},
	}
	limits := make([]usageview.UpstreamUsageLimit, 0, len(inputs))
	for _, input := range inputs {
		if input.limit == nil || *input.limit == 0 {
			continue
		}
		if input.used == nil || *input.limit < 0 || !usageview.ValidNonNegativeNumber(*input.limit) || !usageview.ValidNonNegativeNumber(*input.used) {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		// Keep reported overuse in Used while flooring Remaining at zero, allowing
		// finite subscription windows to show exhaustion without a negative allowance.
		remaining := math.Max(0, *input.limit-*input.used)
		resetAt, err := NormalizeTime(input.resetAt)
		if err != nil {
			return nil, err
		}
		// Derive a reset from the window start only when no reset was supplied. An
		// explicit reset retains precedence over the fixed duration approximation.
		if resetAt == nil && input.start != nil {
			start, startErr := NormalizeTime(input.start)
			if startErr != nil {
				return nil, startErr
			}
			if start != nil {
				value := start.Add(input.duration)
				resetAt = &value
			}
		}
		limits = append(limits, usageview.UpstreamUsageLimit{Name: input.name, Used: input.used, Limit: input.limit, Remaining: &remaining, ResetAt: resetAt})
	}
	return limits, nil
}

// NormalizeSub2APIRateLimits validates and returns 5h, 1d, or 7d allowance
// windows in response order. Trimmed names must be unique, and each window
// requires positive finite Limit plus nonnegative finite Used and Remaining.
//
// Remaining must agree with limit minus used after flooring overuse at zero.
// WindowStart is required as valid timestamp JSON or explicit null, and an
// optional nonzero ResetAt is normalized to UTC. Amount pointers borrow each
// source entry, allowing validation to retain reported values while computed
// checks confirm their consistency.
func NormalizeSub2APIRateLimits(raw []Sub2APIRateLimit) ([]usageview.UpstreamUsageLimit, error) {
	limits := make([]usageview.UpstreamUsageLimit, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item.Window)
		if name == "" || (name != "5h" && name != "1d" && name != "7d") {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		if _, exists := seen[name]; exists {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		seen[name] = struct{}{}
		// Keep the reported overuse visible in Used, but compare its remainder
		// against exhausted capacity of zero so rolling allowances cannot expose
		// negative available quota.
		if item.Limit == nil || item.Used == nil || item.Remaining == nil || *item.Limit <= 0 || !usageview.ValidNonNegativeNumber(*item.Limit) ||
			!usageview.ValidNonNegativeNumber(*item.Used) || !usageview.ValidNonNegativeNumber(*item.Remaining) ||
			!CloseEnough(*item.Remaining, math.Max(0, *item.Limit-*item.Used)) {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		if err := ValidateSub2APIWindowStart(item.WindowStart); err != nil {
			return nil, err
		}
		resetAt, err := NormalizeTime(item.ResetAt)
		if err != nil {
			return nil, err
		}
		limits = append(limits, usageview.UpstreamUsageLimit{Name: name, Used: item.Used, Limit: item.Limit, Remaining: item.Remaining, ResetAt: resetAt})
	}
	return limits, nil
}

// ValidateSub2APIWindowStart accepts an explicit JSON null or a nonzero
// time.Time JSON representation. Absent, blank, malformed, and zero timestamp
// values return the invalid-response error, keeping missing metadata distinct
// from a reported unavailable start.
func ValidateSub2APIWindowStart(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		// The required window_start spelling distinguishes unavailable metadata
		// (explicit null) from a response that omitted the field, which cannot
		// satisfy this adapter's shape validation.
		return usageview.ErrUpstreamUsageInvalidResponse
	}
	if trimmed == "null" {
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(raw, &value); err != nil || value.IsZero() {
		return usageview.ErrUpstreamUsageInvalidResponse
	}
	return nil
}

// NormalizeTime returns a copied UTC timestamp, nil for an absent value, and an
// invalid-response error for a supplied zero time. The source pointer and its
// stored timezone remain unchanged.
func NormalizeTime(value *time.Time) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if value.IsZero() {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	normalized := value.UTC()
	return &normalized, nil
}

// CloseEnough compares two finite amounts with the larger of a 0.000001
// absolute tolerance and 0.00001 times the larger operand magnitude. NaN and
// infinite operands always return false so overflowed arithmetic sums cannot
// satisfy the relative tolerance bound.
func CloseEnough(left, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
		return false
	}
	// Use the relative 0.00001 factor for large balances and the 0.000001 floor for near-zero amounts.
	return math.Abs(left-right) <= math.Max(0.000001, math.Max(math.Abs(left), math.Abs(right))*0.00001)
}
