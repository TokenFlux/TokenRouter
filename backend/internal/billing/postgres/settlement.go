package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// settlement.go commits request charges and task fund reservations, captures
// and releases.
// Completed gateway usage can exceed a pinned subscription's remaining quota,
// while task submission requires a fully funded reservation before upstream
// work starts. Ordinary settlement caps subscription usage and records
// uncovered costs as wallet debt; task funding freezes available wallet money
// and retains source allocations for capture or release. Tasks explicitly
// accepting excess completed usage can debit the wallet beyond that hold.
// Both use a billing action key plus API Key identity to detect repeats and
// conflicting inputs.
//
// Each write attempt owns a fresh SQL transaction, claims its financial
// identity, locks the payer before subscriptions and commits funding with
// counters and task metadata. PostgreSQL deadlocks restart the complete
// attempt. Task allowance refunds use a savepoint so a failed refund can
// retain conservative counts while completed funding remains committable.
// Provider threshold crossings require an outbox write on that same
// transaction; analysis logs and later notifications have separate delivery
// lifetimes.

// Task allowance stages determine whether submission holds counters,
// completion keeps actual usage or cancellation returns previously reserved
// usage.
const (
	taskAllowanceReserve taskAllowanceOperation = iota // reserve held Key and member usage
	taskAllowanceCapture                               // retain actual usage at completion
	taskAllowanceRelease                               // refund previously reserved allowance usage
)

// SettlementStore applies request and task funding through its caller-owned
// database, with calendar accounting and registered transaction-bound task
// writers.
type SettlementStore struct {
	// db remains usable for every funding attempt and unlocked subscription read.
	// NewSettlementStore retains the caller-owned pool; its owner closes it after
	// store operations finish.
	db       *sql.DB           // caller-owned SQL connection pool
	calendar timezone.Calendar // member accounting calendar rules
	// providerOutbox is required when a post-update provider limit crosses from
	// below. An absent writer fails that transaction; actions with no crossing
	// can succeed with nil.
	providerOutbox ProviderQuotaOutbox // provider threshold event writer
	// taskProjections contains the constructor-copied registrations read by every
	// task attempt. Keep the stored map immutable after publication; each factory
	// receives that attempt's new transaction and task reference.
	taskProjections TaskProjectionFactories // scope-indexed transaction writer factories
}

// ProviderQuotaOutbox writes a provider scheduling event on the supplied
// billing transaction when a quota crosses its limit. The caller commits or
// recovers that transaction; callback failures fail the funding attempt.
type ProviderQuotaOutbox func(context.Context, *sql.Tx, int64) error

// taskAllowanceOperation selects which task stage adjusts held Key and member
// usage.
type taskAllowanceOperation int

// usageBillingSubscriptionRow holds scanned quota, lifetime and plan JSON
// values before billing normalization or allocation.
type usageBillingSubscriptionRow struct {
	ID     int64 // selected grant record identifier
	PlanID int64 // selected source plan identifier
	// StartsAt and ExpiresAt bound the selected grant lifetime. Queries establish
	// current eligibility; window normalization uses both timestamps to determine
	// one-time daily quota and expiry-tail reset fit.
	StartsAt  time.Time // inclusive grant start time
	ExpiresAt time.Time // exclusive grant expiry time
	// DailyWindowStart, WeeklyWindowStart and MonthlyWindowStart retain SQL null
	// presence. Normalization can activate or replace eligible windows in memory;
	// allocation persists anchors only on rows receiving positive charges.
	DailyWindowStart   sql.NullTime // nullable daily quota anchor
	WeeklyWindowStart  sql.NullTime // nullable weekly quota anchor
	MonthlyWindowStart sql.NullTime // nullable monthly quota anchor
	// DailyLimitUSD, WeeklyLimitUSD, MonthlyLimitUSD and DailyUsageUSD,
	// WeeklyUsageUSD, MonthlyUsageUSD describe overlapping quota counters. Every
	// positive-limit period counts the same subscription charge, and their
	// smallest remainder caps allocation. Null and nonpositive limits disable
	// their corresponding capacity constraint.
	DailyLimitUSD   sql.NullFloat64 // nullable daily quota USD
	WeeklyLimitUSD  sql.NullFloat64 // nullable weekly quota USD
	MonthlyLimitUSD sql.NullFloat64 // nullable monthly quota USD
	DailyUsageUSD   float64         // recorded daily usage USD
	WeeklyUsageUSD  float64         // recorded weekly usage USD
	MonthlyUsageUSD float64         // recorded monthly usage USD
	// PlanGroupIDsRaw is loaded by the unlocked resolver for the returned minimal
	// plan. The allocation query omits it because SQL has already applied the
	// needed coverage filter.
	PlanGroupIDsRaw []byte // JSON plan group IDs
	// PlanGroupRateMultipliersRaw is decoded afresh for pricing snapshots.
	// Malformed JSON becomes an empty rate map, and consumers select positive
	// configured overrides for the final group.
	PlanGroupRateMultipliersRaw []byte // JSON group rate overrides
}

// TaskProjection writes a task's funding split and allowance flag using the
// billing attempt's supplied SQL transaction. The billing owner completes that
// transaction after funding and allowance changes succeed.
// @project-doc docs/domains/routing_and_billing.md#usage_settlement
type TaskProjection interface {
	// SaveReservation persists the held wallet amount, subscription allocations,
	// mixed hold and settlement estimate for this task on the supplied billing
	// transaction. A write failure returns to the billing owner for whole-attempt
	// recovery before upstream submission.
	SaveReservation(context.Context, float64, []billing.BillingAllocation, float64, float64) error
	// SetAllowanceReserved records whether task Key/member usage was reserved at
	// submission on the supplied transaction. Billing sets it after reservation
	// and clears it after capture or release; a write failure fails the funding
	// attempt.
	SetAllowanceReserved(context.Context, bool) error
}
type (
	// TaskProjectionFactory binds a task owner's metadata writer to one billing
	// attempt's SQL transaction and opaque reference. Each attempt needs a usable
	// participant; billing keeps completion ownership.
	TaskProjectionFactory func(*sql.Tx, billing.TaskReference) TaskProjection
	// TaskProjectionFactories registers the task scopes accepted by this funding
	// store. NewSettlementStore merges copies of these maps; later registrations
	// replace duplicate scope entries.
	TaskProjectionFactories map[billing.TaskScope]TaskProjectionFactory
)

// NewSettlementStore retains the caller-owned database and calendar and merges
// task factory registrations into a fresh map. Later maps replace duplicate
// scopes; factory closures remain shared, and callers keep the database usable
// for subsequent operations.
func NewSettlementStore(sqlDB *sql.DB, calendar timezone.Calendar, outbox ProviderQuotaOutbox, factories ...TaskProjectionFactories) *SettlementStore {
	projections := make(TaskProjectionFactories)
	for _, set := range factories {
		maps.Copy(projections, set)
	}
	return &SettlementStore{db: sqlDB, calendar: calendar, providerOutbox: outbox, taskProjections: projections}
}

// Apply normalizes an exclusively owned usage command and applies its funding
// effects in a store-owned SQL transaction. Nil commands return an empty
// result; a missing database or normalized empty action key fails.
// Context-carried Ent transactions are independent, and SQLSTATE 40P01 permits
// two complete retries under the caller's context.
func (r *SettlementStore) Apply(ctx context.Context, cmd *billing.UsageBillingCommand) (_ *billing.UsageBillingApplyResult, err error) {
	if cmd == nil {
		return &billing.UsageBillingApplyResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}

	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, billing.ErrUsageBillingRequestIDRequired
	}

	// A deadlock aborts its transaction. RetryDeadlock creates at most two
	// further complete attempts through applyOnce, while the supplied context
	// governs their SQL and backoff.
	return postgresinfra.RetryDeadlock(ctx, "usage_billing_apply", 0, func() (*billing.UsageBillingApplyResult, error) {
		return r.applyOnce(ctx, cmd)
	})
}

// applyOnce claims and applies one normalized usage action on a newly opened
// transaction. A matching live or archived identity returns Applied false
// before payer and subscription work; a conflicting fingerprint fails. The
// claim can insert a live row before discovering an archive match, so that
// replay path leaves the attempt for deferred rollback.
//
// The payer lock precedes subscription locks, and funding, team/Key counters
// and provider outbox writes precede commit. Failures return to cleanup with
// the transaction retained. Successful commit clears that cleanup handle; a
// commit error is reported without asserting whether the database's final
// outcome is known.
func (r *SettlementStore) applyOnce(ctx context.Context, cmd *billing.UsageBillingCommand) (_ *billing.UsageBillingApplyResult, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Keep cleanup registered until successful commit. Replay and failure paths
	// can leave a live claim or earlier writes that need this attempt's rollback;
	// cleanup errors do not replace the selected result.
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Claim before payer or subscription changes so matching financial identities
	// exit as replay. An archive match leaves this attempt's earlier live
	// insertion for deferred rollback.
	applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &billing.UsageBillingApplyResult{Applied: false}, nil
	}
	if err := lockUsageBillingUser(ctx, tx, cmd.UserID); err != nil {
		return nil, err
	}

	result := &billing.UsageBillingApplyResult{Applied: true}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
		return nil, err
	}

	// Commit the claim with all completed funding and auxiliary writes. Clear the
	// rollback handle only after commit succeeds, retaining cleanup for every
	// returned failure.
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

// lockUsageBillingUser locks an undeleted positive payer before subscription
// writes and returns ErrUserNotFound when that row is absent. Nonpositive IDs
// skip locking for provider-only actions. FOR NO KEY UPDATE permits the KEY
// SHARE locks used by usage-log foreign keys.
func lockUsageBillingUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	if userID <= 0 {
		return nil
	}
	var lockedUserID int64
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, userID).Scan(&lockedUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return billing.ErrUserNotFound
	}
	return err
}

// ResolveUsableSubscriptionForGroup returns the earliest-expiring currently
// usable grant covering a positive group for a positive user. The read uses
// the store database without reserving quota; callers perform transactional
// allocation before relying on an amount remaining available.
func (r *SettlementStore) ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	return r.resolveUsableSubscriptionForGroup(ctx, userID, groupID, nil)
}

// ResolvePreferredSubscriptionForGroup reads the selected grant for a positive
// user, subscription and group. Invalid subscription IDs return
// ErrSubscriptionNotFound; selection stays pinned, and the returned quota
// snapshot needs transactional reservation before task submission.
func (r *SettlementStore) ResolvePreferredSubscriptionForGroup(ctx context.Context, userID, subscriptionID, groupID int64) (*billing.UserSubscription, error) {
	if subscriptionID <= 0 {
		return nil, billing.ErrSubscriptionNotFound
	}
	return r.resolveUsableSubscriptionForGroup(ctx, userID, groupID, &subscriptionID)
}

