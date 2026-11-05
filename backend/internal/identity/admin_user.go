package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// admin_user.go applies administrator account and balance changes and enriches
// account queries.
// Administrator funding updates use atomic delta or replacement operations
// before a refreshed account is returned. A later account read can fail after
// funding persistence, while profile updates use a selected field mask before
// optional multiplier synchronization and adjustment history.
//
// Cache publication follows applicable successful returns and selected field
// differences, with separately scheduled funding-cache cleanup and diagnostic
// auxiliary failures. Role demotion checks a count before its independent
// update, and RPM display combines independently read counts, metadata and
// overrides from a bounded Key sample. Returned user/maps/timestamps remain
// the available observations supplied by readers, so callers keep completed
// mutations separate from partial enrichment, callback propagation and later
// transaction completion.

// ListUsers returns the repository page enriched with usage activity and
// optional group multipliers. Base account/page failure remains an error; the
// repository supplies nonnil pagination metadata, while later activity or
// multiplier failures are diagnosed and retain the available account view.
//
// A batch multiplier capability is preferred. Failure or missing capability
// selects per-account reads, whose individual failures leave that account's
// earlier map untouched. Returned timestamp pointers and multiplier maps can
// remain shared with their readers, so callers treat this as a sequence of
// observations rather than one consistent independently mutable database
// snapshot.
func (s *UserAdmin) ListUsers(ctx context.Context, page, pageSize int, filters UserListFilters, sortBy, sortOrder string) ([]User, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	users, result, err := s.Users.ListWithFilters(ctx, params, filters)
	if err != nil {
		return nil, 0, err
	}
	if len(users) > 0 {
		userIDs := make([]int64, 0, len(users))
		for i := range users {
			userIDs = append(userIDs, users[i].ID)
		}
		lastUsedByUserID, latestErr := s.Users.GetLatestUsedAtByUserIDs(ctx, userIDs)
		// Base account/page loading succeeded, so diagnose unavailable usage
		// enrichment and leave each returned account's earlier activity field
		// rather than rejecting the whole list.
		if latestErr != nil {
			s.Observer.Printf("service.admin", "failed to load user last_used_at in batch: err=%v", latestErr)
		} else {
			for i := range users {
				users[i].LastUsedAt = lastUsedByUserID[users[i].ID]
			}
		}
	}
	// Prefer a bulk multiplier observation when supported; a failed or absent
	// capability selects per-account reads so usable profile rows can still
	// retain available rate data.
	if s.Rates != nil && len(users) > 0 {
		if batchRepo, ok := s.Rates.(AdminGroupRateBatchReader); ok {
			userIDs := make([]int64, 0, len(users))
			for i := range users {
				userIDs = append(userIDs, users[i].ID)
			}
			ratesByUser, err := batchRepo.GetByUserIDs(ctx, userIDs)
			if err != nil {
				s.Observer.Printf("service.admin", "failed to load user group rates in batch: err=%v", err)
				s.AdminLoadUserGroupRatesOneByOne(ctx, users)
			} else {
				for i := range users {
					if rates, ok := ratesByUser[users[i].ID]; ok {
						users[i].GroupRates = rates
					}
				}
			}
		} else {
			s.AdminLoadUserGroupRatesOneByOne(ctx, users)
		}
	}
	return users, result.Total, nil
}

// AdminLoadUserGroupRatesOneByOne fills group multipliers on the supplied
// user slice through individual reads. Missing Rates skips work and read
// failures are diagnosed while retaining that account's earlier map; callers
// keep this mutable slice stable during hydration.
func (s *UserAdmin) AdminLoadUserGroupRatesOneByOne(ctx context.Context, users []User) {
	if s.Rates == nil {
		return
	}
	for i := range users {
		rates, err := s.Rates.GetByUserID(ctx, users[i].ID)
		// An unreadable multiplier map cannot replace this user's current
		// observation; diagnose that account's failure and continue enriching the
		// other returned rows.
		if err != nil {
			s.Observer.Printf("service.admin", "failed to load user group rates: user_id=%d err=%v", users[i].ID, err)
			continue
		}
		users[i].GroupRates = rates
	}
}

