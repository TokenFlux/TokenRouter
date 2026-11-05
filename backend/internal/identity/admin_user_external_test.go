package identity_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// balanceUserRepoStub models balance primitive selection on one shared
// account for sequential administrator tests.
type balanceUserRepoStub struct {
	// userRepoStub supplies the shared account used by balance selection.
	// Fixture callers initialize this pointer before reads; apply returns
	// missing-account error when it is absent.
	*userRepoStub
	adjustErr error                    // configured balance operation error
	changes   []identity.BalanceChange // successful fixture balance-change observations
}

// balanceRedeemRepoStub records adjustment-code creation while leaving other
// redemption operations unavailable.
type balanceRedeemRepoStub struct {
	// RedeemCodeRepository remains nil for unused methods. This fixture
	// overrides creation/usage delegation, so other promoted repository
	// operations panic if a scenario reaches them.
	billing.RedeemCodeRepository
	// created retains shallow copies of adjustment codes. Their pointer fields
	// remain shared, so tests treat those submitted nested values as stable.
	created []*billing.RedeemCode
}

// authCacheInvalidatorStub records administrator account, group and
// credential publication requests.
type authCacheInvalidatorStub struct {
	userIDs  []int64  // requested account invalidation identifiers
	groupIDs []int64  // requested group invalidation identifiers
	keys     []string // requested credential invalidation values
}

// adminRechargeAffiliateAccruerStub records optional recharge rebate requests
// and returns configured results.
type adminRechargeAffiliateAccruerStub struct {
	calls  []adminRechargeAffiliateAccrual // observed rebate request arguments
	rebate float64                         // configured rebate result in USD
	err    error                           // configured rebate request error
}

// adminRechargeAffiliateAccrual contains the recorded account and amount for
// one rebate request.
type adminRechargeAffiliateAccrual struct {
	userID int64   // recharged fixture account identifier
	amount float64 // USD
}

// rechargeSettingsFixture supplies the promotion runtime switch needed by
// administrator recharge tests.
type rechargeSettingsFixture struct {
	// AdminUserSettings remains nil for unused policy methods. Recharge
	// scenarios invoke the overridden rebate switch, while other promoted
	// setting reads would panic.
	identity.AdminUserSettings
	runtime *promotion.RuntimeSettings // runtime recharge rebate policy reader
}

// batchLimitsUserRepoStub records selected batch limit requests without
// changing persistent account state.
type batchLimitsUserRepoStub struct {
	// UserRepository remains nil for unused account methods. The fixture
	// overrides batch limit recording, so unrelated promoted reads or writes
	// panic if invoked.
	identity.UserRepository
	calls   int     // observed batch update count
	userIDs []int64 // copied selected account identifiers
	// concurrency and rpmLimit retain independently copied submitted pointers.
	// Nil preserves omission for its own limit, while a nonnil zero remains an
	// explicit value recorded before the configured result.
	concurrency *int  // copied selected concurrency value
	rpmLimit    *int  // copied selected RPM value
	affected    int   // configured affected account count
	err         error // configured batch update error
}

// batchLimitsInvalidator records account callbacks and rejects credential
// callbacks in batch-limit scenarios.
type batchLimitsInvalidator struct {
	userIDs []int64 // requested account invalidation identifiers
}

// adminCreationSettingsStore supplies shared missing-setting errors to
// administrator runtime/grant readers.
type adminCreationSettingsStore struct {
	// authSourceDefaultsRepoStub retains the setting map and unused-operation
	// guards. This wrapper changes only missing-key reads to the shared settings
	// error identity.
	authSourceDefaultsRepoStub
}

// adminCreationSettings combines retained runtime and grant readers for
// administrator creation scenarios.
type adminCreationSettings struct {
	// RuntimeSettings and GrantSettings read the same retained creation store
	// through different policy/default methods. Test changes to that shared
	// setting map are visible to both readers.
	// RuntimeSettings is retained over the creation fixture store. Current
	// switch and Key-default reads share that store throughout administrator
	// creation scenarios.
	*identity.RuntimeSettings
	// GrantSettings is retained over the same creation fixture store.
	// Amount/plan reads use supplied startup fallbacks when stored values are
	// unavailable or unusable.
	*identity.GrantSettings
}

// userRepoStubForListUsers supplies shallow account pages and configured
// usage activity observations.
type userRepoStubForListUsers struct {
	// userRepoStub supplies inherited account operations outside the list
	// overrides. Its fixture state is shared by those delegates and is
	// configured before service calls.
	userRepoStub
	// users supplies fixture account values for list copying. Nested maps,
	// slices and pointers retain sharing with the returned shallow account
	// values.
	users                 []identity.User
	err                   error                       // configured account listing error
	listWithFiltersParams pagination.PaginationParams // observed list pagination selections
	// lastUsedByUserID retains shared optional timestamp pointers. Reads select
	// requested entries without copying pointed-to time values before profile
	// hydration.
	lastUsedByUserID map[int64]*time.Time
	lastUsedErr      error // configured usage activity error
}

// userGroupRateRepoStubForListUsers supplies bulk and individual multiplier
// observations for list enrichment.
type userGroupRateRepoStubForListUsers struct {
	batchCalls int     // observed bulk read count
	singleCall []int64 // observed individual account identifiers

	batchErr error // configured bulk read error
	// batchData retains the complete configured multiplier map. Successful bulk
	// reads return it directly, so tests keep outer and nested storage stable.
	batchData map[int64]map[int64]float64

	// singleErr key presence precedes individual data selection. A present nil
	// error still supplies nil data and success for that account.
	singleErr map[int64]error
	// singleData retains per-account multiplier maps used when no error key
	// exists. Successful reads return the selected map directly without nested
	// copying.
	singleData map[int64]map[int64]float64
}

// roleGuardUserRepoStub supplies configured administrator totals before
// role-demotion fixture writes.
type roleGuardUserRepoStub struct {
	// rpmUserRepoStub supplies account reads and observed role writes. This
	// fixture adds a configured role total without synchronizing count and
	// update into a transaction.
	*rpmUserRepoStub
	adminTotal int64 // configured administrator role count
	listCalls  int   // observed role-count read count
}

// rpmStatusUserRepoStub supplies the current account observation used by
// administrator RPM display.
type rpmStatusUserRepoStub struct {
	// UserRepository remains nil for unused methods. This fixture overrides its
	// account lookup, so other promoted account operations panic during these
	// read-only status scenarios.
	identity.UserRepository

	// user is returned directly regardless of requested ID. RPM status scenarios
	// initialize this shared pointer before reading account request limits.
	user *identity.User
}

// rpmStatusAPIKeyRepoStub supplies primary-group Key observations for
// administrator RPM selection.
type rpmStatusAPIKeyRepoStub struct {
	// AdminKeyReader remains nil for unused methods. RPM scenarios use the
	// overridden listing and reject unrelated promoted credential reads through
	// a panic.
	identity.AdminKeyReader
	// keys is returned directly by the fixture listing with its length. Tests
	// keep this slice and group pointers stable during status assembly.
	keys []identity.AdminKeySummary
}