// resolveUsableSubscriptionForGroup reads eligible grant rows in expiry, start
// and ID order using one Go observation time. Rows must belong to the positive
// user, be undeleted and unheld, have stored active or pending status, contain
// that time and cover the positive group through an unrestricted or matching plan. An
// optional preferred ID narrows the same query.
//
// Each row's window counters are normalized in memory before the minimum
// enabled remainder is checked. An unlimited row uses a positive sentinel, and
// the first positive candidate is returned with minimal plan coverage data.
// Exhausted or absent candidates return ErrSubscriptionNotFound; iteration
// errors propagate. This database read leaves quota reservation to the caller.
func (r *SettlementStore) resolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64, preferredSubscriptionID *int64) (*billing.UserSubscription, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	if userID <= 0 || groupID <= 0 {
		return nil, billing.ErrSubscriptionNotFound
	}

	now := time.Now()
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			us.id,
			us.plan_id,
			us.starts_at,
			us.expires_at,
			us.daily_window_start,
			us.weekly_window_start,
			us.monthly_window_start,
			us.daily_limit_usd,
			us.weekly_limit_usd,
			us.monthly_limit_usd,
			us.daily_usage_usd,
			us.weekly_usage_usd,
			us.monthly_usage_usd,
			COALESCE((
				SELECT jsonb_agg(spg.group_id ORDER BY spg.group_id)
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = sp.id
			), '[]'::jsonb),
			COALESCE((
				SELECT jsonb_object_agg(spg.group_id, spg.rate_multiplier)
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = sp.id
					AND spg.rate_multiplier IS NOT NULL
			), '{}'::jsonb)
		FROM user_subscriptions us
		JOIN subscription_plans sp ON sp.id = us.plan_id
		WHERE us.user_id = $1
			AND us.deleted_at IS NULL
			AND us.refund_hold_operation_id IS NULL
			AND us.starts_at <= $5
			AND us.expires_at > $5
			AND us.status IN ($2, $3)
			AND ($6::bigint IS NULL OR us.id = $6)
			AND (
				NOT EXISTS (
					SELECT 1
					FROM subscription_plan_groups spg
					WHERE spg.plan_id = us.plan_id
				)
				OR EXISTS (
					SELECT 1
					FROM subscription_plan_groups spg
					WHERE spg.plan_id = us.plan_id
						AND spg.group_id = $4
				)
			)
		ORDER BY us.expires_at ASC, us.starts_at ASC, us.id ASC
	`,
		userID,
		billing.SubscriptionStatusActive,
		billing.SubscriptionStatusPending,
		groupID,
		now,
		preferredSubscriptionID,
	)
	if err != nil {
		return nil, err
	}
	// Retain cursor cleanup even when a positive candidate returns before
	// iteration ends. Close failures are discarded here after the selected
	// snapshot or primary read failure.
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var row usageBillingSubscriptionRow
		if err := rows.Scan(
			&row.ID,
			&row.PlanID,
			&row.StartsAt,
			&row.ExpiresAt,
			&row.DailyWindowStart,
			&row.WeeklyWindowStart,
			&row.MonthlyWindowStart,
			&row.DailyLimitUSD,
			&row.WeeklyLimitUSD,
			&row.MonthlyLimitUSD,
			&row.DailyUsageUSD,
			&row.WeeklyUsageUSD,
			&row.MonthlyUsageUSD,
			&row.PlanGroupIDsRaw,
			&row.PlanGroupRateMultipliersRaw,
		); err != nil {
			return nil, err
		}

		// Apply billing's in-memory window rules at the same time used for
		// eligibility. Their effective counters determine whether task pricing can
		// select this row before a later transactional reservation.
		row = normalizeUsageBillingSubscriptionRow(row, now)
		// An unlimited row uses a one-dollar sentinel because this lookup chooses a
		// usable source by positivity; its later reservation computes the required
		// amount.
		available := usageBillingSubscriptionAvailable(
			1,
			windowRemaining(row.DailyLimitUSD, row.DailyUsageUSD),
			windowRemaining(row.WeeklyLimitUSD, row.WeeklyUsageUSD),
			windowRemaining(row.MonthlyLimitUSD, row.MonthlyUsageUSD),
		)
		if available > 0 {
			return usageBillingSubscriptionRowToService(userID, row), nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, billing.ErrSubscriptionNotFound
}

// claimUsageBillingKey forwards the normalized command's action key, API Key
// and fingerprint to the transactional deduplication check. Callers interpret
// false as replay and leave this attempt uncommitted, including a possible
// live insert preceding an archive match.
func (r *SettlementStore) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *billing.UsageBillingCommand) (bool, error) {
	return r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
}

// claimUsageBillingRequest claims the action-key/API-Key pair in the supplied
// transaction and reports whether funding should proceed. A duplicate live
// pair compares trimmed fingerprints; a mismatch returns
// ErrUsageBillingRequestConflict, while an equal fingerprint returns false.
// The action key itself is used as supplied.
//
// After a successful live insert, the archive is checked for the same identity
// to prevent charging an aged-out action again. An archive match also compares
// fingerprints and returns false or conflict. Because that path has already
// inserted the live claim, the transaction owner rolls back replay and failure
// attempts before publishing financial results.
func (r *SettlementStore) claimUsageBillingRequest(ctx context.Context, tx *sql.Tx, requestID string, apiKeyID int64, requestFingerprint string) (bool, error) {
	// Stored fingerprints can retain surrounding whitespace through command
	// normalization. Apply trimmed equality when either table already holds this
	// identity, so those retained characters do not create a replay conflict.
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, requestID, apiKeyID, requestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, requestID, apiKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, billing.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// The live insert can succeed for an aged-out action. Compare its archive
	// identity before funding, leaving the owner to roll back the inserted row on
	// replay or conflict.
	var archivedFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT request_fingerprint
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, requestID, apiKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, billing.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

// Reserve applies a task's subscription-first reservation, wallet hold, task
// funding metadata and allowance reservation through the selected scope
// factory. Nil commands return an empty result. Command normalization precedes
// transaction work; computed hold changes are published after the owned
// attempt succeeds.
func (r *SettlementStore) Reserve(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	return r.applyTaskBalanceHold(ctx, cmd, taskAllowanceReserve, func(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, projection TaskProjection) (*billing.TaskFundsResult, error) {
		return reserveUsageBillingTaskBilling(ctx, tx, projection, cmd)
	})
}

// Capture applies the supplied task funding snapshot to actual costs and
// releases unused held amounts through the selected scope factory. The task
// owner supplies its original reservation identity and metadata; this wrapper
// delegates retries and transaction completion.
func (r *SettlementStore) Capture(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	return r.applyTaskBalanceHold(ctx, cmd, taskAllowanceCapture, func(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, projection TaskProjection) (*billing.TaskFundsResult, error) {
		return captureUsageBillingTaskBilling(ctx, tx, cmd)
	})
}

// Release returns a task's recorded subscription and wallet reservations and
// updates its allowance flag through the selected scope factory. The task
// owner supplies the original reserve key; an absent hold claim makes the
// funding refund an empty result.
func (r *SettlementStore) Release(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	return r.applyTaskBalanceHold(ctx, cmd, taskAllowanceRelease, func(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, projection TaskProjection) (*billing.TaskFundsResult, error) {
		return releaseUsageBillingTaskBilling(ctx, tx, cmd)
	})
}

// applyTaskBalanceHold validates the task scope before normalizing an
// exclusively owned command and starting any transaction. Nil input returns an
// empty result; missing database, unregistered scope and empty normalized
// action key fail. The initial normalization remains visible even when later
// work fails.
//
// Every deadlock attempt starts from the same normalized snapshot, with a
// separate allocation slice, so a failed attempt's computed hold or actual
// amount cannot feed the next attempt. Optional allocation ID pointers remain
// shared and need immutable use. Attempt changes replace the caller command
// only after the complete transaction succeeds, including successful replay
// reconstruction.
func (r *SettlementStore) applyTaskBalanceHold(
	ctx context.Context,
	cmd *billing.TaskFundsCommand,
	operation taskAllowanceOperation,
	apply func(context.Context, *sql.Tx, *billing.TaskFundsCommand, TaskProjection) (*billing.TaskFundsResult, error),
) (_ *billing.TaskFundsResult, err error) {
	if cmd == nil {
		return &billing.TaskFundsResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	if r.taskProjections[cmd.Task.Scope] == nil {
		return nil, fmt.Errorf("unsupported billing task scope: %s", cmd.Task.Scope)
	}
	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, billing.ErrUsageBillingRequestIDRequired
	}
	original := cloneTaskBalanceHoldCommand(cmd)

	// Start each retry from the normalized input snapshot. Publish computed
	// hold/actual fields only after an attempt succeeds, preventing rolled-back
	// allocation changes from becoming retry inputs.
	result, err := postgresinfra.RetryDeadlock(ctx, taskAllowanceOperationName(operation), 0, func() (*billing.TaskFundsResult, error) {
		attemptCmd := cloneTaskBalanceHoldCommand(&original)
		attemptResult, attemptErr := r.applyTaskBalanceHoldOnce(ctx, &attemptCmd, operation, apply)
		if attemptErr == nil {
			*cmd = attemptCmd
		}
		return attemptResult, attemptErr
	})
	return result, err
}

// applyTaskBalanceHoldOnce owns one task funding transaction and claims its
// action identity before locking the payer. A replay reconstructs an
// Applied-false result from the supplied funding snapshot and leaves the
// transaction for rollback. A new action obtains a fresh transaction-bound
// participant from its registered scope; a missing factory or nil participant
// fails.
//
// The caller supplies a nonnil funding callback. That callback runs before
// allowance bookkeeping, and both participant writes share the same commit as
// balances, subscription counters and deduplication. A nil callback result
// becomes an empty Applied-true result. Any error leaves cleanup active; only
// successful commit clears the rollback handle.
func (r *SettlementStore) applyTaskBalanceHoldOnce(
	ctx context.Context,
	cmd *billing.TaskFundsCommand,
	operation taskAllowanceOperation,
	apply func(context.Context, *sql.Tx, *billing.TaskFundsCommand, TaskProjection) (*billing.TaskFundsResult, error),
) (_ *billing.TaskFundsResult, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Keep cleanup registered until successful commit. Replay and failure paths
	// can leave a live claim or earlier writes that need this attempt's rollback;
	// cleanup errors do not replace the selected result.
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	if err != nil {
		return nil, err
	}
	if !applied {
		return taskBillingResultForCommand(cmd, operation), nil
	}
	if err := lockUsageBillingUser(ctx, tx, cmd.UserID); err != nil {
		return nil, err
	}

	factory := r.taskProjections[cmd.Task.Scope]
	if factory == nil {
		return nil, fmt.Errorf("unsupported billing task scope: %s", cmd.Task.Scope)
	}
	projection := factory(tx, cmd.Task)
	if projection == nil {
		return nil, errors.New("billing task projection is nil")
	}
	// Apply funding and task allocation writes before Key/member allowance
	// bookkeeping. Both stages use the same participant and transaction, so a
	// later failure prevents normal commit.
	result, err := apply(ctx, tx, cmd, projection)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &billing.TaskFundsResult{}
	}
	result.Applied = true
	if err := r.applyTaskAllowance(ctx, tx, cmd, operation, projection); err != nil {
		return nil, err
	}

	// Commit the claim with all completed funding and auxiliary writes. Clear the
	// rollback handle only after commit succeeds, retaining cleanup for every
	// returned failure.
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

// cloneTaskBalanceHoldCommand copies the outer command and allocation slice
// for independent retry mutations. Nil input yields a zero command. Allocation
// ID pointers and other optional pointers remain shared, so callers keep their
// pointed-to values unchanged across attempts.
func cloneTaskBalanceHoldCommand(cmd *billing.TaskFundsCommand) billing.TaskFundsCommand {
	if cmd == nil {
		return billing.TaskFundsCommand{}
	}
	cloned := *cmd
	cloned.SubscriptionHoldAllocations = append([]billing.BillingAllocation(nil), cmd.SubscriptionHoldAllocations...)
	return cloned
}

// taskAllowanceOperationName returns the existing batch-image-prefixed retry
// log label for reserve, capture or release. Unknown enum values receive the
// unknown label; task scope does not change these diagnostic operation names.
func taskAllowanceOperationName(operation taskAllowanceOperation) string {
	switch operation {
	case taskAllowanceReserve:
		return "usage_billing_batch_image_reserve"
	case taskAllowanceCapture:
		return "usage_billing_batch_image_capture"
	case taskAllowanceRelease:
		return "usage_billing_batch_image_release"
	default:
		return "usage_billing_batch_image_unknown"
	}
}

// applyTaskAllowance adjusts Key and member counters after task funding
// succeeds inside the same transaction. Nil or nonpositive hold-and-base
// commands skip counters and participant flag changes. Reserve charges the
// computed hold and records the allowance flag; a zero ReservedAt selects a
// current time for Key reservation while member reservation observes its own
// calendar time. That chosen Key time is not written back to cmd.ReservedAt;
// task owners retain their original timestamp for later refunds.
//
// Capture with a recorded allowance refunds positive hold-minus-actual usage
// under a savepoint and charges any accepted excess to current Key and member
// windows. Older snapshots charge the full actual usage at capture instead.
// Release refunds the recorded hold when its flag is set. Capture and release
// then clear that flag; successful savepoint recovery can retain conservative
// counts even while the flag and funding commit.
func (r *SettlementStore) applyTaskAllowance(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, operation taskAllowanceOperation, projection TaskProjection) error {
	if cmd == nil || (cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0) {
		return nil
	}
	switch operation {
	case taskAllowanceReserve:
		reservedAt := cmd.ReservedAt
		if reservedAt.IsZero() {
			reservedAt = time.Now()
		}
		// Reserve Key usage before member usage and the stored allowance flag. These
		// writes follow funding within the same transaction, so any later error
		// reaches whole-attempt recovery.
		if err := reserveTaskAPIKeyAllowance(ctx, tx, cmd.APIKeyID, cmd.HoldAmount, reservedAt); err != nil {
			return err
		}
		if err := r.reserveTaskMemberAllowance(ctx, tx, cmd, cmd.HoldAmount); err != nil {
			return err
		}
		return projection.SetAllowanceReserved(ctx, true)
	case taskAllowanceCapture:
		chargeAmount := cmd.ActualAmount
		if cmd.AllowanceReserved {
			// The saved flag identifies allowance already counted at submission. Refund
			// only unused held usage; savepoint recovery keeps completed funding
			// committable when paired counter refunds fail.
			adjustment := cmd.HoldAmount - cmd.ActualAmount
			chargeAmount = math.Max(-adjustment, 0)
			if adjustment > 0 {
				if err := rollbackTaskAllowanceBestEffort(ctx, tx, cmd.Task.ID, func() error {
					if err := releaseTaskAPIKeyAllowance(ctx, tx, cmd.APIKeyID, adjustment, cmd.ReservedAt); err != nil {
						return err
					}
					return releaseTaskMemberAllowance(ctx, tx, cmd, adjustment)
				}); err != nil {
					return err
				}
			}
		}
		if chargeAmount > 0 {
			// Count accepted excess, or the full legacy charge, only after funding
			// succeeds. These writes share its transaction and cannot double-count a replay.
			if err := chargeTaskAPIKey(ctx, tx, cmd.APIKeyID, chargeAmount); err != nil {
				return err
			}
			if cmd.TeamID != nil && cmd.ActorUserID > 0 && cmd.ActorUserID != cmd.UserID {
				if err := r.incrementUsageBillingTeamMember(ctx, tx, *cmd.TeamID, cmd.ActorUserID, chargeAmount, time.Now()); err != nil {
					return err
				}
			}
		}
		return projection.SetAllowanceReserved(ctx, false)
	case taskAllowanceRelease:
		// Refund this cancellation's full recorded hold beneath one allowance
		// savepoint. Paired Key/member updates can otherwise retain different counts
		// when the later refund fails.
		if cmd.AllowanceReserved {
			if err := rollbackTaskAllowanceBestEffort(ctx, tx, cmd.Task.ID, func() error {
				if err := releaseTaskAPIKeyAllowance(ctx, tx, cmd.APIKeyID, cmd.HoldAmount, cmd.ReservedAt); err != nil {
					return err
				}
				return releaseTaskMemberAllowance(ctx, tx, cmd, cmd.HoldAmount)
			}); err != nil {
				return err
			}
		}
		return projection.SetAllowanceReserved(ctx, false)
	default:
		return nil
	}
}

// rollbackTaskAllowanceBestEffort runs a nonnil allowance refund callback
// beneath a fixed savepoint in the caller's transaction. A callback error
// rolls back its partial Key/member changes and releases the savepoint.
// Successful recovery logs that error and returns nil, allowing the caller's
// previously completed funding updates to commit with conservative allowance
// counts retained.
//
// Savepoint creation, rollback-to-savepoint and release failures propagate
// because the caller can no longer assume isolated recovery succeeded. A
// successful callback also releases its savepoint before returning. The
// surrounding transaction owner decides commit or rollback and supplies
// callback execution within the same transaction.
func rollbackTaskAllowanceBestEffort(ctx context.Context, tx *sql.Tx, batchID string, rollback func() error) error {
	const savepoint = "batch_image_allowance_rollback"
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
		return err
	}
	// A failed refund may have already changed the Key before member failure.
	// Undo both to this savepoint, then suppress the callback error only after
	// recovery and release succeed.
	if err := rollback(); err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rollbackErr != nil {
			return rollbackErr
		}
		if _, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); releaseErr != nil {
			return releaseErr
		}
		logger.LegacyPrintf("repository.usage_billing", "[BatchImage] allowance rollback skipped: batch=%s error=%v", batchID, err)
		return nil
	}
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint)
	return err
}

// taskHoldClaimRequestID returns the task owner's recorded reservation action
// key verbatim. Nil input returns an empty string; billing uses this supplied
// identity even when its prefix or task ID belongs to a different task scope.
func taskHoldClaimRequestID(cmd *billing.TaskFundsCommand) string {
	if cmd == nil {
		return ""
	}
	return cmd.Task.ReserveRequestID
}

// reserveTaskAPIKeyAllowance atomically adds a task hold to an active,
// undeleted Key with team_owner_disabled=false whose total and rolling limits
// can cover it. Equality is accepted, and reaching a positive total quota stores
// quota_exhausted. The caller supplies the reservation timestamp and a
// validated monetary amount.
//
// Missing or expired rolling anchors select new usage; the five-hour anchor
// uses reservedAt, while day and week anchors use database-session midnight.
// No matching update triggers a diagnostic reread with its own current-time
// observation, so the reported error can reflect a later window. Other SQL
// errors propagate to the transaction owner.
func reserveTaskAPIKeyAllowance(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64, reservedAt time.Time) error {
	var id int64
	// Use the supplied reservation time for rolling-limit guards and replacement
	// anchors. Later refunds test that original time against current windows,
	// keeping newer window usage attributable to its own reservations.
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys SET
			quota_used = quota_used + $1,
			usage_5h = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= $5 THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= $5 THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= $5 THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= $5 THEN $5 ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= $5 THEN date_trunc('day', $5::timestamptz) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= $5 THEN date_trunc('day', $5::timestamptz) ELSE window_7d_start END,
			status = CASE WHEN quota > 0 AND quota_used + $1 >= quota THEN $3 ELSE status END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND status = $4 AND team_owner_disabled = FALSE
		  AND (quota <= 0 OR quota_used + $1 <= quota)
		  AND (rate_limit_5h <= 0 OR (CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= $5 THEN 0 ELSE usage_5h END) + $1 <= rate_limit_5h)
		  AND (rate_limit_1d <= 0 OR (CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= $5 THEN 0 ELSE usage_1d END) + $1 <= rate_limit_1d)
		  AND (rate_limit_7d <= 0 OR (CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= $5 THEN 0 ELSE usage_7d END) + $1 <= rate_limit_7d)
		RETURNING id`, amount, apiKeyID, "quota_exhausted", "active", reservedAt).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// A rejected guarded update has no direct reason code. Reread the Key to
	// choose a quota, rolling-limit or availability error for the task owner.
	return taskAPIKeyAllowanceError(ctx, tx, apiKeyID, amount)
}

