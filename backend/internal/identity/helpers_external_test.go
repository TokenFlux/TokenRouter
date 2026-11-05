package identity_test

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// Authentication tests choose repository behavior separately from provider
// proof and session storage. Account fixtures differ in pointer retention and
// selective copying, so a test observes only the branch and mutation
// supported by the fixture it supplies.
//
// Settings fixtures keep configured strings and read counters together under
// their own mutex, while their setup maps must be stable before concurrent
// reads. Database-free domain guards and accepted cache writes do not
// simulate transactions, expiry or rotation. Shared constructors assemble the
// same production settings/service adapters with explicit test defaults and
// preserve nil dependencies as nil interfaces where absence selects a tested
// path.

// defaultSubscriptionAssignerStub records subscription assignment values and
// selects an optional configured failure.
type defaultSubscriptionAssignerStub struct {
	// calls retains value-copied assignment inputs for inspection. Nested
	// pointer fields remain shared with the submitted input and must stay stable
	// during assertions.
	calls []billing.AssignSubscriptionInput
	// err is returned after recording nonnil input. It prevents the success path
	// from dereferencing input or constructing a subscription result.
	err error
}

// ensureEmailCall records an account and mailbox supplied to identity ensure.
type ensureEmailCall struct {
	userID int64  // target account identity value
	email  string // requested verified mailbox text
}

// replaceEmailCall records the account and two mailboxes supplied to identity
// replacement.
type replaceEmailCall struct {
	userID   int64  // target account identity value
	oldEmail string // prior identity mailbox text
	newEmail string // replacement identity mailbox text
}

// emailSyncRepoStub retains account pointers and records email-identity
// intents for sequential synchronization tests.
type emailSyncRepoStub struct {
	// user is the retained account pointer. Create and Update replace it;
	// GetByID copies only the outer account, leaving its nested references
	// shared.
	user *identity.User
	// nextID fills an absent supplied account ID only when nonzero. Existing
	// positive or negative IDs remain as submitted to Create.
	nextID      int64
	updateCalls int // observed account update count
	// created retains the pointers submitted to successful Create calls. Tests
	// keep those account objects stable while inspecting the fixture history.
	created []*identity.User
	// updated retains pointers submitted to Update calls. Masks are ignored, so
	// later account mutation can also change earlier observed records.
	updated      []*identity.User
	ensureCalls  []ensureEmailCall  // recorded identity ensure requests
	replaceCalls []replaceEmailCall // recorded identity replacement requests
	// ensureErr is returned after an ensure request is recorded. Tests can
	// inspect attempted identity synchronization even when that capability
	// fails.
	ensureErr error
	// replaceErr is returned after a replacement request is recorded. Existing
	// account-pointer state remains available for the test to inspect.
	replaceErr error
}

// emailNormalizationRepoStub selectively clones account collections and
// records mailbox checks for sequential normalization tests.
type emailNormalizationRepoStub struct {
	// user retains a selectively cloned account. Reads clone its selected
	// collections again, while reference fields outside that helper can remain
	// shared.
	user *identity.User

	existsByEmail bool // configured exact mailbox conflict
	// existsByEmailErr wins over the exact conflict boolean after mailbox
	// recording. Failure tests can still inspect the attempted input.
	existsByEmailErr   error
	existsByNormalized bool // configured alias mailbox conflict
	// existsByNormalizedErr wins over the alias conflict boolean after
	// normalized-text recording. The fixture returns it without deriving
	// additional aliases.
	existsByNormalizedErr error
	// createErr rejects creation before call history or current account is
	// changed. Tests select this failure directly through fixture setup.
	createErr error
	// getByIDErr overrides account reads before cloning state. It permits
	// readback failures independently of configured current account presence.
	getByIDErr error
	// updateErr rejects ordinary updates before recording or replacing current
	// state. Guarded updates select their separate configured failure instead.
	updateErr error
	// normalizedUpdateErr rejects guarded updates before recording
	// mailbox/account values. It leaves the current fixture account available
	// after failure.
	normalizedUpdateErr     error
	existsByEmailCalls      []string // recorded exact mailbox lookups
	existsByNormalizedCalls []string // recorded alias mailbox lookups
	// createCalls retains selectively cloned creation inputs. Current fixture
	// state receives another clone, separating the helper-copied collections in
	// those two records.
	createCalls []*identity.User
	// updateCalls retains selectively cloned ordinary updates. The submitted
	// masks are ignored and copied collections are separate from current fixture
	// state.
	updateCalls           []*identity.User
	normalizedUpdateCalls []string // recorded guarded mailbox values
	// normalizedUpdateUsers retains selectively cloned guarded account updates.
	// Other reference fields follow cloneEmailNormalizationUser and can remain
	// shared with submitted objects.
	normalizedUpdateUsers []*identity.User
}

// userRepoStub retains account pointers and domain counts for sequential
// authentication tests.
type userRepoStub struct {
	// user retains the current account by pointer. Successful reads share it,
	// and creation/update also publish the same pointer in fixture records.
	user *identity.User
	// getErr wins over configured account presence for GetByID. Include-deleted
	// lookup delegates to that same selected read outcome.
	getErr error
	// createErr rejects creation before assigning an ID or publishing account
	// pointers. Domain-guard counts increase only after this creation succeeds.
	createErr error
	// deleteErr is returned after recording the requested deletion ID. Account
	// and email-map entries remain retained regardless of that outcome.
	deleteErr error
	exists    bool // configured mailbox conflict observation
	// existsErr wins over the configured conflict boolean for exact and
	// delegated normalized checks. Those checks ignore their supplied mailbox.
	existsErr error
	// nextID fills a zero account ID only when nonzero. Existing IDs remain
	// unchanged when the fixture retains the account.
	nextID int64
	// created retains successful account creation pointers. AddBalance visits
	// these records as well as user, so duplicate references can be incremented
	// twice.
	created []*identity.User
	// updated retains each submitted account pointer and ignores update masks.
	// Subsequent account mutation can change the observed history entry.
	updated    []*identity.User
	deletedIDs []int64 // observed deletion identity values
	// usersByEmail maps exact email text to retained account pointers. Update
	// adds the current address without removing a previous map key.
	usersByEmail map[string]*identity.User
	// getByEmailErr wins before map or current-account lookup. Otherwise exact
	// map entries precede the current account email match.
	getByEmailErr error
	// domainCounts supplies configured registration-domain occupancy and
	// increments after successful guarded creation. Nil or absent entries report
	// zero to the count reader.
	domainCounts map[string]int
	// domainCountErr overrides CountUsersByEmailDomain before reading configured
	// occupancy. The guarded creator separately checks its domainCounts map
	// directly.
	domainCountErr   error
	domainGuardCalls []string // observed guarded registration domains
}