// GetUser reads a current account and enriches it with usage activity and
// optional group multipliers. Base account errors abort; enrichment errors
// remain diagnostic, preserving existing fields, while returned
// maps/timestamp pointers follow their reader's sharing behavior.
func (s *UserAdmin) GetUser(ctx context.Context, id int64) (*User, error) {
	user, err := s.Users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	lastUsedAt, latestErr := s.Users.GetLatestUsedAtByUserID(ctx, id)
	// The account read already succeeded; diagnose activity-summary failure and
	// retain its earlier timestamp so optional enrichment does not reject the
	// profile result.
	if latestErr != nil {
		s.Observer.Printf("service.admin", "failed to load user last_used_at: user_id=%d err=%v", id, latestErr)
	} else {
		user.LastUsedAt = lastUsedAt
	}

	if s.Rates != nil {
		rates, err := s.Rates.GetByUserID(ctx, id)
		// Optional multiplier lookup failed after account loading; keep the
		// existing map and return the account while diagnostics retain the failed
		// enrichment observation.
		if err != nil {
			s.Observer.Printf("service.admin", "failed to load user group rates: user_id=%d err=%v", id, err)
		} else {
			user.GroupRates = rates
		}
	}
	return user, nil
}

// GetUserIncludeDeleted delegates account lookup including soft-deleted rows
// for administrator history. It returns repository errors unchanged and does
// not perform GetUser's optional activity/rate enrichment, so callers use its
// returned audit fields as supplied.
func (s *UserAdmin) GetUserIncludeDeleted(ctx context.Context, id int64) (*User, error) {
	return s.Users.GetByIDIncludeDeleted(ctx, id)
}

// AdminNormalizeUserRole accepts exact admin or user text and returns an
// error for other nonempty input. Empty text returns fallback as supplied
// without revalidating it; callers select that fallback from their creation
// or existing-account context.
func AdminNormalizeUserRole(role, fallback string) (string, error) {
	if role == "" {
		return fallback, nil
	}
	if role != RoleAdmin && role != RoleUser {
		return "", fmt.Errorf("invalid role: %q (must be %s or %s)", role, RoleAdmin, RoleUser)
	}
	return role, nil
}

// CreateUser saves a planned account from required administrator input and
// user storage. Omitted balance reads current defaults or zero, while
// explicit balance zero is retained; Key allowance starts from the
// current/default limit and accepts a valid supplied zero as unlimited. Other
// numeric limits come from the input.
//
// Role validation and password hashing precede persistence, with the optional
// normalized-mailbox switch selecting the guarded create method. The supplied
// group slices remain shared with the working account through creation. A
// successful repository return precedes privilege diagnostics and optional
// default plan assignment; failed plan assignment is diagnostic and does not
// remove the created account. HTTP callers supply administrative
// authorization and shaped credential data.
func (s *UserAdmin) CreateUser(ctx context.Context, input *CreateUserInput) (*User, error) {
	balance := 0.0
	if input.Balance != nil {
		balance = *input.Balance
	} else if s.Settings != nil {
		balance = s.Settings.GetDefaultBalance(ctx)
	}
	apiKeyLimit := DefaultUserAPIKeyLimit
	if s.Settings != nil {
		apiKeyLimit = s.Settings.GetDefaultUserAPIKeyLimit(ctx)
	}
	if input.APIKeyLimit != nil {
		if !IsValidUserAPIKeyLimit(*input.APIKeyLimit) {
			return nil, ErrUserAPIKeyLimitInvalid
		}
		apiKeyLimit = *input.APIKeyLimit
	}

	role, err := AdminNormalizeUserRole(input.Role, RoleUser)
	if err != nil {
		return nil, err
	}

	user := &User{
		Email:                input.Email,
		Username:             input.Username,
		Notes:                input.Notes,
		Role:                 role,
		Balance:              balance,
		Concurrency:          input.Concurrency,
		RPMLimit:             input.RPMLimit,
		APIKeyLimit:          apiKeyLimit,
		Status:               StatusActive,
		AllowedGroups:        input.AllowedGroups,
		DisabledPublicGroups: input.DisabledPublicGroups,
	}
	if err := user.SetPassword(input.Password); err != nil {
		return nil, err
	}
	// Choose the normalized-mailbox write guard from current policy before
	// persistence; the working group slices and selected creation amounts remain
	// the caller's stable input.
	createUser := s.Users.Create
	if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
		normalizedEmail := NormalizeRegistrationEmailAddress(user.Email)
		if normalizedEmail != "" {
			createUser = func(createCtx context.Context, createUser *User) error {
				return s.Users.CreateWithNormalizedEmailGuard(createCtx, createUser, normalizedEmail)
			}
		}
	}
	if err := createUser(ctx, user); err != nil {
		return nil, err
	}
	if user.Role == RoleAdmin {
		s.Observer.Printf("service.admin", "audit: admin user created actor_admin_id=%d target_user_id=%d",
			input.ActorAdminID, user.ID)
	}
	// Account persistence has returned successfully before optional plan
	// assignment; later assignment failures are diagnosed and do not delete the
	// created account here.
	s.AdminAssignDefaultSubscriptions(ctx, user.ID)
	return user, nil
}