// rpmStatusGroupRepoStub supplies group pointers for administrator status
// name and ceiling lookup.
type rpmStatusGroupRepoStub struct {
	// AdminGroupReader remains nil for unused methods. The fixture overrides
	// lightweight metadata lookup, while a full group validation read would
	// panic.
	identity.AdminGroupReader

	// groups retains display/ceiling pointers indexed by group ID. Missing
	// entries read as nil, while present pointers remain shared with status
	// fixtures.
	groups map[int64]*identity.AdminGroup
}

// rpmStatusRateRepoStub supplies optional account override pointers for
// administrator RPM selection.
type rpmStatusRateRepoStub struct {
	// UserGroupRateRepository remains nil for unused methods. RPM scenarios
	// override ceiling lookup, so multiplier synchronization or other promoted
	// operations panic if unexpectedly invoked.
	billing.UserGroupRateRepository
	// overrides retains selected ceiling pointers indexed by group ID. Nil means
	// no override, while nonnil zero is retained as an explicit unlimited
	// selection.
	overrides map[int64]*int
}

// rpmStatusCacheStub supplies prepared request observations without recording
// or expiring cached state.
type rpmStatusCacheStub struct {
	// UserRPMCache remains nil for unimplemented operations. The fixture
	// supplies no-op increments and prepared reads, retaining no Redis timing or
	// atomic-count guarantees.
	scheduler.UserRPMCache
	userUsed  int           // configured account request count
	groupUsed map[int64]int // configured group request counts
}

// rpmUserRepoStub records shallow account updates and selected masks for
// administrator limit/role scenarios.
type rpmUserRepoStub struct {
	// userRepoStub supplies inherited account reads around the observed update.
	// When present, Update installs its shallow copy on this shared base
	// fixture.
	*userRepoStub
	// lastUpdated retains the same shallow copy installed on the base fixture.
	// Nested account references remain shared with the submitted update
	// snapshot.
	lastUpdated *identity.User
	lastFields  identity.UserUpdateFields // observed selected account fields
}

// AdjustBalance applies the supplied delta to the shared fixture account
// through apply. It ignores requested ID/context and returns configured or
// candidate-state errors; the sequential fixture does not model SQL atomicity
// or concurrent deductions.
func (s *balanceUserRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	return s.apply(func(current float64) float64 { return current + delta })
}

// SetBalance applies replacement funds to the shared fixture account through
// apply. It ignores requested ID/context and preserves candidate rejection
// without a successful change record; tests use it to identify primitive
// delegation.
func (s *balanceUserRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	return s.apply(func(float64) float64 { return value })
}

// apply calculates a candidate from required next and the shared account
// balance. Configured error or missing account returns before mutation;
// negative candidates return before/after observations with
// ErrBalanceNegative, while success changes funds and records the result.
func (s *balanceUserRepoStub) apply(next func(current float64) float64) (identity.BalanceChange, error) {
	if s.adjustErr != nil {
		return identity.BalanceChange{}, s.adjustErr
	}
	if s.userRepoStub == nil || s.user == nil {
		return identity.BalanceChange{}, identity.ErrUserNotFound
	}
	change := identity.BalanceChange{Old: s.user.Balance}
	change.New = next(change.Old)
	if change.New < 0 {
		return change, identity.ErrBalanceNegative
	}
	s.user.Balance = change.New
	s.changes = append(s.changes, change)
	return change, nil
}

// Create records a shallow adjustment-code copy when nonnil and returns
// success. Pointer fields remain shared with the submitted record; nil input
// produces no creation observation and the fixture persists no independent
// database state.
func (s *balanceRedeemRepoStub) Create(ctx context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	clone := *code
	s.created = append(s.created, &clone)
	return nil
}

// AccrueInviteRebate records account/amount before returning the configured
// rebate and error. It updates no affiliate balances, so these tests observe
// the request even when optional accrual is configured to fail.
func (s *adminRechargeAffiliateAccruerStub) AccrueInviteRebate(_ context.Context, userID int64, amount float64) (float64, error) {
	s.calls = append(s.calls, adminRechargeAffiliateAccrual{userID: userID, amount: amount})
	return s.rebate, s.err
}

// adminRechargeSettingService assembles promotion runtime settings for one
// rebate-policy selection. Enabled input stores true and false leaves the key
// absent; other administrator settings remain unavailable through the
// fixture's nil embedded interface.
func adminRechargeSettingService(enabled bool) identity.AdminUserSettings {
	values := map[string]string{}
	if enabled {
		values[promotion.SettingKeyAffiliateAdminRechargeEnabled] = "true"
	}
	return rechargeSettingsFixture{runtime: promotion.NewRuntimeSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: values}})}
}

// InvalidateAuthCacheByKey records the credential whose authentication
// publication was requested.
func (s *authCacheInvalidatorStub) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	s.keys = append(s.keys, key)
}

// InvalidateAuthCacheByUserID records the account whose authentication
// publication was requested.
func (s *authCacheInvalidatorStub) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	s.userIDs = append(s.userIDs, userID)
}

// InvalidateAuthCacheByGroupID records the group whose authentication
// publication was requested.
func (s *authCacheInvalidatorStub) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	s.groupIDs = append(s.groupIDs, groupID)
}

// TestAdminService_UpdateUserBalance_UsesAtomicPrimitives selects add,
// subtract and replacement modes with a sequential balance fixture. It checks
// delegation through AdjustBalance/SetBalance and recorded before/after
// values, while the inherited full-row Update path would panic.
func TestAdminService_UpdateUserBalance_UsesAtomicPrimitives(t *testing.T) {
	tests := []struct {
		name      string                 // administrator scenario description label
		operation string                 // selected funding operation mode
		amount    float64                // USD
		want      identity.BalanceChange // expected fixture balance change
	}{
		{name: "add", operation: "add", amount: 5, want: identity.BalanceChange{Old: 10, New: 15}},
		{name: "subtract", operation: "subtract", amount: 4, want: identity.BalanceChange{Old: 10, New: 6}},
		{name: "set", operation: "set", amount: 2, want: identity.BalanceChange{Old: 10, New: 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}}
			svc := newBalanceAdminForTest(repo, &balanceRedeemRepoStub{}, nil, nil, nil)

			user, err := svc.UpdateUserBalance(context.Background(), 7, tt.amount, tt.operation, "")
			require.NoError(t, err)
			require.Equal(t, []identity.BalanceChange{tt.want}, repo.changes)
			require.Equal(t, tt.want.New, user.Balance)
		})
	}
}

// TestAdminService_UpdateUserBalance_RejectsNegativeResult requests a
// subtraction larger than the fixture balance. It checks the formatted
// rejection, unchanged account funds and absence of successful adjustment
// records.
func TestAdminService_UpdateUserBalance_RejectsNegativeResult(t *testing.T) {
	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 7, Balance: 3}}}
	svc := newBalanceAdminForTest(repo, &balanceRedeemRepoStub{}, nil, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 4, "subtract", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "balance cannot be negative")
	require.Empty(t, repo.changes, "refused adjustment must not be applied")
	require.Equal(t, 3.0, repo.user.Balance)
}

// TestAdminService_UpdateUserBalance_RejectsUnknownOperation supplies an
// unsupported funding operation for an existing account. It requires an error
// before any successful balance-change observation is recorded.
func TestAdminService_UpdateUserBalance_RejectsUnknownOperation(t *testing.T) {
	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}}
	svc := newBalanceAdminForTest(repo, &balanceRedeemRepoStub{}, nil, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 1, "multiply", "")
	require.Error(t, err)
	require.Empty(t, repo.changes)
}

