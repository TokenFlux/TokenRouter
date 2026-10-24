package billing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// subscription_service.go issues and maintains subscription timelines, quota
// windows and user-facing progress.
// A user's grants for the same plan form a timeline: issuance can queue after
// the latest future expiry, while revoke or validity changes shift later
// slots. Persistent operations coordinate through recipient-first locks and
// selected transactions; joined owners retain rollback and publication
// decisions. Positive payment order identities replay an existing grant before
// term validation, whereas ordinary issuance creates a new row.
//
// Quota prechecks and presentation can clear local stale counters, but
// conditional stored resets can succeed without applying this request's write.
// Admission therefore rereads stored maintenance results. Injected service
// time, system-time status/day/countdown helpers and calendar-specific anchors
// are separate observations, so display and maintenance do not share one
// frozen clock across every phase.

// MaxValidityDays bounds assigned positive validity and signed extension
// magnitude; nonpositive issuance counts use their separate thirty-day default
// before this cap applies.
const MaxValidityDays = 36500

// Grant errors distinguish missing inputs, lifetime/status and quota failures
// for mutation and admission callers. MaxExpiresAt separately supplies the
// shared UTC deadline horizon for constructed and shifted grants.
var (
	ErrSubscriptionAlreadyExists = apperror.Conflict("SUBSCRIPTION_ALREADY_EXISTS", "subscription already exists")
	ErrSubscriptionNilInput      = apperror.BadRequest("SUBSCRIPTION_NIL_INPUT", "subscription input cannot be nil")
	ErrSubscriptionNotFound      = apperror.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found")
	// MaxExpiresAt caps expiries after calendar-day addition and chain shifts. It
	// leaves starts unchanged, so callers still apply each mutation's validity
	// checks.
	MaxExpiresAt = time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC)

	ErrSubscriptionExpired         = apperror.Forbidden("SUBSCRIPTION_EXPIRED", "subscription has expired")
	ErrSubscriptionSuspended       = apperror.Forbidden("SUBSCRIPTION_SUSPENDED", "subscription is suspended")
	ErrSubscriptionNotRevoked      = apperror.Conflict("SUBSCRIPTION_NOT_REVOKED", "subscription is not revoked")
	ErrSubscriptionRestoreConflict = apperror.Conflict("SUBSCRIPTION_RESTORE_CONFLICT", "subscription already exists for this user and plan")
	ErrSubscriptionNotActive       = apperror.Conflict("SUBSCRIPTION_NOT_ACTIVE", "subscription is not active")
	ErrSubscriptionQuotaAvailable  = apperror.Conflict("SUBSCRIPTION_QUOTA_NOT_EXHAUSTED", "subscription still has available quota")
	ErrInvalidInput                = apperror.BadRequest("INVALID_INPUT", "at least one of resetDaily, resetWeekly, or resetMonthly must be true")
	ErrDailyLimitExceeded          = apperror.TooManyRequests("DAILY_LIMIT_EXCEEDED", "daily usage limit exceeded")
	ErrWeeklyLimitExceeded         = apperror.TooManyRequests("WEEKLY_LIMIT_EXCEEDED", "weekly usage limit exceeded")
	ErrMonthlyLimitExceeded        = apperror.TooManyRequests("MONTHLY_LIMIT_EXCEEDED", "monthly usage limit exceeded")
	ErrAdjustWouldExpire           = apperror.BadRequest("ADJUST_WOULD_EXPIRE", "adjustment would result in invalid subscription window")

	ErrSubscriptionInvalid = apperror.Forbidden("SUBSCRIPTION_INVALID", "subscription is invalid or expired")
)

// SubscriptionGroupReader resolves optional group display names for grants
// with plan coverage identities.
type SubscriptionGroupReader interface {
	// GetByIDLite resolves lightweight group display data for a supplied
	// identity. Enrichment keeps an ID-only placeholder for a nil row and for a
	// failure other than a missing-resource error, and drops the identity when the
	// error reports that no such group exists. The reader owns its persistence and
	// returned-value policy.
	GetByIDLite(context.Context, int64) (*SubscriptionPlanGroup, error)
}

// SubscriptionTransactions supplies selected transaction work, user-first
// grant locks and preferred-Key rebinding. The PostgreSQL adapter can reuse
// caller transactions or run nonpersistent fixtures directly; outer
// transaction owners retain commit, rollback and publication decisions.
type SubscriptionTransactions interface {
	// HasPersistence reports available client or selected transaction bindings
	// for this context. The PostgreSQL adapter checks binding presence rather
	// than database health, letting services choose persistent coordination or
	// direct fixture work.
	HasPersistence(context.Context) bool
	// Within runs a nonnil callback on the selected transaction binding or owned
	// work. PostgreSQL prefers a bound transaction, then the context transaction,
	// otherwise its base client; joined owners retain commit/rollback and receive
	// no inner savepoint.
	//
	// Owned work begins before callback execution, commits after success and
	// attempts rollback after failure. Absent persistence executes directly, so
	// services cannot infer rollback capability from callback execution alone.
	// Callers publish cache/notification effects after their selected transaction
	// commits.
	Within(context.Context, func(context.Context) error) error
	// GetPlan reads the supplied plan identity using the selected transaction
	// client or base client. Success supplies a usable nonnil plan. PostgreSQL
	// performs no sale-state filtering; missing bindings and lookup errors
	// propagate to template selection.
	GetPlan(context.Context, int64) (*SubscriptionPlan, error)
	// LockGrantUser locks the recipient user within selected transaction context
	// before grant reads/writes. PostgreSQL returns success without a selected
	// transaction; services call it inside Within when persistent serialization
	// is required.
	LockGrantUser(context.Context, int64) error
	// LockSubscription discovers the target recipient and locks user before
	// subscription in selected transaction context. PostgreSQL supplies no lock
	// when transaction selection is absent; callbacks reread the complete target
	// after successful locking.
	LockSubscription(context.Context, int64) error
	// LockSelfRevoke locks the supplied user then target subscription within
	// selected transaction context. The caller establishes their ownership
	// relationship and rereads eligibility before revocation; PostgreSQL maps
	// subscription-lock query failures to not-found.
	LockSelfRevoke(context.Context, int64, int64) error
	// RebindKeys changes nondeleted subscription-mode Keys pointing to the old
	// grant and returns affected count. PostgreSQL can return zero for
	// nonpositive identities or absent base client; the outer caller owns commit
	// and cache publication.
	RebindKeys(context.Context, int64, int64) (int, error)
}

// SubscriptionService issues grant timelines and coordinates quota
// maintenance, revocation and display reads. Repository and mutation
// dependencies remain caller-owned; construction starts no worker, and the
// expiry service owns periodic status/reminder scanning.
type SubscriptionService struct {
	// groupRepo may be nil for ID-only enrichment. Lookup failures and nil
	// results likewise retain group identity while leaving its displayed name
	// empty.
	groupRepo SubscriptionGroupReader // optional plan group display reader
	// userSubRepo is retained as supplied with no constructor replacement.
	// Repository-based methods require it, while pure progress calculation can
	// use a service without that dependency.
	userSubRepo UserSubscriptionRepository // issued grant persistence repository
	// transactions supplies binding checks, locks and mutation contexts. Grant
	// mutation paths require a usable adapter; constructor omission is suitable
	// only for methods that do not consult it.
	transactions SubscriptionTransactions // selected grant mutation adapter
	// clock uses the first optional runtime value, retaining its callbacks and
	// calendar pointer. Several display/reset helpers still observe system time
	// independently of its injected Now.
	clock DateRuntime // retained observation and calendar inputs
}

// SelfRevokeSubscriptionResult identifies the revoked grant, selected
// successor and observed Key rebinding count.
type SelfRevokeSubscriptionResult struct {
	RevokedSubscriptionID int64 // selected revoked grant identity
	// ReplacementSubscriptionID is present when a pending successor was selected
	// for Key rebinding. Joined transaction callers still control whether these
	// callback writes commit.
	ReplacementSubscriptionID *int64 // optional selected successor identity
	ReboundAPIKeyCount        int    // observed preferred-Key rebind count
}

// AssignSubscriptionInput supplies grant recipient, source plan and optional
// issued-term overrides. Payment fulfillment can supply its saved template,
// while a positive source-order identity can return an existing grant before
// template validation.
type AssignSubscriptionInput struct {
	UserID int64 // target recipient user identity
	PlanID int64 // selected subscription plan identity
	// ValidityDays overrides live-plan duration only when positive.
	// Provided-template and nonpersistent paths normalize nonpositive values to
	// thirty before applying MaxValidityDays.
	ValidityDays int // requested issued day count
	// DailyLimitUSD, WeeklyLimitUSD and MonthlyLimitUSD are retained pointers.
	// Nil inherits the live plan when that path is selected, but remains
	// unlimited under UseProvidedTemplate or nonpersistent fixture issuance.
	DailyLimitUSD   *float64 // optional daily quota USD
	WeeklyLimitUSD  *float64 // optional weekly quota USD
	MonthlyLimitUSD *float64 // optional monthly quota USD
	// UseProvidedTemplate retains the supplied quota pointers and normalized days
	// without reading current plan terms. Payment fulfillment sets it when an
	// order carries saved entitlement terms.
	UseProvidedTemplate bool // supplied plan snapshot selection
	// SourceOrderID participates in replay only when positive. A first matching
	// repository row returns before recipient/plan comparison or template
	// validation, and persistent issuance rechecks after recipient locking.
	SourceOrderID *int64 // optional issuance replay identity
	// AssignedBy is stored as a pointer only when positive. The created record
	// initially points into this input field, which callers keep stable while a
	// repository retains that pointer. Zero or negative input omits the identity;
	// a persistence reread can return separately loaded pointer storage.
	AssignedBy int64  // optional assigning administrator identity
	Notes      string // issued grant audit notes
}