// AdminEnsureNotLastAdmin rejects demotion when the observed undeleted
// administrator count is absent or at most one. It requests one row without
// subscriptions but uses pagination Total for the full role count; disabled
// administrators still participate because no status filter is supplied.
//
// A failed count returns a wrapped error. The count is separate from
// UpdateUser's later role write, so concurrent demotions can both pass and
// this check does not reserve a remaining active administrator. Callers
// retain that limitation when assessing role-management policy rather than
// treating success as a transactional last-administrator guarantee.
func (s *UserAdmin) AdminEnsureNotLastAdmin(ctx context.Context) error {
	noSubs := false
	_, result, err := s.Users.ListWithFilters(ctx,
		pagination.PaginationParams{Page: 1, PageSize: 1},
		UserListFilters{Role: RoleAdmin, IncludeSubscriptions: &noSubs},
	)
	if err != nil {
		return fmt.Errorf("count admin users: %w", err)
	}
	// This role-only count needs another record before admitting administrator demotion.
	if result == nil || result.Total <= 1 {
		return errors.New("cannot demote the last admin user")
	}
	return nil
}

// AdminAssignDefaultSubscriptions requests current default plans for an
// already created positive account when settings and assigner exist. Each
// plan carries the same account and administrator-default notes, and
// successful entitlement/pending results are discarded.
//
// Individual assignment failure is diagnosed and later plans continue. These
// calls do not use authentication's per-plan savepoint wrapper, so actual
// transaction participation and recovery belong to the assigner and
// surrounding caller. Earlier completed grants can remain when later ones
// fail; the method returns no aggregate result and does not undo account
// creation.
func (s *UserAdmin) AdminAssignDefaultSubscriptions(ctx context.Context, userID int64) {
	if s.Settings == nil || s.Subscriptions == nil || userID <= 0 {
		return
	}
	items := s.Settings.GetDefaultSubscriptions(ctx)
	// Attempt current default plans after account creation; diagnose individual
	// failures and continue, leaving transaction participation and any already
	// completed grant to the assigner.
	for _, item := range items {
		if _, _, err := s.Subscriptions.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID: userID,
			PlanID: item.PlanID,
			Notes:  "auto assigned by default user subscriptions setting",
		}); err != nil {
			s.Observer.Printf("service.admin", "failed to assign default subscription: user_id=%d plan_id=%d err=%v", userID, item.PlanID, err)
		}
	}
}