// TestAdminService_UpdateUserBalance_InvalidatesAuthCache adds funds to an
// existing account with callback and history fixtures. It checks account
// invalidation and one recorded adjustment after successful balance
// delegation.
func TestAdminService_UpdateUserBalance_InvalidatesAuthCache(t *testing.T) {
	baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := newBalanceAdminForTest(repo, redeemRepo, invalidator, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "")
	require.NoError(t, err)
	require.Equal(t, []int64{7}, invalidator.userIDs)
	require.Len(t, redeemRepo.created, 1)
}

// TestAdminService_UpdateUserBalance_NoChangeNoInvalidate sets funds to their
// existing amount with invalidation/history fixtures. It checks that a zero
// difference creates no authentication callback or adjustment record.
func TestAdminService_UpdateUserBalance_NoChangeNoInvalidate(t *testing.T) {
	baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := newBalanceAdminForTest(repo, redeemRepo, invalidator, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 10, "set", "")
	require.NoError(t, err)
	require.Empty(t, invalidator.userIDs)
	require.Empty(t, redeemRepo.created)
}

// TestAdminService_UpdateUserBalance_AdminRechargeAffiliateRebate combines
// rebate policy with add, subtract and set funding modes. It checks that only
// the enabled positive add case requests affiliate accrual.
func TestAdminService_UpdateUserBalance_AdminRechargeAffiliateRebate(t *testing.T) {
	tests := []struct {
		name      string                          // administrator scenario description label
		enabled   bool                            // configured recharge rebate switch
		operation string                          // selected funding operation mode
		amount    float64                         // USD
		wantCalls []adminRechargeAffiliateAccrual // expected rebate request observations
	}{
		{
			name:      "disabled by default",
			operation: "add",
			amount:    5,
		},
		{
			name:      "enabled add",
			enabled:   true,
			operation: "add",
			amount:    0.1,
			wantCalls: []adminRechargeAffiliateAccrual{{userID: 7, amount: 0.1}},
		},
		{
			name:      "enabled set increase",
			enabled:   true,
			operation: "set",
			amount:    15,
		},
		{
			name:      "enabled subtract",
			enabled:   true,
			operation: "subtract",
			amount:    5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
			repo := &balanceUserRepoStub{userRepoStub: baseRepo}
			redeemRepo := &balanceRedeemRepoStub{}
			affiliate := &adminRechargeAffiliateAccruerStub{}
			svc := newBalanceAdminForTest(repo, redeemRepo, nil, adminRechargeSettingService(tt.enabled), affiliate)

			_, err := svc.UpdateUserBalance(context.Background(), 7, tt.amount, tt.operation, "")
			require.NoError(t, err)
			require.Equal(t, tt.wantCalls, affiliate.calls)
		})
	}
}

// TestAdminService_UpdateUserBalance_AffiliateFailureDoesNotRollbackRecharge
// makes optional affiliate accrual fail after a successful add. It checks the
// retained increased balance, observed accrual arguments and completed
// adjustment history.
func TestAdminService_UpdateUserBalance_AffiliateFailureDoesNotRollbackRecharge(t *testing.T) {
	baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{}
	affiliate := &adminRechargeAffiliateAccruerStub{err: errors.New("affiliate unavailable")}
	svc := newBalanceAdminForTest(repo, redeemRepo, nil, adminRechargeSettingService(true), affiliate)

	user, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "")
	require.NoError(t, err)
	require.Equal(t, 15.0, user.Balance)
	require.Equal(t, []adminRechargeAffiliateAccrual{{userID: 7, amount: 5}}, affiliate.calls)
	require.Len(t, redeemRepo.created, 1)
}

// CreateUsage accepts adjustment-history delegation and returns success
// without storing a usage record.
func (*balanceRedeemRepoStub) CreateUsage(context.Context, *billing.RedeemCodeUsage) error {
	return nil
}

// IsAffiliateAdminRechargeEnabled delegates the fixture's runtime
// administrator-recharge rebate switch to promotion settings.
func (s rechargeSettingsFixture) IsAffiliateAdminRechargeEnabled(ctx context.Context) bool {
	return s.runtime.IsAffiliateAdminRechargeEnabled(ctx)
}

// newBalanceAdminForTest shares the sequential account fixture with
// atomic-operation interfaces and assembles billing history without a
// database connection. Optional callbacks/policy/accrual are installed when
// supplied; tests observe delegation and outcomes rather than transaction
// rollback.
func newBalanceAdminForTest(users *balanceUserRepoStub, records *balanceRedeemRepoStub, invalidator *authCacheInvalidatorStub, settings identity.AdminUserSettings, affiliate *adminRechargeAffiliateAccruerStub) *identity.UserAdmin {
	d := identity.AdminDependencies{Users: users, Balances: users, Settings: settings, Records: billing.NewRedeemAdmin(records, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)}
	if invalidator != nil {
		d.Invalidator = invalidator
	}
	if affiliate != nil {
		d.Affiliates = affiliate
	}
	return identity.NewUserAdmin(d)
}

// BatchUpdateLimits records a copied ID list and separately copied supplied
// limit pointers before returning configured count/error. Nil pointers remain
// omitted, and the fixture changes no accounts, letting tests inspect request
// selection even on failure.
func (s *batchLimitsUserRepoStub) BatchUpdateLimits(_ context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	s.calls++
	s.userIDs = append([]int64(nil), userIDs...)
	s.concurrency = cloneBatchLimitValue(concurrency)
	s.rpmLimit = cloneBatchLimitValue(rpmLimit)
	return s.affected, s.err
}

// cloneBatchLimitValue copies a present integer into fresh pointed-to
// storage. Nil remains nil, preserving omission when fixtures record batch
// limit requests without retaining the caller's mutable pointer.
func cloneBatchLimitValue(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// TestAdminServiceBatchUpdateLimitsPassesOnlyProvidedFields submits explicit
// concurrency zero with duplicate and nonpositive account IDs. It checks
// positive-ID deduplication, omitted RPM retention and callbacks for the
// cleaned input accounts.
func TestAdminServiceBatchUpdateLimitsPassesOnlyProvidedFields(t *testing.T) {
	concurrency := 0
	repo := &batchLimitsUserRepoStub{
		affected: 2,
	}
	invalidator := &batchLimitsInvalidator{}
	service := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	affected, err := service.BatchUpdateLimits(
		context.Background(),
		[]int64{3, 0, 3, 7, -1},
		&concurrency,
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 2, affected)
	require.Equal(t, []int64{3, 7}, repo.userIDs)
	require.Equal(t, pointerToInt(0), repo.concurrency)
	require.Nil(t, repo.rpmLimit)
	require.Equal(t, []int64{3, 7}, invalidator.userIDs)
}

// TestAdminServiceBatchUpdateLimitsDoesNotInvalidateCacheOnRepositoryError
// makes a selected RPM batch write return an error. It checks zero reported
// affected rows and no authentication publication after the failed repository
// return.
func TestAdminServiceBatchUpdateLimitsDoesNotInvalidateCacheOnRepositoryError(t *testing.T) {
	rpmLimit := 60
	repo := &batchLimitsUserRepoStub{
		err: errors.New("database unavailable"),
	}
	invalidator := &batchLimitsInvalidator{}
	service := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	affected, err := service.BatchUpdateLimits(context.Background(), []int64{1, 2}, nil, &rpmLimit)

	require.EqualError(t, err, "database unavailable")
	require.Zero(t, affected)
	require.Empty(t, invalidator.userIDs)
}

// TestAdminServiceBatchUpdateLimitsRequiresAField submits account IDs with
// both limit pointers omitted. It checks rejection before any batch-write
// call is recorded by the repository fixture.
func TestAdminServiceBatchUpdateLimitsRequiresAField(t *testing.T) {
	repo := &batchLimitsUserRepoStub{}
	service := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: &batchLimitsInvalidator{}})

	affected, err := service.BatchUpdateLimits(context.Background(), []int64{1}, nil, nil)

	require.Error(t, err)
	require.Zero(t, affected)
	require.Zero(t, repo.calls)
}