// GrantPlanTemplate contains normalized validity days and selected quota
// pointers for a new issued grant.
type GrantPlanTemplate struct {
	ValidityDays int // normalized issued day count
	// DailyLimitUSD, WeeklyLimitUSD and MonthlyLimitUSD retain selected
	// input/plan pointers. Issuance copies the same pointers into the grant, so
	// callers keep those source values unchanged during persistence.
	DailyLimitUSD   *float64 // optional daily quota USD
	WeeklyLimitUSD  *float64 // optional weekly quota USD
	MonthlyLimitUSD *float64 // optional monthly quota USD
}

// BulkAssignSubscriptionInput supplies an ordered recipient list and shared
// plan-term overrides for separate grant attempts.
type BulkAssignSubscriptionInput struct {
	// UserIDs is consumed in supplied order without deduplication. Repeated
	// identities can create multiple queued grants, while the result status map
	// retains their latest attempt.
	UserIDs []int64 // ordered recipient attempt identities
	PlanID  int64   // selected subscription plan identity
	// ValidityDays passes to each ordinary assignment, where positive input
	// overrides live-plan duration. Nonpersistent terms normalize nonpositive
	// input to the default issued duration.
	ValidityDays int // requested issued day override
	// DailyLimitUSD, WeeklyLimitUSD and MonthlyLimitUSD pass into each ordinary
	// assignment. Nonnil pointers override live-plan quotas; nil inherits under
	// persistence and remains unlimited for direct nonpersistent terms.
	DailyLimitUSD   *float64 // optional daily quota USD
	WeeklyLimitUSD  *float64 // optional weekly quota USD
	MonthlyLimitUSD *float64 // optional monthly quota USD
	// AssignedBy passes to each recipient assignment. Positive values become an
	// optional assigning-user pointer, while nonpositive values leave the new
	// grant without an assigner.
	AssignedBy int64  // assigning administrator identity
	Notes      string // shared issued grant notes
}

// BulkAssignResult collects per-attempt counters and records alongside the
// latest status for each recipient identity.
type BulkAssignResult struct {
	// SuccessCount and CreatedCount both increment for each successful attempt,
	// FailedCount counts failures, and ReusedCount remains zero. These counters
	// count attempts rather than unique recipient IDs.
	SuccessCount int // successful grant attempt count
	CreatedCount int // successful grant attempt count
	ReusedCount  int // unused reuse count slot
	FailedCount  int // failed grant attempt count
	// Subscriptions appends each successful returned grant by value in attempt
	// order. Optional pointers and nested relations remain shared with that
	// returned record.
	Subscriptions []UserSubscription // successful shallow grant values
	Errors        []string           // failed attempt detail strings
	// Statuses overwrites entries for repeated UserIDs with each attempt's
	// active, queued or failed result. Counts and returned grant slices still
	// retain every attempt.
	Statuses map[int64]string // latest status by recipient
}

// UserSubscriptionGroupFilter supplies repository-backed plan coverage
// filtering required by positive-group usable-grant selection.
type UserSubscriptionGroupFilter interface {
	// FilterByGroup selects supplied grants admitted by current plan-group
	// mappings for a positive group. The repository owns returned-value sharing;
	// service selection requires this capability instead of trusting previously
	// hydrated plan metadata.
	FilterByGroup(ctx context.Context, subs []UserSubscription, groupID int64) ([]UserSubscription, error)
}

// SubscriptionProgress contains grant lifetime/status display and optional
// stored quota-window detail for presentation.
type SubscriptionProgress struct {
	ID            int64     `json:"id"`              // displayed issued grant identity
	PlanID        int64     `json:"plan_id"`         // displayed source plan identity
	PlanName      string    `json:"plan_name"`       // supplied or localized plan text
	StartsAt      time.Time `json:"starts_at"`       // displayed inclusive grant start
	ExpiresAt     time.Time `json:"expires_at"`      // displayed exclusive grant deadline
	Status        string    `json:"status"`          // effective status at service time
	ExpiresInDays int       `json:"expires_in_days"` // system-time rounded lifetime days
	// Daily, Weekly and Monthly are omitted when their limit is
	// absent/nonpositive or their anchor is missing. CalculateProgress uses
	// separate reset/countdown observations for these supplied window snapshots.
	Daily   *UsageWindowProgress `json:"daily,omitempty"`   // optional daily quota detail
	Weekly  *UsageWindowProgress `json:"weekly,omitempty"`  // optional weekly quota detail
	Monthly *UsageWindowProgress `json:"monthly,omitempty"` // optional monthly quota detail
}

// UsageWindowProgress contains one configured quota window's displayed amounts
// and reset countdown observations.
type UsageWindowProgress struct {
	LimitUSD float64 `json:"limit_usd"` // USD
	UsedUSD  float64 `json:"used_usd"`  // USD
	// RemainingUSD is limit minus supplied usage, clamped only below zero.
	// Negative usage can therefore make the displayed remaining amount exceed the
	// limit.
	RemainingUSD float64 `json:"remaining_usd"` // USD
	// Percentage divides usage by a limit comparing positive and caps above one
	// hundred. Negative usage can retain negative percentages; an unordered limit
	// leaves the initialized zero percentage.
	Percentage  float64   `json:"percentage"`   // used share in percent
	WindowStart time.Time `json:"window_start"` // supplied quota window anchor
	// ResetsAt prefers a supplied reset deadline over anchor plus duration. A
	// past deadline stays displayed while its countdown is clamped to zero.
	ResetsAt time.Time `json:"resets_at"` // selected reset or expiry time
	// ResetsInSeconds uses time.Until independently of service clock, truncates
	// fractional seconds, then clamps below zero. It does not change the selected
	// ResetsAt timestamp.
	ResetsInSeconds int64 `json:"resets_in_seconds"` // system-time nonnegative countdown seconds
}

// NewSubscriptionService retains the supplied repositories and mutation
// adapter, using only the first optional DateRuntime. Omitted clock values use
// system time/local calendar through runtime helpers; methods require the
// dependencies relevant to their path.
func NewSubscriptionService(groups SubscriptionGroupReader, repo UserSubscriptionRepository, transactions SubscriptionTransactions, clocks ...DateRuntime) *SubscriptionService {
	clock := DateRuntime{}
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	return &SubscriptionService{groupRepo: groups, userSubRepo: repo, transactions: transactions, clock: clock}
}

// EnrichSubscriptionPlanGroups updates loaded plan display data in the
// supplied grant slice, tolerating a nil service. It recomputes
// GroupsRestricted from GroupIDs and keeps already populated ApplicableGroups.
// Empty group IDs also skip lookup, leaving any existing displayed entries
// retained.
//
// A group that storage reports as missing leaves the display list, because a plan
// can outlive a group it once named and the subscriber has no remaining access
// through that identity. A nil row, an absent reader, or any other read failure
// keeps the identity with an empty name instead: those outcomes leave the group's
// existence undetermined, and dropping it would hide access that still works.
// Loaded Plan pointers are mutated directly and can be shared across grants.
// Callers keep those plan objects stable during enrichment and separately decide
// whether an existing display list still matches changed group IDs.
func (s *SubscriptionService) EnrichSubscriptionPlanGroups(ctx context.Context, subscriptions []UserSubscription) {
	if s == nil {
		return
	}
	for i := range subscriptions {
		plan := subscriptions[i].Plan
		if plan == nil {
			continue
		}
		plan.GroupsRestricted = len(plan.GroupIDs) > 0
		if len(plan.GroupIDs) == 0 || len(plan.ApplicableGroups) > 0 {
			continue
		}
		// Resolve names while distinguishing a deleted group from an unreadable one, so
		// only the confirmed absence removes an entry from the display.
		plan.ApplicableGroups = make([]SubscriptionPlanGroup, 0, len(plan.GroupIDs))
		for _, groupID := range plan.GroupIDs {
			group := SubscriptionPlanGroup{ID: groupID}
			if s.groupRepo != nil {
				resolved, err := s.groupRepo.GetByIDLite(ctx, groupID)
				if apperror.IsNotFound(err) {
					continue
				}
				if err == nil && resolved != nil {
					group.Name = resolved.Name
				}
			}
			plan.ApplicableGroups = append(plan.ApplicableGroups, group)
		}
	}
}

// Stop is an inert lifecycle hook; periodic subscription workers belong to
// SubscriptionExpiryService.
func (s *SubscriptionService) Stop() {}