// settingRepoStub reads configured strings and counts selected reads under a
// fixture mutex.
type settingRepoStub struct {
	// mu protects values, err, getValueCalls and getMultipleCalls during
	// selected reads. Tests complete direct configuration before concurrent
	// reads and inspect counters after operations finish.
	mu               sync.Mutex        // fixture read accounting lock
	values           map[string]string // configured setting string map
	err              error             // configured selected read failure
	getValueCalls    int               // observed single read count
	getMultipleCalls int               // observed selected batch count
}

// emailCacheStub supplies configured verification data while other
// email-cache operations return fixed fixture results.
type emailCacheStub struct {
	// data is the shared verification response returned for every mailbox. A nil
	// value with nil err leaves proof-state interpretation to its consumer.
	data *identity.VerificationCodeData
	// err overrides verification-data reads for every mailbox. Other
	// fixed-result email-cache methods do not inspect this configured failure.
	err error
}

// refreshTokenCacheStub accepts token writes and treats credential reads as
// missing.
type refreshTokenCacheStub struct{}

// turnstileVerifierSpy records token submissions and supplies a selected
// verification outcome.
type turnstileVerifierSpy struct {
	called    int    // observed token submission count
	lastToken string // most recent submitted token
	// result is a retained verification response returned when err is nil. Nil
	// result selects a fresh response whose Success is true.
	result *identity.TurnstileVerifyResponse
	// err is returned after recording each submission. It wins over a configured
	// result pointer and the default successful response.
	err error
}

// authSettingsFixture combines provider, grant, runtime, display and
// promotion readers over one store.
type authSettingsFixture struct {
	// RuntimeSettings, GrantSettings, OAuthSettings, DisplaySettings and
	// promotion share one repository supplied by newAuthSettingsFixture.
	// Startup/provider snapshots belong to their individual readers, while
	// current flat setting values come from that common store.
	*identity.RuntimeSettings                            // current authentication runtime switches
	*identity.GrantSettings                              // current signup entitlement choices
	*identity.OAuthSettings                              // effective provider configuration reader
	*site.DisplaySettings                                // current site display settings
	promotion                 *promotion.RuntimeSettings // current referral promotion switches
}

// captchaSettingsStore counts runtime-setting reads and supplies configured
// batch failures.
type captchaSettingsStore struct {
	// RuntimeSettingsStore supplies any method not overridden by this counting
	// fixture. A nil embedded implementation panics if a test reaches those
	// other capabilities.
	identity.RuntimeSettingsStore
	// mu protects values, err, getValueCalls and getMultipleCalls during
	// selected reads. Tests complete direct configuration before concurrent
	// reads and inspect counters after operations finish.
	mu               sync.Mutex        // fixture read accounting lock
	values           map[string]string // configured setting string map
	err              error             // configured selected read failure
	getValueCalls    int               // observed single read count
	getMultipleCalls int               // observed selected batch count
}

// captchaAuthSettings exposes runtime CAPTCHA/email switches through the
// authentication settings interface.
type captchaAuthSettings struct {
	// AuthSettings supplies authentication methods outside the two overridden
	// runtime capabilities. Tests populate it before reaching those methods; a
	// nil embedded interface cannot handle them.
	identity.AuthSettings
	// runtime is the required reader for CAPTCHA and email-verification
	// switches. Its store defines configured read errors and the effective
	// provider selection.
	runtime *identity.RuntimeSettings
}

// authSourceDefaultsRepoStub records provider-default batches and retains
// current setting values for sequential tests.
type authSourceDefaultsRepoStub struct {
	// values retains the current settings map from setup. SetMultiple merges
	// submitted strings; callers use the fixture sequentially during mutation
	// and reads.
	values map[string]string
	// updates records a fresh copy of the latest submitted batch. Empty
	// submission allocates an empty record and leaves current values unchanged.
	updates map[string]string
}

// tencentCaptchaVerifierStub records proof inputs and returns configured
// vendor response/error observations.
type tencentCaptchaVerifierStub struct {
	// response is returned by shared pointer together with err after input
	// recording. It can be nil for configured vendor failure cases.
	response    *identity.TencentCaptchaVerifyResponse
	err         error                              // configured vendor verifier error
	calls       int                                // observed ticket verification count
	proof       identity.TencentCaptchaProof       // most recent submitted proof
	remoteIP    string                             // most recent remote address
	credentials identity.TencentCaptchaCredentials // most recent vendor credentials
}

// AssignOrExtendSubscription records a value copy of nonnil input before
// returning its configured error. Successful calls require nonnil input and
// return user/plan IDs with false; pointer fields inside recorded input
// remain shared.
func (s *defaultSubscriptionAssignerStub) AssignOrExtendSubscription(_ context.Context, input *billing.AssignSubscriptionInput) (*billing.UserSubscription, bool, error) {
	if input != nil {
		s.calls = append(s.calls, *input)
	}
	if s.err != nil {
		return nil, false, s.err
	}
	return &billing.UserSubscription{UserID: input.UserID, PlanID: input.PlanID}, false, nil
}

// Create retains the supplied account in current and created pointers. A
// configured nonzero nextID fills an absent ID; this sequential fixture
// performs no uniqueness check or persistence transaction.
func (s *emailSyncRepoStub) Create(_ context.Context, user *identity.User) error {
	if s.nextID != 0 && user.ID == 0 {
		user.ID = s.nextID
	}
	s.created = append(s.created, user)
	s.user = user
	return nil
}

// CreateWithNormalizedEmailGuard delegates to Create and retains its account
// pointer. The normalized mailbox argument is ignored, so this fixture
// observes email synchronization without proving alias locking or conflict
// rejection.
func (s *emailSyncRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	return s.Create(ctx, user)
}

// GetByID returns a shallow account copy or user-not-found when no account is
// configured. The supplied ID is ignored and nested pointers/maps/slices
// remain shared with the retained fixture account.
func (s *emailSyncRepoStub) GetByID(_ context.Context, _ int64) (*identity.User, error) {
	if s.user == nil {
		return nil, identity.ErrUserNotFound
	}
	cloned := *s.user
	return &cloned, nil
}

// GetByEmail returns user-not-found for every mailbox lookup in this fixture.
func (s *emailSyncRepoStub) GetByEmail(_ context.Context, _ string) (*identity.User, error) {
	return nil, identity.ErrUserNotFound
}

// GetFirstAdmin returns a fixture error for an unexpected administrator
// lookup.
func (s *emailSyncRepoStub) GetFirstAdmin(context.Context) (*identity.User, error) {
	return nil, fmt.Errorf("unexpected GetFirstAdmin call")
}

