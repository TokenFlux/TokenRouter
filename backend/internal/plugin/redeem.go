package plugin

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// redeem.go authorizes single-use credential issuance through the common plugin
// ledger. Balance codes consume face-value and count allowances, concurrency codes
// consume count allowances, and subscription codes require an allowed plan and
// a bounded duration. Deterministic credentials make replay stable without adopting
// an unrelated existing code whose creation has no matching ledger evidence.
//
// Subscription defaults resolve inside the locked issuance transaction after
// replay lookup. The accepted day count is frozen on the code and recorded in
// the result, while request identity retains the original zero-default intent.
// Later plan edits therefore cannot expand a previously accepted grant or prevent
// its replay. Actual redemption remains with billing, including ordinary balance
// redemption's recharge and applicable affiliate behavior.

// concurrencyValuePattern admits canonical positive integer text before parsing the native users.concurrency integer storage range used on redemption.
var concurrencyValuePattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// RedeemInput identifies one credential request and its plugin-wide replay identity.
type RedeemInput struct {
	Type        string // requested balance, concurrency, or subscription type
	Value       string // requested decimal or integer face value
	PlanID      int64  // requested subscription plan identifier
	Days        int32  // requested duration; zero selects plan default
	ExpiresUnix int64  // credential expiration; zero means permanent
	Key         string // plugin-wide operation replay key
	Reason      string // user-visible issuance explanation text
}

// RedeemCredential carries validated terms to the existing billing credential creator.
type RedeemCredential struct {
	PluginID  string     // authenticated issuing plugin identity
	Code      string     // deterministic uppercase credential text
	Type      string     // validated entitlement credential type
	Value     string     // normalized nonnegative face value
	PlanID    int64      // selected subscription plan identifier
	Days      int32      // frozen normalized subscription day count
	ExpiresAt *time.Time // optional credential expiration instant
	Reason    string     // validated issuance explanation text
}

// RedeemResult retains the issued code and effective subscription duration for replay evidence.
type RedeemResult struct {
	Code     string `json:"code"`                    // issued single-use credential text
	Days     int32  `json:"validity_days,omitempty"` // frozen effective subscription day count
	Replayed bool   `json:"replayed"`                // original issued result was reused
}

// RedeemEffects resolves plan durations and creates ordinary transaction-bound credentials.
type RedeemEffects interface {
	// SubscriptionDays resolves a live plan and normalizes its requested duration.
	// Zero selects the plan's current default. The supplied context carries the
	// ledger transaction, and missing plans return the stable plan-not-allowed error.
	SubscriptionDays(context.Context, int64, int32) (int32, error)
	// CreateCode persists one ordinary credential within the supplied ledger transaction.
	// Issuance fixes MaxUses to one, disables payment eligibility requirements, and
	// records plugin ownership in notes. It does not redeem the resulting credential.
	CreateCode(context.Context, RedeemCredential) error
}

// RedeemCodes owns replay-safe issuance policy while billing owns subsequent redemption.
type RedeemCodes struct {
	writes   *Writes        // shared quota and replay transaction engine
	grants   *Grants        // persistent deterministic code derivation key
	effects  RedeemEffects  // existing plan and credential collaborators
	location *time.Location // host calendar used for daily allowances
}

// NewRedeemCodes binds issuance policy to the shared ledger and persistent signing identity.
// A nil location selects time.Local. The creator and plan reader must join the
// supplied transaction context so code creation and replay evidence commit together.
func NewRedeemCodes(writes *Writes, grants *Grants, effects RedeemEffects, location *time.Location) *RedeemCodes {
	if location == nil {
		location = time.Local
	}
	return &RedeemCodes{writes: writes, grants: grants, effects: effects, location: location}
}

// Issue creates a validated single-use credential or returns its original stored result.
// Permission and stable input validation precede replay lookup. Relative expiration
// and live plan defaults are checked only for a new operation, so time or plan
// changes cannot invalidate an already committed replay.
//
// The normalized request retains zero as an explicit request for plan defaults,
// while the transaction's quota callback resolves and freezes the effective days.
// Subscription codes have no monetary face value, so their ledger amount is zero
// and the result preserves issued days. Creation and ledger insertion share one
// transaction; failure at either boundary cannot leave an unrecorded usable code.
func (s *RedeemCodes) Issue(ctx context.Context, binding Binding, input RedeemInput) (*RedeemResult, error) {
	if !slices.Contains(binding.Permissions, "redeem.issue") {
		return nil, ErrPermissionNotGranted
	}
	value, amount, err := normalizeRedeemInput(input)
	if err != nil {
		return nil, err
	}
	intent := struct {
		Type        string `json:"type"`          // validated credential entitlement type
		Value       string `json:"value"`         // normalized credential face value
		PlanID      int64  `json:"plan_id"`       // requested subscription plan identity
		Days        int32  `json:"validity_days"` // original duration selection including zero
		ExpiresUnix int64  `json:"expires_unix"`  // original optional credential expiry
		Reason      string `json:"reason"`        // exact issuance explanation text
	}{Type: input.Type, Value: value, PlanID: input.PlanID, Days: input.Days, ExpiresUnix: input.ExpiresUnix, Reason: input.Reason}
	request, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	credential := RedeemCredential{
		PluginID: binding.PluginID,
		Code:     s.grants.RedeemCode(binding.PluginID, input.Key),
		Type:     input.Type,
		Value:    value,
		PlanID:   input.PlanID,
		Days:     input.Days,
		Reason:   input.Reason,
	}
	mutation := Mutation{Key: input.Key, Kind: LedgerRedeemIssue, Amount: value, Request: request, State: LedgerCompleted, Reason: input.Reason}
	result, err := s.writes.Apply(ctx, binding, mutation, func(bound context.Context, tx LedgerTransaction, limits Limits, now time.Time) error {
		return s.checkQuota(bound, tx, limits.Redeem, now, input, amount, &credential)
	}, func(bound context.Context) (json.RawMessage, error) {
		if err := s.effects.CreateCode(bound, credential); err != nil {
			return nil, err
		}
		return json.Marshal(RedeemResult{Code: credential.Code, Days: credential.Days})
	})
	if err != nil {
		return nil, err
	}
	var issued RedeemResult
	if err := json.Unmarshal(result.Entry.Result, &issued); err != nil {
		return nil, err
	}
	issued.Replayed = result.Replayed
	return &issued, nil
}