// taskAPIKeyAllowanceError diagnoses a failed reservation by rereading the
// undeleted Key. Missing, disabled-owner and non-active rows become
// Key-not-found unless stored quota_exhausted selects the total-quota error.
// Total quota is checked before five-hour, daily and weekly amounts.
//
// Rolling diagnostics use a fresh Go time rather than the reservation's
// earlier timestamp, with absent or elapsed anchors contributing zero usage. A
// boundary change can therefore remove the earlier failing limit from this
// observation. If no current limit explains rejection, ErrAPIKeyNotFound
// remains the result; the caller keeps the failed reservation transaction
// uncommitted.
func taskAPIKeyAllowanceError(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) error {
	var status string
	var ownerDisabled bool
	var quota, quotaUsed, limit5h, limit1d, limit7d, usage5h, usage1d, usage7d float64
	var start5h, start1d, start7d sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT status, team_owner_disabled, quota, quota_used, rate_limit_5h, rate_limit_1d, rate_limit_7d,
		       usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start
		FROM api_keys WHERE id = $1 AND deleted_at IS NULL`, apiKeyID).
		Scan(&status, &ownerDisabled, &quota, &quotaUsed, &limit5h, &limit1d, &limit7d, &usage5h, &usage1d, &usage7d, &start5h, &start1d, &start7d)
	if errors.Is(err, sql.ErrNoRows) {
		return billing.ErrAPIKeyNotFound
	}
	if err != nil {
		return err
	}
	if ownerDisabled || status != "active" {
		if status == "quota_exhausted" {
			return billing.ErrAPIKeyQuotaExhausted
		}
		return billing.ErrAPIKeyNotFound
	}
	if quota > 0 && quotaUsed+amount > quota {
		return billing.ErrAPIKeyQuotaExhausted
	}
	// Reevaluate elapsed windows at current Go time using the Key's fixed
	// five-hour, twenty-four-hour and seven-day durations. A changed boundary can
	// remove the earlier guarded update's failing limit.
	now := time.Now()
	if limit5h > 0 && effectiveSQLWindowUsage(usage5h, start5h, 5*time.Hour, now)+amount > limit5h {
		return billing.ErrAPIKeyRateLimit5hExceeded
	}
	if limit1d > 0 && effectiveSQLWindowUsage(usage1d, start1d, 24*time.Hour, now)+amount > limit1d {
		return billing.ErrAPIKeyRateLimit1dExceeded
	}
	if limit7d > 0 && effectiveSQLWindowUsage(usage7d, start7d, 7*24*time.Hour, now)+amount > limit7d {
		return billing.ErrAPIKeyRateLimit7dExceeded
	}
	return billing.ErrAPIKeyNotFound
}

// effectiveSQLWindowUsage returns zero for an absent anchor or a duration
// ending at or before now. Otherwise it returns usage unchanged, including
// future anchors and signed values; callers provide the window duration
// appropriate to their Key limit.
func effectiveSQLWindowUsage(usage float64, start sql.NullTime, duration time.Duration, now time.Time) float64 {
	if !start.Valid || !start.Time.Add(duration).After(now) {
		return 0
	}
	return usage
}

// reserveTaskMemberAllowance adds the held amount to an active member's
// calendar day, week and month counters when a team actor differs from the
// payer. Missing teams, nonpositive actors and owner-as-actor commands skip
// this step. Positive limits require the post-reservation amounts to fit, with
// equality accepted.
//
// Calendar starts come from a fresh Go observation and the store calendar,
// independently of cmd.ReservedAt. Absent or older anchors reset the matching
// counter. A rejected update is reread for ordered limit diagnosis; an owner
// role in that reread returns success without a member counter write. The
// transaction owner completes or recovers the funding sequence.
func (r *SettlementStore) reserveTaskMemberAllowance(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, amount float64) error {
	if cmd.TeamID == nil || cmd.ActorUserID <= 0 || cmd.ActorUserID == cmd.UserID {
		return nil
	}
	now := time.Now()
	dailyStart := r.calendar.StartOfDay(now)
	weeklyStart := r.calendar.StartOfWeek(now)
	monthlyStart := r.calendar.StartOfMonth(now)
	var id int64
	err := tx.QueryRowContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $3 ELSE daily_usage_usd + $3 END,
			weekly_usage_usd = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $3 ELSE weekly_usage_usd + $3 END,
			monthly_usage_usd = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $3 ELSE monthly_usage_usd + $3 END,
			daily_window_start = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $4 ELSE daily_window_start END,
			weekly_window_start = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $5 ELSE weekly_window_start END,
			monthly_window_start = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $6 ELSE monthly_window_start END,
			updated_at = $7
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'
		  AND (daily_limit_usd <= 0 OR (CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN 0 ELSE daily_usage_usd END) + $3 <= daily_limit_usd)
		  AND (weekly_limit_usd <= 0 OR (CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN 0 ELSE weekly_usage_usd END) + $3 <= weekly_limit_usd)
		  AND (monthly_limit_usd <= 0 OR (CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN 0 ELSE monthly_usage_usd END) + $3 <= monthly_limit_usd)
		RETURNING id`, *cmd.TeamID, cmd.ActorUserID, amount, dailyStart, weeklyStart, monthlyStart, now).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// The guarded member update can miss for role, membership or limit reasons.
	// Reread with these same calendar starts so the task receives the first
	// observed limit error.
	return taskMemberAllowanceError(ctx, tx, *cmd.TeamID, cmd.ActorUserID, amount, dailyStart, weeklyStart, monthlyStart)
}