// UpdateUser applies administrator-selected account fields through a mask and
// then requests separate multiplier/history effects. Required input/user
// storage supplies the working account; nonpositive nonnil multipliers are
// rejected before lookup, and the currently admin account cannot receive
// literal disabled even when a role change was also submitted. Personal recharge
// overrides must be finite and positive; clearing and setting cannot be combined.
//
// Credential changes select applicable mailbox guards, while role demotion
// checks a separately observed administrator count. Working fields and
// referenced slices can change before later validation or persistence
// failure. Balance input is not read here. After successful account return,
// multiplier sync failure is diagnostic, selected authentication changes
// request optional invalidation, and a concurrency difference requests
// history whose failure is also diagnosed. The core does not reload
// multiplier data after synchronization, so callers distinguish that side
// effect from the returned account view.
func (s *UserAdmin) UpdateUser(ctx context.Context, id int64, input *UpdateUserInput) (*User, error) {
	if input.BalanceRechargeMultiplier != nil &&
		(input.ClearBalanceRechargeMultiplier || !IsValidBalanceRechargeMultiplier(*input.BalanceRechargeMultiplier)) {
		return nil, ErrBalanceRechargeMultiplierInvalid
	}
	if input.GroupRates != nil {
		for groupID, rate := range input.GroupRates {
			if rate != nil && *rate <= 0 {
				return nil, fmt.Errorf("rate_multiplier must be > 0 (group_id=%d)", groupID)
			}
		}
	}

	user, err := s.Users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if user.Role == "admin" && input.Status == "disabled" {
		return nil, errors.New("cannot disable admin user")
	}

	oldConcurrency := user.Concurrency
	oldStatus := user.Status
	oldRole := user.Role
	normalizedEmail := ""
	emailChanged := false
	oldRPMLimit := user.RPMLimit
	oldAllowedGroups := append([]int64(nil), user.AllowedGroups...)
	oldDisabledPublicGroups := append([]int64(nil), user.DisabledPublicGroups...)

	// Select submitted columns/relations for the working snapshot, keeping
	// unrelated balance and admission fields written by other requests out of
	// this account update.
	var fields UserUpdateFields
	if input.ClearBalanceRechargeMultiplier {
		user.BalanceRechargeMultiplier = nil
		fields.BalanceRechargeMultiplier = true
	} else if input.BalanceRechargeMultiplier != nil {
		multiplier := *input.BalanceRechargeMultiplier
		user.BalanceRechargeMultiplier = &multiplier
		fields.BalanceRechargeMultiplier = true
	}

	if input.Email != "" {
		emailChanged = input.Email != user.Email
		if emailChanged {
			if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
				normalizedEmail = NormalizeRegistrationEmailAddress(input.Email)
			}
			if normalizedEmail == "" {
				exists, err := s.Users.ExistsByEmail(ctx, input.Email)
				if err != nil {
					return nil, err
				}
				if exists {
					return nil, ErrEmailExists
				}
			}
		}
		user.Email = input.Email
		fields.Email = true
	}
	if input.Password != "" {
		if err := user.SetPassword(input.Password); err != nil {
			return nil, err
		}
		fields.PasswordHash = true
	}

	if input.Username != nil {
		user.Username = *input.Username
		fields.Username = true
	}
	if input.Notes != nil {
		user.Notes = *input.Notes
		fields.Notes = true
	}

	if input.Status != "" {
		user.Status = input.Status
		fields.Status = true
	}

	if input.Role != "" {
		role, err := AdminNormalizeUserRole(input.Role, user.Role)
		if err != nil {
			return nil, err
		}
		// Demotion requests a fresh administrator count before role persistence;
		// this service check remains separate from that write, while the HTTP
		// caller handles acting-user self-demotion.
		if user.Role == RoleAdmin && role == RoleUser {
			if err := s.AdminEnsureNotLastAdmin(ctx); err != nil {
				return nil, err
			}
		}
		user.Role = role
		fields.Role = true
	}

	if input.Concurrency != nil {
		user.Concurrency = *input.Concurrency
		fields.Concurrency = true
	}

	if input.RPMLimit != nil {
		user.RPMLimit = *input.RPMLimit
		fields.RPMLimit = true
	}
	if input.APIKeyLimit != nil {
		if !IsValidUserAPIKeyLimit(*input.APIKeyLimit) {
			return nil, ErrUserAPIKeyLimitInvalid
		}
		user.APIKeyLimit = *input.APIKeyLimit
		fields.APIKeyLimit = true
	}

	if input.AllowedGroups != nil {
		user.AllowedGroups = *input.AllowedGroups
		fields.AllowedGroups = true
	}
	if input.DisabledPublicGroups != nil {
		user.DisabledPublicGroups = *input.DisabledPublicGroups
		fields.DisabledPublicGroups = true
	}

	updateUser := s.Users.Update
	if emailChanged && normalizedEmail != "" {
		updateUser = func(updateCtx context.Context, updateUser *User, updateFields UserUpdateFields) error {
			return s.Users.UpdateWithNormalizedEmailGuard(updateCtx, updateUser, normalizedEmail, updateFields)
		}
	}

	if err := updateUser(ctx, user, fields); err != nil {
		return nil, err
	}

	if user.Role != oldRole {
		s.Observer.Printf("service.admin", "audit: user role changed actor_admin_id=%d target_user_id=%d old_role=%s new_role=%s",
			input.ActorAdminID, user.ID, oldRole, user.Role)
	}

	// Persist selected account fields before separate multiplier
	// synchronization; sync failure is diagnostic and subsequent
	// invalidation/history decisions still use the submitted state.
	if input.GroupRates != nil && s.Rates != nil {
		if err := s.Rates.SyncUserGroupRates(ctx, user.ID, input.GroupRates); err != nil {
			s.Observer.Printf("service.admin", "failed to sync user group rates: user_id=%d err=%v", user.ID, err)
		}
	}

	if s.Invalidator != nil {
		// Account admission fields and group permission sets drive cached
		// authentication; request publication after the write, including submitted
		// multiplier sync even when that optional sync failed.
		if user.Concurrency != oldConcurrency ||
			user.Status != oldStatus ||
			user.Role != oldRole ||
			user.RPMLimit != oldRPMLimit ||
			!AdminSameInt64Set(user.AllowedGroups, oldAllowedGroups) ||
			!AdminSameInt64Set(user.DisabledPublicGroups, oldDisabledPublicGroups) ||
			input.GroupRates != nil {
			s.Invalidator.InvalidateAuthCacheByUserID(ctx, user.ID)
		}
	}

	// Record a changed concurrency allowance after persistence and cache
	// requests; history failure stays diagnostic while the selected account
	// limit remains the update result.
	concurrencyDiff := user.Concurrency - oldConcurrency
	if concurrencyDiff != 0 {
		// Express the concurrency difference as the numeric amount used by
		// adjustment history.
		if err := s.Records.RecordAdjustment(ctx, user.ID, AdjustmentTypeAdminConcurrency, float64(concurrencyDiff), ""); err != nil {
			s.Observer.Printf("service.admin", "failed to create concurrency adjustment redeem code: %v", err)
		}
	}

	return user, nil
}