// checkQuota resolves new issuance terms while holding the plugin's ledger lock.
// Expiration must be future and no more than 365 days away. Every credential
// consumes the shared daily count, while only balance codes consume monetary totals.
//
// Plan defaults resolve after replay and whitelist checks, preserving both old
// results and the administrator's current issuance authority. Requested positive
// days are checked before native normalization; resolved defaults are checked
// again afterward. The resulting duration and expiry are copied into the local
// credential that the subsequent creator persists on this same transaction context.
func (s *RedeemCodes) checkQuota(ctx context.Context, tx LedgerTransaction, limits RedeemLimits, now time.Time, input RedeemInput, amount Amount, credential *RedeemCredential) error {
	// Credential lifetimes use a fixed 365-day duration bound.
	if input.ExpiresUnix != 0 {
		expires := time.Unix(input.ExpiresUnix, 0).UTC()
		if !expires.After(now) || expires.After(now.Add(365*24*time.Hour)) {
			return ErrInvalidArgument
		}
		credential.ExpiresAt = &expires
	}
	switch input.Type {
	case "balance":
		if exceedsAmount("0", amount, limits.MaxBalancePerCode) {
			return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "redeem.max_balance_per_code"})
		}
	case "concurrency":
		// Normalization already bounds this canonical integer, so conversion cannot fail within the transaction's quota callback.
		value, _ := strconv.ParseInt(credential.Value, 10, 32)
		if value > limits.MaxConcurrencyPerCode {
			return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "redeem.max_concurrency_per_code"})
		}
	case "subscription":
		if !slices.Contains(limits.AllowedPlanIDs, input.PlanID) {
			return ErrPlanNotAllowed
		}
		if int64(input.Days) > limits.MaxSubscriptionDaysPerCode {
			return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "redeem.max_subscription_days_per_code"})
		}
		days, err := s.effects.SubscriptionDays(ctx, input.PlanID, input.Days)
		if err != nil {
			return err
		}
		if days <= 0 {
			return ErrInvalidArgument
		}
		if int64(days) > limits.MaxSubscriptionDaysPerCode {
			return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "redeem.max_subscription_days_per_code"})
		}
		credential.Days = days
	}
	since, until := dayWindow(now, s.location)
	query := LedgerQuery{Kind: LedgerRedeemIssue, Since: since, Until: until}
	all, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if all.Count >= limits.MaxCodesPerDay {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "redeem.max_codes_per_day"})
	}
	if input.Type == "balance" {
		query.Subtype = "balance"
		balance, err := tx.Totals(ctx, query)
		if err != nil {
			return err
		}
		if exceedsAmount(balance.Amount, amount, limits.MaxBalanceValuePerDay) {
			return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "redeem.max_balance_value_per_day"})
		}
	}
	return nil
}

// normalizeRedeemInput checks supported types and canonicalizes their meaningful value fields.
// Subscription value must be empty or zero, and only subscriptions may select
// plans or days. Concurrency fits native integer storage; balance follows wallet precision rules.
func normalizeRedeemInput(input RedeemInput) (string, Amount, error) {
	if err := validateOperation(input.Key, input.Reason); err != nil {
		return "", 0, err
	}
	if input.ExpiresUnix < 0 || input.Days < 0 {
		return "", 0, ErrInvalidArgument
	}
	if input.Type != "subscription" && (input.PlanID != 0 || input.Days != 0) {
		return "", 0, ErrInvalidArgument
	}
	switch input.Type {
	case "balance":
		amount, err := ParseAmount(input.Value)
		if err != nil {
			return "", 0, err
		}
		return amount.String(), amount, nil
	case "concurrency":
		if !concurrencyValuePattern.MatchString(input.Value) {
			return "", 0, ErrInvalidArgument
		}
		value, err := strconv.ParseInt(input.Value, 10, 32)
		if err != nil || value <= 0 {
			return "", 0, ErrInvalidArgument
		}
		return strconv.FormatInt(value, 10), 0, nil
	case "subscription":
		if input.PlanID <= 0 || (input.Value != "" && input.Value != "0") {
			return "", 0, ErrInvalidArgument
		}
		return "0", 0, nil
	default:
		return "", 0, ErrInvalidArgument
	}
}