// resolveGrantPlanTemplate selects issued validity and quotas from supplied
// terms or the current plan. Nil input or nonpositive PlanID fails.
// UseProvidedTemplate, or absent persistence, normalizes input days and
// retains input quota pointers directly before quota validation.
//
// Persistent live-plan selection converts its configured validity unit, then
// applies positive day and nonnil quota overrides from input. Nil quota
// overrides retain the plan value; provided-template nil values remain
// unlimited. Negative quota validation propagates without a separate
// finite-value check. The caller resolves this template before its owned
// recipient-lock transaction, so locking that user does not freeze a
// concurrently changing plan template.
func (s *SubscriptionService) resolveGrantPlanTemplate(ctx context.Context, input *AssignSubscriptionInput) (*GrantPlanTemplate, error) {
	if input == nil || input.PlanID <= 0 {
		return nil, fmt.Errorf("assign subscription: invalid plan_id")
	}

	template := &GrantPlanTemplate{}
	// Resolve supplied snapshots before probing persistence. Both supplied and
	// fixture terms validate here before issuance chooses its transaction, so an
	// invalid quota cannot reach grant writes.
	if input.UseProvidedTemplate || !s.transactions.HasPersistence(ctx) {
		template.ValidityDays = NormalizeAssignValidityDays(input.ValidityDays)
		template.DailyLimitUSD = input.DailyLimitUSD
		template.WeeklyLimitUSD = input.WeeklyLimitUSD
		template.MonthlyLimitUSD = input.MonthlyLimitUSD
		if err := ValidatePlanQuotas(template.DailyLimitUSD, template.WeeklyLimitUSD, template.MonthlyLimitUSD); err != nil {
			return nil, err
		}
		return template, nil
	}

	plan, err := s.transactions.GetPlan(ctx, input.PlanID)
	if err != nil {
		return nil, fmt.Errorf("assign subscription: get plan %d: %w", input.PlanID, err)
	}

	template.ValidityDays = NormalizeAssignValidityDays(ComputeValidityDays(plan.ValidityDays, plan.ValidityUnit))
	template.DailyLimitUSD = plan.DailyLimitUSD
	template.WeeklyLimitUSD = plan.WeeklyLimitUSD
	template.MonthlyLimitUSD = plan.MonthlyLimitUSD
	if input.ValidityDays > 0 {
		template.ValidityDays = NormalizeAssignValidityDays(input.ValidityDays)
	}
	if input.DailyLimitUSD != nil {
		template.DailyLimitUSD = input.DailyLimitUSD
	}
	if input.WeeklyLimitUSD != nil {
		template.WeeklyLimitUSD = input.WeeklyLimitUSD
	}
	if input.MonthlyLimitUSD != nil {
		template.MonthlyLimitUSD = input.MonthlyLimitUSD
	}
	if err := ValidatePlanQuotas(template.DailyLimitUSD, template.WeeklyLimitUSD, template.MonthlyLimitUSD); err != nil {
		return nil, err
	}
	return template, nil
}

// AssignSubscription delegates grant issuance to AssignOrExtendSubscription
// and discards its pending flag. Source-order replay, term selection,
// transaction ownership and returned persistence errors follow that entry
// point's grant rules.
func (s *SubscriptionService) AssignSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, error) {
	sub, _, err := s.AssignOrExtendSubscription(ctx, input)
	return sub, err
}

// AssignOrExtendSubscription returns an existing positive source-order match
// or creates a new user-plan grant, with a queued/pending flag. Nil input
// fails. A matching order returns before template validation or recipient/plan
// comparison, and its flag uses IsPending after current status normalization.
//
// Otherwise terms are resolved before transaction entry. Persistent issuance
// joins or creates selected transaction work, locks the recipient user, and
// repeats the source-order lookup before finding the latest plan grant.
// Without persistence it runs directly with fixture dependencies. A new grant
// starts at a future latest expiry or current service time; callbacks and
// joined transaction completion follow the mutation adapter's ownership rules.
func (s *SubscriptionService) AssignOrExtendSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error) {
	if input == nil {
		return nil, false, ErrSubscriptionNilInput
	}
	if existing, found, err := s.findSourceOrderSubscription(ctx, input.SourceOrderID); err != nil {
		return nil, false, err
	} else if found {
		return existing, existing.IsPending(), nil
	}

	// Resolve terms before entering any owned user-lock transaction. The selected
	// template is a captured input to issuance, so later user locking does not
	// refresh plan changes.
	template, err := s.resolveGrantPlanTemplate(ctx, input)
	if err != nil {
		return nil, false, err
	}

	if !s.transactions.HasPersistence(ctx) {
		return s.assignOrExtendSubscriptionUnlocked(ctx, input, template)
	}
	var result *UserSubscription
	var queued bool
	err = s.transactions.Within(ctx, func(txCtx context.Context) error {
		var e error
		result, queued, e = s.assignOrExtendSubscriptionInTx(txCtx, input, template)
		return e
	})
	return result, queued, err
}

// findSourceOrderSubscription returns the first visible repository match for a
// positive source-order pointer. Nil/nonpositive values report absent; read
// errors propagate, and found rows are normalized with system time before
// returning without recipient or plan filtering.
func (s *SubscriptionService) findSourceOrderSubscription(ctx context.Context, sourceOrderID *int64) (*UserSubscription, bool, error) {
	if sourceOrderID == nil || *sourceOrderID <= 0 {
		return nil, false, nil
	}
	subs, err := s.userSubRepo.ListBySourceOrderID(ctx, *sourceOrderID)
	if err != nil {
		return nil, false, err
	}
	if len(subs) == 0 {
		return nil, false, nil
	}
	NormalizeSubscriptionStatus(subs)
	return &subs[0], true, nil
}

// assignOrExtendSubscriptionInTx locks the input recipient before rechecking
// source-order replay and creating a grant. Its caller supplies selected
// transaction context and a previously resolved template. A found order
// returns that existing record and its system-time pending flag.
//
// The second lookup observes issuance that may have completed while this call
// waited for the user lock. A missing order delegates latest-plan timeline
// selection and persistence to the unlocked helper on the same context. Error
// recovery and lock release remain with transaction ownership; no plan-row
// lock is acquired for the template already selected.
func (s *SubscriptionService) assignOrExtendSubscriptionInTx(ctx context.Context, input *AssignSubscriptionInput, template *GrantPlanTemplate) (*UserSubscription, bool, error) {
	// Lock the recipient before the second order lookup and latest-plan read.
	// This serializes same-user issuance while preserving the owner's selected
	// transaction lifetime.
	if err := s.transactions.LockGrantUser(ctx, input.UserID); err != nil {
		return nil, false, err
	}
	if existing, found, err := s.findSourceOrderSubscription(ctx, input.SourceOrderID); err != nil {
		return nil, false, err
	} else if found {
		return existing, existing.IsPending(), nil
	}
	return s.assignOrExtendSubscriptionUnlocked(ctx, input, template)
}

// assignOrExtendSubscriptionUnlocked constructs a new grant from a nonnil
// input and selected template using service time. Any latest user-plan read
// error is treated as no prior grant. A future latest expiry selects the new
// start and queued flag regardless of that row's stored status; otherwise the
// grant starts now.
//
// Calendar-day addition uses the chosen start's zone, then caps expiry at
// MaxExpiresAt without moving start. Quota and source-order pointers remain
// shared with the selected input/template. Create is followed by GetByID so
// the result includes stored defaults and relations; a reread failure can
// follow an applied create unless the caller transaction rolls back.
// Persistent callers hold the recipient lock around this sequence.
func (s *SubscriptionService) assignOrExtendSubscriptionUnlocked(ctx context.Context, input *AssignSubscriptionInput, template *GrantPlanTemplate) (*UserSubscription, bool, error) {
	now := s.clock.now()
	// Treat every latest-plan read error as no prior row, so this path can
	// attempt current-start issuance after a storage error as well as an absent
	// grant.
	latest, err := s.userSubRepo.GetLatestByUserIDAndPlanID(ctx, input.UserID, input.PlanID)
	if err != nil {
		latest = nil
	}

	startsAt := now
	queued := false
	if latest != nil && latest.ExpiresAt.After(now) {
		startsAt = latest.ExpiresAt
		queued = true
	}
	expiresAt := startsAt.AddDate(0, 0, template.ValidityDays)
	// Cap the computed end while retaining the selected queued start. This helper
	// performs no later start-before-end validation when that start itself lies
	// beyond the horizon.
	if expiresAt.After(MaxExpiresAt) {
		expiresAt = MaxExpiresAt
	}

	status := SubscriptionStatusActive
	if startsAt.After(now) {
		status = SubscriptionStatusPending
	}

	sub := &UserSubscription{
		UserID:          input.UserID,
		PlanID:          input.PlanID,
		StartsAt:        startsAt,
		ExpiresAt:       expiresAt,
		Status:          status,
		DailyLimitUSD:   template.DailyLimitUSD,
		WeeklyLimitUSD:  template.WeeklyLimitUSD,
		MonthlyLimitUSD: template.MonthlyLimitUSD,
		AssignedAt:      now,
		SourceOrderID:   input.SourceOrderID,
		Notes:           input.Notes,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if input.AssignedBy > 0 {
		sub.AssignedBy = &input.AssignedBy
	}

	// Create before rereading defaults and relations. A later read failure can
	// follow the new row write; only caller transaction recovery can undo that
	// persistence.
	if err := s.userSubRepo.Create(ctx, sub); err != nil {
		return nil, false, err
	}
	created, err := s.userSubRepo.GetByID(ctx, sub.ID)
	if err != nil {
		return nil, false, err
	}
	return created, queued, nil
}

// BulkAssignSubscription processes a nonnil ordered input list through
// independent issuance calls and returns accumulated results. Duplicate user
// IDs remain separate attempts. Each success increments SuccessCount and
// CreatedCount and appends a shallow grant value; ReusedCount stays zero.
//
// Failures increment FailedCount, append user-tagged error text and allow
// later recipients to proceed. Statuses overwrites the entry for each repeated
// identity with active, queued or failed, while counters continue counting
// attempts. It supplies no source-order identity or provided-template flag and
// opens no transaction covering the entire list; outer context transactions
// can still be joined by individual attempts.
func (s *SubscriptionService) BulkAssignSubscription(ctx context.Context, input *BulkAssignSubscriptionInput) (*BulkAssignResult, error) {
	result := &BulkAssignResult{
		Subscriptions: make([]UserSubscription, 0),
		Errors:        make([]string, 0),
		Statuses:      make(map[int64]string),
	}

	for _, userID := range input.UserIDs {
		sub, queued, err := s.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:          userID,
			PlanID:          input.PlanID,
			ValidityDays:    input.ValidityDays,
			DailyLimitUSD:   input.DailyLimitUSD,
			WeeklyLimitUSD:  input.WeeklyLimitUSD,
			MonthlyLimitUSD: input.MonthlyLimitUSD,
			AssignedBy:      input.AssignedBy,
			Notes:           input.Notes,
		})
		// Record this recipient's failure and continue the remaining independent
		// attempts. Earlier successful grants remain in the result, and repeated
		// user status entries can be replaced.
		if err != nil {
			result.FailedCount++
			result.Errors = append(result.Errors, fmt.Sprintf("user %d: %v", userID, err))
			result.Statuses[userID] = "failed"
			continue
		}
		result.SuccessCount++
		result.CreatedCount++
		result.Subscriptions = append(result.Subscriptions, *sub)
		if queued {
			result.Statuses[userID] = "queued"
		} else {
			result.Statuses[userID] = "active"
		}
	}

	return result, nil
}