// Update records and retains the supplied account while increasing its call
// count. Update masks are ignored, so assertions inspect the received account
// rather than selective database field writes.
func (s *emailSyncRepoStub) Update(_ context.Context, user *identity.User, _ identity.UserUpdateFields) error {
	s.updateCalls++
	s.updated = append(s.updated, user)
	s.user = user
	return nil
}

// UpdateWithNormalizedEmailGuard delegates to Update with the original
// account and mask. It ignores normalized mailbox text; its observed call
// does not implement alias uniqueness or a database lock.
func (s *emailSyncRepoStub) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string, fields identity.UserUpdateFields) error {
	return s.Update(ctx, user, fields)
}

// Delete accepts account deletion as a successful fixture no-op.
func (s *emailSyncRepoStub) Delete(context.Context, int64) error { return nil }

// GetUserAvatar returns a fixture error for an unexpected avatar lookup.
func (s *emailSyncRepoStub) GetUserAvatar(context.Context, int64) (*identity.UserAvatar, error) {
	return nil, fmt.Errorf("unexpected GetUserAvatar call")
}

// UpsertUserAvatar returns a fixture error for an unexpected avatar write.
func (s *emailSyncRepoStub) UpsertUserAvatar(context.Context, int64, identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	return nil, fmt.Errorf("unexpected UpsertUserAvatar call")
}

// DeleteUserAvatar returns a fixture error for an unexpected avatar deletion.
func (s *emailSyncRepoStub) DeleteUserAvatar(context.Context, int64) error {
	return fmt.Errorf("unexpected DeleteUserAvatar call")
}

// List returns a fixture error for unexpected paginated account enumeration.
func (s *emailSyncRepoStub) List(context.Context, pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	return nil, nil, fmt.Errorf("unexpected List call")
}

// ListWithFilters returns a fixture error for unexpected filtered account
// enumeration.
func (s *emailSyncRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	return nil, nil, fmt.Errorf("unexpected ListWithFilters call")
}