// pointerToInt returns fresh storage containing the expected integer fixture
// value.
func pointerToInt(value int) *int {
	return &value
}

// InvalidateAuthCacheByUserID records account publication requested by the
// selected batch-limit path.
func (s *batchLimitsInvalidator) InvalidateAuthCacheByUserID(_ context.Context, id int64) {
	s.userIDs = append(s.userIDs, id)
}

// InvalidateAuthCacheByKey panics because batch-limit scenarios expect
// account-scoped publication callbacks.
func (s *batchLimitsInvalidator) InvalidateAuthCacheByKey(context.Context, string) {
	panic("unexpected key invalidation")
}

// TestAdminService_CreateUser_Success submits account credentials, initial
// funds, limits and group selections to administrator creation. It checks the
// assigned ID, password hash and saved account fields through the creation
// fixture.
func TestAdminService_CreateUser_Success(t *testing.T) {
	repo := &userRepoStub{nextID: 10}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	balance := 12.5

	input := &identity.CreateUserInput{
		Email:                "user@test.com",
		Password:             "strong-pass",
		Username:             "tester",
		Notes:                "note",
		Balance:              &balance,
		Concurrency:          7,
		AllowedGroups:        []int64{3, 5},
		DisabledPublicGroups: []int64{8},
	}

	user, err := svc.CreateUser(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(10), user.ID)
	require.Equal(t, input.Email, user.Email)
	require.Equal(t, input.Username, user.Username)
	require.Equal(t, input.Notes, user.Notes)
	require.Equal(t, balance, user.Balance)
	require.Equal(t, input.Concurrency, user.Concurrency)
	require.Equal(t, identity.DefaultUserAPIKeyLimit, user.APIKeyLimit)
	require.Equal(t, input.AllowedGroups, user.AllowedGroups)
	require.Equal(t, input.DisabledPublicGroups, user.DisabledPublicGroups)
	require.Equal(t, identity.RoleUser, user.Role)
	require.Equal(t, billing.StatusActive, user.Status)
	require.True(t, user.CheckPassword(input.Password))
	require.Len(t, repo.created, 1)
	require.Equal(t, user, repo.created[0])
}

// TestAdminService_CreateUser_APIKeyLimitDefaultsAndExplicitZero creates
// accounts first with omitted Key allowance and then with explicit zero. It
// checks current default inheritance and zero retaining its unlimited
// representation.
func TestAdminService_CreateUser_APIKeyLimitDefaultsAndExplicitZero(t *testing.T) {
	repo := &userRepoStub{nextID: 13}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		identity.SettingKeyDefaultUserAPIKeyLimit: "45",
	}}}, identity.GrantSettingsOptions{})
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService})

	inherited, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "inherited-limit@test.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, 45, inherited.APIKeyLimit)

	unlimited := 0
	explicit, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:       "unlimited-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &unlimited,
	})
	require.NoError(t, err)
	require.Equal(t, 0, explicit.APIKeyLimit)
}

// TestAdminService_CreateUser_RejectsNegativeAPIKeyLimit submits a negative
// explicit Key allowance during administrator creation. It requires the limit
// validation error and no account creation observation.
func TestAdminService_CreateUser_RejectsNegativeAPIKeyLimit(t *testing.T) {
	repo := &userRepoStub{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	negative := -1

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:       "negative-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &negative,
	})

	require.ErrorIs(t, err, identity.ErrUserAPIKeyLimitInvalid)
	require.Empty(t, repo.created)
}

// TestAdminService_CreateUser_RejectsAPIKeyLimitAboveDatabaseRange submits an
// explicit Key allowance above the supported database range. It requires the
// shared validation error before the creation fixture records an account.
func TestAdminService_CreateUser_RejectsAPIKeyLimitAboveDatabaseRange(t *testing.T) {
	repo := &userRepoStub{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	tooHigh := identity.MaxUserAPIKeyLimit + 1

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:       "too-high-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &tooHigh,
	})

	require.ErrorIs(t, err, identity.ErrUserAPIKeyLimitInvalid)
	require.Empty(t, repo.created)
}

// TestAdminService_CreateUser_UsesDefaultBalanceWhenBalanceOmitted omits
// initial funds while configured signup balance is present. It checks that
// both the returned and recorded account use that current default amount.
func TestAdminService_CreateUser_UsesDefaultBalanceWhenBalanceOmitted(t *testing.T) {
	repo := &userRepoStub{nextID: 11}
	defaults := identity.GrantSettingsOptions{DefaultBalance: 0}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		billing.SettingKeyDefaultBalance: "0.02",
	}}}, defaults)
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService})

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "default-balance@test.com",
		Password: "strong-pass",
	})

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 0.02, user.Balance)
	require.Len(t, repo.created, 1)
	require.Equal(t, 0.02, repo.created[0].Balance)
}

// TestAdminService_CreateUser_ExplicitZeroBalanceOverridesDefault submits
// explicit zero funds alongside a nonzero configured default. It checks that
// omission and zero remain distinct for the returned and saved account.
func TestAdminService_CreateUser_ExplicitZeroBalanceOverridesDefault(t *testing.T) {
	repo := &userRepoStub{nextID: 12}
	defaults := identity.GrantSettingsOptions{DefaultBalance: 0}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		billing.SettingKeyDefaultBalance: "0.02",
	}}}, defaults)
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService})
	balance := 0.0

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "zero-balance@test.com",
		Password: "strong-pass",
		Balance:  &balance,
	})

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 0.0, user.Balance)
	require.Len(t, repo.created, 1)
	require.Equal(t, 0.0, repo.created[0].Balance)
}

// TestAdminService_CreateUser_EmailExists makes account creation report an
// occupied email after input preparation. It checks propagation of
// ErrEmailExists and absence of a successful recorded account.
func TestAdminService_CreateUser_EmailExists(t *testing.T) {
	repo := &userRepoStub{createErr: identity.ErrEmailExists}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "dup@test.com",
		Password: "password",
	})
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Empty(t, repo.created)
}