// taskMemberAllowanceError rereads current team membership after a held-amount
// update finds no eligible row. Missing active membership returns
// ErrTeamMembershipRequired, while an owner returns nil. For other roles,
// positive daily, weekly and monthly limits are checked in that order using
// the supplied calendar starts.
//
// An absent or older stored anchor contributes zero usage; anchors at or after
// the expected start retain their counters. The first exceeded limit chooses
// its specific error. If these observed limits fit but the update was still
// rejected, membership-required remains the result, and the caller's
// transaction handles the failed reservation.
func taskMemberAllowanceError(ctx context.Context, tx *sql.Tx, teamID, userID int64, amount float64, dailyStart, weeklyStart, monthlyStart time.Time) error {
	var role string
	var dailyLimit, weeklyLimit, monthlyLimit, dailyUsage, weeklyUsage, monthlyUsage float64
	var dailyWindow, weeklyWindow, monthlyWindow sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT role, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
		       daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
		       daily_window_start, weekly_window_start, monthly_window_start
		FROM team_memberships WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL`, teamID, userID).
		Scan(&role, &dailyLimit, &weeklyLimit, &monthlyLimit, &dailyUsage, &weeklyUsage, &monthlyUsage, &dailyWindow, &weeklyWindow, &monthlyWindow)
	if errors.Is(err, sql.ErrNoRows) {
		return billing.ErrTeamMembershipRequired
	}
	if err != nil {
		return err
	}
	// A current owner row is outside the member-only guarded update. Accept this
	// diagnosis as success so a changed role does not reject the already prepared
	// funding.
	if role == "owner" {
		return nil
	}
	if dailyLimit > 0 && effectiveNaturalWindowUsage(dailyUsage, dailyWindow, dailyStart)+amount > dailyLimit {
		return billing.ErrTeamMemberDailyExceeded
	}
	if weeklyLimit > 0 && effectiveNaturalWindowUsage(weeklyUsage, weeklyWindow, weeklyStart)+amount > weeklyLimit {
		return billing.ErrTeamMemberWeeklyExceeded
	}
	if monthlyLimit > 0 && effectiveNaturalWindowUsage(monthlyUsage, monthlyWindow, monthlyStart)+amount > monthlyLimit {
		return billing.ErrTeamMemberMonthlyExceeded
	}
	return billing.ErrTeamMembershipRequired
}

// effectiveNaturalWindowUsage returns zero when the stored calendar anchor is
// absent or older than expectedStart. Anchors at or after that start retain
// usage unchanged; future anchors and signed usage are left for the caller's
// domain validation.
func effectiveNaturalWindowUsage(usage float64, window sql.NullTime, expectedStart time.Time) float64 {
	if !window.Valid || window.Time.Before(expectedStart) {
		return 0
	}
	return usage
}

// releaseTaskAPIKeyAllowance decreases total Key usage and the rolling windows
// that still contain the original reservation timestamp, clamping refunded
// counters at zero. It targets the original ID even after deletion, and an
// absent row is a successful no-op.
//
// A stored quota_exhausted Key can reactivate when team_owner_disabled=false
// and its reduced total falls below the quota or the quota is unlimited. Other
// statuses remain as stored. The caller supplies the previously reserved
// amount and timestamp and wraps paired member refunds in a savepoint to
// recover partial allowance writes.
func releaseTaskAPIKeyAllowance(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64, reservedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			quota_used = GREATEST(0, quota_used - $1),
			usage_5h = CASE WHEN window_5h_start <= $3 AND $3 < window_5h_start + INTERVAL '5 hours' THEN GREATEST(0, usage_5h - $1) ELSE usage_5h END,
			usage_1d = CASE WHEN window_1d_start <= $3 AND $3 < window_1d_start + INTERVAL '24 hours' THEN GREATEST(0, usage_1d - $1) ELSE usage_1d END,
			usage_7d = CASE WHEN window_7d_start <= $3 AND $3 < window_7d_start + INTERVAL '7 days' THEN GREATEST(0, usage_7d - $1) ELSE usage_7d END,
			status = CASE WHEN status = $4 AND team_owner_disabled = FALSE AND (quota <= 0 OR GREATEST(0, quota_used - $1) < quota) THEN $5 ELSE status END,
			updated_at = NOW()
		WHERE id = $2`, amount, apiKeyID, reservedAt, "quota_exhausted", "active")
	return err
}

// releaseTaskMemberAllowance refunds the actor's recorded team usage when the
// command has a team and a positive actor distinct from the payer. Membership
// must have existed at ReservedAt; its current role does not restrict the
// refund. Missing matching rows are successful no-ops.
//
// Each calendar counter decreases only when its current day, week or month
// interval contains that reservation time, so a later window keeps its own
// usage. Refunded values clamp at zero. The caller uses its transaction and
// allowance savepoint to keep this update consistent with the preceding Key
// refund.
func releaseTaskMemberAllowance(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, amount float64) error {
	if cmd.TeamID == nil || cmd.ActorUserID <= 0 || cmd.ActorUserID == cmd.UserID {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN daily_window_start <= $4 AND $4 < daily_window_start + INTERVAL '1 day' THEN GREATEST(0, daily_usage_usd - $3) ELSE daily_usage_usd END,
			weekly_usage_usd = CASE WHEN weekly_window_start <= $4 AND $4 < weekly_window_start + INTERVAL '7 days' THEN GREATEST(0, weekly_usage_usd - $3) ELSE weekly_usage_usd END,
			monthly_usage_usd = CASE WHEN monthly_window_start <= $4 AND $4 < monthly_window_start + INTERVAL '1 month' THEN GREATEST(0, monthly_usage_usd - $3) ELSE monthly_usage_usd END,
			updated_at = NOW()
		WHERE team_id = $1 AND user_id = $2
		  AND joined_at <= $4 AND (left_at IS NULL OR left_at > $4)`, *cmd.TeamID, cmd.ActorUserID, amount, cmd.ReservedAt)
	return err
}

// chargeTaskAPIKey records completed usage absent from a task's allowance hold.
// Legacy captures supply the entire actual charge; captures above a recorded
// hold supply only the excess. It updates the original Key ID without admission
// or deletion filters, and a missing row is a successful no-op. Rolling windows
// use database NOW for their expiration and replacement anchors.
//
// Reaching a positive total quota stores quota_exhausted when the stored
// team_owner_disabled flag is false, including a row with another status. This differs from
// ordinary request quota transitions, which require an active undeleted row
// for status changes. The caller executes this compatibility charge in the
// same transaction as capture and member accounting.
func chargeTaskAPIKey(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			quota_used = quota_used + $1,
			usage_5h = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			status = CASE WHEN quota > 0 AND quota_used + $1 >= quota AND team_owner_disabled = FALSE THEN $3 ELSE status END,
			updated_at = NOW()
		WHERE id = $2`, amount, apiKeyID, "quota_exhausted")
	return err
}