// GetLatestUsedAtByUserIDs returns a fresh empty usage-time map for this
// fixture.
func (s *emailSyncRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

// GetLatestUsedAtByUserID reports an absent usage timestamp with no fixture
// error.
func (s *emailSyncRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

// UpdateUserLastActiveAt accepts activity-time updates without mutating the
// fixture account.
func (s *emailSyncRepoStub) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

// UpdateBalance accepts balance updates without changing this email-sync
// fixture.
func (s *emailSyncRepoStub) UpdateBalance(context.Context, int64, float64) error { return nil }

// AddBalance accepts balance additions without changing the retained fixture
// account.
func (s *emailSyncRepoStub) AddBalance(context.Context, int64, float64) error { return nil }

// DeductBalance returns zero funds and success without changing fixture
// balance.
func (s *emailSyncRepoStub) DeductBalance(context.Context, int64, float64) (float64, error) {
	return 0, nil
}

// AdjustBalance panics when signed balance adjustment reaches this email-sync
// fixture.
func (s *emailSyncRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance panics when balance replacement reaches this email-sync fixture.
func (s *emailSyncRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// UpdateConcurrency accepts concurrency updates without changing the retained
// fixture account.
func (s *emailSyncRepoStub) UpdateConcurrency(context.Context, int64, int) error { return nil }

// BatchSetConcurrency returns zero affected accounts for a fixture
// concurrency replacement.
func (s *emailSyncRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}

// BatchAddConcurrency returns zero affected accounts for a fixture
// concurrency increment.
func (s *emailSyncRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}

// BatchUpdateLimits returns zero affected accounts for a fixture limit
// update.
func (s *emailSyncRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}

// ExistsByEmail reports no exact-mailbox conflict for this email-sync
// fixture.
func (s *emailSyncRepoStub) ExistsByEmail(context.Context, string) (bool, error) { return false, nil }

// ExistsByNormalizedEmail reports no normalized-mailbox conflict for this
// email-sync fixture.
func (s *emailSyncRepoStub) ExistsByNormalizedEmail(context.Context, string) (bool, error) {
	return false, nil
}

// LockRegistrationEmail accepts mailbox locking as a successful fixture
// no-op.
func (s *emailSyncRepoStub) LockRegistrationEmail(context.Context, string) error { return nil }

// RemoveGroupFromAllowedGroups returns zero affected accounts for fixture
// group removal.
func (s *emailSyncRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	return 0, nil
}

// AddGroupToAllowedGroups accepts group additions without changing the
// retained fixture account.
func (s *emailSyncRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error { return nil }

// RemoveGroupFromUserAllowedGroups accepts user-group removal without
// changing the retained fixture account.
func (s *emailSyncRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	return nil
}

// ListUserAuthIdentities returns an absent identity list for this email-sync
// fixture.
func (s *emailSyncRepoStub) ListUserAuthIdentities(context.Context, int64) ([]identity.UserAuthIdentityRecord, error) {
	return nil, nil
}

// UnbindUserAuthProvider accepts provider unlinking as a successful fixture
// no-op.
func (s *emailSyncRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error { return nil }

// UpdateTotpSecret accepts TOTP secret updates without changing the retained
// account.
func (s *emailSyncRepoStub) UpdateTotpSecret(context.Context, int64, *string) error { return nil }

// EnableTotp accepts TOTP enablement without changing the retained fixture
// account.
func (s *emailSyncRepoStub) EnableTotp(context.Context, int64) error { return nil }

// DisableTotp accepts TOTP disablement without changing the retained fixture
// account.
func (s *emailSyncRepoStub) DisableTotp(context.Context, int64) error { return nil }

// GetByIDIncludeDeleted delegates to GetByID with its shallow-copy and
// missing-account behavior. The fixture has no deleted-account filter, so
// this method observes the same configured account regardless of the
// requested ID.
func (s *emailSyncRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetByID(ctx, id)
}

// EnsureEmailAuthIdentity records the account and mailbox before returning
// ensureErr. Recorded calls remain available after failure; the fixture
// creates no identity row and has no uniqueness or transaction behavior.
func (s *emailSyncRepoStub) EnsureEmailAuthIdentity(_ context.Context, userID int64, email string) error {
	s.ensureCalls = append(s.ensureCalls, ensureEmailCall{userID: userID, email: email})
	return s.ensureErr
}

// ReplaceEmailAuthIdentity records account, prior mailbox and replacement
// mailbox before returning replaceErr. It creates no identity row, so failure
// assertions inspect ordered intent rather than database rollback.
func (s *emailSyncRepoStub) ReplaceEmailAuthIdentity(_ context.Context, userID int64, oldEmail, newEmail string) error {
	s.replaceCalls = append(s.replaceCalls, replaceEmailCall{
		userID:   userID,
		oldEmail: oldEmail,
		newEmail: newEmail,
	})
	return s.replaceErr
}

// cloneEmailNormalizationUser shallow-copies an optional account and
// separately copies AllowedGroups, BalanceNotifyExtraEmails and GroupRates.
// Other reference fields remain shared; empty copied slices can become nil,
// and absent input returns nil.
func cloneEmailNormalizationUser(u *identity.User) *identity.User {
	if u == nil {
		return nil
	}
	cloned := *u
	if u.AllowedGroups != nil {
		cloned.AllowedGroups = append([]int64(nil), u.AllowedGroups...)
	}
	if u.BalanceNotifyExtraEmails != nil {
		cloned.BalanceNotifyExtraEmails = append([]billing.NotifyEmailSummary(nil), u.BalanceNotifyExtraEmails...)
	}
	if u.GroupRates != nil {
		cloned.GroupRates = make(map[int64]float64, len(u.GroupRates))
		maps.Copy(cloned.GroupRates, u.GroupRates)
	}
	return &cloned
}

// Create returns createErr before recording a successful account creation. It
// records a selective account clone and stores another clone, keeping the
// copied collections independent while other nested references remain shared.
func (s *emailNormalizationRepoStub) Create(_ context.Context, user *identity.User) error {
	if s.createErr != nil {
		return s.createErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.createCalls = append(s.createCalls, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

// CreateWithNormalizedEmailGuard delegates to Create and its selective
// cloning/error behavior. The normalized mailbox argument is ignored, so
// tests use separate conflict observations and do not obtain a persistence
// guard here.
func (s *emailNormalizationRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	return s.Create(ctx, user)
}

// GetByID returns getByIDErr or a selective clone of the current account.
// Requested IDs are ignored, and absent fixture state produces nil with nil
// error through the clone helper.
func (s *emailNormalizationRepoStub) GetByID(context.Context, int64) (*identity.User, error) {
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	return cloneEmailNormalizationUser(s.user), nil
}

// GetByIDIncludeDeleted delegates to GetByID with its selective clone and
// configured error. The fixture holds no deletion state, so include-deleted
// lookup observes the same configured account and nil case.
func (s *emailNormalizationRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetByID(ctx, id)
}

// GetByEmail returns user-not-found for every exact-mailbox read in this
// fixture.
func (s *emailNormalizationRepoStub) GetByEmail(context.Context, string) (*identity.User, error) {
	return nil, identity.ErrUserNotFound
}

// GetFirstAdmin returns user-not-found for administrator reads in this
// normalization fixture.
func (s *emailNormalizationRepoStub) GetFirstAdmin(context.Context) (*identity.User, error) {
	return nil, identity.ErrUserNotFound
}

// Update returns updateErr before recording a selective account clone. It
// ignores the update mask and stores another clone; copied collections
// separate recorded/current state while other references can remain shared.
func (s *emailNormalizationRepoStub) Update(_ context.Context, user *identity.User, _ identity.UserUpdateFields) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.updateCalls = append(s.updateCalls, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

// UpdateWithNormalizedEmailGuard returns normalizedUpdateErr before recording
// mailbox text and a selective account clone. The mask is ignored and the
// current account receives another clone; no lock or alias uniqueness is
// implemented.
func (s *emailNormalizationRepoStub) UpdateWithNormalizedEmailGuard(_ context.Context, user *identity.User, normalizedEmail string, _ identity.UserUpdateFields) error {
	if s.normalizedUpdateErr != nil {
		return s.normalizedUpdateErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.normalizedUpdateCalls = append(s.normalizedUpdateCalls, normalizedEmail)
	s.normalizedUpdateUsers = append(s.normalizedUpdateUsers, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

// Delete panics when account deletion reaches the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

// GetUserAvatar panics when avatar reads reach the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) GetUserAvatar(context.Context, int64) (*identity.UserAvatar, error) {
	panic("unexpected GetUserAvatar call")
}

// UpsertUserAvatar panics when avatar writes reach the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) UpsertUserAvatar(context.Context, int64, identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

// DeleteUserAvatar panics when avatar deletion reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) DeleteUserAvatar(context.Context, int64) error {
	panic("unexpected DeleteUserAvatar call")
}

// List panics when paginated account listing reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) List(context.Context, pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

// ListWithFilters panics when filtered account listing reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

// GetLatestUsedAtByUserIDs returns a fresh empty map for
// normalization-fixture activity queries.
func (s *emailNormalizationRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

// GetLatestUsedAtByUserID returns an absent usage timestamp with no fixture
// error.
func (s *emailNormalizationRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

// UpdateUserLastActiveAt accepts activity-time updates without changing
// normalization-fixture state.
func (s *emailNormalizationRepoStub) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

// AddBalance panics when balance addition reaches the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) AddBalance(context.Context, int64, float64) error {
	panic("unexpected AddBalance call")
}

// UpdateBalance panics when balance updates reach the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected UpdateBalance call")
}

// DeductBalance panics when balance deduction reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) DeductBalance(context.Context, int64, float64) (float64, error) {
	panic("unexpected DeductBalance call")
}

// AdjustBalance panics when signed funding adjustment reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) AdjustBalance(context.Context, int64, float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance panics when balance replacement reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) SetBalance(context.Context, int64, float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// UpdateConcurrency panics when concurrency updates reach the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected UpdateConcurrency call")
}

// BatchSetConcurrency panics when batch concurrency replacement reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

// BatchAddConcurrency panics when batch concurrency addition reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}

// BatchUpdateLimits panics when batch account-limit writes reach the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
}

// ExistsByEmail records the supplied mailbox before returning its configured
// existence result. The configured error wins over that result, allowing
// tests to inspect attempted lookups after a failure.
func (s *emailNormalizationRepoStub) ExistsByEmail(_ context.Context, email string) (bool, error) {
	s.existsByEmailCalls = append(s.existsByEmailCalls, email)
	if s.existsByEmailErr != nil {
		return false, s.existsByEmailErr
	}
	return s.existsByEmail, nil
}

// ExistsByNormalizedEmail records normalized mailbox text before returning
// the configured conflict observation. Its configured error wins over the
// boolean; the fixture does not derive mailbox aliases itself.
func (s *emailNormalizationRepoStub) ExistsByNormalizedEmail(_ context.Context, normalizedEmail string) (bool, error) {
	s.existsByNormalizedCalls = append(s.existsByNormalizedCalls, normalizedEmail)
	if s.existsByNormalizedErr != nil {
		return false, s.existsByNormalizedErr
	}
	return s.existsByNormalized, nil
}

// LockRegistrationEmail panics when registration-mailbox locking reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) LockRegistrationEmail(context.Context, string) error {
	panic("unexpected LockRegistrationEmail call")
}

// RemoveGroupFromAllowedGroups panics when group removal across accounts
// reaches the mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

// AddGroupToAllowedGroups panics when account group addition reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

// RemoveGroupFromUserAllowedGroups panics when individual account-group
// removal reaches the mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

// ListUserAuthIdentities returns an absent identity list for this
// normalization fixture.
func (s *emailNormalizationRepoStub) ListUserAuthIdentities(context.Context, int64) ([]identity.UserAuthIdentityRecord, error) {
	return nil, nil
}

// UnbindUserAuthProvider panics when provider unlinking reaches the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

// UpdateTotpSecret panics when TOTP secret writes reach the
// mailbox-normalization fixture.
func (s *emailNormalizationRepoStub) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected UpdateTotpSecret call")
}

// EnableTotp panics when TOTP enablement reaches the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) EnableTotp(context.Context, int64) error {
	panic("unexpected EnableTotp call")
}

// DisableTotp panics when TOTP disablement reaches the mailbox-normalization
// fixture.
func (s *emailNormalizationRepoStub) DisableTotp(context.Context, int64) error {
	panic("unexpected DisableTotp call")
}

// Create returns createErr before retaining the supplied account pointer. A
// configured ID fills an absent one, then creation records, exact-email map
// and current account share that account for sequential tests. Successful
// paths require a nonnil account for exact-mailbox indexing.
func (s *userRepoStub) Create(ctx context.Context, user *identity.User) error {
	if s.createErr != nil {
		return s.createErr
	}
	if s.nextID != 0 && user.ID == 0 {
		user.ID = s.nextID
	}
	s.created = append(s.created, user)
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*identity.User)
	}
	s.usersByEmail[user.Email] = user
	s.user = user
	return nil
}

// CreateWithNormalizedEmailGuard delegates to Create and its configured
// creation error. It ignores normalized mailbox text, so this helper provides
// the interface shape while domain-specific tests use their separate guarded
// method.
func (s *userRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	return s.Create(ctx, user)
}

// CountUsersByEmailDomain returns the configured error or count for the
// supplied domain. Missing map entries, including a nil map, report zero;
// this fixture performs no mailbox parsing or database query.
func (s *userRepoStub) CountUsersByEmailDomain(_ context.Context, domain string) (int, error) {
	if s.domainCountErr != nil {
		return 0, s.domainCountErr
	}
	return s.domainCounts[domain], nil
}

// CreateWithRegistrationEmailGuards records the requested domain and rejects
// its positive configured count. Successful account creation precedes
// incrementing the in-memory domain count; normalized mailbox text and
// concurrent database locking are outside this fixture.
func (s *userRepoStub) CreateWithRegistrationEmailGuards(ctx context.Context, user *identity.User, _ string, domain string) error {
	// Record the attempted domain before occupancy rejection, then create the
	// account before increasing occupancy; this ordering lets tests distinguish
	// rejected requests from successful registration.
	s.domainGuardCalls = append(s.domainGuardCalls, domain)
	if s.domainCounts[domain] > 0 {
		return identity.ErrEmailDomainRegistrationLimit
	}
	if err := s.Create(ctx, user); err != nil {
		return err
	}
	if s.domainCounts == nil {
		s.domainCounts = make(map[string]int)
	}
	s.domainCounts[domain]++
	return nil
}

// GetByID returns its configured error, user-not-found or the retained
// account pointer. The requested ID is ignored, and successful callers share
// all account fields with current fixture state.
func (s *userRepoStub) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.user == nil {
		return nil, identity.ErrUserNotFound
	}
	return s.user, nil
}

// GetByEmail looks up exact supplied email text after checking its configured
// error. A map miss can use the current account when its email matches;
// returned account pointers remain shared.
func (s *userRepoStub) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	if s.getByEmailErr != nil {
		return nil, s.getByEmailErr
	}
	if s.usersByEmail != nil {
		if user, ok := s.usersByEmail[email]; ok {
			return user, nil
		}
	}
	// A missing exact-map entry can still use the retained current account,
	// allowing fixtures with only user configured to exercise existing-mailbox
	// authentication branches.
	if s.user != nil && s.user.Email == email {
		return s.user, nil
	}
	return nil, identity.ErrUserNotFound
}

// GetFirstAdmin panics when administrator-account discovery reaches the
// authentication-account fixture.
func (s *userRepoStub) GetFirstAdmin(ctx context.Context) (*identity.User, error) {
	panic("unexpected GetFirstAdmin call")
}

// Update records and retains the supplied account, also publishing it under
// its exact email. The update mask is ignored and prior email-map entries
// remain; callers use this fixture sequentially and provide a nonnil account
// for email-map indexing.
func (s *userRepoStub) Update(ctx context.Context, user *identity.User, fields identity.UserUpdateFields) error {
	s.updated = append(s.updated, user)
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*identity.User)
	}
	s.usersByEmail[user.Email] = user
	s.user = user
	return nil
}