// TestAdminService_CreateUser_CreateError makes the account writer fail with
// a configured storage error. It checks that administrator creation preserves
// that error and records no successful account.
func TestAdminService_CreateUser_CreateError(t *testing.T) {
	createErr := errors.New("db down")
	repo := &userRepoStub{createErr: createErr}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "user@test.com",
		Password: "password",
	})
	require.ErrorIs(t, err, createErr)
	require.Empty(t, repo.created)
}

// TestAdminService_CreateUser_AssignsDefaultSubscriptions creates an account
// with a configured default plan and recording assigner. It checks that the
// assignment receives the created account ID and selected plan.
func TestAdminService_CreateUser_AssignsDefaultSubscriptions(t *testing.T) {
	repo := &userRepoStub{nextID: 21}
	assigner := &defaultSubscriptionAssignerStub{}
	defaults := identity.GrantSettingsOptions{DefaultBalance: 0, DefaultConcurrency: 1}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		billing.SettingKeyDefaultSubscriptions: `[{"plan_id":5}]`,
	}}}, defaults)
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService, Subscriptions: assigner})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "new-user@test.com",
		Password: "password",
	})
	require.NoError(t, err)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(21), assigner.calls[0].UserID)
	require.Equal(t, int64(5), assigner.calls[0].PlanID)
}

// GetValue returns the fixture's exact setting text or ErrSettingNotFound for
// absence. Runtime/default readers use that missing-key identity; tests
// configure the inherited map before service calls and retain shared map
// storage.
func (s *adminCreationSettingsStore) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return value, nil
}

// newAdminCreationSettings assembles runtime and grant readers over one
// retained fixture store. Supplied grant options select startup fallbacks,
// while the shared setting map supplies current values; recharge policy is
// deliberately unavailable for creation scenarios.
func newAdminCreationSettings(repo *adminCreationSettingsStore, opts identity.GrantSettingsOptions) *adminCreationSettings {
	return &adminCreationSettings{identity.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound), identity.NewGrantSettings(repo, opts)}
}

// IsAffiliateAdminRechargeEnabled panics because account-creation scenarios
// do not request recharge rebate policy.
func (*adminCreationSettings) IsAffiliateAdminRechargeEnabled(context.Context) bool {
	panic("unexpected recharge policy read")
}