// applyUsageBillingEffects allocates completed usage to locked subscriptions,
// then deducts uncovered wallet cost even when the wallet becomes negative.
// Base-priced commands convert uncovered base at the balance rate; legacy
// commands use the uncovered billable amount directly. The returned
// allocations record actual subscription charges and the wallet's quantized
// deduction.
//
// Team member totals use the combined actual charge. Positive Key quota and
// rolling-cost fields enable their writes, with base-priced commands replacing
// those amounts by the combined charge. Positive provider cost is counted for
// API Key and Bedrock provider types. All writes and any threshold outbox
// event use the caller's transaction, which recovers earlier effects after a
// later error.
func (r *SettlementStore) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *billing.UsageBillingCommand, result *billing.UsageBillingApplyResult) error {
	remainingAmount, subscriptionAmount, allocations, err := allocateUsageBillingSubscriptions(ctx, tx, cmd)
	if err != nil {
		return err
	}
	result.BillingAllocations = allocations
	result.SubscriptionAmountUSD = subscriptionAmount

	// Completed upstream usage needs a full monetary settlement. The planner caps
	// subscription counters, and uncovered base uses the wallet rate even when
	// the payer's available balance becomes debt.
	balanceAmount := remainingAmount
	if usageBillingUsesBaseAmount(cmd) {
		balanceAmount = remainingAmount * usageBillingNonNegativeRate(cmd.BalanceRateMultiplier)
	}
	if balanceAmount > 0 {
		newBalance, deductedAmount, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, balanceAmount)
		if err != nil {
			return err
		}
		result.BalanceAmountUSD = deductedAmount
		if deductedAmount > 0 {
			result.NewBalance = &newBalance
			result.BillingAllocations = append(result.BillingAllocations, billing.BillingAllocation{
				Type:      billing.BillingAllocationTypeBalance,
				AmountUSD: deductedAmount,
			})
		}
	}

	billableAmount := result.SubscriptionAmountUSD + result.BalanceAmountUSD
	if cmd.TeamID != nil && cmd.ActorUserID > 0 && cmd.ActorUserID != cmd.UserID {
		if err := r.incrementUsageBillingTeamMember(ctx, tx, *cmd.TeamID, cmd.ActorUserID, billableAmount, time.Now()); err != nil {
			return err
		}
	}
	if usageBillingUsesBaseAmount(cmd) && cmd.BaseAmountUSD > 0 {
		effectiveRate := billableAmount / cmd.BaseAmountUSD
		result.EffectiveRateMultiplier = &effectiveRate
	}
	if cmd.APIKeyQuotaCost > 0 {
		quotaCost := cmd.APIKeyQuotaCost
		if usageBillingUsesBaseAmount(cmd) {
			quotaCost = billableAmount
		}
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, quotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		rateLimitCost := cmd.APIKeyRateLimitCost
		if usageBillingUsesBaseAmount(cmd) {
			rateLimitCost = billableAmount
		}
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd.APIKeyID, rateLimitCost); err != nil {
			return err
		}
	}

	if cmd.ProviderQuotaCost > 0 && (strings.EqualFold(cmd.ProviderType, capability.ProviderTypeAPIKey) || strings.EqualFold(cmd.ProviderType, capability.ProviderTypeBedrock)) {
		quotaState, err := incrementUsageBillingProviderQuota(ctx, tx, cmd.ProviderID, cmd.ProviderQuotaCost, r.providerOutbox)
		if err != nil {
			return err
		}
		result.QuotaState = quotaState
	}

	return nil
}

// incrementUsageBillingTeamMember counts positive completed usage in an active
// member's natural calendar windows. Nonpositive amounts return success; the
// caller supplies the observation time and owns the transaction. Missing or
// older anchors reset their counters before charging.
//
// A member that left or became owner while upstream work ran produces no
// matching update, and affected-row count is deliberately unexamined. Funding
// can therefore finish even when member usage is no longer counted. Current
// active members receive each daily, weekly and monthly increment using starts
// derived from the store calendar.
func (r *SettlementStore) incrementUsageBillingTeamMember(ctx context.Context, tx *sql.Tx, teamID, actorUserID int64, amount float64, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	dailyStart := r.calendar.StartOfDay(now)
	weeklyStart := r.calendar.StartOfWeek(now)
	monthlyStart := r.calendar.StartOfMonth(now)
	_, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $3 ELSE daily_usage_usd + $3 END,
			weekly_usage_usd = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $3 ELSE weekly_usage_usd + $3 END,
			monthly_usage_usd = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $3 ELSE monthly_usage_usd + $3 END,
			daily_window_start = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $4 ELSE daily_window_start END,
			weekly_window_start = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $5 ELSE weekly_window_start END,
			monthly_window_start = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $6 ELSE monthly_window_start END,
			updated_at = $7
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`,
		teamID, actorUserID, amount, dailyStart, weeklyStart, monthlyStart, now)
	if err != nil {
		return err
	}
	// An actor can leave or become owner while upstream work runs. SQL failure
	// still propagates, but a missing active-member match is accepted so
	// completed funding can commit.
	return nil
}

// usageBillingSubscriptionRowToService constructs an active service grant from
// a row already selected for current eligibility. Nullable times and limits
// become fresh optional values, and plan group collections are decoded afresh.
// The attached plan contains its ID and coverage/rates, so consumers needing
// other plan metadata perform a separate read.
func usageBillingSubscriptionRowToService(userID int64, row usageBillingSubscriptionRow) *billing.UserSubscription {
	return &billing.UserSubscription{
		ID:                 row.ID,
		UserID:             userID,
		PlanID:             row.PlanID,
		StartsAt:           row.StartsAt,
		ExpiresAt:          row.ExpiresAt,
		Status:             billing.SubscriptionStatusActive,
		DailyWindowStart:   usageBillingNullableTimePtr(row.DailyWindowStart),
		WeeklyWindowStart:  usageBillingNullableTimePtr(row.WeeklyWindowStart),
		MonthlyWindowStart: usageBillingNullableTimePtr(row.MonthlyWindowStart),
		DailyLimitUSD:      usageBillingNullableFloat64Ptr(row.DailyLimitUSD),
		WeeklyLimitUSD:     usageBillingNullableFloat64Ptr(row.WeeklyLimitUSD),
		MonthlyLimitUSD:    usageBillingNullableFloat64Ptr(row.MonthlyLimitUSD),
		DailyUsageUSD:      row.DailyUsageUSD,
		WeeklyUsageUSD:     row.WeeklyUsageUSD,
		MonthlyUsageUSD:    row.MonthlyUsageUSD,
		Plan: &billing.SubscriptionPlan{
			ID:                   row.PlanID,
			GroupIDs:             parseInt64JSONSlice(row.PlanGroupIDsRaw),
			GroupRateMultipliers: parseInt64Float64JSONMap(row.PlanGroupRateMultipliersRaw),
		},
	}
}

// parseInt64JSONSlice decodes stored plan group IDs into a new slice. Empty,
// malformed or JSON-null input returns nil; decoded IDs are retained as
// supplied, so callers establish any positive-ID and coverage validation
// separately.
func parseInt64JSONSlice(data []byte) []int64 {
	if len(data) == 0 {
		return nil
	}
	var out []int64
	// Malformed stored coverage becomes absent group data in the returned minimal
	// plan. Eligibility has already been selected by SQL, and this decoder has no
	// independent admission role.
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// parseInt64Float64JSONMap decodes plan rates into a new integer-keyed map and
// returns an allocated empty map for absent, malformed or JSON-null input.
// Unparseable and nonpositive keys are skipped; decoded float values remain
// unchanged.
//
// String keys are converted in base ten, so aliases such as "01" and "1"
// address the same integer ID. If both occur, Go map iteration chooses the
// later assignment, with no deterministic alias preference. Consumers apply
// their own rate normalization; this conversion retains stored values for
// those decisions.
func parseInt64Float64JSONMap(data []byte) map[int64]float64 {
	if len(data) == 0 {
		return map[int64]float64{}
	}
	var raw map[string]float64
	// Treat malformed rate metadata as an empty override map, letting later
	// source-rate selection use its configured default rather than failing the
	// funding snapshot conversion.
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(raw))
	for key, value := range raw {
		// Keep positive numeric group keys for rate lookup. String aliases can
		// collapse onto one integer identity, so stored inputs should use canonical
		// keys to avoid iteration-dependent replacement.
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		out[id] = value
	}
	return out
}

// usageBillingNullableTimePtr returns nil for an invalid SQL time and a
// pointer to a copied time for a valid value. The resulting optional value is
// detached from the caller's SQL wrapper.
func usageBillingNullableTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

// usageBillingNullableFloat64Ptr returns nil for an invalid SQL number and a
// pointer to a copied value otherwise. Zero and negative configured limits
// remain represented so billing's positive-limit rule can interpret them.
func usageBillingNullableFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// allocateUsageBillingSubscriptions locks eligible payer subscriptions in
// expiry, start and ID order and applies billing's ordered allocation plan.
// The caller locks the payer first and supplies a normalized command. Balance
// mode or a nonpositive basis skips this query; an optional preferred ID
// narrows the eligible rows.
//
// Positive groups require an unrestricted or matching plan, while pinned mode
// without a group requires an unrestricted plan. Rows must be free of refund
// holds and use database NOW for eligibility; the planner observes Go time after rows close for effective
// windows and source rates. Each planned counter update is persisted in order
// on the same transaction, so a later failure requires owner rollback before
// exposing any allocation.
func allocateUsageBillingSubscriptions(ctx context.Context, tx *sql.Tx, cmd *billing.UsageBillingCommand) (float64, float64, []billing.BillingAllocation, error) {
	amountUSD, querySubscriptions, err := billing.SubscriptionAllocationRequest(cmd)
	if err != nil {
		return 0, 0, nil, err
	}
	if !querySubscriptions {
		return amountUSD, 0, nil, nil
	}

	query := `
		SELECT
			id,
			plan_id,
			starts_at,
			expires_at,
			daily_window_start,
			weekly_window_start,
			monthly_window_start,
			daily_limit_usd,
			weekly_limit_usd,
			monthly_limit_usd,
			daily_usage_usd,
			weekly_usage_usd,
			monthly_usage_usd,
			COALESCE((
				SELECT jsonb_object_agg(spg.group_id, spg.rate_multiplier)
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = user_subscriptions.plan_id
					AND spg.rate_multiplier IS NOT NULL
			), '{}'::jsonb)
		FROM user_subscriptions
		WHERE user_id = $1
			AND deleted_at IS NULL
			AND refund_hold_operation_id IS NULL
			AND starts_at <= NOW()
			AND expires_at > NOW()
			AND status IN ($2, $3)
			AND ($4::bigint IS NULL OR id = $4)
`
	var preferredSubscriptionID any
	if cmd.PreferredSubscriptionID != nil {
		preferredSubscriptionID = *cmd.PreferredSubscriptionID
	}
	args := []any{cmd.UserID, billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, preferredSubscriptionID}
	if cmd.GroupID != nil && *cmd.GroupID > 0 {
		query += `
			AND EXISTS (
				SELECT 1
				FROM subscription_plans sp
				WHERE sp.id = user_subscriptions.plan_id
					AND (
						NOT EXISTS (
							SELECT 1
							FROM subscription_plan_groups spg
							WHERE spg.plan_id = user_subscriptions.plan_id
						)
						OR EXISTS (
							SELECT 1
							FROM subscription_plan_groups spg
							WHERE spg.plan_id = user_subscriptions.plan_id
								AND spg.group_id = $5
						)
					)
				)
`
		args = append(args, *cmd.GroupID)
	} else if cmd.APIKeyBillingMode == billing.APIKeyBillingModeSubscription {
		// Pinned funding without a final positive group can select unrestricted
		// plans. A restricted plan has no matching group observation here, so
		// require its mapping table to be empty.
		query += `
			AND NOT EXISTS (
				SELECT 1
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = user_subscriptions.plan_id
			)
`
	}
	query += `
		ORDER BY expires_at ASC, starts_at ASC, id ASC
		FOR UPDATE
	`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, nil, err
	}
	// Read every locked snapshot and close its cursor before issuing counter
	// updates on this connection. Early read failures discard secondary close
	// errors while retaining the primary failure.
	subscriptions := make([]usageBillingSubscriptionRow, 0)
	for rows.Next() {
		var row usageBillingSubscriptionRow
		if err := rows.Scan(
			&row.ID,
			&row.PlanID,
			&row.StartsAt,
			&row.ExpiresAt,
			&row.DailyWindowStart,
			&row.WeeklyWindowStart,
			&row.MonthlyWindowStart,
			&row.DailyLimitUSD,
			&row.WeeklyLimitUSD,
			&row.MonthlyLimitUSD,
			&row.DailyUsageUSD,
			&row.WeeklyUsageUSD,
			&row.MonthlyUsageUSD,
			&row.PlanGroupRateMultipliersRaw,
		); err != nil {
			_ = rows.Close()
			return 0, 0, nil, err
		}
		subscriptions = append(subscriptions, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, nil, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, nil, err
	}

	snapshots := make([]billing.SettlementSubscription, len(subscriptions))
	for i, row := range subscriptions {
		snapshots[i] = settlementSubscriptionSnapshot(row)
	}
	plan, err := billing.AllocateSubscriptions(cmd, snapshots, time.Now())
	if err != nil {
		return 0, 0, nil, err
	}
	// Persist the planner's ordered quota replacements after all snapshots are
	// collected. The transaction owner recovers preceding rows if a later update
	// fails, preventing partial allocation publication.
	for _, row := range plan.Updates {
		if err := updateUsageBillingSubscription(ctx, tx, row.ID, row.DailyWindowStart, row.WeeklyWindowStart, row.MonthlyWindowStart, row.DailyUsageUSD, row.WeeklyUsageUSD, row.MonthlyUsageUSD); err != nil {
			return 0, 0, nil, err
		}
	}
	return plan.Remaining, plan.SubscriptionAmount, plan.Allocations, nil
}

// usageBillingUsesBaseAmount delegates selection of the unmultiplied
// allocation basis to billing. A positive BaseAmountUSD selects base pricing;
// nil or nonpositive base input uses legacy billable amounts, independently of
// the command's supplied rate values.
func usageBillingUsesBaseAmount(cmd *billing.UsageBillingCommand) bool {
	return billing.UsesBaseAmount(cmd)
}

// usageBillingNonNegativeRate delegates signed multiplier normalization to
// billing.NonNegativeRate. Negative inputs become zero, while other values
// remain available to the allocation calculation; callers validate nonfinite
// pricing values before persistent use.
func usageBillingNonNegativeRate(rate float64) float64 { return billing.NonNegativeRate(rate) }

// normalizeUsageBillingSubscriptionRow returns a row copy with billing's
// effective window anchors and counters at the supplied time. It delegates
// lifetime, outer-limit and elapsed-window decisions, retains the row's raw
// plan data, and performs no database update.
func normalizeUsageBillingSubscriptionRow(row usageBillingSubscriptionRow, now time.Time) usageBillingSubscriptionRow {
	normalized := billing.NormalizeSettlementSubscription(settlementSubscriptionSnapshot(row), now)
	row.DailyWindowStart = nullTimePtr(normalized.DailyWindowStart)
	row.WeeklyWindowStart = nullTimePtr(normalized.WeeklyWindowStart)
	row.MonthlyWindowStart = nullTimePtr(normalized.MonthlyWindowStart)
	row.DailyUsageUSD = normalized.DailyUsageUSD
	row.WeeklyUsageUSD = normalized.WeeklyUsageUSD
	row.MonthlyUsageUSD = normalized.MonthlyUsageUSD
	return row
}

// windowRemaining interprets a nullable SQL limit through billing's
// positive-limit rule and subtracts observed usage. Nil or nonpositive limits
// return nil for unlimited; finite remaining values clamp at zero for
// exhausted windows.
func windowRemaining(limit sql.NullFloat64, used float64) *float64 {
	return billing.RemainingWindowAmount(usageBillingNullableFloat64Ptr(limit), used)
}

// usageBillingSubscriptionAvailable delegates selection of the minimum
// supplied remainder to billing. Nil window values mean unlimited, and
// unlimitedAmount is returned when every window is unlimited; callers choose a
// suitable positive read sentinel or requested allocation basis.
func usageBillingSubscriptionAvailable(unlimitedAmount float64, values ...*float64) float64 {
	return billing.SubscriptionAvailableAmount(unlimitedAmount, values...)
}

// updateUsageBillingSubscription writes all three optional anchors and usage
// counters to an undeleted subscription on the caller's transaction. Nil
// anchors become SQL nulls; zero affected rows return ErrSubscriptionNotFound.
// Callers supply a locked snapshot and recover prior allocation writes after
// an error.
func updateUsageBillingSubscription(
	ctx context.Context,
	tx *sql.Tx,
	subscriptionID int64,
	dailyWindowStart *time.Time,
	weeklyWindowStart *time.Time,
	monthlyWindowStart *time.Time,
	dailyUsageUSD float64,
	weeklyUsageUSD float64,
	monthlyUsageUSD float64,
) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE user_subscriptions
		SET
			daily_window_start = $1,
			weekly_window_start = $2,
			monthly_window_start = $3,
			daily_usage_usd = $4,
			weekly_usage_usd = $5,
			monthly_usage_usd = $6,
			updated_at = NOW()
		WHERE id = $7
			AND deleted_at IS NULL
	`, nullTimePtr(dailyWindowStart), nullTimePtr(weeklyWindowStart), nullTimePtr(monthlyWindowStart), dailyUsageUSD, weeklyUsageUSD, monthlyUsageUSD, subscriptionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrSubscriptionNotFound
	}
	return nil
}