// UpdateWithNormalizedEmailGuard delegates to Update with the supplied
// account and mask. It ignores normalized mailbox text, so assertions inspect
// retained account mutation without obtaining alias-conflict or locking
// behavior.
func (s *userRepoStub) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string, fields identity.UserUpdateFields) error {
	return s.Update(ctx, user, fields)
}

// Delete records the requested ID before returning deleteErr. Existing
// account/map/creation records remain available, so cleanup tests inspect the
// attempted deletion rather than a removed in-memory account.
func (s *userRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

// GetUserAvatar panics when avatar reads reach the authentication-account
// fixture.
func (s *userRepoStub) GetUserAvatar(ctx context.Context, userID int64) (*identity.UserAvatar, error) {
	panic("unexpected GetUserAvatar call")
}

// UpsertUserAvatar panics when avatar writes reach the authentication-account
// fixture.
func (s *userRepoStub) UpsertUserAvatar(ctx context.Context, userID int64, input identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

// DeleteUserAvatar panics when avatar deletion reaches the
// authentication-account fixture.
func (s *userRepoStub) DeleteUserAvatar(ctx context.Context, userID int64) error {
	panic("unexpected DeleteUserAvatar call")
}

// List panics when paginated account listing reaches the
// authentication-account fixture.
func (s *userRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

// ListWithFilters panics when filtered account listing reaches the
// authentication-account fixture.
func (s *userRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

// GetLatestUsedAtByUserIDs panics when batch usage-time reads reach the
// authentication-account fixture.
func (s *userRepoStub) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserIDs call")
}

// GetLatestUsedAtByUserID panics when single-account usage-time reads reach
// the authentication-account fixture.
func (s *userRepoStub) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserID call")
}

// UpdateUserLastActiveAt panics when activity-time mutation reaches the
// authentication-account fixture.
func (s *userRepoStub) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	panic("unexpected UpdateUserLastActiveAt call")
}

// UpdateBalance panics when balance updates reach the authentication-account
// fixture.
func (s *userRepoStub) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected UpdateBalance call")
}