// AdminSameInt64Set compares the positive ID sets represented by two slices.
// It discards nonpositive IDs and duplicate values, sorts fresh working
// slices and treats nil, empty and invalid-only inputs as the same empty set.
//
// UpdateUser uses this result to select cache publication for group
// permissions, whose meaning depends on membership rather than list order.
// The helper leaves input slices unchanged and does not decide whether
// submitted groups should be persisted or validated; the selected account
// field mask and repository still own those writes.
func AdminSameInt64Set(a, b []int64) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	// Permission equality ignores order, duplicate and nonpositive IDs before
	// cache selection; fresh working slices keep that comparison from changing
	// either submitted relation list.
	normalize := func(values []int64) []int64 {
		seen := make(map[int64]struct{}, len(values))
		out := make([]int64, 0, len(values))
		for _, value := range values {
			if value <= 0 {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
		slices.Sort(out)
		return out
	}
	left := normalize(a)
	right := normalize(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// BatchUpdateConcurrency deduplicates positive account IDs and delegates
// exact set or add mode. Empty usable IDs returns zero before mode
// validation; PostgreSQL replacement and additive operations apply their own
// zero floors.
//
// A failed repository update returns before publication. After success, every
// cleaned input ID receives optional account invalidation, even when the
// affected-row count is smaller than the input set. The returned count
// describes the repository write and does not confirm cache propagation or
// record per-account adjustment history; callers keep those effects separate.
func (s *UserAdmin) BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error) {
	cleaned := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		cleaned = append(cleaned, userID)
	}
	if len(cleaned) == 0 {
		return 0, nil
	}

	var affected int
	var err error
	switch mode {
	case "set":
		affected, err = s.Users.BatchSetConcurrency(ctx, cleaned, value)
	case "add":
		affected, err = s.Users.BatchAddConcurrency(ctx, cleaned, value)
	default:
		return 0, errors.New("invalid mode: must be 'set' or 'add'")
	}
	if err != nil {
		return 0, err
	}

	if s.Invalidator != nil {
		// The batch write returned successfully; publish every cleaned account ID
		// because this result count does not identify which cached account
		// observations actually changed.
		for _, userID := range cleaned {
			s.Invalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
	}
	return affected, nil
}

// BatchUpdateLimits requires a supplied concurrency or RPM pointer,
// deduplicates positive IDs and delegates the selected limits together. Empty
// usable IDs returns zero without writing; PostgreSQL floors negative
// selected values and retains nil fields.
//
// Repository failure returns before optional invalidation. A successful
// return publishes every cleaned input ID regardless of the affected count,
// then returns that count unchanged. Callers keep the input pointers stable
// during the write and distinguish selected values, actual affected rows and
// callback propagation rather than treating the count as a per-account
// completion list.
func (s *UserAdmin) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	if concurrency == nil && rpmLimit == nil {
		return 0, fmt.Errorf("at least one of concurrency or rpm_limit is required")
	}

	cleaned := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		cleaned = append(cleaned, userID)
	}
	if len(cleaned) == 0 {
		return 0, nil
	}

	affected, err := s.Users.BatchUpdateLimits(ctx, cleaned, concurrency, rpmLimit)
	if err != nil {
		return 0, err
	}
	if s.Invalidator != nil {
		// The selected limit write returned successfully; publish every cleaned
		// input account, leaving the affected count separate from which
		// authentication snapshots received a change request.
		for _, userID := range cleaned {
			s.Invalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
	}
	return affected, nil
}

// UpdateUserBalance applies exact set, add or subtract through required
// atomic balance storage, then reads the current account. Negative-result
// errors use the adjuster's companion before/after observations; other errors
// remain errors, and successful funding can still precede a failed account
// readback.
//
// After a usable account result, nonzero difference requests optional
// authentication invalidation and adjustment history. Positive add can also
// request a policy-enabled affiliate rebate, whose failure is diagnostic.
// Optional funding-cache cleanup runs through the selected background runner
// with a fresh five-second context, while history errors are diagnosed after
// persistence. The returned account was read before those later effects;
// callers retain that ordering and do not infer rollback from a subsequent
// read or auxiliary failure.
func (s *UserAdmin) UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*User, error) {
	// Choose the adjuster's atomic delta/replacement operation instead of
	// writing a read balance snapshot; successful funding precedes the account
	// read and later auxiliary effects.
	var (
		change BalanceChange
		err    error
	)
	switch operation {
	case "set":
		change, err = s.Balances.SetBalance(ctx, userID, balance)
	case "add":
		change, err = s.Balances.AdjustBalance(ctx, userID, balance)
	case "subtract":
		change, err = s.Balances.AdjustBalance(ctx, userID, -balance)
	default:
		return nil, fmt.Errorf("unsupported balance operation: %q", operation)
	}
	// Report rejected balance observations at two-decimal USD display precision;
	// the formatted error exposes companion amounts and does not wrap
	// ErrBalanceNegative for errors.Is.
	if errors.Is(err, ErrBalanceNegative) {
		return nil, fmt.Errorf("balance cannot be negative, current balance: %.2f, requested operation would result in: %.2f", change.Old, change.New)
	}
	if err != nil {
		return nil, err
	}

	user, err := s.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	balanceDiff := change.New - change.Old
	if s.Invalidator != nil && balanceDiff != 0 {
		s.Invalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	s.AdminTryAccrueAffiliateRebateForAdminRecharge(ctx, userID, operation, balance)

	// Funding has already completed; schedule optional cache cleanup
	// independently of request cancellation, while a rejected configured runner
	// supplies no automatic synchronous retry.
	if s.BalanceCache != nil {
		s.RunBackground("service/admin_user.go:UpdateUserBalance", func() {
			// Give post-write funding-cache cleanup a fresh five-second budget and
			// release its timer afterward; cleanup failure is diagnosed without
			// undoing the account adjustment.
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.BalanceCache.InvalidateUserBalance(cacheCtx, userID); err != nil {
				s.Observer.Printf("service.admin", "invalidate user balance cache failed: user_id=%d err=%v", userID, err)
			}
		})
	}

	// Save history from the adjuster's successful before/after difference, after
	// optional cache/rebate requests; a history error remains diagnostic for the
	// already completed funding change.
	if balanceDiff != 0 {
		if err := s.Records.RecordAdjustment(ctx, user.ID, AdjustmentTypeAdminBalance, balanceDiff, notes); err != nil {
			s.Observer.Printf("service.admin", "failed to create balance adjustment redeem code: %v", err)
		}
	}

	return user, nil
}

// AdminTryAccrueAffiliateRebateForAdminRecharge requests rebate only for
// exact add mode with a positive amount and configured enabled
// policy/service. It runs after balance adjustment; failure is diagnosed, and
// a positive returned rebate is logged without changing the completed
// recharge result.
func (s *UserAdmin) AdminTryAccrueAffiliateRebateForAdminRecharge(ctx context.Context, userID int64, operation string, amount float64) {
	if operation != "add" || amount <= 0 || s.Settings == nil || s.Affiliates == nil {
		return
	}
	if !s.Settings.IsAffiliateAdminRechargeEnabled(ctx) {
		return
	}

	// The positive add recharge already persisted before this optional rebate
	// request; diagnose accrual failure and keep the primary funding result in
	// place.
	rebate, err := s.Affiliates.AccrueInviteRebate(ctx, userID, amount)
	// Keep fractional USD detail in rebate diagnostics; these eight-decimal
	// formats affect message text while the earlier primary funding write keeps
	// its stored amount.
	if err != nil {
		s.Observer.Printf("service.admin", "affiliate rebate failed for admin recharge: user_id=%d amount=%.8f err=%v", userID, amount, err)
		return
	}
	if rebate > 0 {
		s.Observer.Printf("service.admin", "affiliate rebate accrued for admin recharge: user_id=%d amount=%.8f rebate=%.8f", userID, amount, rebate)
	}
}

// GetUserRPMStatus combines current account limits with cached usage and
// primary groups from the first thousand Key observations. Missing RPM
// returns its availability error; account or Key reads remain fatal errors,
// while count/metadata/override errors are diagnosed and retain available
// companion observations.
//
// Positive group IDs are deduplicated and sorted before display. Readable
// group metadata selects its ceiling/source, then a readable nonnil account
// override replaces that choice, including zero. Absent metadata and override
// can leave empty source/name with zero ceiling. These independent reads
// provide administrator status observations rather than a synchronized
// admission decision or full account Key catalogue.
func (s *UserAdmin) GetUserRPMStatus(ctx context.Context, userID int64) (*UserRPMStatus, error) {
	if s.RPM == nil {
		return nil, ErrRPMStatusUnavailable
	}

	user, err := s.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	userRPMUsed, err := s.RPM.GetUserRPM(ctx, userID)
	// A failed count read still supplies its companion observation; retain it
	// for status while the readable account limit and later group observations
	// remain available.
	if err != nil {
		s.Observer.Printf("service.admin", "failed to get user rpm: user_id=%d err=%v", userID, err)
	}

	// Use the first thousand Key observations for display groups; discard total
	// count and keep this bounded sample separate from a complete account
	// credential catalogue.
	keys, _, err := s.Keys.List(ctx, userID, 1, 1000, "", "")
	if err != nil {
		return nil, err
	}

	groupIDSet := make(map[int64]struct{})
	for _, key := range keys {
		if key.GroupID != nil && *key.GroupID > 0 {
			groupIDSet[*key.GroupID] = struct{}{}
		}
	}

	groupIDs := make([]int64, 0, len(groupIDSet))
	for groupID := range groupIDSet {
		groupIDs = append(groupIDs, groupID)
	}
	slices.Sort(groupIDs)

	var perGroup []UserGroupRPMStatus
	for _, groupID := range groupIDs {
		used, getErr := s.RPM.GetUserGroupRPM(ctx, userID, groupID)
		// These groups were already selected from Key observations; count-read
		// failure retains the companion value rather than removing the group before
		// metadata/override selection.
		if getErr != nil {
			s.Observer.Printf("service.admin", "failed to get user group rpm: user_id=%d group_id=%d err=%v", userID, groupID, getErr)
		}

		entry := UserGroupRPMStatus{
			GroupID: groupID,
			Used:    used,
		}

		// Optional group metadata fills display name and default ceiling; missing
		// or failed data leaves initial fields available for a later account
		// override to replace.
		if s.Groups != nil {
			if group, groupErr := s.Groups.GetByIDLite(ctx, groupID); groupErr == nil && group != nil {
				entry.GroupName = group.Name
				entry.Limit = group.RPMLimit
				entry.Source = "group"
			} else if groupErr != nil {
				s.Observer.Printf("service.admin", "failed to get group rpm status metadata: group_id=%d err=%v", groupID, groupErr)
			}
		}

		// A readable nonnil account override supersedes group metadata, including
		// zero; failed or absent override lookup leaves the earlier selected
		// ceiling/source observation.
		if s.Rates != nil {
			override, overrideErr := s.Rates.GetRPMOverrideByUserAndGroup(ctx, userID, groupID)
			if overrideErr != nil {
				s.Observer.Printf("service.admin", "failed to get rpm override: user_id=%d group_id=%d err=%v", userID, groupID, overrideErr)
			} else if override != nil {
				entry.Limit = *override
				entry.Source = "override"
			}
		}

		perGroup = append(perGroup, entry)
	}

	return &UserRPMStatus{
		UserRPMUsed:  userRPMUsed,
		UserRPMLimit: user.RPMLimit,
		PerGroup:     perGroup,
	}, nil
}

// GetUserUsageStats returns fixed zero placeholder metrics with the supplied
// period label. It ignores account ID and context, queries no usage data and
// returns success, so callers cannot treat this response as measured account
// activity.
func (s *UserAdmin) GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error) {
	return map[string]any{
		"period":          period,
		"total_requests":  0,
		"total_cost":      0.0,
		"total_tokens":    0,
		"avg_duration_ms": 0,
	}, nil
}