// TestAdminService_CreateUser_DoesNotReturnPartialSuccessFromEmailIdentityResync
// configures an unexpected email-identity resync failure around account
// creation. It checks successful account return with no extra identity
// ensure/replace calls from the administrator core.
func TestAdminService_CreateUser_DoesNotReturnPartialSuccessFromEmailIdentityResync(t *testing.T) {
	repo := &emailSyncRepoStub{
		nextID:    55,
		ensureErr: fmt.Errorf("unexpected email resync"),
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "admin-created@example.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(55), user.ID)
	require.Empty(t, repo.ensureCalls)
	require.Empty(t, repo.replaceCalls)
}

// TestAdminService_UpdateUser_DoesNotReturnPartialSuccessFromEmailIdentityResync
// configures an unexpected identity resync failure while changing account
// email. It checks successful selected account update and no separate
// ensure/replace requests from the administrator core.
func TestAdminService_UpdateUser_DoesNotReturnPartialSuccessFromEmailIdentityResync(t *testing.T) {
	repo := &emailSyncRepoStub{
		user: &identity.User{
			ID:          91,
			Email:       "before@example.com",
			Role:        identity.RoleUser,
			Status:      billing.StatusActive,
			Concurrency: 3,
		},
		replaceErr: fmt.Errorf("unexpected email resync"),
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	updated, err := svc.UpdateUser(context.Background(), 91, &identity.UpdateUserInput{
		Email: "after@example.com",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, "after@example.com", updated.Email)
	require.Empty(t, repo.replaceCalls)
	require.Empty(t, repo.ensureCalls)
}

// TestAdminService_GetUserIncludeDeleted provides a fixture account with a
// soft-delete timestamp. It checks that the administrator audit lookup
// returns that account ID and retains its deleted-state observation.
func TestAdminService_GetUserIncludeDeleted(t *testing.T) {
	ts := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	repo := &userRepoStub{user: &identity.User{ID: 7, Email: "del@test.com", DeletedAt: &ts}}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	got, err := svc.GetUserIncludeDeleted(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.ID)
	require.NotNil(t, got.DeletedAt)
}

// ListWithFilters records pagination/sort inputs and returns shallow account
// copies with fixture totals. Filters are ignored, configured error precedes
// copying, and nested account slices/maps/pointers remain shared with the
// supplied user list.
func (s *userRepoStubForListUsers) ListWithFilters(_ context.Context, params pagination.PaginationParams, _ identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	s.listWithFiltersParams = params
	if s.err != nil {
		return nil, nil, s.err
	}
	out := make([]identity.User, len(s.users))
	copy(out, s.users)
	return out, &pagination.PaginationResult{
		Total:    int64(len(out)),
		Page:     params.Page,
		PageSize: params.PageSize,
	}, nil
}

// GetLatestUsedAtByUserIDs selects requested timestamp entries into a fresh
// outer map. Configured error precedes selection, missing IDs are omitted and
// returned timestamp pointers stay shared with the fixture activity map.
func (s *userRepoStubForListUsers) GetLatestUsedAtByUserIDs(_ context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	if s.lastUsedErr != nil {
		return nil, s.lastUsedErr
	}
	result := make(map[int64]*time.Time, len(userIDs))
	for _, userID := range userIDs {
		if ts, ok := s.lastUsedByUserID[userID]; ok {
			result[userID] = ts
		}
	}
	return result, nil
}

// GetLatestUsedAtByUserID returns the shared timestamp pointer for the
// requested account. Configured activity error takes precedence, while absent
// entries return nil without error so the caller can distinguish unavailable
// from missing observation.
func (s *userRepoStubForListUsers) GetLatestUsedAtByUserID(_ context.Context, userID int64) (*time.Time, error) {
	if s.lastUsedErr != nil {
		return nil, s.lastUsedErr
	}
	return s.lastUsedByUserID[userID], nil
}

// GetByUserIDs records a bulk-read count and returns configured error or the
// shared multiplier map. Requested IDs are ignored; administrator listing
// selects its own account entries from the returned data.
func (s *userGroupRateRepoStubForListUsers) GetByUserIDs(_ context.Context, _ []int64) (map[int64]map[int64]float64, error) {
	s.batchCalls++
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	return s.batchData, nil
}

// GetByUserID records the account before selecting error-map presence ahead
// of its shared multiplier map. A present nil error still returns nil data;
// absent selections return an allocated empty map for that account.
func (s *userGroupRateRepoStubForListUsers) GetByUserID(_ context.Context, userID int64) (map[int64]float64, error) {
	s.singleCall = append(s.singleCall, userID)
	if err, ok := s.singleErr[userID]; ok {
		return nil, err
	}
	if rates, ok := s.singleData[userID]; ok {
		return rates, nil
	}
	return map[int64]float64{}, nil
}

// GetByUserAndGroup panics because list scenarios read complete account
// multiplier maps.
func (s *userGroupRateRepoStubForListUsers) GetByUserAndGroup(_ context.Context, userID, groupID int64) (*float64, error) {
	panic("unexpected GetByUserAndGroup call")
}

// GetRPMOverrideByUserAndGroup panics because these fixtures support
// multiplier enrichment, not RPM selection.
func (s *userGroupRateRepoStubForListUsers) GetRPMOverrideByUserAndGroup(_ context.Context, _, _ int64) (*int, error) {
	panic("unexpected GetRPMOverrideByUserAndGroup call")
}

// SyncUserGroupRates panics because list enrichment performs reads without
// multiplier writes.
func (s *userGroupRateRepoStubForListUsers) SyncUserGroupRates(_ context.Context, userID int64, rates map[int64]*float64) error {
	panic("unexpected SyncUserGroupRates call")
}

// GetByGroupID panics because list scenarios select multiplier data by
// account.
func (s *userGroupRateRepoStubForListUsers) GetByGroupID(_ context.Context, _ int64) ([]billing.UserGroupRateEntry, error) {
	panic("unexpected GetByGroupID call")
}

// SyncGroupRateMultipliers panics because account listing does not update
// group multiplier definitions.
func (s *userGroupRateRepoStubForListUsers) SyncGroupRateMultipliers(_ context.Context, _ int64, _ []billing.GroupRateMultiplierInput) error {
	panic("unexpected SyncGroupRateMultipliers call")
}

// SyncGroupRPMOverrides panics because list enrichment does not write RPM
// override configuration.
func (s *userGroupRateRepoStubForListUsers) SyncGroupRPMOverrides(_ context.Context, _ int64, _ []billing.GroupRPMOverrideInput) error {
	panic("unexpected SyncGroupRPMOverrides call")
}

// ClearGroupRPMOverrides panics because these list scenarios do not clear
// group ceilings.
func (s *userGroupRateRepoStubForListUsers) ClearGroupRPMOverrides(_ context.Context, _ int64) error {
	panic("unexpected ClearGroupRPMOverrides call")
}

// DeleteByGroupID panics because account enrichment does not delete group
// multiplier data.
func (s *userGroupRateRepoStubForListUsers) DeleteByGroupID(_ context.Context, _ int64) error {
	panic("unexpected DeleteByGroupID call")
}

// DeleteByUserID panics because these list scenarios do not remove account
// rate data.
func (s *userGroupRateRepoStubForListUsers) DeleteByUserID(_ context.Context, userID int64) error {
	panic("unexpected DeleteByUserID call")
}

// TestAdminService_ListUsers_BatchRateFallbackToSingle makes bulk multiplier
// lookup fail for a two-account page. It checks one batch attempt, individual
// reads for both accounts and each returned account receiving its configured
// multiplier.
func TestAdminService_ListUsers_BatchRateFallbackToSingle(t *testing.T) {
	userRepo := &userRepoStubForListUsers{
		users: []identity.User{
			{ID: 101, Username: "u1"},
			{ID: 202, Username: "u2"},
		},
	}
	rateRepo := &userGroupRateRepoStubForListUsers{
		batchErr: errors.New("batch unavailable"),
		singleData: map[int64]map[int64]float64{
			101: {11: 1.1},
			202: {22: 2.2},
		},
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: userRepo, Rates: rateRepo})

	users, total, err := svc.ListUsers(context.Background(), 1, 20, identity.UserListFilters{}, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, users, 2)
	require.Equal(t, 1, rateRepo.batchCalls)
	require.ElementsMatch(t, []int64{101, 202}, rateRepo.singleCall)
	require.Equal(t, 1.1, users[0].GroupRates[11])
	require.Equal(t, 2.2, users[1].GroupRates[22])
}

// TestAdminService_ListUsers_PassesSortParams requests a specific page and
// email sort from the administrator list. It checks the exact pagination/sort
// values observed by the repository fixture.
func TestAdminService_ListUsers_PassesSortParams(t *testing.T) {
	userRepo := &userRepoStubForListUsers{
		users: []identity.User{{ID: 1, Email: "a@example.com"}},
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: userRepo})

	_, _, err := svc.ListUsers(context.Background(), 2, 50, identity.UserListFilters{}, "email", "ASC")
	require.NoError(t, err)
	require.Equal(t, pagination.PaginationParams{
		Page:      2,
		PageSize:  50,
		SortBy:    "email",
		SortOrder: "ASC",
	}, userRepo.listWithFiltersParams)
}

// TestAdminService_ListUsers_PopulatesLastUsedAt provides a latest usage
// timestamp for an account page. It checks retained page totals and the
// populated account observation within the fixture time tolerance.
func TestAdminService_ListUsers_PopulatesLastUsedAt(t *testing.T) {
	// Use whole-second fixture time for the usage observation, keeping this
	// timestamp distinct from authentication activity while avoiding subsecond
	// fixture precision in the assertion.
	lastUsed := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	userRepo := &userRepoStubForListUsers{
		users: []identity.User{{ID: 101, Email: "u@example.com"}},
		lastUsedByUserID: map[int64]*time.Time{
			101: &lastUsed,
		},
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: userRepo})

	users, total, err := svc.ListUsers(context.Background(), 1, 20, identity.UserListFilters{}, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	require.NotNil(t, users[0].LastUsedAt)
	require.WithinDuration(t, lastUsed, *users[0].LastUsedAt, time.Second)
}

// TestAdminService_UpdateUser_UsesNormalizedEmailGuardWhenEnabled changes
// email under the normalization switch using a Gmail-compatible alias. It
// checks the mailbox guard value while retaining submitted email text and
// avoiding the ordinary occupancy preflight.
func TestAdminService_UpdateUser_UsesNormalizedEmailGuardWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &identity.User{
			ID:       11,
			Email:    "old@example.com",
			Role:     identity.RoleUser,
			Status:   billing.StatusActive,
			Username: "tester",
		},
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{identity.SettingKeyRegistrationEmailNormalization: "true"}}}, identity.GrantSettingsOptions{})})

	updated, err := svc.UpdateUser(context.Background(), 11, &identity.UpdateUserInput{
		Email: "Y.o.u.r.N.a.m.e+alias@googlemail.com.",
	})
	require.NoError(t, err)
	require.Equal(t, "Y.o.u.r.N.a.m.e+alias@googlemail.com.", updated.Email)
	require.Empty(t, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@gmail.com"}, repo.normalizedUpdateCalls)
	require.Len(t, repo.normalizedUpdateUsers, 1)
	require.Equal(t, "Y.o.u.r.N.a.m.e+alias@googlemail.com.", repo.normalizedUpdateUsers[0].Email)
}

// TestAdminService_CreateUser_WithAdminRole submits administrator role during
// account creation with ordinary credential data. It checks that the created
// account retains the selected global administrator role.
func TestAdminService_CreateUser_WithAdminRole(t *testing.T) {
	repo := &userRepoStub{nextID: 30}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "admin@test.com",
		Password: "strong-pass",
		Role:     identity.RoleAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, identity.RoleAdmin, user.Role)
}

// TestAdminService_CreateUser_DefaultsToUserRole omits global role during
// administrator account creation. It checks that the returned account
// receives the ordinary user role selected by the creation default.
func TestAdminService_CreateUser_DefaultsToUserRole(t *testing.T) {
	repo := &userRepoStub{nextID: 31}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "plain@test.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, identity.RoleUser, user.Role)
}