// nullTimePtr converts an optional time to a nullable SQL value. Nil becomes
// an invalid wrapper; nonnil values are copied with Valid true, including a
// supplied zero time, so SQL arguments retain the caller's presence choice.
func nullTimePtr(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

// deductUsageBillingBalance subtracts completed request cost from an undeleted
// payer and returns the new balance and actual quantized deduction. Wallet
// debt is permitted, and a missing payer returns ErrUserNotFound. The caller
// owns the transaction and locks the payer before subscription work.
//
// The CTE retains FOR NO KEY UPDATE so a later wallet write does not upgrade
// to a lock conflicting with usage-log foreign-key checks. Amounts use
// billing's decimal eight-place rounding before NUMERIC arithmetic.
// ScanSingleRow closes its result before subsequent SQL and reports cleanup
// errors alongside row failures.
func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, float64, error) {
	// Quantize before subtracting from NUMERIC(20,8) so wallet subtraction and
	// Key addition receive one eight-place amount rather than independently
	// rounding the same raw half tie.
	amount = billing.QuantizeUsageBillingAmount(amount)
	const query = `
		WITH locked_user AS (
			SELECT id, balance
			FROM users
			WHERE id = $2
				AND deleted_at IS NULL
				FOR NO KEY UPDATE
		), updated AS (
			UPDATE users
			SET balance = locked_user.balance - $1,
				updated_at = NOW()
			FROM locked_user
			WHERE users.id = locked_user.id
			RETURNING users.balance
		)
		SELECT updated.balance, $1::numeric AS deducted_amount
		FROM updated
	`

	var (
		newBalance     float64
		deductedAmount float64
	)
	if err := postgresinfra.ScanSingleRow(ctx, tx, query, []any{amount, userID}, &newBalance, &deductedAmount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, billing.ErrUserNotFound
		}
		return 0, 0, err
	}
	return newBalance, deductedAmount, nil
}

// reserveUsageBillingTaskBilling reserves eligible subscription quota, freezes
// uncovered wallet money and stores their task funding split on the caller's
// transaction. Nil or nonpositive hold-and-base input returns an empty result.
// A pinned mode rejects any remaining basis beyond its small tolerance;
// subscription writes may already have occurred, so the owner recovers that
// failure before submission.
//
// Versioned pricing retains covered base and source rates and calculates the
// capture estimate from the resulting split. The computed mixed hold replaces
// cmd.HoldAmount before allowance charging, after the action fingerprint has
// been claimed. SaveReservation persists the same split and estimate before
// the outer transaction can commit; its failure also requires recovery of
// preceding quota and wallet writes.
func reserveUsageBillingTaskBilling(ctx context.Context, tx *sql.Tx, projection TaskProjection, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	result := &billing.TaskFundsResult{}
	if cmd == nil || (cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0) {
		return result, nil
	}
	allocationCommand := &billing.UsageBillingCommand{
		APIKeyBillingMode:               cmd.APIKeyBillingMode,
		PreferredSubscriptionID:         cmd.PreferredSubscriptionID,
		UserID:                          cmd.UserID,
		GroupID:                         cmd.GroupID,
		BillableAmountUSD:               cmd.HoldAmount,
		BaseAmountUSD:                   cmd.BaseAmountUSD,
		SubscriptionRateMultiplier:      cmd.SubscriptionRateMultiplier,
		SubscriptionRateMultiplierScale: cmd.SubscriptionRateMultiplierScale,
		BalanceRateMultiplier:           cmd.BalanceRateMultiplier,
		DisablePlanGroupRateMultiplier:  cmd.DisablePlanGroupRateMultiplier,
		IncludeAllocationPricing:        cmd.PricingSnapshotVersion >= 2,
	}
	allocationCommand.Normalize()
	remainingBase, subscriptionAmount, allocations, err := allocateUsageBillingSubscriptions(ctx, tx, allocationCommand)
	if err != nil {
		return nil, err
	}
	// Subscription allocation may already have written counters. A pinned hold
	// rejects remaining basis beyond this tolerance before wallet freezing,
	// requiring transaction rollback before task submission.
	if allocationCommand.APIKeyBillingMode == billing.APIKeyBillingModeSubscription && remainingBase > 1e-10 {
		return nil, billing.ErrPreferredSubscriptionInsufficient
	}
	balanceAmount := remainingBase
	if usageBillingUsesBaseAmount(allocationCommand) {
		balanceAmount = remainingBase * usageBillingNonNegativeRate(cmd.BalanceRateMultiplier)
	}
	// Normalize the wallet portion at its eight-place storage precision before
	// freezing it, so the saved funding split uses the same amount as the guarded
	// balance update.
	balanceAmount = normalizeTaskBalanceAmount(balanceAmount)

	balanceCommand := *cmd
	balanceCommand.HoldAmount = balanceAmount
	balanceResult, err := reserveUsageBillingTaskBalance(ctx, tx, &balanceCommand)
	if err != nil {
		return nil, err
	}
	if balanceResult != nil {
		result.NewBalance = balanceResult.NewBalance
		result.FrozenBalance = balanceResult.FrozenBalance
	}
	result.SubscriptionAmountUSD = subscriptionAmount
	result.BalanceAmountUSD = balanceAmount
	result.BillingAllocations = append(result.BillingAllocations, allocations...)
	if balanceAmount > 0 {
		result.BillingAllocations = append(result.BillingAllocations, billing.BillingAllocation{
			Type:           billing.BillingAllocationTypeBalance,
			AmountUSD:      balanceAmount,
			BaseAmountUSD:  remainingBase,
			RateMultiplier: usageBillingNonNegativeRate(cmd.BalanceRateMultiplier),
		})
	}
	result.HoldAmountUSD = subscriptionAmount + balanceAmount
	result.EstimatedAmountUSD = result.HoldAmountUSD
	if cmd.PricingSnapshotVersion >= 2 && cmd.BaseAmountUSD > 0 {
		captureCommand := *cmd
		captureCommand.BalanceHoldAmount = balanceAmount
		captureCommand.SubscriptionHoldAllocations = allocations
		captureCommand.ActualBaseAmountUSD = cmd.BaseAmountUSD
		plan, planErr := billing.PlanTaskCapture(&captureCommand)
		if planErr != nil {
			return nil, planErr
		}
		result.EstimatedAmountUSD = plan.ActualAmountUSD
	}
	// Versioned base fingerprints omit the final mixed hold, allowing this
	// computed total to replace it after claim. Save the funding split before the
	// outer allowance stage uses that total.
	cmd.HoldAmount = result.HoldAmountUSD
	if err := projection.SaveReservation(ctx, balanceAmount, allocations, result.HoldAmountUSD, result.EstimatedAmountUSD); err != nil {
		return nil, err
	}
	return result, nil
}

