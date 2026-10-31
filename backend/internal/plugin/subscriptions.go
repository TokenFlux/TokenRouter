package plugin

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

// subscriptions.go authorizes direct plugin subscription grants through the
// common write ledger. Administrators separately confirm the capability and
// select allowed plan IDs in installation limits; the empty default list permits
// no plan. Positive requested days and daily count limits are checked before
// the existing subscription service can create or queue any entitlement.
//
// The subscription service joins the ledger transaction and retains its existing
// plan-template and timeline rules. Replay returns the originally issued record
// instead of extending the user's subscription again. Authentication invalidation
// follows a new commit with an independent bounded context, while the stored
// subscription notes identify the plugin and requested reason. Plugin grants
// never borrow payment order identifiers for their replay identity.

// SubscriptionInput identifies one requested entitlement and its plugin-wide replay key.
type SubscriptionInput struct {
	UserID int64  // target account identifier
	PlanID int64  // requested subscription plan identifier
	Days   int32  // requested positive calendar-day duration
	Key    string // plugin-wide operation replay key
	Reason string // user-visible entitlement explanation
}

// SubscriptionGrant records the issued or queued entitlement returned by billing.
type SubscriptionGrant struct {
	ID          int64 `json:"subscription_id"` // issued subscription record identifier
	ExpiresUnix int64 `json:"expires_unix"`    // expiration in Unix seconds
	Queued      bool  `json:"queued"`          // entitlement starts after an existing grant
	Replayed    bool  `json:"replayed"`        // original issued result was reused
}

// SubscriptionEffects adapts existing transactional issuance and authentication invalidation.
type SubscriptionEffects interface {
	// Assign creates or queues one grant using the supplied ledger transaction context.
	// Billing retains its live plan template, duration normalization, and timeline rules.
	// Plugin issuance leaves AssignedBy and SourceOrderID empty and records the supplied notes.
	Assign(context.Context, int64, int64, int32, string) (SubscriptionGrant, error)
	// InvalidateUserAuth requests authentication-cache removal after a committed new grant.
	// The caller supplies a fresh bounded context. This existing invalidation capability
	// is best effort and does not repeat entitlement issuance on failure.
	InvalidateUserAuth(context.Context, int64)
}

// Subscriptions enforces plugin plan selection and issuance quotas around billing.
type Subscriptions struct {
	writes   *Writes             // shared replay and transaction engine
	effects  SubscriptionEffects // existing entitlement issuance and cache adapter
	location *time.Location      // host calendar used for daily quotas
}

// NewSubscriptions binds entitlement issuance to current plugin policy and host calendar.
// A nil timezone uses the application's time.Local. Construction performs no
// database writes and grants no plan before an authorized operation arrives.
func NewSubscriptions(writes *Writes, effects SubscriptionEffects, location *time.Location) *Subscriptions {
	if location == nil {
		location = time.Local
	}
	return &Subscriptions{writes: writes, effects: effects, location: location}
}

// Grant commits one allowed-plan subscription or returns its prior idempotent result.
// Requested days must be positive; this RPC does not select plan defaults from
// zero. Permission and input validation precede ledger replay and quota inspection.
//
// New issuance calls the existing billing service on the ledger's context, keeping
// its entitlement and replay record atomic. Billing determines whether the result
// queues after an earlier grant and applies the live plan's quota template.
// After a successful new commit, authentication invalidation receives its own
// budget. Replays skip issuance and invalidation even if plan policy has since changed.
func (s *Subscriptions) Grant(ctx context.Context, binding Binding, input SubscriptionInput) (*SubscriptionGrant, error) {
	if !slices.Contains(binding.Permissions, "subscription.grant") {
		return nil, ErrPermissionNotGranted
	}
	if input.UserID <= 0 || input.PlanID <= 0 || input.Days <= 0 {
		return nil, ErrInvalidArgument
	}
	if err := validateOperation(input.Key, input.Reason); err != nil {
		return nil, err
	}
	intent := struct {
		UserID int64  `json:"user_id"`       // target account identifier
		PlanID int64  `json:"plan_id"`       // selected subscription plan identifier
		Days   int32  `json:"validity_days"` // requested positive calendar-day duration
		Reason string `json:"reason"`        // exact entitlement explanation text
	}{UserID: input.UserID, PlanID: input.PlanID, Days: input.Days, Reason: input.Reason}
	request, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	mutation := Mutation{
		Key:     input.Key,
		Kind:    LedgerSubscriptionGrant,
		UserID:  &input.UserID,
		Amount:  "0",
		Request: request,
		State:   LedgerCompleted,
		Reason:  input.Reason,
	}
	result, err := s.writes.Apply(ctx, binding, mutation, func(bound context.Context, tx LedgerTransaction, limits Limits, now time.Time) error {
		return s.checkQuota(bound, tx, limits.Subscription, now, input)
	}, func(bound context.Context) (json.RawMessage, error) {
		grant, err := s.effects.Assign(bound, input.UserID, input.PlanID, input.Days, "[plugin:"+binding.PluginID+"] "+input.Reason)
		if err != nil {
			return nil, err
		}
		return json.Marshal(grant)
	})
	if err != nil {
		return nil, err
	}
	var grant SubscriptionGrant
	if err := json.Unmarshal(result.Entry.Result, &grant); err != nil {
		return nil, err
	}
	grant.Replayed = result.Replayed
	if !result.Replayed {
		// The entitlement already exists durably; cache invalidation gets a separate budget so caller cancellation cannot suppress the required post-commit attempt.
		after, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		s.effects.InvalidateUserAuth(after, input.UserID)
	}
	return &grant, nil
}

// checkQuota enforces plan allowlisting, duration, and host-calendar issuance counts.
// The caller already owns the plugin's ledger lock. Count comparisons precede
// the business effect, and all checks use the same accounting day captured by Writes.
// The allowlist defaults empty, so granting the permission alone cannot select
// an entitlement plan. Zero duration or count ceilings prohibit their operation.
// Per-user and aggregate counts remain separate queries inside the same locked
// transaction; successful replay never consumes another position in either count.
func (s *Subscriptions) checkQuota(ctx context.Context, tx LedgerTransaction, limits SubscriptionLimits, now time.Time, input SubscriptionInput) error {
	if !slices.Contains(limits.AllowedPlanIDs, input.PlanID) {
		return ErrPlanNotAllowed
	}
	if int64(input.Days) > limits.MaxDaysPerGrant {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "subscription.max_days_per_grant"})
	}
	since, until := dayWindow(now, s.location)
	query := LedgerQuery{Kind: LedgerSubscriptionGrant, UserID: input.UserID, Since: since, Until: until}
	user, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if user.Count >= limits.MaxGrantsPerUserPerDay {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "subscription.max_grants_per_user_per_day"})
	}
	query.UserID = 0
	all, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if all.Count >= limits.MaxGrantsPerDay {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "subscription.max_grants_per_day"})
	}
	return nil
}