// NormalizeAssignValidityDays selects thirty days for nonpositive input and
// caps larger counts at MaxValidityDays. Other positive day counts pass
// through unchanged; date arithmetic and expiry caps are applied separately by
// grant methods.
func NormalizeAssignValidityDays(days int) int {
	if days <= 0 {
		days = 30
	}
	if days > MaxValidityDays {
		days = MaxValidityDays
	}
	return days
}

// revokeSubscriptionLocked reads the target and its user-plan chain, then
// deletes the target and shifts eligible later entries. Its caller has
// acquired the persistence lock when available. RevokeChainDelta selects the
// unelapsed lifetime to remove from that timeline at one service-clock
// observation.
//
// Deletion precedes shifts inside selected mutation work, so a later update
// error needs transaction recovery to undo the earlier deletion. Joined
// transactions provide no inner savepoint, and nonpersistent fixtures run
// these writes directly. This administrator path performs no active-status or
// highest-quota check; self-service revocation uses its separate locked
// eligibility workflow.
func (s *SubscriptionService) revokeSubscriptionLocked(ctx context.Context, subscriptionID int64) error {
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return err
	}

	chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
	if err != nil {
		return err
	}

	now := s.clock.now()
	chainDelta := RevokeChainDelta(sub, now)
	// Delete the selected grant before applying its remaining-duration shift to
	// later siblings. Shared transaction recovery must undo that delete if any
	// later update fails.
	return s.withSubscriptionMutationTx(ctx, func(txCtx context.Context) error {
		if err := s.userSubRepo.Delete(txCtx, sub.ID); err != nil {
			return err
		}
		if chainDelta != 0 {
			if err := s.ShiftLaterChain(txCtx, chain, sub, chainDelta); err != nil {
				return err
			}
		}
		return nil
	})
}

// RevokeOwnExhaustedSubscription rejects nonpositive identities, then
// delegates user-owned exhausted-grant revocation through selected transaction
// work. The inner method handles locks, maintenance, reread, deletion, chain
// shift and optional Key rebind; an outer transaction retains commit/rollback
// ownership.
func (s *SubscriptionService) RevokeOwnExhaustedSubscription(ctx context.Context, userID, subscriptionID int64) (*SelfRevokeSubscriptionResult, error) {
	if userID <= 0 || subscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	var result *SelfRevokeSubscriptionResult
	err := s.transactions.Within(ctx, func(txCtx context.Context) error {
		var e error
		result, e = s.revokeOwnExhaustedSubscriptionInTx(txCtx, userID, subscriptionID)
		return e
	})
	return result, err
}

// revokeOwnExhaustedSubscriptionInTx revokes a present owned grant after
// checking current active status and its highest finite quota. It first reads
// ownership/deletion, then obtains user/subscription locks and evaluates the
// initial snapshot. Window activation and conditional reset run before a
// stored reread rechecks ownership, status and exhausted quota.
//
// The supplied chain selects the earliest still-pending entry starting at or
// after the target expiry as a replacement. Delete precedes shifting later
// entries; a selected replacement then receives preferred-Key bindings through
// the transaction adapter. Errors require the selected transaction owner to
// recover preceding writes. Locks are delegated, returned IDs describe the
// callback result, and joined callers publish cache effects after their own
// successful commit.
func (s *SubscriptionService) revokeOwnExhaustedSubscriptionInTx(ctx context.Context, userID, subscriptionID int64) (*SelfRevokeSubscriptionResult, error) {
	sub, err := s.userSubRepo.GetByIDIncludeDeleted(ctx, subscriptionID)
	if err != nil || sub == nil || sub.UserID != userID {
		return nil, ErrSubscriptionNotFound
	}
	if sub.DeletedAt != nil {
		return nil, ErrSubscriptionNotActive
	}

	// Use user-first locks before maintenance and the final stored eligibility
	// reread. The initial ownership snapshot identifies the target, while later
	// reads decide whether revocation proceeds.
	if err := s.transactions.LockSelfRevoke(ctx, userID, subscriptionID); err != nil {
		return nil, err
	}

	now := s.clock.now()
	if sub.EffectiveStatus(now) != SubscriptionStatusActive {
		return nil, ErrSubscriptionNotActive
	}
	if err := s.CheckAndActivateWindow(ctx, sub); err != nil {
		return nil, err
	}
	if err := s.CheckAndResetWindows(ctx, sub); err != nil {
		return nil, err
	}
	sub, err = s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil || sub == nil || sub.UserID != userID {
		return nil, ErrSubscriptionNotFound
	}
	now = s.clock.now()
	if sub.EffectiveStatus(now) != SubscriptionStatusActive {
		return nil, ErrSubscriptionNotActive
	}
	if !sub.HighestQuotaExhausted() {
		return nil, ErrSubscriptionQuotaAvailable
	}

	chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
	if err != nil {
		return nil, err
	}
	var replacement *UserSubscription
	for i := range chain {
		item := chain[i]
		if item.ID == sub.ID ||
			item.StartsAt.Before(sub.ExpiresAt) ||
			!item.ExpiresAt.After(now) ||
			item.Status != SubscriptionStatusPending ||
			item.EffectiveStatus(now) != SubscriptionStatusPending {
			continue
		}
		if replacement == nil || item.StartsAt.Before(replacement.StartsAt) {
			candidate := item
			replacement = &candidate
		}
	}

	// Delete before shifting siblings and rebinding Keys. The selected
	// transaction owner must recover preceding writes if a later step fails, then
	// publish effects after commit.
	if err := s.userSubRepo.Delete(ctx, sub.ID); err != nil {
		return nil, err
	}
	if delta := RevokeChainDelta(sub, now); delta != 0 {
		if err := s.ShiftLaterChain(ctx, chain, sub, delta); err != nil {
			return nil, err
		}
	}

	result := &SelfRevokeSubscriptionResult{RevokedSubscriptionID: sub.ID}
	if replacement != nil {
		result.ReplacementSubscriptionID = &replacement.ID
		count, err := s.transactions.RebindKeys(ctx, sub.ID, replacement.ID)
		if err != nil {
			return nil, err
		}
		result.ReboundAPIKeyCount = count
	}
	return result, nil
}

// RestoreSubscription restores a deleted grant through the current user-plan
// overlap checks. Persistence-enabled calls join or create selected
// transaction work and lock the recipient; nonpersistent fixtures execute
// directly, with recovery ownership defined by the mutation adapter.
func (s *SubscriptionService) RestoreSubscription(ctx context.Context, subscriptionID int64) (*UserSubscription, error) {
	if !s.transactions.HasPersistence(ctx) {
		return s.restoreSubscriptionUnlocked(ctx, subscriptionID)
	}
	var result *UserSubscription
	err := s.transactions.Within(ctx, func(txCtx context.Context) error {
		var e error
		result, e = s.restoreSubscriptionInTx(txCtx, subscriptionID)
		return e
	})
	return result, err
}