// GetUserBalanceHistory delegates paginated funding-history reads to required
// Records with the supplied code type. It preserves returned records, counts,
// aggregate amount and errors; this operation performs no account funding
// mutation.
func (s *UserAdmin) GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]RedeemCode, int64, float64, error) {
	return s.Records.GetUserBalanceHistory(ctx, userID, page, pageSize, codeType)
}

// AdminCompatibleAdminAuthIdentityProviderKeys returns the trimmed requested
// namespace and WeChat compatibility candidates. Blank input stays as a
// one-element blank list; WeChat adds current/legacy keys when distinct under
// case-insensitive comparison, while supplied key case remains unchanged.
func AdminCompatibleAdminAuthIdentityProviderKeys(providerType, providerKey string) []string {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	providerKey = strings.TrimSpace(providerKey)
	if providerKey == "" {
		return []string{providerKey}
	}
	if providerType != "wechat" {
		return []string{providerKey}
	}

	// Keep the supplied namespace case while adding distinct WeChat
	// compatibility candidates; callers pair this lookup list with their
	// separately selected write namespace.
	keys := []string{providerKey}
	if !strings.EqualFold(providerKey, "wechat-main") {
		keys = append(keys, "wechat-main")
	}
	if !strings.EqualFold(providerKey, "wechat") {
		keys = append(keys, "wechat")
	}
	return keys
}