// TestAdminService_CreateUser_InvalidRoleRejected submits an unsupported
// global role during administrator creation. It requires rejection before the
// account creation fixture records a saved user.
func TestAdminService_CreateUser_InvalidRoleRejected(t *testing.T) {
	repo := &userRepoStub{nextID: 32}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "bad@test.com",
		Password: "strong-pass",
		Role:     "superuser",
	})
	require.Error(t, err)
	require.Empty(t, repo.created, "非法角色不应写入用户")
}

// TestAdminService_UpdateUser_PromoteToAdmin promotes an ordinary account
// through the selected role update. It checks the returned administrator role
// and the corresponding authentication publication request.
func TestAdminService_UpdateUser_PromoteToAdmin(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &batchLimitsInvalidator{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: identity.RoleAdmin})
	require.NoError(t, err)
	require.Equal(t, identity.RoleAdmin, updated.Role)
	require.Equal(t, []int64{42}, invalidator.userIDs, "角色变更应失效认证缓存")
}

// TestAdminService_UpdateUser_RoleOmittedKeepsExisting changes display name
// while omitting role for an administrator account. It checks that the
// returned account retains its earlier global administrator role.
func TestAdminService_UpdateUser_RoleOmittedKeepsExisting(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleAdmin}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	newName := "renamed"
	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Username: &newName})
	require.NoError(t, err)
	require.Equal(t, identity.RoleAdmin, updated.Role, "未提供 role 时不应改变现有角色")
}

// TestAdminService_UpdateUser_InvalidRoleRejected submits an unsupported
// replacement role for an ordinary account. It requires rejection before the
// update fixture receives a selected account write.
func TestAdminService_UpdateUser_InvalidRoleRejected(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: "root"})
	require.Error(t, err)
	require.Nil(t, repo.lastUpdated, "非法角色不应触发持久化")
}

// ListWithFilters records a count read and returns configured administrator
// Total with no rows. Filters/pagination are ignored, so demotion tests model
// the count result and cannot establish transaction or active-administrator
// invariants.
func (s *roleGuardUserRepoStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _ identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	s.listCalls++
	return nil, &pagination.PaginationResult{Total: s.adminTotal}, nil
}

// TestAdminService_UpdateUser_DemoteLastAdminRejected reports one
// administrator during a demotion request. It checks the last-administrator
// rejection, one count read and no persisted role update observation.
func TestAdminService_UpdateUser_DemoteLastAdminRejected(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "a@example.com", Role: identity.RoleAdmin}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: identity.RoleUser})
	require.Error(t, err)
	require.Contains(t, err.Error(), "last admin")
	require.Nil(t, repo.lastUpdated, "最后一个管理员不应被降级持久化")
	require.Equal(t, 1, repo.listCalls, "降级路径应触发管理员计数")
}

// TestAdminService_UpdateUser_DemoteAdminAllowedWhenOthersExist reports two
// administrators before demoting the target account. It checks successful
// role change and the selected ordinary role recorded by the update fixture.
func TestAdminService_UpdateUser_DemoteAdminAllowedWhenOthersExist(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "a@example.com", Role: identity.RoleAdmin}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 2}
	invalidator := &batchLimitsInvalidator{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: identity.RoleUser})
	require.NoError(t, err)
	require.Equal(t, identity.RoleUser, updated.Role)
	require.NotNil(t, repo.lastUpdated)
	require.Equal(t, identity.RoleUser, repo.lastUpdated.Role, "存在其他管理员时允许降级")
}

// TestAdminService_UpdateUser_PromoteDoesNotCountAdmins promotes an ordinary
// account with an administrator-count fixture present. It checks successful
// promotion while verifying that the count reader was unused.
func TestAdminService_UpdateUser_PromoteDoesNotCountAdmins(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleUser}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: &batchLimitsInvalidator{}})

	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: identity.RoleAdmin})
	require.NoError(t, err)
	require.Equal(t, identity.RoleAdmin, updated.Role)
	require.Equal(t, 0, repo.listCalls, "升级路径不应触发管理员计数")
}

// GetByID returns the fixture's shared account regardless of requested
// identifier. It returns nil without error when unconfigured, so RPM status
// scenarios provide a usable account before invoking the service.
func (s *rpmStatusUserRepoStub) GetByID(_ context.Context, _ int64) (*identity.User, error) {
	return s.user, nil
}

// List returns the shared Key observation slice and its length regardless of
// account/pagination inputs. This fixture supplies a bounded status scenario
// and does not model repository filtering or multi-page reads.
func (s *rpmStatusAPIKeyRepoStub) List(_ context.Context, _ int64, _, _ int, _, _ string) ([]identity.AdminKeySummary, int64, error) {
	return s.keys, int64(len(s.keys)), nil
}

// GetByIDLite returns the shared group pointer selected by ID. Missing
// entries return nil without error, letting status selection retain initial
// metadata fields while a later configured account override can still apply.
func (s *rpmStatusGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*identity.AdminGroup, error) {
	return s.groups[id], nil
}

// GetRPMOverrideByUserAndGroup returns the configured shared override pointer
// for groupID and ignores account ID. Missing groups return nil without
// error; a supplied zero remains a present override for status selection.
func (s *rpmStatusRateRepoStub) GetRPMOverrideByUserAndGroup(_ context.Context, _, groupID int64) (*int, error) {
	return s.overrides[groupID], nil
}

// IncrementUserGroupRPM returns zero without changing fixture request
// observations. These fixtures exercise status reads rather than admission
// counting, so callers cannot use the successful result as a recorded request
// increment.
func (s *rpmStatusCacheStub) IncrementUserGroupRPM(context.Context, int64, int64) (int, error) {
	return 0, nil
}

// IncrementUserRPM returns zero without changing fixture request
// observations. These fixtures exercise status reads rather than admission
// counting, so callers cannot use the successful result as a recorded request
// increment.
func (s *rpmStatusCacheStub) IncrementUserRPM(context.Context, int64) (int, error) {
	return 0, nil
}

// GetUserGroupRPM returns the configured group count and nil for any account
// ID. Missing map entries read as zero; the fixture models status
// observations without Redis clock buckets or expiry behavior.
func (s *rpmStatusCacheStub) GetUserGroupRPM(_ context.Context, _, groupID int64) (int, error) {
	return s.groupUsed[groupID], nil
}

// GetUserRPM returns the configured account count without considering the
// requested ID. It performs no cache lookup or timing, so these tests compare
// prepared status data rather than minute-window storage behavior.
func (s *rpmStatusCacheStub) GetUserRPM(context.Context, int64) (int, error) {
	return s.userUsed, nil
}