// restoreSubscriptionInTx reads the retained target to identify its recipient,
// locks that user in selected transaction context, then delegates restoration
// rereads. Missing lookup or lock errors propagate before the restore write;
// the caller owns transaction completion.
func (s *SubscriptionService) restoreSubscriptionInTx(ctx context.Context, subscriptionID int64) (*UserSubscription, error) {
	initial, err := s.userSubRepo.GetByIDIncludeDeleted(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if err := s.transactions.LockGrantUser(ctx, initial.UserID); err != nil {
		return nil, err
	}
	return s.restoreSubscriptionUnlocked(ctx, subscriptionID)
}

// restoreSubscriptionUnlocked rereads a retained grant and requires a deletion
// marker before restoring it. It derives status from a shallow copy with
// deletion cleared at service time. A nonexpired candidate must avoid strict
// lifetime overlap with every supplied undeleted, unexpired sibling except
// itself.
//
// Expired restoration skips that timeline query. RestoredSubscriptionStatus
// derives the label after clearing deletion on a copy, then the repository
// clears the stored marker with that selected status. Persistent callers hold
// the recipient lock; direct fixtures can apply the write without transaction
// recovery. Repository read and restore errors remain available to the caller.
func (s *SubscriptionService) restoreSubscriptionUnlocked(ctx context.Context, subscriptionID int64) (*UserSubscription, error) {
	sub, err := s.userSubRepo.GetByIDIncludeDeleted(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub.DeletedAt == nil {
		return nil, ErrSubscriptionNotRevoked
	}

	now := s.clock.now()
	restoredStatus := RestoredSubscriptionStatus(sub, now)
	if restoredStatus != SubscriptionStatusExpired {
		chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
		if err != nil {
			return nil, err
		}
		if SubscriptionRestoreWouldOverlap(sub, chain, now) {
			return nil, ErrSubscriptionRestoreConflict
		}
	}

	// Restore only after any required overlap query passes. This clears deletion
	// with the chosen stored status; outer ownership governs rollback when
	// restoration is joined work.
	return s.userSubRepo.Restore(ctx, subscriptionID, restoredStatus)
}

// RestoredSubscriptionStatus clears deletion on a shallow grant copy and
// evaluates its lifetime at the supplied instant. Nil returns expired. Inside
// a live interval, stored suspension remains suspended, while a stored revoked
// label becomes active under EffectiveStatus's ordinary rule. The input's
// deletion marker and other fields remain unchanged.
func RestoredSubscriptionStatus(sub *UserSubscription, now time.Time) string {
	if sub == nil {
		return SubscriptionStatusExpired
	}
	cp := *sub
	cp.DeletedAt = nil
	return cp.EffectiveStatus(now)
}

// SubscriptionRestoreWouldOverlap checks a nonnil restoration against supplied
// undeleted siblings whose expiry is after now. Nil restoration returns false.
// It skips the same ID, then applies strict interval comparisons, so touching
// endpoints do not count as overlap.
//
// Stored sibling status does not exclude a row from this calculation, and the
// input order does not change the boolean result. The caller supplies its
// user-plan chain under appropriate persistence locks; this helper performs no
// repository or ownership query and leaves every grant unchanged.
func SubscriptionRestoreWouldOverlap(restored *UserSubscription, chain []UserSubscription, now time.Time) bool {
	if restored == nil {
		return false
	}
	for i := range chain {
		item := chain[i]
		if item.ID == restored.ID || item.DeletedAt != nil || !item.ExpiresAt.After(now) {
			continue
		}
		if restored.StartsAt.Before(item.ExpiresAt) && item.StartsAt.Before(restored.ExpiresAt) {
			return true
		}
	}
	return false
}

// RevokeChainDelta chooses the timeline shift removing a grant's unelapsed
// issued lifetime. Nil input returns zero. Before start it removes the whole
// start-to-expiry duration; within remaining lifetime it removes
// now-to-expiry, and an already reached expiry returns zero.
//
// The decision depends on timestamps rather than stored status or quota. For
// an ordered lifetime the shift is nonpositive, preserving elapsed service
// when an active grant is revoked and closing the future slot when a queued
// grant is revoked. This helper changes no fields and performs no lifetime
// validation; malformed endpoints can produce a different sign.
func RevokeChainDelta(sub *UserSubscription, now time.Time) time.Duration {
	if sub == nil {
		return 0
	}
	if now.Before(sub.StartsAt) {
		return sub.StartsAt.Sub(sub.ExpiresAt)
	}
	if sub.ExpiresAt.After(now) {
		return now.Sub(sub.ExpiresAt)
	}
	return 0
}

// extendSubscriptionLocked adjusts the target expiry by a bounded signed day
// count and updates its later timeline. Lookup errors become
// ErrSubscriptionNotFound. An elapsed target uses now as the addition anchor
// and rejects negative days; an unelapsed one adds to its old expiry.
// MaxExpiresAt then caps the result, which must remain after StartsAt.
//
// The selected chain is shifted by the elapsed difference between new and old
// expiry after the target expiry write. Status is updated from captured
// service time and the new lifetime, all through selected mutation work;
// persistent callers already hold user/subscription locks. A final reread
// supplies the result, and joined transaction owners recover errors and
// publish effects themselves.
func (s *SubscriptionService) extendSubscriptionLocked(ctx context.Context, subscriptionID int64, days int) (*UserSubscription, error) {
	// Normalize target read failure to the existing subscription-not-found
	// result. This caller-facing classification discards the repository
	// distinction between absence and another lookup failure.
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	if days > MaxValidityDays {
		days = MaxValidityDays
	}
	if days < -MaxValidityDays {
		days = -MaxValidityDays
	}

	now := s.clock.now()
	oldExpiresAt := sub.ExpiresAt
	var newExpiresAt time.Time
	if !oldExpiresAt.After(now) {
		if days < 0 {
			return nil, ErrAdjustWouldExpire
		}
		newExpiresAt = now.AddDate(0, 0, days)
	} else {
		newExpiresAt = oldExpiresAt.AddDate(0, 0, days)
	}
	// Cap the target deadline before its start-order check, so this mutation
	// rejects a capped end that does not remain after the unchanged target start.
	if newExpiresAt.After(MaxExpiresAt) {
		newExpiresAt = MaxExpiresAt
	}
	if !newExpiresAt.After(sub.StartsAt) {
		return nil, ErrAdjustWouldExpire
	}

	delta := newExpiresAt.Sub(oldExpiresAt)
	chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
	if err != nil {
		return nil, err
	}

	return s.persistSubscriptionExpiryChange(ctx, subscriptionID, sub, chain, newExpiresAt, delta, now)
}

// withSubscriptionMutationTx delegates the nonnil mutation callback to the
// configured transaction adapter with its caller context. Joined or owned
// transaction completion and direct nonpersistent execution follow that
// adapter's selection rules.
func (s *SubscriptionService) withSubscriptionMutationTx(ctx context.Context, mutate func(context.Context) error) error {
	return s.transactions.Within(ctx, mutate)
}

// setSubscriptionValidityDaysLocked replaces remaining validity with a
// positive bounded day count from now or a future start. Target lookup errors
// become ErrSubscriptionNotFound. The new expiry is capped and must remain
// after the stored start; later siblings are selected from the original
// user-plan timeline.
//
// Selected transaction work writes expiry, shifts eligible siblings by its
// change from the old expiry, then writes status from one captured
// service-time observation. Persistent callers hold the subscription lock
// before this helper reads. Final reread errors can occur after joined writes;
// their owner decides rollback and post-commit publication. Direct
// nonpersistent execution can retain earlier mutations after a failure.
func (s *SubscriptionService) setSubscriptionValidityDaysLocked(ctx context.Context, subscriptionID int64, validityDays int) (*UserSubscription, error) {
	// Normalize target read failure to the existing subscription-not-found
	// result. The returned classification does not expose whether absence or a
	// different repository failure caused it.
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	if validityDays <= 0 {
		return nil, ErrAdjustWouldExpire
	}
	if validityDays > MaxValidityDays {
		validityDays = MaxValidityDays
	}

	now := s.clock.now()
	oldExpiresAt := sub.ExpiresAt
	newExpiresAt := TargetSubscriptionExpiresAt(sub, now, validityDays)
	// Cap the target deadline before its start-order check, so this mutation
	// rejects a capped end that does not remain after the unchanged target start.
	if newExpiresAt.After(MaxExpiresAt) {
		newExpiresAt = MaxExpiresAt
	}
	if !newExpiresAt.After(sub.StartsAt) {
		return nil, ErrAdjustWouldExpire
	}

	delta := newExpiresAt.Sub(oldExpiresAt)
	chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
	if err != nil {
		return nil, err
	}

	return s.persistSubscriptionExpiryChange(ctx, subscriptionID, sub, chain, newExpiresAt, delta, now)
}

// persistSubscriptionExpiryChange writes a computed target deadline, moves its
// later timeline and returns the repository's refreshed target. Callers supply
// the captured target view, its selected chain, the computed delta and the
// observation time after validating expiry. Persistent callers retain the user
// and subscription locks around this work.
//
// The selected transaction callback writes expiry before moving siblings and
// storing target status. The first failure stops the remaining operations. A
// successful callback is followed by a reread on ctx, so joined transaction
// owners decide recovery when that read fails.
func (s *SubscriptionService) persistSubscriptionExpiryChange(
	ctx context.Context,
	subscriptionID int64,
	sub *UserSubscription,
	chain []UserSubscription,
	newExpiresAt time.Time,
	delta time.Duration,
	now time.Time,
) (*UserSubscription, error) {
	// Keep the captured target view for sibling selection while the repository
	// writes its new deadline. Transaction ownership determines recovery if a
	// later update fails.
	err := s.transactions.Within(ctx, func(txCtx context.Context) error {
		if err := s.userSubRepo.ExtendExpiry(txCtx, subscriptionID, newExpiresAt); err != nil {
			return err
		}
		if delta != 0 {
			if err := s.ShiftLaterChain(txCtx, chain, sub, delta); err != nil {
				return err
			}
		}

		var status string
		if !newExpiresAt.After(now) {
			status = SubscriptionStatusExpired
		} else if now.Before(sub.StartsAt) {
			status = SubscriptionStatusPending
		} else {
			status = SubscriptionStatusActive
		}
		return s.userSubRepo.UpdateStatus(txCtx, subscriptionID, status)
	})
	if err != nil {
		return nil, err
	}
	return s.userSubRepo.GetByID(ctx, subscriptionID)
}

// TargetSubscriptionExpiresAt adds the supplied calendar-day count to
// SubscriptionValidityAnchor in that anchor's zone. It performs no count
// validation or expiry cap; service mutation callers apply their own
// admissible-day and MaxExpiresAt rules.
func TargetSubscriptionExpiresAt(sub *UserSubscription, now time.Time, validityDays int) time.Time {
	return SubscriptionValidityAnchor(sub, now).AddDate(0, 0, validityDays)
}

// SubscriptionValidityAnchor selects a nonnil grant's future StartsAt,
// otherwise the supplied now. Nil, already-started and elapsed grants use now;
// this value determines calendar-day addition without consulting stored
// status.
func SubscriptionValidityAnchor(sub *UserSubscription, now time.Time) time.Time {
	if sub != nil && now.Before(sub.StartsAt) {
		return sub.StartsAt
	}
	return now
}

// ShiftLaterChain updates supplied siblings beginning at or after the anchor's
// old expiry, skipping the anchor ID. Nil anchor or zero delta is a no-op. It
// follows supplied order, adds the same elapsed duration to both endpoints,
// caps expiry alone, and derives status from one service-clock observation.
//
// Each shallow item copy is passed to repository Update, retaining optional
// pointers and plan data. Earlier updates can survive a later error unless the
// caller transaction rolls them back; this helper opens no transaction or
// locks. It does not validate user/plan membership, overall overlap or a
// capped row's end after start. Callers supply the intended locked chain and
// recover partial persistence work.
func (s *SubscriptionService) ShiftLaterChain(ctx context.Context, chain []UserSubscription, anchor *UserSubscription, delta time.Duration) error {
	if delta == 0 || anchor == nil {
		return nil
	}
	now := s.clock.now()
	// Follow the supplied chain order for sequential endpoint/status updates. An
	// error stops later rows, leaving preceding writes for caller transaction
	// recovery and their optional nested values shared.
	for i := range chain {
		item := chain[i]
		if item.ID == anchor.ID {
			continue
		}
		if item.StartsAt.Before(anchor.ExpiresAt) {
			continue
		}
		item.StartsAt = item.StartsAt.Add(delta)
		item.ExpiresAt = item.ExpiresAt.Add(delta)
		// Cap expiry alone after shifting both endpoints. This can shorten the
		// stored duration, and the helper does not revalidate ordering against the
		// shifted start.
		if item.ExpiresAt.After(MaxExpiresAt) {
			item.ExpiresAt = MaxExpiresAt
		}
		switch {
		case !item.ExpiresAt.After(now):
			item.Status = SubscriptionStatusExpired
		case now.Before(item.StartsAt):
			item.Status = SubscriptionStatusPending
		default:
			item.Status = SubscriptionStatusActive
		}
		if err := s.userSubRepo.Update(ctx, &item); err != nil {
			return err
		}
	}
	return nil
}

// GetByID reads one grant and replaces its returned status with
// EffectiveStatus at service time. Repository errors propagate; callers supply
// a repository that returns a usable nonnil grant on success, and no status
// write is persisted.
func (s *SubscriptionService) GetByID(ctx context.Context, id int64) (*UserSubscription, error) {
	sub, err := s.userSubRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.clock.now()
	sub.Status = sub.EffectiveStatus(now)
	return sub, nil
}

// GetSubscriptionForAPIKey returns a grant belonging to the paying user under
// the repository's nonnil-success rule. Missing dependencies, nonpositive
// identities, lookup errors and ownership mismatch become
// ErrSubscriptionNotFound; GetByID dereferences a successful row before this
// wrapper's later nil check.
func (s *SubscriptionService) GetSubscriptionForAPIKey(ctx context.Context, userID, subscriptionID int64) (*UserSubscription, error) {
	if s == nil || s.userSubRepo == nil || userID <= 0 || subscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	subscription, err := s.GetByID(ctx, subscriptionID)
	// Collapse failed, absent and foreign-user lookups into the same not-found
	// result for Key selection. The original repository cause is not returned
	// from this wrapper.
	if err != nil || subscription == nil || subscription.UserID != userID {
		return nil, ErrSubscriptionNotFound
	}
	return subscription, nil
}

// GetActiveSubscription returns the first repository-active row with the
// requested plan ID. Read errors propagate and no matching row yields
// ErrSubscriptionNotFound; this selection performs no quota or final-group
// coverage check.
func (s *SubscriptionService) GetActiveSubscription(ctx context.Context, userID, planID int64) (*UserSubscription, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].PlanID == planID {
			return &subs[i], nil
		}
	}
	return nil, ErrSubscriptionNotFound
}