// AdminCanonicalAdminAuthIdentityProviderKey chooses the namespace to write
// from requested/existing keys. Non-WeChat providers prefer a nonblank
// request, then existing text. WeChat selects wechat-main for a recognized
// existing legacy/current key or a requested current key.
//
// A requested legacy wechat key with no existing key remains that requested
// key; BindUserAuthIdentity supplies the empty existing argument before
// storage lookup. Other nonblank requests retain their case/text, and blank
// requests retain the existing key. Callers keep compatibility lookup
// candidates aligned with this selected write value; the helper itself
// performs no ownership read or relationship migration.
func AdminCanonicalAdminAuthIdentityProviderKey(providerType, existingKey, requestedKey string) string {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	existingKey = strings.TrimSpace(existingKey)
	requestedKey = strings.TrimSpace(requestedKey)
	if providerType != "wechat" {
		if requestedKey != "" {
			return requestedKey
		}
		return existingKey
	}
	// Recognized existing legacy/current state selects the current namespace; a
	// legacy request with no existing key follows the later requested-text
	// branch instead.
	if strings.EqualFold(existingKey, "wechat") || strings.EqualFold(existingKey, "wechat-main") || strings.EqualFold(requestedKey, "wechat-main") {
		return "wechat-main"
	}
	if requestedKey != "" {
		return requestedKey
	}
	return existingKey
}