// TestAdminService_GetUserRPMStatus_AggregatesUserAndGroupLimits provides
// duplicate primary groups, cached counts and one account override. It checks
// sorted distinct group output, inherited group ceiling and the overriding
// account-specific limit/source.
func TestAdminService_GetUserRPMStatus_AggregatesUserAndGroupLimits(t *testing.T) {
	groupOneID := int64(1)
	groupTwoID := int64(2)
	override := 7
	svc := identity.NewUserAdmin(identity.AdminDependencies{
		Users: &rpmStatusUserRepoStub{user: &identity.User{
			ID:       42,
			RPMLimit: 20,
		}},
		Keys: &rpmStatusAPIKeyRepoStub{keys: []identity.AdminKeySummary{
			{ID: 100, GroupID: &groupTwoID},
			{ID: 101, GroupID: &groupOneID},
			{ID: 102, GroupID: &groupTwoID},
			{ID: 103},
		}},
		Groups: &rpmStatusGroupRepoStub{groups: map[int64]*identity.AdminGroup{
			groupOneID: {ID: groupOneID, Name: "group-one", RPMLimit: 10},
			groupTwoID: {ID: groupTwoID, Name: "group-two", RPMLimit: 60},
		}},
		Rates: &rpmStatusRateRepoStub{overrides: map[int64]*int{
			groupTwoID: &override,
		}},
		RPM: &rpmStatusCacheStub{
			userUsed: 5,
			groupUsed: map[int64]int{
				groupOneID: 3,
				groupTwoID: 4,
			},
		},
	})

	status, err := svc.GetUserRPMStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, &identity.UserRPMStatus{
		UserRPMUsed:  5,
		UserRPMLimit: 20,
		PerGroup: []identity.UserGroupRPMStatus{
			{GroupID: groupOneID, GroupName: "group-one", Used: 3, Limit: 10, Source: "group"},
			{GroupID: groupTwoID, GroupName: "group-two", Used: 4, Limit: 7, Source: "override"},
		},
	}, status)
}

// Update records a shallow account copy and selected field mask, then
// installs that copy on an available base fixture. Nil input succeeds without
// recording; nested account data stays shared and the mask is observed rather
// than enforced.
func (s *rpmUserRepoStub) Update(_ context.Context, user *identity.User, fields identity.UserUpdateFields) error {
	if user == nil {
		return nil
	}
	clone := *user
	s.lastUpdated = &clone
	s.lastFields = fields
	if s.userRepoStub != nil {
		s.user = &clone
	}
	return nil
}

// TestAdminService_UpdateUser_InvalidatesAuthCacheOnRPMLimitChange changes
// only the account RPM limit with an update-mask fixture. It checks the
// selected RPM mask/value and account authentication publication after the
// write.
func TestAdminService_UpdateUser_InvalidatesAuthCacheOnRPMLimitChange(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", RPMLimit: 10}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &batchLimitsInvalidator{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	newRPM := 60
	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{
		RPMLimit: &newRPM,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 60, updated.RPMLimit)
	require.Equal(t, identity.UserUpdateFields{RPMLimit: true}, repo.lastFields)
	require.Equal(t, []int64{42}, invalidator.userIDs, "仅修改 RPMLimit 也应失效 API Key 认证缓存")
}

// TestAdminService_UpdateUser_NoInvalidateWhenRPMLimitUnchanged renames an
// account while submitting its unchanged RPM value. It checks that these
// selected writes do not request authentication invalidation when admission
// observations stay equal.
func TestAdminService_UpdateUser_NoInvalidateWhenRPMLimitUnchanged(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", RPMLimit: 10, Username: "old"}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &batchLimitsInvalidator{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	newName := "new"
	sameRPM := 10
	_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{
		Username: &newName,
		RPMLimit: &sameRPM,
	})
	require.NoError(t, err)
	require.Empty(t, invalidator.userIDs, "只改 username 不应触发认证缓存失效")
}

// TestAdminService_UpdateUser_InvalidatesAuthCacheOnDisabledPublicGroupsChange
// changes the selected public-group exclusion list. It checks the recorded
// field mask/value and account authentication publication for changed group
// permissions.
func TestAdminService_UpdateUser_InvalidatesAuthCacheOnDisabledPublicGroupsChange(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", DisabledPublicGroups: []int64{1}}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &batchLimitsInvalidator{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: invalidator})

	disabled := []int64{1, 3}
	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{
		DisabledPublicGroups: &disabled,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, updated.DisabledPublicGroups)
	require.Equal(t, identity.UserUpdateFields{DisabledPublicGroups: true}, repo.lastFields)
	require.Equal(t, []int64{42}, invalidator.userIDs)
}

// TestAdminService_UpdateUser_SavesExplicitZeroAPIKeyLimit submits explicit
// Key allowance zero for an existing account. It checks the returned/stored
// zero and the dedicated APIKeyLimit update mask.
func TestAdminService_UpdateUser_SavesExplicitZeroAPIKeyLimit(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", APIKeyLimit: 100}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	limit := 0

	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{APIKeyLimit: &limit})

	require.NoError(t, err)
	require.Equal(t, 0, updated.APIKeyLimit)
	require.Equal(t, 0, repo.lastUpdated.APIKeyLimit)
	require.Equal(t, identity.UserUpdateFields{APIKeyLimit: true}, repo.lastFields)
}

// TestAdminService_UpdateUser_RejectsNegativeAPIKeyLimit submits a negative
// replacement Key allowance. It checks the validation error, no update
// observation and the unchanged fixture account allowance.
func TestAdminService_UpdateUser_RejectsNegativeAPIKeyLimit(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", APIKeyLimit: 100}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	limit := -1

	_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{APIKeyLimit: &limit})

	require.ErrorIs(t, err, identity.ErrUserAPIKeyLimitInvalid)
	require.Nil(t, repo.lastUpdated)
	require.Equal(t, 100, base.user.APIKeyLimit)
}

// TestAdminService_UpdateUser_RejectsAPIKeyLimitAboveDatabaseRange submits a
// replacement Key allowance beyond the database range. It checks the shared
// rejection, no selected account write and the retained existing allowance.
func TestAdminService_UpdateUser_RejectsAPIKeyLimitAboveDatabaseRange(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", APIKeyLimit: 100}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	limit := identity.MaxUserAPIKeyLimit + 1

	_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{APIKeyLimit: &limit})

	require.ErrorIs(t, err, identity.ErrUserAPIKeyLimitInvalid)
	require.Nil(t, repo.lastUpdated)
	require.Equal(t, 100, base.user.APIKeyLimit)
}

// TestAdminService_RechargeMultiplierEdits exercises setting, replacing, clearing,
// and omitting the personal rate. Only explicit edits select the persistence
// mask, and invalid numbers fail before any account write.
func TestAdminService_RechargeMultiplierEdits(t *testing.T) {
	for _, value := range []float64{18, 0.5, 10, 1} {
		repo := &rpmUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 42}}}
		svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
		updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{BalanceRechargeMultiplier: &value})
		require.NoError(t, err)
		require.Equal(t, value, *updated.BalanceRechargeMultiplier)
		require.Equal(t, identity.UserUpdateFields{BalanceRechargeMultiplier: true}, repo.lastFields)
		updated, err = svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{})
		require.NoError(t, err)
		require.Equal(t, value, *updated.BalanceRechargeMultiplier)
		require.Empty(t, repo.lastFields)
		updated, err = svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{ClearBalanceRechargeMultiplier: true})
		require.NoError(t, err)
		require.Nil(t, updated.BalanceRechargeMultiplier)
		require.True(t, repo.lastFields.BalanceRechargeMultiplier)
	}
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		repo := &rpmUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 42}}}
		svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
		_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{BalanceRechargeMultiplier: &value})
		require.ErrorIs(t, err, identity.ErrBalanceRechargeMultiplierInvalid)
		require.Nil(t, repo.lastUpdated)
	}
}