// GetUsableSubscription returns the first quota-usable grant with its
// maintenance flag after repository-active selection. Only the first positive
// optional group ID activates repository-backed coverage filtering. Candidates
// are stably ordered by expiry then start, preserving exact-time input ties.
//
// ValidateAndCheckLimits can locally clear stale usage before selection, so a
// returned maintenance flag requires stored maintenance and reread before
// admission. The listed quota, expired, suspended and invalid errors skip a
// candidate; other errors, including a revoked candidate's
// ErrSubscriptionNotFound, stop selection. Quota-rejected candidates can
// already carry local counter changes, while status rejection returns before
// clearing usage. No usable result returns ErrSubscriptionNotFound.
func (s *SubscriptionService) GetUsableSubscription(ctx context.Context, userID int64, groupID ...int64) (*UserSubscription, bool, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	if len(groupID) > 0 && groupID[0] > 0 {
		subs, err = s.filterSubscriptionsByGroup(ctx, subs, groupID[0])
		if err != nil {
			return nil, false, err
		}
	}
	// Order candidate grants by expiry and then start, preserving input order for
	// exact timestamp ties. This controls which remaining quota snapshot can be
	// selected first.
	sort.SliceStable(subs, func(i, j int) bool {
		if subs[i].ExpiresAt.Equal(subs[j].ExpiresAt) {
			return subs[i].StartsAt.Before(subs[j].StartsAt)
		}
		return subs[i].ExpiresAt.Before(subs[j].ExpiresAt)
	})
	for i := range subs {
		sub := &subs[i]
		needsMaintenance, validateErr := s.ValidateAndCheckLimits(sub)
		if validateErr == nil {
			return sub, needsMaintenance, nil
		}
		// Skip only the listed quota or lifetime failures and inspect the next
		// candidate. Other errors retain their classification and stop selection
		// before later grants are evaluated.
		if errors.Is(validateErr, ErrDailyLimitExceeded) ||
			errors.Is(validateErr, ErrWeeklyLimitExceeded) ||
			errors.Is(validateErr, ErrMonthlyLimitExceeded) ||
			errors.Is(validateErr, ErrSubscriptionExpired) ||
			errors.Is(validateErr, ErrSubscriptionSuspended) ||
			errors.Is(validateErr, ErrSubscriptionInvalid) {
			continue
		}
		return nil, false, validateErr
	}
	return nil, false, ErrSubscriptionNotFound
}

// filterSubscriptionsByGroup delegates positive-group filtering to the
// repository's required optional capability. Nonpositive IDs or empty input
// return the same slice; absent service/repository or missing capability
// returns an error rather than trusting hydrated plan metadata.
func (s *SubscriptionService) filterSubscriptionsByGroup(ctx context.Context, subs []UserSubscription, groupID int64) ([]UserSubscription, error) {
	if groupID <= 0 || len(subs) == 0 {
		return subs, nil
	}
	if s == nil || s.userSubRepo == nil {
		return nil, fmt.Errorf("subscription plan group filter unavailable")
	}
	filter, ok := s.userSubRepo.(UserSubscriptionGroupFilter)
	if !ok {
		return nil, fmt.Errorf("subscription plan group filter unavailable")
	}
	return filter.FilterByGroup(ctx, subs, groupID)
}

// ListUserSubscriptions reads the user's grants, then normalizes eligible
// stale window fields and display status on returned rows. These changes are
// in-memory; reset/status helpers use separate system-time observations rather
// than one frozen service clock.
func (s *SubscriptionService) ListUserSubscriptions(ctx context.Context, userID int64) ([]UserSubscription, error) {
	subs, err := s.userSubRepo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs, s.clock.calendar())
	NormalizeSubscriptionStatus(subs)
	return subs, nil
}

// ListActiveUserSubscriptions reads repository-active grants before
// normalizing eligible stale windows and displayed status. Returned row fields
// change in-memory only; repository filtering and helper status/reset time
// observations remain independent.
func (s *SubscriptionService) ListActiveUserSubscriptions(ctx context.Context, userID int64) ([]UserSubscription, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs, s.clock.calendar())
	NormalizeSubscriptionStatus(subs)
	return subs, nil
}

// ListSubscriptionsBySourceOrderID reads exact order-linked grants and
// normalizes their returned stale-window and status fields. Read errors
// propagate, with no order-owner validation or persisted maintenance
// introduced by these display transformations.
func (s *SubscriptionService) ListSubscriptionsBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]UserSubscription, error) {
	subs, err := s.userSubRepo.ListBySourceOrderID(ctx, sourceOrderID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs, s.clock.calendar())
	NormalizeSubscriptionStatus(subs)
	return subs, nil
}

// ListPlanSubscriptions counts/pages plan grants through the repository, then
// normalizes returned window/status fields in-memory. The repository owns
// effective pagination and ordering; helper reset and status observations use
// system time independently of page reads.
func (s *SubscriptionService) ListPlanSubscriptions(ctx context.Context, planID int64, page, pageSize int) ([]UserSubscription, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	subs, pag, err := s.userSubRepo.ListByPlanID(ctx, planID, params)
	if err != nil {
		return nil, nil, err
	}
	normalizeExpiredWindows(subs, s.clock.calendar())
	NormalizeSubscriptionStatus(subs)
	return subs, pag, nil
}

// List forwards supplied page, owner, plan, status and sort choices while
// discarding platform. Repository helpers normalize query limits/offsets;
// returned rows receive local window/status normalization from independent
// helper clock observations.
func (s *SubscriptionService) List(ctx context.Context, page, pageSize int, userID, planID *int64, status, _platform, sortBy, sortOrder string) ([]UserSubscription, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	subs, pag, err := s.userSubRepo.List(ctx, params, userID, planID, status, "", sortBy, sortOrder)
	if err != nil {
		return nil, nil, err
	}
	normalizeExpiredWindows(subs, s.clock.calendar())
	NormalizeSubscriptionStatus(subs)
	return subs, pag, nil
}

// normalizeExpiredWindows clears anchors and usage on the supplied outer
// records when their reset predicates permit automatic advancement. It writes
// no repository state. Daily uses the supplied calendar, while each
// daily/weekly/monthly predicate takes its own system-time observation.
//
// One-time daily lifetime and expiry-tail rules can retain a stale anchor and
// usage rather than clear them. Eligible periods become missing-anchor display
// state until persisted activation/reset is performed elsewhere; nested plan
// and quota pointers remain shared. Callers use these rows for presentation
// and separately run stored maintenance before consuming locally cleared
// counters for admission.
func normalizeExpiredWindows(subs []UserSubscription, calendar timezone.Calendar) {
	// Clear only local display fields for periods whose automatic reset
	// predicates pass. Stored counters remain untouched, and a later admission
	// path must perform persistence maintenance and reread.
	for i := range subs {
		sub := &subs[i]
		if sub.NeedsDailyReset(calendar) {
			sub.DailyWindowStart = nil
			sub.DailyUsageUSD = 0
		}
		if sub.NeedsWeeklyReset() {
			sub.WeeklyWindowStart = nil
			sub.WeeklyUsageUSD = 0
		}
		if sub.NeedsMonthlyReset() {
			sub.MonthlyWindowStart = nil
			sub.MonthlyUsageUSD = 0
		}
	}
}