// AdminAdminAuthIdentityProviderKeyRank ranks WeChat current namespace first,
// custom namespace second and legacy namespace last after trimmed
// case-insensitive comparison. Other provider types return zero, leaving tie
// selection to the caller's record order.
func AdminAdminAuthIdentityProviderKeyRank(providerType, providerKey string) int {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	providerKey = strings.TrimSpace(providerKey)
	if providerType != "wechat" {
		return 0
	}
	switch {
	case strings.EqualFold(providerKey, "wechat-main"):
		return 0
	case strings.EqualFold(providerKey, "wechat"):
		return 2
	default:
		return 1
	}
}

// AdminNormalizeAdminBindChannelInput trims channel/application/subject into
// a new struct and copies metadata through the administrator JSON helper. Nil
// input or any blank required field returns nil; callers distinguish omitted
// channel from invalid supplied channel before persistence.
func AdminNormalizeAdminBindChannelInput(input *AdminBindAuthIdentityChannelInput) *AdminBindAuthIdentityChannelInput {
	if input == nil {
		return nil
	}
	channel := &AdminBindAuthIdentityChannelInput{
		Channel:        strings.TrimSpace(input.Channel),
		ChannelAppID:   strings.TrimSpace(input.ChannelAppID),
		ChannelSubject: strings.TrimSpace(input.ChannelSubject),
		Metadata:       AdminCloneAdminAuthIdentityMetadata(input.Metadata),
	}
	if channel.Channel == "" || channel.ChannelAppID == "" || channel.ChannelSubject == "" {
		return nil
	}
	return channel
}

// AdminNormalizeAdminAuthIdentityProviderType returns a supported
// administrative provider label after trimming/lowercasing. Email, LinuxDo,
// OIDC, WeChat and DingTalk are accepted; other labels return empty text and
// are rejected by binding input validation.
func AdminNormalizeAdminAuthIdentityProviderType(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "email":
		return "email"
	case "linuxdo":
		return "linuxdo"
	case "oidc":
		return "oidc"
	case "wechat":
		return "wechat"
	case "dingtalk":
		return "dingtalk"
	default:
		return ""
	}
}

// AdminCloneAdminAuthIdentityMetadata copies metadata through JSON when both
// conversion steps succeed. Nil input returns nil and empty input returns an
// allocated empty map; a successful round trip produces decoded values whose
// numbers/types follow encoding/json's representation.
//
// Marshal or unmarshal failure selects an outer-map copy with shared nested
// references instead. Callers therefore do not assume every result is an
// independent deep copy or preserves concrete Go value types. The channel
// normalizer and binding response builder use this same copy rule and retain
// stable input data while conversion runs.
func AdminCloneAdminAuthIdentityMetadata(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	if len(input) == 0 {
		return map[string]any{}
	}
	// Try JSON conversion for independently decoded metadata; marshal or
	// unmarshal failure falls back to an outer copy, retaining nested references
	// and their caller-owned stability.
	data, err := json.Marshal(input)
	if err != nil {
		out := make(map[string]any, len(input))
		maps.Copy(out, input)
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		out = make(map[string]any, len(input))
		maps.Copy(out, input)
	}
	return out
}