// AddBalance increments each matching current or created account reference in
// this sequential fixture. It visits the current pointer first and then every
// matching entry in the creation history, leaving accounts reachable only
// through the email map alone.
//
// An account referenced once in each visited location receives two
// increments, and repeated creation entries add further increments. Tests
// account for that aliasing when inspecting signup grants; this helper
// neither deduplicates account identities nor models the database funding
// update.
func (s *userRepoStub) AddBalance(ctx context.Context, id int64, amount float64) error {
	if s.user != nil && s.user.ID == id {
		s.user.Balance += amount
	}
	// Visit creation records after the current pointer; matching shared
	// references receive another increment, so assertions account for every
	// occurrence in this fixture mutation sequence.
	for _, user := range s.created {
		if user != nil && user.ID == id {
			user.Balance += amount
		}
	}
	return nil
}

// DeductBalance panics when balance deduction reaches the
// authentication-account fixture.
func (s *userRepoStub) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected DeductBalance call")
}

// AdjustBalance panics when signed funding adjustment reaches the
// authentication-account fixture.
func (s *userRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance panics when balance replacement reaches the
// authentication-account fixture.
func (s *userRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// UpdateConcurrency panics when concurrency updates reach the
// authentication-account fixture.
func (s *userRepoStub) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	panic("unexpected UpdateConcurrency call")
}

// BatchSetConcurrency panics when batch concurrency replacement reaches the
// authentication-account fixture.
func (s *userRepoStub) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

// BatchAddConcurrency panics when batch concurrency addition reaches the
// authentication-account fixture.
func (s *userRepoStub) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}

// BatchUpdateLimits panics when batch account-limit writes reach the
// authentication-account fixture.
func (s *userRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
}

// ExistsByEmail returns existsErr before its configured conflict boolean. It
// ignores mailbox text, allowing tests to select the conflict branch
// independently of the fixture exact-email lookup map.
func (s *userRepoStub) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	if s.existsErr != nil {
		return false, s.existsErr
	}
	return s.exists, nil
}

// ExistsByNormalizedEmail delegates its normalized text to ExistsByEmail. The
// fixture therefore uses the same configured error/boolean for exact and
// normalized checks and performs no independent alias derivation.
func (s *userRepoStub) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	return s.ExistsByEmail(ctx, normalizedEmail)
}

// LockRegistrationEmail accepts registration-mailbox locking as a successful
// fixture no-op.
func (s *userRepoStub) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	return nil
}

// RemoveGroupFromAllowedGroups panics when group removal across accounts
// reaches the authentication-account fixture.
func (s *userRepoStub) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

// RemoveGroupFromUserAllowedGroups panics when individual account-group
// removal reaches the authentication-account fixture.
func (s *userRepoStub) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

// AddGroupToAllowedGroups panics when account group addition reaches the
// authentication-account fixture.
func (s *userRepoStub) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

// ListUserAuthIdentities panics when authentication-identity enumeration
// reaches the authentication-account fixture.
func (s *userRepoStub) ListUserAuthIdentities(ctx context.Context, userID int64) ([]identity.UserAuthIdentityRecord, error) {
	panic("unexpected ListUserAuthIdentities call")
}

// UnbindUserAuthProvider panics when provider unlinking reaches the
// authentication-account fixture.
func (s *userRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

// UpdateTotpSecret panics when TOTP secret writes reach the
// authentication-account fixture.
func (s *userRepoStub) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	panic("unexpected UpdateTotpSecret call")
}

// EnableTotp panics when TOTP enablement reaches the authentication-account
// fixture.
func (s *userRepoStub) EnableTotp(ctx context.Context, userID int64) error {
	panic("unexpected EnableTotp call")
}

// DisableTotp panics when TOTP disablement reaches the authentication-account
// fixture.
func (s *userRepoStub) DisableTotp(ctx context.Context, userID int64) error {
	panic("unexpected DisableTotp call")
}

// GetByIDIncludeDeleted delegates to GetByID with its shared pointer and
// configured error. The fixture has no deletion filter, so include-deleted
// lookup uses the same retained account state.
func (s *userRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetByID(ctx, id)
}

// newOAuthEmailFlowAuthService assembles database-free email/OAuth
// authentication with explicit grant and JWT defaults. Supplied
// repositories/cache and settings-map storage remain shared; the fixture
// includes an email service even when its supplied cache is nil.
func newOAuthEmailFlowAuthService(
	userRepo identity.UserRepository,
	redeemRepo billing.RedeemCodeRepository,
	refreshTokenCache identity.RefreshTokenCache,
	settings map[string]string,
	emailCache identity.EmailCache,
) *identity.AuthService {
	cfg := &config.Config{
		Auth: config.AuthConfig{JWT: config.JWTConfig{
			Secret:                "test-secret",
			AccessTokenTTLMinutes: 60,
			RefreshTokenTTLDays:   7,
		}},
		UserDefaults: config.UserDefaultsConfig{
			Balance:     3.5,
			Concurrency: 2,
		},
	}

	settingService := newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	emailService := identity.NewEmailChallenges(emailCache, notification.NewMailer(&settingRepoStub{values: settings}, smtp.New()))

	return identitytestkit.Auth(
		nil, &identity.AuthDependencies{Users: userRepo, Redeem: redeemRepo, RefreshTokens: refreshTokenCache, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService), Email: identitytestkit.Email(emailService)},
	)
}

// Get panics when full setting-record lookup reaches this read-only fixture.
func (s *settingRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue counts and reads one setting while holding the fixture mutex. A
// configured error wins; otherwise absent keys return setting-not-found and
// stored string values are returned unchanged.
func (s *settingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	// Keep the read counter and selected settings observation under the same
	// lock, so concurrent authentication calls can be compared with one
	// completed fixture read.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getValueCalls++
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settingscore.ErrSettingNotFound
}

// Set panics when a single setting write reaches this read-only fixture.
func (s *settingRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple counts and reads a selected settings batch under the fixture
// mutex. A configured error wins; success returns a fresh subset map omitting
// absent keys, with setup mutations completed before concurrent reads.
func (s *settingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	// Keep the read counter and selected settings observation under the same
	// lock, so concurrent authentication calls can be compared with one
	// completed fixture read.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getMultipleCalls++
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			result[key] = v
		}
	}
	return result, nil
}