// NormalizeSubscriptionStatus replaces displayed status on supplied outer rows
// using one current system-time observation for the entire slice. It writes no
// repository state and does not use a service's injected DateRuntime.Now
// callback.
func NormalizeSubscriptionStatus(subs []UserSubscription) {
	now := time.Now()
	for i := range subs {
		subs[i].Status = subs[i].EffectiveStatus(now)
	}
}

// CheckAndActivateWindow evaluates activation on a nonnil grant at service
// time and writes selected anchors through the repository. The shared anchor
// is midnight in that time's own zone; counters and supplied snapshot fields
// remain unchanged.
func (s *SubscriptionService) CheckAndActivateWindow(ctx context.Context, sub *UserSubscription) error {
	now := s.clock.now()
	activation := sub.WindowActivationAt(now)
	if !activation.Any() {
		return nil
	}
	windowStart := startOfDay(now)
	return s.userSubRepo.ActivateWindows(ctx, sub.ID, windowStart, activation)
}

// adminResetQuotaLocked requires at least one selected period, reads the
// target, then resets selected anchors/counters at midnight in service time's
// zone. Its caller holds persistence locks and owns transaction recovery;
// final reread returns stored values.
func (s *SubscriptionService) adminResetQuotaLocked(ctx context.Context, subscriptionID int64, resetDaily, resetWeekly, resetMonthly bool) (*UserSubscription, error) {
	if !resetDaily && !resetWeekly && !resetMonthly {
		return nil, ErrInvalidInput
	}
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	windowStart := startOfDay(s.clock.now())
	if err := s.userSubRepo.ResetUsageWindows(ctx, sub.ID, resetDaily, resetWeekly, resetMonthly, windowStart); err != nil {
		return nil, err
	}
	return s.userSubRepo.GetByID(ctx, subscriptionID)
}

// CheckAndResetWindows delegates quota reset decisions using one service-clock
// observation. The supplied grant is mutated after successful reset requests;
// conditional no-op success still requires a stored reread before admission.
func (s *SubscriptionService) CheckAndResetWindows(ctx context.Context, sub *UserSubscription) error {
	return s.CheckAndResetWindowsAt(ctx, sub, s.clock.now())
}

// CheckAndResetWindowsAt evaluates daily, weekly and monthly reset decisions
// at one supplied instant for a nonnil grant. Daily computes the replacement
// through the configured calendar; weekly/monthly use midnight in now's own
// zone. Each repository request compares its captured expected anchor before
// changing storage.
//
// After successful requests, local anchors/counters are replaced even when a
// concurrent conditional reset won and this write affected no rows. A later
// period error can follow earlier stored and local changes; caller
// transactions govern persistence recovery, but local fields are not restored
// here. EnsureWindowMaintenance rereads the stored grant before that cleared
// local usage can support admission.
func (s *SubscriptionService) CheckAndResetWindowsAt(ctx context.Context, sub *UserSubscription, now time.Time) error {
	// Use one decision instant across the sequential resets, but retain each
	// period's anchor-selection rule. Conditional no-op successes still replace
	// local counters and require a stored admission reread.
	windowStart := startOfDay(now)
	if dailyWindowStart, ok := sub.AutomaticDailyWindowStartWithCalendar(now, s.clock.calendar()); ok {
		expectedWindowStart := sub.DailyWindowStart
		if err := s.userSubRepo.ResetDailyUsage(ctx, sub.ID, expectedWindowStart, dailyWindowStart); err != nil {
			return err
		}
		sub.DailyWindowStart = &dailyWindowStart
		sub.DailyUsageUSD = 0
	}
	if sub.NeedsWeeklyResetAt(now) {
		expectedWindowStart := sub.WeeklyWindowStart
		if err := s.userSubRepo.ResetWeeklyUsage(ctx, sub.ID, expectedWindowStart, windowStart); err != nil {
			return err
		}
		sub.WeeklyWindowStart = &windowStart
		sub.WeeklyUsageUSD = 0
	}
	if sub.NeedsMonthlyResetAt(now) {
		expectedWindowStart := sub.MonthlyWindowStart
		if err := s.userSubRepo.ResetMonthlyUsage(ctx, sub.ID, expectedWindowStart, windowStart); err != nil {
			return err
		}
		sub.MonthlyWindowStart = &windowStart
		sub.MonthlyUsageUSD = 0
	}
	return nil
}

// EnsureWindowMaintenance activates eligible missing anchors, resets quota
// windows and returns a stored grant reread for admission. Nil input returns
// ErrSubscriptionNilInput, and repository errors stop the sequence before its
// later operations.
//
// Activation leaves the supplied snapshot unchanged, while successful reset
// requests can replace its local counters even when a concurrent conditional
// reset won. The final reread supplies the actual stored usage before those
// locally cleared values can support admission. Caller-selected repository
// context governs persistence recovery; an error can follow earlier window
// writes or local changes, which are not restored by this helper.
func (s *SubscriptionService) EnsureWindowMaintenance(ctx context.Context, sub *UserSubscription) (*UserSubscription, error) {
	if sub == nil {
		return nil, ErrSubscriptionNilInput
	}
	if err := s.CheckAndActivateWindow(ctx, sub); err != nil {
		return nil, err
	}
	if err := s.CheckAndResetWindows(ctx, sub); err != nil {
		return nil, err
	}
	// Reread after activation and reset requests, including successful
	// conditional no-ops. Admission must use this stored snapshot instead of the
	// locally cleared counters that another reset may have superseded.
	return s.userSubRepo.GetByID(ctx, sub.ID)
}

// CheckUsageLimits delegates overlapping quota checks for a nonnil grant and
// supplied additional cost. It ignores caller context, selecting the first
// daily, weekly or monthly failure through CheckSubscriptionUsageLimits.
func (s *SubscriptionService) CheckUsageLimits(_ context.Context, sub *UserSubscription, additionalCost float64) error {
	return CheckSubscriptionUsageLimits(sub, additionalCost)
}

// CheckSubscriptionUsageLimits checks daily, weekly then monthly limits on a
// nonnil grant, returning the first failure. Zero additional cost requires
// remaining capacity; nonzero cost permits equality and performs no separate
// finite or sign validation.
func CheckSubscriptionUsageLimits(sub *UserSubscription, additionalCost float64) error {
	if SubscriptionWindowLimitExceeded(sub.DailyLimitUSD, sub.DailyUsageUSD, additionalCost) {
		return ErrDailyLimitExceeded
	}
	if SubscriptionWindowLimitExceeded(sub.WeeklyLimitUSD, sub.WeeklyUsageUSD, additionalCost) {
		return ErrWeeklyLimitExceeded
	}
	if SubscriptionWindowLimitExceeded(sub.MonthlyLimitUSD, sub.MonthlyUsageUSD, additionalCost) {
		return ErrMonthlyLimitExceeded
	}
	return nil
}

// SubscriptionWindowLimitExceeded reports a breached configured quota using
// supplied usage and additional cost. Absent or nonpositive limits return
// false. Zero additionalCost is an admission check before the final amount is
// known, so usage already equal to the limit rejects it.
//
// A nonzero additional amount instead compares used plus that amount strictly
// above the limit, permitting an exact final fit. Negative additional amounts
// participate in the same arithmetic, and float finiteness is not validated.
// Callers select admission or known-cost usage deliberately; this helper
// performs no window normalization or persistence.
func SubscriptionWindowLimitExceeded(limit *float64, used float64, additionalCost float64) bool {
	if limit == nil || *limit <= 0 {
		return false
	}
	// A zero additional cost checks admission before the final charge is known,
	// so an already exhausted window cannot admit another request.
	if additionalCost == 0 {
		return used >= *limit
	}
	return used+additionalCost > *limit
}

// ValidateAndCheckLimits evaluates status and quota on a nonnil grant,
// clearing eligible stale usage in that supplied snapshot. Effective status
// maps expired, suspended, pending and revoked to their respective errors
// before reset decisions. Daily/weekly/monthly predicates then take separate
// system-time observations, while status and activation use the service clock.
//
// The maintenance flag can accompany a quota error after local counters
// change. When it is true, callers run EnsureWindowMaintenance and check its
// stored result before admission; the cleared snapshot alone is insufficient
// after a conditional reset race. This helper persists neither status nor
// usage, leaves anchors unchanged during precheck, and validates no separate
// finite-value domain.
func (s *SubscriptionService) ValidateAndCheckLimits(sub *UserSubscription) (needsMaintenance bool, err error) {
	switch sub.EffectiveStatus(s.clock.now()) {
	case SubscriptionStatusExpired:
		return false, ErrSubscriptionExpired
	case SubscriptionStatusSuspended:
		return false, ErrSubscriptionSuspended
	case SubscriptionStatusPending:
		return false, ErrSubscriptionInvalid
	case SubscriptionStatusRevoked:
		return false, ErrSubscriptionNotFound
	}
	if sub.NeedsDailyReset(s.clock.calendar()) {
		sub.DailyUsageUSD = 0
		needsMaintenance = true
	}
	if sub.NeedsWeeklyReset() {
		sub.WeeklyUsageUSD = 0
		needsMaintenance = true
	}
	if sub.NeedsMonthlyReset() {
		sub.MonthlyUsageUSD = 0
		needsMaintenance = true
	}
	if sub.NeedsWindowActivationAt(s.clock.now()) {
		needsMaintenance = true
	}
	return needsMaintenance, s.CheckUsageLimits(context.Background(), sub, 0)
}