// captureUsageBillingTaskBilling plans actual task charges from the supplied
// reservation snapshot, refunds unused subscription usage and settles the held
// wallet portion in that order. The outer wrapper supplies a nonnil command
// and an owned transaction with the payer locked. Snapshots with positive
// holds and recorded allowance or source metadata require the owner's
// reserve-key/API-Key claim; legacy frozen-only snapshots bypass that lookup.
//
// PlanTaskCapture determines retained charges and refunds before SQL changes
// begin. Its actual amount updates cmd, and the returned result combines
// retained allocations with wallet observations. A later wallet or refund
// error can follow earlier subscription writes, so the transaction owner
// recovers the whole attempt before publishing success.
func captureUsageBillingTaskBilling(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	// Recorded source or allowance metadata makes capture depend on the supplied
	// reserve-key/API-Key pair being visible. Legacy frozen-only snapshots bypass
	// this identity check, leaving the owner responsible for their held wallet
	// amount.
	if cmd != nil && cmd.HoldAmount > 0 && (cmd.AllowanceReserved || cmd.BalanceHoldAmount > 0 || len(cmd.SubscriptionHoldAllocations) > 0) {
		held, err := taskHoldClaimExists(ctx, tx, taskHoldClaimRequestID(cmd), cmd.APIKeyID)
		if err != nil {
			return nil, err
		}
		if !held {
			return nil, errors.New("batch image billing hold is missing")
		}
	}
	plan, err := billing.PlanTaskCapture(cmd)
	if err != nil {
		return nil, err
	}
	cmd.ActualAmount = plan.ActualAmountUSD
	// Refund unused subscription counters before wallet capture. Both use the
	// same transaction, so a later frozen-wallet failure requires rollback of
	// these preceding counter changes.
	if err := releaseTaskSubscriptionAllocations(ctx, tx, cmd, plan.SubscriptionReleases); err != nil {
		return nil, err
	}

	balanceCommand := *cmd
	balanceCommand.HoldAmount = plan.BalanceHoldAmount
	balanceCommand.ActualAmount = plan.BalanceAmountUSD
	balanceResult, err := captureUsageBillingTaskBalance(ctx, tx, &balanceCommand)
	if err != nil {
		return nil, err
	}
	if balanceResult == nil {
		balanceResult = &billing.TaskFundsResult{}
	}
	balanceResult.SubscriptionAmountUSD = plan.SubscriptionAmountUSD
	balanceResult.BalanceAmountUSD = plan.BalanceAmountUSD
	balanceResult.HoldAmountUSD = billing.TotalTaskHoldAmount(cmd)
	balanceResult.EstimatedAmountUSD = plan.ActualAmountUSD
	balanceResult.ActualAmountUSD = plan.ActualAmountUSD
	balanceResult.BillingAllocations = plan.BillingAllocations
	return balanceResult, nil
}

// releaseUsageBillingTaskBilling refunds a task's recorded subscription split
// and effective wallet hold when its reserve-key/API-Key claim is visible. Nil
// input and an absent live/archive claim return an empty result; the
// absent-claim path logs the skip before any financial refund.
//
// Subscription refunds run before frozen wallet release on the supplied
// transaction, using the original reservation timestamp for window
// attribution. The task owner supplies correct funding metadata and
// reservation identity because this lookup does not compare task ID, payer or
// fingerprint. A later refund error requires owner recovery of earlier writes;
// the outer allowance stage separately handles its recorded flag.
func releaseUsageBillingTaskBilling(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if cmd == nil {
		return &billing.TaskFundsResult{}, nil
	}
	held, err := taskHoldClaimExists(ctx, tx, taskHoldClaimRequestID(cmd), cmd.APIKeyID)
	if err != nil {
		return nil, err
	}
	// An absent reserve identity prevents a refund from consuming aggregate
	// frozen money attributed to other tasks. Return an empty funding result; the
	// outer allowance stage still handles its supplied flag.
	if !held {
		logger.LegacyPrintf("repository.usage_billing", "[BatchImage] release skipped, hold was never reserved: batch=%s", cmd.Task.ID)
		return &billing.TaskFundsResult{}, nil
	}
	// Return recorded subscription usage before releasing the effective wallet
	// hold. A later wallet failure leaves earlier counter refunds for the
	// surrounding transaction owner to recover.
	if err := releaseTaskSubscriptionAllocations(ctx, tx, cmd, cmd.SubscriptionHoldAllocations); err != nil {
		return nil, err
	}
	balanceAmount := billing.EffectiveTaskBalanceHoldAmount(cmd)
	return releaseUsageBillingTaskFrozenBalance(ctx, tx, cmd.UserID, balanceAmount)
}

// releaseTaskSubscriptionAllocations refunds positive subscription entries
// with present IDs for the command's payer. Nil commands skip work, as do
// other allocation types, missing IDs and nonpositive amounts. It processes
// supplied order in the caller's transaction; zero matched rows return
// ErrSubscriptionNotFound.
//
// Each current positive-limit window decreases only when its interval contains
// ReservedAt, and usage clamps at zero. The ID/payer predicate permits refunds
// after soft deletion or status changes, while a later window retains its own
// usage. A later entry failure can follow earlier refunds, so the transaction
// owner recovers the complete funding attempt.
func releaseTaskSubscriptionAllocations(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand, allocations []billing.BillingAllocation) error {
	if cmd == nil {
		return nil
	}
	// Attribute refunds to SQL intervals of twenty-four hours, seven days or
	// thirty days containing ReservedAt. Day-based timestamptz additions follow
	// the database session calendar across offset changes; later windows retain
	// their own usage when the reservation lies outside those intervals.
	for _, allocation := range allocations {
		if allocation.Type != billing.BillingAllocationTypeSubscription || allocation.SubscriptionID == nil || allocation.AmountUSD <= 0 {
			continue
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE user_subscriptions
			SET daily_usage_usd = CASE
					WHEN daily_limit_usd IS NOT NULL AND daily_limit_usd > 0
						AND daily_window_start <= $4 AND $4 < daily_window_start + INTERVAL '24 hours'
					THEN GREATEST(0, daily_usage_usd - $1) ELSE daily_usage_usd END,
				weekly_usage_usd = CASE
					WHEN weekly_limit_usd IS NOT NULL AND weekly_limit_usd > 0
						AND weekly_window_start <= $4 AND $4 < weekly_window_start + INTERVAL '7 days'
					THEN GREATEST(0, weekly_usage_usd - $1) ELSE weekly_usage_usd END,
				monthly_usage_usd = CASE
					WHEN monthly_limit_usd IS NOT NULL AND monthly_limit_usd > 0
						AND monthly_window_start <= $4 AND $4 < monthly_window_start + INTERVAL '30 days'
					THEN GREATEST(0, monthly_usage_usd - $1) ELSE monthly_usage_usd END,
				updated_at = NOW()
			WHERE id = $2 AND user_id = $3
		`, allocation.AmountUSD, *allocation.SubscriptionID, cmd.UserID, cmd.ReservedAt)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return billing.ErrSubscriptionNotFound
		}
	}
	return nil
}

// taskBillingResultForCommand reconstructs an Applied-false replay result from
// the caller's task snapshot. Nil, release and unknown-operation commands
// return an empty result. Capture uses PlanTaskCapture; a planning error is
// suppressed into that empty replay result because this path applies no new
// funds.
//
// Reserve totals positive subscription entries and the effective wallet hold,
// retaining their optional allocation pointers. Versioned base pricing may
// recompute its estimate through a capture plan; a failed estimate leaves the
// hold-based default. These figures describe supplied funding metadata and do
// not reread current balances or validate recorded allocation identities.
func taskBillingResultForCommand(cmd *billing.TaskFundsCommand, operation taskAllowanceOperation) *billing.TaskFundsResult {
	result := &billing.TaskFundsResult{Applied: false}
	if cmd == nil {
		return result
	}
	if operation == taskAllowanceCapture {
		plan, err := billing.PlanTaskCapture(cmd)
		// A replay applies no new funding writes. A failed capture reconstruction is
		// suppressed into Applied false with empty amounts, so callers do not
		// mistake it for a newly charged result.
		if err != nil {
			return result
		}
		result.SubscriptionAmountUSD = plan.SubscriptionAmountUSD
		result.BalanceAmountUSD = plan.BalanceAmountUSD
		result.HoldAmountUSD = billing.TotalTaskHoldAmount(cmd)
		result.EstimatedAmountUSD = plan.ActualAmountUSD
		result.ActualAmountUSD = plan.ActualAmountUSD
		result.BillingAllocations = plan.BillingAllocations
		return result
	}
	if operation != taskAllowanceReserve {
		return result
	}
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type == billing.BillingAllocationTypeSubscription && allocation.AmountUSD > 0 {
			result.SubscriptionAmountUSD += allocation.AmountUSD
			result.BillingAllocations = append(result.BillingAllocations, allocation)
		}
	}
	result.BalanceAmountUSD = billing.EffectiveTaskBalanceHoldAmount(cmd)
	if result.BalanceAmountUSD > 0 {
		result.BillingAllocations = append(result.BillingAllocations, billing.BillingAllocation{
			Type:      billing.BillingAllocationTypeBalance,
			AmountUSD: result.BalanceAmountUSD,
		})
	}
	result.HoldAmountUSD = result.SubscriptionAmountUSD + result.BalanceAmountUSD
	result.EstimatedAmountUSD = result.HoldAmountUSD
	if cmd.PricingSnapshotVersion >= 2 && cmd.BaseAmountUSD > 0 {
		captureCommand := *cmd
		captureCommand.ActualBaseAmountUSD = cmd.BaseAmountUSD
		// Recompute versioned estimates from the recorded split when planning
		// succeeds. A planning error retains the hold-based estimate because this
		// replay path cannot replace already stored funding metadata.
		if plan, err := billing.PlanTaskCapture(&captureCommand); err == nil {
			result.EstimatedAmountUSD = plan.ActualAmountUSD
		}
	}
	return result
}

// reserveUsageBillingTaskBalance transfers a positive task hold from an
// undeleted payer's available wallet to frozen balance and returns both
// resulting values. Nonpositive holds return an empty result. The caller
// supplies a nonnil command and a previously normalized hold, locks the payer,
// and owns transaction completion.
//
// The guarded update requires available balance to cover the full hold. When
// it matches no row, a second query distinguishes an absent undeleted payer
// from insufficient funds; other SQL errors propagate. This helper can run
// after subscription updates, so the owner rolls back the surrounding
// reservation on either failure.
func reserveUsageBillingTaskBalance(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if cmd.HoldAmount <= 0 {
		return &billing.TaskFundsResult{}, nil
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			frozen_balance = COALESCE(frozen_balance, 0) + $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &billing.TaskFundsResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// A guarded wallet update cannot distinguish a missing undeleted payer from
	// insufficient funds. Keep database lookup failures visible before selecting
	// the corresponding funding error.
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, billing.ErrUserNotFound
	}
	return nil, billing.ErrTaskInsufficientBalance
}

// captureUsageBillingTaskBalance converts a recorded wallet hold into actual
// cost, returning the available and frozen values after releasing any unused
// difference. The caller supplies a nonnil command and owns the transaction.
// Hold and actual amounts use scaled-binary eight-place rounding; wholly
// nonpositive inputs return an empty result.
//
// Without TaskOverageChargeBalance, a rounded actual-minus-hold difference
// greater than 1e-8 fails before SQL. An authorized excess debits available
// balance even when it becomes negative. The update removes only the original
// hold from frozen funds, preserving other tasks' reservations. A missing update
// is classified by a payer reread as missing user or insufficient frozen
// balance, leaving the owner to recover preceding funding writes.
func captureUsageBillingTaskBalance(ctx context.Context, tx *sql.Tx, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	// Round both task wallet operands before the excess check and frozen-balance
	// adjustment. The comparison therefore uses the same eight-place amounts sent
	// to wallet SQL.
	holdAmount := normalizeTaskBalanceAmount(cmd.HoldAmount)
	actualAmount := normalizeTaskBalanceAmount(cmd.ActualAmount)
	if holdAmount <= 0 && actualAmount <= 0 {
		return &billing.TaskFundsResult{}, nil
	}
	// Bounded tasks retain their excess guard. Accepted overages use this same
	// atomic update to release the original hold and debit only its missing funds.
	if cmd.OveragePolicy != billing.TaskOverageChargeBalance && actualAmount-holdAmount > 0.00000001 {
		return nil, billing.ErrTaskSettlementCostExceedsHold
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance
				+ CASE WHEN $1 > $2 THEN $1 - $2 ELSE 0 END
				- CASE WHEN $2 > $1 THEN $2 - $1 ELSE 0 END,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, holdAmount, actualAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &billing.TaskFundsResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// A guarded wallet update cannot distinguish a missing undeleted payer from
	// insufficient funds. Keep database lookup failures visible before selecting
	// the corresponding funding error.
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, billing.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

// releaseUsageBillingTaskFrozenBalance returns a rounded positive held amount
// from an undeleted payer's frozen wallet to available balance. Nonpositive
// amounts return an empty result. The caller establishes the reservation's
// identity before this helper and owns transaction completion.
//
// The update requires frozen balance to cover the full refund, returning both
// post-update values. A failed row match triggers an undeleted-payer check to
// distinguish missing user from insufficient frozen funds. This helper does
// not identify an individual task within aggregate frozen balance, so callers
// supply the previously recorded effective hold and recover earlier
// subscription refunds on failure.
func releaseUsageBillingTaskFrozenBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (*billing.TaskFundsResult, error) {
	// Round the task refund to eight fractional dollars before its frozen-fund
	// guard; saved wallet amounts use that precision.
	amount = normalizeTaskBalanceAmount(amount)
	if amount <= 0 {
		return &billing.TaskFundsResult{}, nil
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, amount, userID).Scan(&balance, &frozen)
	if err == nil {
		return &billing.TaskFundsResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// A guarded wallet update cannot distinguish a missing undeleted payer from
	// insufficient funds. Keep database lookup failures visible before selecting
	// the corresponding funding error.
	if exists, existsErr := userExistsForBilling(ctx, tx, userID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, billing.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

// normalizeTaskBalanceAmount rounds a task wallet amount to eight decimal
// places after binary floating-point scaling. Capture and release use it
// before frozen-balance comparisons; ordinary request amounts instead use
// billing's decimal quantizer, and callers validate finiteness separately.
func normalizeTaskBalanceAmount(amount float64) float64 {
	// Eight fractional dollar places match users.balance and users.frozen_balance
	// storage used by guarded task wallet comparisons.
	return math.Round(amount*1e8) / 1e8
}

// taskHoldClaimExists reports whether the original reserve-key/API-Key pair is
// visible in the live or archive deduplication table on this transaction. It
// reads live first and tries archive only after no rows. The task owner
// supplies the matching reservation identity and funding metadata; this lookup
// does not compare fingerprint, payer or task ID.
func taskHoldClaimExists(ctx context.Context, tx *sql.Tx, holdRequestID string, apiKeyID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// Archived action identities remain relevant after live deduplication
	// retention. Try archive only after a successful live lookup reports no
	// matching pair, keeping other SQL failures visible.
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// userExistsForBilling checks whether an undeleted payer is visible in the
// supplied transaction. No rows returns false without error; SQL failures
// propagate so guarded wallet writes do not misclassify a failed read as
// insufficient funds.
func userExistsForBilling(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// incrementUsageBillingAPIKeyQuota adds decimal-quantized completed usage to
// the original Key ID, including soft-deleted rows. A missing Key returns
// ErrAPIKeyNotFound. Its returned boolean reports a positive total quota
// crossing from below, even when the row's status or deletion marker prevents
// a status transition.
//
// The stored status changes to quota_exhausted only for an active undeleted
// Key crossing that limit. Other statuses, credentials and deletion markers
// remain as stored while usage is counted. The caller uses the same
// transaction as wallet and subscription writes, so a failure here requires
// recovery of those preceding effects.
func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
	// Charge the admitted request's original Key after soft deletion too. Use the
	// wallet's decimal eight-place amount before quota addition so the two sides
	// cannot round a raw half tie differently.
	amount = billing.QuantizeUsageBillingAmount(amount)
	var exhausted bool
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + $1,
			status = CASE
				WHEN deleted_at IS NULL AND quota > 0
					AND status = $3
					AND quota_used < quota
					AND quota_used + $1 >= quota
				THEN $4
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $2
		RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
	`, amount, apiKeyID, "active", "quota_exhausted").Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, billing.ErrAPIKeyNotFound
	}
	if err != nil {
		return false, err
	}
	return exhausted, nil
}