// SetMultiple panics when batch setting writes reach this read-only fixture.
func (s *settingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll panics when full setting enumeration reaches this selected-read
// fixture.
func (s *settingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete panics when setting deletion reaches this read-only fixture.
func (s *settingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// StoreRefreshToken accepts refresh-token writes without retaining fixture
// credential data.
func (s *refreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identity.RefreshTokenData, time.Duration) error {
	return nil
}

// GetRefreshToken returns refresh-token-not-found for every credential read
// in this fixture.
func (s *refreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identity.RefreshTokenData, error) {
	return nil, identity.ErrRefreshTokenNotFound
}

// DeleteRefreshToken accepts individual refresh-token deletion as a
// successful fixture no-op.
func (s *refreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

// DeleteUserRefreshTokens accepts user-wide refresh deletion as a successful
// fixture no-op.
func (s *refreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error {
	return nil
}

// DeleteTokenFamily accepts family-wide refresh deletion as a successful
// fixture no-op.
func (s *refreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

// AddToUserTokenSet accepts user token-index additions without retaining
// index members.
func (s *refreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

// AddToFamilyTokenSet accepts family token-index additions without retaining
// index members.
func (s *refreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

// GetUserTokenHashes returns an absent user token-index list with fixture
// success.
func (s *refreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

// GetFamilyTokenHashes returns an absent family token-index list with fixture
// success.
func (s *refreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

// IsTokenInFamily reports no refresh-family membership for every fixture
// credential.
func (s *refreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

// GetVerificationCode returns err before its configured verification-data
// pointer. It ignores mailbox text, retains shared data and permits nil with
// nil error, so tests select proof state directly.
func (s *emailCacheStub) GetVerificationCode(ctx context.Context, email string) (*identity.VerificationCodeData, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.data, nil
}

// SetVerificationCode accepts verification-code writes without retaining data
// or expiration.
func (s *emailCacheStub) SetVerificationCode(ctx context.Context, email string, data *identity.VerificationCodeData, ttl time.Duration) error {
	return nil
}

// DeleteVerificationCode accepts verification-code deletion without changing
// configured fixture data.
func (s *emailCacheStub) DeleteVerificationCode(ctx context.Context, email string) error {
	return nil
}

// GetNotifyVerifyCode returns absent notification verification data with no
// fixture error.
func (s *emailCacheStub) GetNotifyVerifyCode(ctx context.Context, email string) (*identity.VerificationCodeData, error) {
	return nil, nil
}

// SetNotifyVerifyCode accepts notification verification writes without
// retaining data or expiration.
func (s *emailCacheStub) SetNotifyVerifyCode(ctx context.Context, email string, data *identity.VerificationCodeData, ttl time.Duration) error {
	return nil
}

// DeleteNotifyVerifyCode accepts notification verification deletion as a
// successful fixture no-op.
func (s *emailCacheStub) DeleteNotifyVerifyCode(ctx context.Context, email string) error {
	return nil
}

// GetPasswordResetToken returns absent password-reset data with no fixture
// error.
func (s *emailCacheStub) GetPasswordResetToken(ctx context.Context, email string) (*identity.PasswordResetTokenData, error) {
	return nil, nil
}

// SetPasswordResetToken accepts password-reset token writes without retaining
// data or expiration.
func (s *emailCacheStub) SetPasswordResetToken(ctx context.Context, email string, data *identity.PasswordResetTokenData, ttl time.Duration) error {
	return nil
}

// DeletePasswordResetToken accepts password-reset token deletion as a
// successful fixture no-op.
func (s *emailCacheStub) DeletePasswordResetToken(ctx context.Context, email string) error {
	return nil
}

// IsPasswordResetEmailInCooldown reports no password-reset cooldown for every
// fixture mailbox.
func (s *emailCacheStub) IsPasswordResetEmailInCooldown(ctx context.Context, email string) bool {
	return false
}

// SetPasswordResetEmailCooldown accepts reset-email cooldown writes without
// retaining expiration state.
func (s *emailCacheStub) SetPasswordResetEmailCooldown(ctx context.Context, email string, ttl time.Duration) error {
	return nil
}

// GetNotifyCodeUserRate returns zero notification-code requests for every
// fixture account.
func (s *emailCacheStub) GetNotifyCodeUserRate(ctx context.Context, userID int64) (int64, error) {
	return 0, nil
}

// IncrNotifyCodeUserRate returns zero notification-code requests without
// incrementing retained fixture state.
func (s *emailCacheStub) IncrNotifyCodeUserRate(ctx context.Context, userID int64, window time.Duration) (int64, error) {
	return 0, nil
}

// newAuthService assembles database-free authentication with the shared
// account fixture and fixed JWT/grant defaults. A nil settings map omits
// settings, and a nil email cache omits email capability; supplied mutable
// fixture state remains shared.
func newAuthService(repo *userRepoStub, settings map[string]string, emailCache identity.EmailCache) *identity.AuthService {
	cfg := &config.Config{
		Auth: config.AuthConfig{JWT: config.JWTConfig{
			Secret:                "test-secret",
			AccessTokenTTLMinutes: 60,
		}},
		UserDefaults: config.UserDefaultsConfig{
			Balance:     3.5,
			Concurrency: 2,
		},
	}

	var settingService *authSettingsFixture
	if settings != nil {
		settingService = newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	}

	var emailService *identity.EmailChallenges
	if emailCache != nil {
		emailService = identity.NewEmailChallenges(emailCache, notification.NewMailer(&settingRepoStub{values: settings}, smtp.New()))
	}

	return identitytestkit.Auth(
		nil, &identity.AuthDependencies{Users: repo, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService), Email: identitytestkit.Email(emailService)},
	)
}

// ConsumeRefreshToken reports that every submitted fixture refresh credential
// is absent.
func (s *refreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}

// VerifyToken records invocation count and token before selecting the result.
// The configured error wins, a configured response is returned by shared
// pointer, and otherwise a fresh successful response is produced.
func (s *turnstileVerifierSpy) VerifyToken(_ context.Context, _ string, token, _ string) (*identity.TurnstileVerifyResponse, error) {
	s.called++
	s.lastToken = token
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	// Absent configured response uses a fresh success object after recording the
	// token, allowing routing tests to observe invocation without supplying
	// provider-specific response fields.
	return &identity.TurnstileVerifyResponse{Success: true}, nil
}

// newAuthSettingsFixture builds authentication/display/promotion readers over
// the same repository. Optional startup configuration supplies grant values
// and snapshotted provider defaults; nil configuration leaves zero grant
// defaults and no OAuth startup snapshot.
func newAuthSettingsFixture(repo settingscore.Repository, cfg *config.Config) *authSettingsFixture {
	var defaults *identity.OAuthSettingsDefaults
	grants := identity.GrantSettingsOptions{}
	if cfg != nil {
		defaults = &identity.OAuthSettingsDefaults{LinuxDo: cfg.Auth.OAuth.LinuxDo, DingTalk: cfg.Auth.OAuth.DingTalk.IdentityDefaults(), OIDC: cfg.Auth.OAuth.OIDC, WeChat: cfg.Auth.OAuth.WeChat.IdentityDefaults(), GitHubOAuth: cfg.Auth.OAuth.GitHub, GoogleOAuth: cfg.Auth.OAuth.Google}
		grants.DefaultBalance = cfg.UserDefaults.Balance
		grants.DefaultConcurrency = cfg.UserDefaults.Concurrency
	}
	return &authSettingsFixture{
		RuntimeSettings: identity.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound),
		GrantSettings:   identity.NewGrantSettings(repo, grants),
		OAuthSettings:   identity.NewOAuthSettings(repo, defaults, identityprovider.ResolveSettingsOIDCMetadata),
		DisplaySettings: site.NewDisplaySettings(repo, nil),
		promotion:       promotion.NewRuntimeSettings(repo),
	}
}

// GetDingTalkConnectOAuthConfig converts the effective DingTalk reader result
// to registration-policy fields. It returns that value with the same error,
// allowing tests to observe the enabled/bypass/policy inputs authentication
// uses.
func (s *authSettingsFixture) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	value, err := s.OAuthSettings.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: value.Enabled, BypassRegistration: value.BypassRegistration, CorpRestrictionPolicy: value.CorpRestrictionPolicy}, err
}

// IsInvitationCodeEnabled delegates invitation-code enablement to the shared
// promotion settings reader.
func (s *authSettingsFixture) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsInvitationCodeEnabled(ctx)
}

// IsPromoCodeEnabled delegates signup-promotion enablement to the shared
// promotion settings reader.
func (s *authSettingsFixture) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsPromoCodeEnabled(ctx)
}

// authSettingsPort converts an optional concrete fixture to the
// authentication settings interface. A nil pointer becomes a nil interface,
// keeping dependency-absence branches observable rather than passing a typed
// nil receiver.
func authSettingsPort(value *authSettingsFixture) identity.AuthSettings {
	if value == nil {
		return nil
	}
	return value
}

// rebuildSessionForTest replaces the session service from current options,
// users, refresh cache, settings and observer. The auth service and options
// are required; callers rebuild before concurrent operations after changing
// fixture dependencies.
func rebuildSessionForTest(service *identity.AuthService) {
	service.SessionService = identity.NewSessionService(service.Options.JWT, service.Users, service.RefreshTokens, service.Settings, service.Observer.Log)
}

// GetValue counts and reads one setting while holding the fixture mutex. A
// configured error wins; otherwise absent keys return setting-not-found and
// stored string values are returned unchanged.
func (s *captchaSettingsStore) GetValue(_ context.Context, key string) (string, error) {
	// Keep the read counter and selected settings observation under the same
	// lock, so concurrent authentication calls can be compared with one
	// completed fixture read.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getValueCalls++
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settingscore.ErrSettingNotFound
}

// GetMultiple counts and reads a selected settings batch under the fixture
// mutex. A configured error wins; success returns a fresh subset map omitting
// absent keys, with setup mutations completed before concurrent reads.
func (s *captchaSettingsStore) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	// Keep the read counter and selected settings observation under the same
	// lock, so concurrent authentication calls can be compared with one
	// completed fixture read.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getMultipleCalls++
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

// GetCaptchaProviderConfig delegates effective CAPTCHA provider selection to
// its retained runtime reader. Tests provide that reader before invocation;
// other embedded authentication methods require their own configured
// implementation.
func (s captchaAuthSettings) GetCaptchaProviderConfig(ctx context.Context) (identity.CaptchaProviderConfig, error) {
	return s.runtime.GetCaptchaProviderConfig(ctx)
}

// IsEmailVerifyEnabled delegates the email-verification switch to its
// retained runtime reader. The reader owns settings failure/default
// interpretation; tests supply it before any authentication call reaches this
// capability.
func (s captchaAuthSettings) IsEmailVerifyEnabled(ctx context.Context) bool {
	return s.runtime.IsEmailVerifyEnabled(ctx)
}

// captchaAuthOptions constructs release-mode authentication options with the
// requested mandatory-CAPTCHA flag. Other option fields stay zero, letting
// CAPTCHA tests exercise release policy without an assembled JWT or signup
// configuration.
func captchaAuthOptions(required bool) *identity.AuthOptions {
	options := &identity.AuthOptions{}
	options.Server.Mode = "release"
	options.Turnstile.Required = required
	return options
}

// Get panics when full setting-record reads reach this provider-default
// fixture.
func (s *authSourceDefaultsRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue panics when single setting reads reach this provider-batch
// fixture.
func (s *authSourceDefaultsRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

// Set panics when single setting writes reach this provider-batch fixture.
func (s *authSourceDefaultsRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple returns a fresh selected subset of configured provider
// settings. Absent keys remain absent, and the source map stays shared with
// test setup, which completes before this sequential read.
func (s *authSourceDefaultsRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple copies submitted settings into a fresh update record and merges
// their strings into current values. Empty submissions retain an allocated
// update map; tests use this unsynchronized fixture sequentially.
func (s *authSourceDefaultsRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.updates = make(map[string]string, len(settings))
	for key, value := range settings {
		s.updates[key] = value
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}

// GetAll panics when full settings enumeration reaches this provider-default
// fixture.
func (s *authSourceDefaultsRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete panics when setting deletion reaches this provider-default fixture.
func (s *authSourceDefaultsRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// VerifyTicket records credentials, proof, remote IP and call count before
// returning configured response/error. The response pointer remains shared,
// and this fixture performs no SDK request or credential validation.
func (s *tencentCaptchaVerifierStub) VerifyTicket(_ context.Context, credentials identity.TencentCaptchaCredentials, proof identity.TencentCaptchaProof, remoteIP string) (*identity.TencentCaptchaVerifyResponse, error) {
	s.calls++
	s.credentials = credentials
	s.proof = proof
	s.remoteIP = remoteIP
	return s.response, s.err
}