// DoWindowMaintenance performs best-effort activation and reset for a nonnil
// grant under a fresh five-second background budget. It discards repository
// errors and leaves any local reset mutations visible; admission uses the
// separate maintenance-and-reread path.
func (s *SubscriptionService) DoWindowMaintenance(sub *UserSubscription) {
	if sub == nil {
		return
	}
	// Run compatibility maintenance under a fresh background budget, separate
	// from any request cancellation. Discard operation errors here; strict
	// admission uses the separate stored maintenance-and-reread entry.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sub.NeedsWindowActivationAt(s.clock.now()) {
		_ = s.CheckAndActivateWindow(ctx, sub)
	}
	_ = s.CheckAndResetWindows(ctx, sub)
}

// GetSubscriptionProgress reads a grant by ID, mapping any lookup error to
// ErrSubscriptionNotFound. User-presentation context localizes a shallow outer
// copy and its plan; CalculateProgress then supplies display observations,
// with authorization owned by its HTTP caller.
func (s *SubscriptionService) GetSubscriptionProgress(ctx context.Context, subscriptionID int64) (*SubscriptionProgress, error) {
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	// Map every progress lookup failure to subscription-not-found, keeping
	// repository diagnostics out of this method's returned error classification
	// before any presentation transformation.
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	// Localize user presentation on a shallow outer copy and a localized plan
	// value. Retained quota and other optional grant pointers still follow
	// repository conversion sharing.
	if locale.UserPresentation(ctx) {
		localized := *sub
		localized.Plan = LocalizePlan(sub.Plan, locale.FromContext(ctx))
		sub = &localized
	}
	return s.CalculateProgress(sub), nil
}

// CalculateProgress builds lifetime/status and optional quota detail for a
// nonnil supplied grant. Effective status uses service time, while
// DaysRemaining and window countdowns observe system time separately. Plan
// name is read as supplied, including any localization already applied by the
// caller.
//
// Each window requires a nonnil limit not at or below zero and a stored
// anchor. Reset helpers can substitute grant expiry for an unavailable next
// period; NormalizedWindowProgress applies that supplied deadline. This
// calculation changes no grant fields, writes no maintenance, and can display
// retained stale usage. Callers provide suitable financial values because
// progress arithmetic performs no separate finite-value validation.
func (s *SubscriptionService) CalculateProgress(sub *UserSubscription) *SubscriptionProgress {
	now := s.clock.now()
	progress := &SubscriptionProgress{
		ID:            sub.ID,
		PlanID:        sub.PlanID,
		StartsAt:      sub.StartsAt,
		ExpiresAt:     sub.ExpiresAt,
		Status:        sub.EffectiveStatus(now),
		ExpiresInDays: sub.DaysRemaining(),
	}
	if sub.Plan != nil {
		progress.PlanName = sub.Plan.Name
	}
	if limit, ok := NormalizedWindowProgress(sub.DailyLimitUSD, sub.DailyUsageUSD, sub.DailyResetTime(s.clock.calendar()), sub.DailyWindowStart, SubscriptionDailyWindow); ok {
		progress.Daily = limit
	}
	if limit, ok := NormalizedWindowProgress(sub.WeeklyLimitUSD, sub.WeeklyUsageUSD, sub.WeeklyResetTime(), sub.WeeklyWindowStart, SubscriptionWeeklyWindow); ok {
		progress.Weekly = limit
	}
	if limit, ok := NormalizedWindowProgress(sub.MonthlyLimitUSD, sub.MonthlyUsageUSD, sub.MonthlyResetTime(), sub.MonthlyWindowStart, SubscriptionMonthlyWindow); ok {
		progress.Monthly = limit
	}
	return progress
}

// NormalizedWindowProgress builds one quota display when a limit and anchor
// are present and the limit is not at or below zero. Otherwise it returns nil
// with false. A supplied resetAt wins over anchor plus duration, allowing
// one-time or expiry-tail displays to use the grant's ending instant.
//
// Remaining amount clamps below zero and percentage caps above one hundred,
// while negative usage can still yield a negative percentage or remaining
// amount above the limit. UsedUSD remains as supplied. Countdown uses system
// time, truncates fractional seconds and clamps negative results to zero
// without moving ResetsAt. Inputs retain their values; no separate
// finite-number or duration validation is performed.
func NormalizedWindowProgress(limit *float64, used float64, resetAt, windowStart *time.Time, duration time.Duration) (*UsageWindowProgress, bool) {
	if limit == nil || *limit <= 0 || windowStart == nil {
		return nil, false
	}
	resetsAt := windowStart.Add(duration)
	// An expiry-capped deadline can precede a full quota duration. Prefer the
	// supplied deadline so a short grant displays its selected quota end.
	if resetAt != nil {
		resetsAt = *resetAt
	}
	remaining := *limit - used
	// Overuse displays zero available dollars while retaining the supplied usage.
	if remaining < 0 {
		remaining = 0
	}
	// Convert the used fraction to percent; unordered limits keep zero here.
	percentage := 0.0
	if *limit > 0 {
		percentage = (used / *limit) * 100
		// Cap overuse at full consumption while keeping negative shares unchanged.
		if percentage > 100 {
			percentage = 100
		}
	}
	// Convert the separate system-time countdown to whole seconds and clamp past
	// deadlines at zero. This changes countdown display without moving the
	// selected reset/expiry timestamp.
	resetsIn := max(int64(time.Until(resetsAt).Seconds()), 0)
	return &UsageWindowProgress{
		LimitUSD:        *limit,
		UsedUSD:         used,
		RemainingUSD:    remaining,
		Percentage:      percentage,
		WindowStart:     *windowStart,
		ResetsAt:        resetsAt,
		ResetsInSeconds: resetsIn,
	}, true
}

// RevokeSubscription delegates target deletion and later-chain shifting
// through withLockedSubscription. Persistent calls lock the recipient then
// target before rereads; direct nonpersistent execution and joined transaction
// recovery follow the mutation adapter's rules.
func (s *SubscriptionService) RevokeSubscription(ctx context.Context, subscriptionID int64) error {
	_, err := withLockedSubscription(s, ctx, subscriptionID, func(txCtx context.Context) (struct{}, error) {
		return struct{}{}, s.revokeSubscriptionLocked(txCtx, subscriptionID)
	})
	return err
}

// ExtendSubscription adjusts signed validity and later timeline through
// selected locked mutation work. Expired grants reject negative days, expiry
// is bounded, and outer transaction owners recover failures and publish
// effects after commit.
func (s *SubscriptionService) ExtendSubscription(ctx context.Context, subscriptionID int64, days int) (*UserSubscription, error) {
	return withLockedSubscription(s, ctx, subscriptionID, func(txCtx context.Context) (*UserSubscription, error) {
		return s.extendSubscriptionLocked(txCtx, subscriptionID, days)
	})
}

// SetSubscriptionValidityDays replaces remaining validity from now or a future
// start through selected locked work. Positive days are required; later
// siblings shift with the expiry change, and outer transaction owners control
// recovery.
func (s *SubscriptionService) SetSubscriptionValidityDays(ctx context.Context, subscriptionID int64, validityDays int) (*UserSubscription, error) {
	return withLockedSubscription(s, ctx, subscriptionID, func(txCtx context.Context) (*UserSubscription, error) {
		return s.setSubscriptionValidityDaysLocked(txCtx, subscriptionID, validityDays)
	})
}

// AdminResetQuota rejects an empty period selection before locked mutation
// work, then writes selected counters/anchors and rereads the grant.
// Persistent callers use user-first locks; joined transaction owners handle
// recovery and later cache publication.
func (s *SubscriptionService) AdminResetQuota(ctx context.Context, subscriptionID int64, resetDaily, resetWeekly, resetMonthly bool) (*UserSubscription, error) {
	if !resetDaily && !resetWeekly && !resetMonthly {
		return nil, ErrInvalidInput
	}
	return withLockedSubscription(s, ctx, subscriptionID, func(txCtx context.Context) (*UserSubscription, error) {
		return s.adminResetQuotaLocked(txCtx, subscriptionID, resetDaily, resetWeekly, resetMonthly)
	})
}

// withLockedSubscription invokes a nonnil callback after the configured
// adapter's subscription lock when persistence is available. It joins or
// creates selected transaction work; lock failure returns before callback
// execution. The callback performs its own complete grant reread and eligible
// mutation on that selected context.
//
// Without persistence, it calls the callback directly with the original
// context, acquiring no locks or rollback capability. Successful callback
// results can still accompany transaction-completion errors, so callers use
// the error before publishing effects. Bound/context transaction owners retain
// commit and rollback decisions; the adapter's lock acquires the discovered
// recipient user before the target row.
func withLockedSubscription[T any](s *SubscriptionService, ctx context.Context, id int64, fn func(context.Context) (T, error)) (T, error) {
	// Fixtures without persistence run the callback directly, so no
	// user/subscription locks or rollback mechanism are introduced by this
	// wrapper around their repository mutations.
	if !s.transactions.HasPersistence(ctx) {
		return fn(ctx)
	}
	var result T
	err := s.transactions.Within(ctx, func(txCtx context.Context) error {
		// Acquire the adapter's user-first subscription lock before the callback
		// rereads its target. Reused transaction owners retain these locks until
		// their own completion.
		if err := s.transactions.LockSubscription(txCtx, id); err != nil {
			return err
		}
		var err error
		result, err = fn(txCtx)
		return err
	})
	return result, err
}