// incrementUsageBillingAPIKeyRateLimit adds decimal-quantized completed usage
// to the original Key's rolling counters, including soft-deleted records. It
// uses the shared SQL window rules and reports ErrAPIKeyNotFound for zero
// affected rows; the caller owns the charging transaction and its recovery.
func incrementUsageBillingAPIKeyRateLimit(ctx context.Context, tx *sql.Tx, apiKeyID int64, cost float64) error {
	// Use the same decimal eight-place amount as wallet and total-quota
	// arithmetic, preventing opposite rounding directions from splitting the
	// recorded financial effect across counters.
	cost = billing.QuantizeUsageBillingAmount(cost)
	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
`+apiKeyRateLimitUsageSetSQL+`
		WHERE id = $2
	`, cost, apiKeyID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrAPIKeyNotFound
	}
	return nil
}

// incrementUsageBillingProviderQuota increments an undeleted provider's total
// usage and configured positive daily/weekly/monthly counters using their stored
// expiration rules. It returns the post-update quota state or
// ErrProviderNotFound for no row. The caller supplies the normalized provider
// cost and owns the transaction containing this update.
//
// After the result cursor closes, each positive limit crossing compares
// post-usage with post-minus-amount. A reset starts that comparison from zero.
// Any crossing requires a same-transaction outbox callback so scheduling can
// observe exhaustion; absent callbacks or enqueue failures return errors and
// the owner recovers the funding attempt. Later scheduler publication and
// notification delivery have their own lifetimes.
func incrementUsageBillingProviderQuota(ctx context.Context, tx *sql.Tx, providerID int64, amount float64, outbox ProviderQuotaOutbox) (*billing.ProviderQuotaState, error) {
	rows, err := tx.QueryContext(ctx,
		providerQuotaIncrementSQL+`
		RETURNING
			COALESCE((extra->>'quota_used')::numeric, 0),
			COALESCE((extra->>'quota_limit')::numeric, 0),
			COALESCE((extra->>'quota_daily_used')::numeric, 0),
			COALESCE((extra->>'quota_daily_limit')::numeric, 0),
			COALESCE((extra->>'quota_weekly_used')::numeric, 0),
			COALESCE((extra->>'quota_weekly_limit')::numeric, 0),
			COALESCE((extra->>'quota_monthly_used')::numeric, 0),
			COALESCE((extra->>'quota_monthly_limit')::numeric, 0)`,
		amount, providerID)
	if err != nil {
		return nil, err
	}

	// Collect the returned quota state and close the cursor before the outbox
	// issues more SQL. Failed reads discard secondary close errors and return
	// their selected primary failure.
	var state billing.ProviderQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
			&state.MonthlyUsed, &state.MonthlyLimit,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		return nil, billing.ErrProviderNotFound
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// Close rows before issuing more SQL on this connection. The pq driver cannot
	// start another query while this result set remains unread and may report
	// "unexpected Parse response".
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Enqueue a same-transaction scheduling event when a positive limit moves
	// from below to at least its threshold. For a periodic reset,
	// post-usage equals this amount, so the prior comparison starts at zero.

	crossedTotal := state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit
	crossedDaily := state.DailyLimit > 0 && state.DailyUsed >= state.DailyLimit && (state.DailyUsed-amount) < state.DailyLimit
	crossedWeekly := state.WeeklyLimit > 0 && state.WeeklyUsed >= state.WeeklyLimit && (state.WeeklyUsed-amount) < state.WeeklyLimit
	crossedMonthly := state.MonthlyLimit > 0 && state.MonthlyUsed >= state.MonthlyLimit && (state.MonthlyUsed-amount) < state.MonthlyLimit
	if crossedTotal || crossedDaily || crossedWeekly || crossedMonthly {
		if outbox == nil {
			return nil, errors.New("provider quota outbox is not configured")
		}
		if err := outbox(ctx, tx, providerID); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: provider=%d err=%v", providerID, err)
			return nil, err
		}
	}
	return &state, nil
}

// settlementSubscriptionSnapshot converts one selected SQL row into billing's
// allocation snapshot. Nullable times and limits become fresh optional values
// and JSON rates become a new map; plan coverage filtering and eligibility
// remain the storage caller's responsibility before allocation.
func settlementSubscriptionSnapshot(row usageBillingSubscriptionRow) billing.SettlementSubscription {
	return billing.SettlementSubscription{ID: row.ID, PlanID: row.PlanID, StartsAt: row.StartsAt, ExpiresAt: row.ExpiresAt, DailyWindowStart: usageBillingNullableTimePtr(row.DailyWindowStart), WeeklyWindowStart: usageBillingNullableTimePtr(row.WeeklyWindowStart), MonthlyWindowStart: usageBillingNullableTimePtr(row.MonthlyWindowStart), DailyLimitUSD: usageBillingNullableFloat64Ptr(row.DailyLimitUSD), WeeklyLimitUSD: usageBillingNullableFloat64Ptr(row.WeeklyLimitUSD), MonthlyLimitUSD: usageBillingNullableFloat64Ptr(row.MonthlyLimitUSD), DailyUsageUSD: row.DailyUsageUSD, WeeklyUsageUSD: row.WeeklyUsageUSD, MonthlyUsageUSD: row.MonthlyUsageUSD, PlanGroupRateMultipliers: parseInt64Float64JSONMap(row.PlanGroupRateMultipliersRaw)}
}
