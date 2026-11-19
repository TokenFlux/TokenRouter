package apikey_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// userRepoStubForGroupUpdate records permission grants and returns selected
// user views for administration.
type userRepoStubForGroupUpdate struct {
	addGroupErr    error          // configured permission grant failure
	addGroupCalled bool           // observed permission grant attempt
	addedUserID    int64          // captured granted creator identifier
	addedGroupID   int64          // captured granted target identifier
	user           *identity.User // configured user permission view
	getErr         error          // configured user lookup failure
}

// apiKeyRepoStubForGroupUpdate returns selected Key copies and captures
// administrative configuration writes.
type apiKeyRepoStubForGroupUpdate struct {
	key       *apikey.APIKey // configured shallow credential source
	getErr    error          // configured credential lookup failure
	updateErr error          // configured configuration write failure
	updated   *apikey.APIKey // captured successful configuration record
}

// groupRepoStubForGroupUpdate records target lookups and returns selected
// shallow group views.
type groupRepoStubForGroupUpdate struct {
	group          *routing.Group // configured shallow group source
	getErr         error          // configured target lookup failure
	lastGetByIDArg int64          // captured target lookup identifier
}

// keyGroupsFixture clones configured target group data through the production
// routing copier.
type keyGroupsFixture struct {
	apikey.GroupRepository                              // unused group methods require setup
	source                 *groupRepoStubForGroupUpdate // retained configured target fixture
}

// authCacheInvalidatorStub records supplied credential secrets as
// administrative invalidation intent.
type authCacheInvalidatorStub struct {
	keys []string // captured invalidated credential secrets
}

// TestAdminService_AdminUpdateAPIKeyGroupID_KeyNotFound supplies a missing-Key
// repository result to administrative binding. It checks the use case returns
// that load failure remains the result for a requested positive target.
func TestAdminService_AdminUpdateAPIKeyGroupID_KeyNotFound(t *testing.T) {
	repo := &apiKeyRepoStubForGroupUpdate{getErr: apikey.ErrAPIKeyNotFound}
	svc := newKeyAdminForTest(repo, nil, nil, nil)

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 999, int64Ptr(1))
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_NilGroupID_NoOp omits a binding
// change for an existing ordinary record. It checks the loaded Key is returned
// and the update fixture captures no write.
func TestAdminService_AdminUpdateAPIKeyGroupID_NilGroupID_NoOp(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(5)}
	repo := &apiKeyRepoStubForGroupUpdate{key: existing}
	svc := newKeyAdminForTest(repo, nil, nil, nil)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.APIKey.ID)
	// An omitted group ID leaves the key untouched.
	require.Nil(t, repo.updated)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_Unbind supplies zero for a
// currently bound credential. It checks the response clears GroupID and Group,
// the captured write has no GroupID, and credential invalidation is requested.
func TestAdminService_AdminUpdateAPIKeyGroupID_Unbind(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(5), Group: &routing.Group{ID: 5, Name: "Old"}}
	repo := &apiKeyRepoStubForGroupUpdate{key: existing}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(repo, nil, nil, cache)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(0))
	require.NoError(t, err)
	require.Nil(t, got.APIKey.GroupID, "group_id should be nil after unbind")
	require.Nil(t, got.APIKey.Group, "group object should be nil after unbind")
	require.NotNil(t, repo.updated, "Update should have been called")
	require.Nil(t, repo.updated.GroupID)
	require.Equal(t, []string{"sk-test"}, cache.keys, "cache should be invalidated")
}

// TestAdminService_AdminUpdateAPIKeyGroupID_BindActiveGroup selects an active
// public target for an eligible creator. It checks requested target lookup,
// returned group data, captured binding and successful invalidation intent.
func TestAdminService_AdminUpdateAPIKeyGroupID_BindActiveGroup(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Pro", Status: billing.StatusActive}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42}}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, cache)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	require.Equal(t, int64(10), *apiKeyRepo.updated.GroupID)
	require.Equal(t, []string{"sk-test"}, cache.keys)
	// The repository lookup uses the requested group ID.
	require.Equal(t, int64(10), groupRepo.lastGetByIDArg)
	// The response includes the selected group details.
	require.NotNil(t, got.APIKey.Group)
	require.Equal(t, "Pro", got.APIKey.Group.Name)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_SameGroup_Idempotent reselects the
// credential's current active public group. It checks the resulting binding
// stays the same while a configuration write and invalidation still occur.
func TestAdminService_AdminUpdateAPIKeyGroupID_SameGroup_Idempotent(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: int64Ptr(10), Group: &routing.Group{ID: 10, Name: "Pro"}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Pro", Status: billing.StatusActive}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42}}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, cache)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	// Selecting the current group still writes the key and invalidates its cache.
	require.NotNil(t, apiKeyRepo.updated)
	require.Equal(t, []string{"sk-test"}, cache.keys)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_GroupNotFound supplies a target
// repository missing-group result. It checks administrative binding retains
// that error identity instead of accepting or creating a target group.
func TestAdminService_AdminUpdateAPIKeyGroupID_GroupNotFound(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{getErr: routing.ErrGroupNotFound}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, nil, nil)

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(99))
	require.ErrorIs(t, err, routing.ErrGroupNotFound)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_GroupNotActive supplies an inactive
// target for an existing Key. It checks the use case rejects the configuration
// with the GROUP_NOT_ACTIVE application reason.
func TestAdminService_AdminUpdateAPIKeyGroupID_GroupNotActive(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 5, Status: billing.StatusDisabled}}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, nil, nil)

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(5))
	require.Error(t, err)
	require.Equal(t, "GROUP_NOT_ACTIVE", apperror.Reason(err))
}

// TestAdminService_AdminUpdateAPIKeyGroupID_UpdateFails fails the repository
// update during requested unbinding. It checks the administrative use case
// returns the configuration write failure with its operation context.
func TestAdminService_AdminUpdateAPIKeyGroupID_UpdateFails(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, Key: "sk-test", GroupID: int64Ptr(3)}
	repo := &apiKeyRepoStubForGroupUpdate{key: existing, updateErr: errors.New("db write error")}
	svc := newKeyAdminForTest(repo, nil, nil, nil)

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(0))
	require.Error(t, err)
	require.Contains(t, err.Error(), "update api key")
}

// TestAdminService_AdminUpdateAPIKeyGroupID_NegativeGroupID submits a negative
// target against an existing ordinary credential. It checks the use case
// reports the INVALID_GROUP_ID application reason for that supplied target.
func TestAdminService_AdminUpdateAPIKeyGroupID_NegativeGroupID(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	svc := newKeyAdminForTest(apiKeyRepo, nil, nil, nil)

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(-5))
	require.Error(t, err)
	require.Equal(t, "INVALID_GROUP_ID", apperror.Reason(err))
}

// TestAdminService_AdminUpdateAPIKeyGroupID_PointerIsolation changes the
// caller's target ID variable after successful administration. It checks the
// returned and captured binding pointers retain the originally selected group
// value.
func TestAdminService_AdminUpdateAPIKeyGroupID_PointerIsolation(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Pro", Status: billing.StatusActive}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42}}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, cache)

	inputGID := int64(10)
	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, &inputGID)
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	// The stored group ID must be independent of the input pointer.
	inputGID = 999
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	require.Equal(t, int64(10), *apiKeyRepo.updated.GroupID)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_NilCacheInvalidator binds a
// permitted active target with no direct cache notifier. It checks
// configuration still succeeds and returns the supplied target identifier.
func TestAdminService_AdminUpdateAPIKeyGroupID_NilCacheInvalidator(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test"}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 7, Status: billing.StatusActive}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42}}
	// Cache invalidation is optional.
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, nil)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(7))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(7), *got.APIKey.GroupID)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_ExclusiveGroup_AddsAllowedGroup
// selects an active exclusive target for a creator record. It checks permission
// grant arguments, returned binding and reported exclusive grant details.
func TestAdminService_AdminUpdateAPIKeyGroupID_ExclusiveGroup_AddsAllowedGroup(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Exclusive", Status: billing.StatusActive, IsExclusive: true}}
	userRepo := &userRepoStubForGroupUpdate{}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, cache)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
	// Exclusive group binding grants access to the key owner.
	require.True(t, userRepo.addGroupCalled)
	require.Equal(t, int64(42), userRepo.addedUserID)
	require.Equal(t, int64(10), userRepo.addedGroupID)
	// The result reports the granted group for the API response.
	require.True(t, got.AutoGrantedGroupAccess)
	require.NotNil(t, got.GrantedGroupID)
	require.Equal(t, int64(10), *got.GrantedGroupID)
	require.Equal(t, "Exclusive", got.GrantedGroupName)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_NonExclusiveGroup_NoAllowedGroupUpdate
// selects an active public target for an eligible user. It checks successful
// binding makes no exclusive grant request and leaves grant reporting false.
func TestAdminService_AdminUpdateAPIKeyGroupID_NonExclusiveGroup_NoAllowedGroupUpdate(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Public", Status: billing.StatusActive, IsExclusive: false}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42}}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, cache)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	// Public groups do not require an explicit access grant.
	require.False(t, userRepo.addGroupCalled)
	require.False(t, got.AutoGrantedGroupAccess)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_PublicGroupDisabledForUser_ReturnsError
// supplies a creator denial for the selected public target. It checks binding
// rejection occurs before Key update, grant request or credential invalidation.
func TestAdminService_AdminUpdateAPIKeyGroupID_PublicGroupDisabledForUser_ReturnsError(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Public", Status: billing.StatusActive, IsExclusive: false}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42, DisabledPublicGroups: []int64{10}}}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, cache)

	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.ErrorIs(t, err, apikey.ErrGroupNotAllowed)
	require.Nil(t, apiKeyRepo.updated)
	require.Empty(t, cache.keys)
	require.False(t, userRepo.addGroupCalled)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_DoesNotRequireSubscriptionForBinding
// supplies an active public target and existing user permission without
// subscription storage. It checks administrative group binding succeeds
// independently of a funding selection query.
func TestAdminService_AdminUpdateAPIKeyGroupID_DoesNotRequireSubscriptionForBinding(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Sub", Status: billing.StatusActive, IsExclusive: false}}
	userRepo := &userRepoStubForGroupUpdate{user: &identity.User{ID: 42}}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, nil)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.NoError(t, err)
	require.NotNil(t, got.APIKey.GroupID)
	require.Equal(t, int64(10), *got.APIKey.GroupID)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_ExclusiveGroup_AllowedGroupAddFails_ReturnsError
// fails the exclusive permission grant. It checks the use case returns the
// grant failure and the Key repository captures no subsequent configuration
// write.
func TestAdminService_AdminUpdateAPIKeyGroupID_ExclusiveGroup_AllowedGroupAddFails_ReturnsError(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: nil}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	groupRepo := &groupRepoStubForGroupUpdate{group: &routing.Group{ID: 10, Name: "Exclusive", Status: billing.StatusActive, IsExclusive: true}}
	userRepo := &userRepoStubForGroupUpdate{addGroupErr: errors.New("db error")}
	svc := newKeyAdminForTest(apiKeyRepo, groupRepo, userRepo, nil)

	// A failed access grant aborts the key update.
	_, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(10))
	require.Error(t, err)
	require.Contains(t, err.Error(), "add group to user allowed groups")
	require.True(t, userRepo.addGroupCalled)
	// The failed grant leaves the key untouched.
	require.Nil(t, apiKeyRepo.updated)
}

// TestAdminService_AdminUpdateAPIKeyGroupID_Unbind_NoAllowedGroupUpdate removes
// a credential's exclusive target. It checks the binding clears while the
// fixture receives no permission grant and the result reports no added access.
func TestAdminService_AdminUpdateAPIKeyGroupID_Unbind_NoAllowedGroupUpdate(t *testing.T) {
	existing := &apikey.APIKey{ID: 1, UserID: 42, Key: "sk-test", GroupID: int64Ptr(10), Group: &routing.Group{ID: 10, Name: "Exclusive"}}
	apiKeyRepo := &apiKeyRepoStubForGroupUpdate{key: existing}
	userRepo := &userRepoStubForGroupUpdate{}
	cache := &authCacheInvalidatorStub{}
	svc := newKeyAdminForTest(apiKeyRepo, nil, userRepo, cache)

	got, err := svc.AdminUpdateAPIKeyGroupID(context.Background(), 1, int64Ptr(0))
	require.NoError(t, err)
	require.Nil(t, got.APIKey.GroupID)
	// Unbinding a key leaves the user's group permissions unchanged.
	require.False(t, userRepo.addGroupCalled)
	require.False(t, got.AutoGrantedGroupAccess)
}

// AddGroupToAllowedGroups records creator and target identifiers before
// returning its configured error. The fixture models grant intent, performs no
// real permission persistence and is used sequentially by each administrative
// scenario.
func (s *userRepoStubForGroupUpdate) AddGroupToAllowedGroups(_ context.Context, userID int64, groupID int64) error {
	s.addGroupCalled = true
	s.addedUserID = userID
	s.addedGroupID = groupID
	return s.addGroupErr
}

// Create panics when the administrator group fixture receives an unsupported
// Create call.
func (s *userRepoStubForGroupUpdate) Create(context.Context, *identity.User) error {
	panic("unexpected")
}

// CreateWithNormalizedEmailGuard panics when the administrator group fixture
// receives an unsupported CreateWithNormalizedEmailGuard call.
func (s *userRepoStubForGroupUpdate) CreateWithNormalizedEmailGuard(context.Context, *identity.User, string) error {
	panic("unexpected")
}

// GetByID returns the configured error or a user copy with independent allowed
// and denied group slices. Missing user reports user-not-found; other nested
// maps, slices and pointers remain shared, and the requested ID is ignored.
func (s *userRepoStubForGroupUpdate) GetByID(context.Context, int64) (*identity.User, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.user == nil {
		return nil, identity.ErrUserNotFound
	}
	clone := *s.user
	if s.user.AllowedGroups != nil {
		clone.AllowedGroups = append([]int64(nil), s.user.AllowedGroups...)
	}
	if s.user.DisabledPublicGroups != nil {
		clone.DisabledPublicGroups = append([]int64(nil), s.user.DisabledPublicGroups...)
	}
	return &clone, nil
}

// GetByEmail panics when the administrator group fixture receives an
// unsupported GetByEmail call.
func (s *userRepoStubForGroupUpdate) GetByEmail(context.Context, string) (*identity.User, error) {
	panic("unexpected")
}

// GetFirstAdmin panics when the administrator group fixture receives an
// unsupported GetFirstAdmin call.
func (s *userRepoStubForGroupUpdate) GetFirstAdmin(context.Context) (*identity.User, error) {
	panic("unexpected")
}

// Update panics when the administrator group fixture receives an unsupported
// Update call.
func (s *userRepoStubForGroupUpdate) Update(context.Context, *identity.User, identity.UserUpdateFields) error {
	panic("unexpected")
}

// UpdateWithNormalizedEmailGuard panics when the administrator group fixture
// receives an unsupported UpdateWithNormalizedEmailGuard call.
func (s *userRepoStubForGroupUpdate) UpdateWithNormalizedEmailGuard(context.Context, *identity.User, string, identity.UserUpdateFields) error {
	panic("unexpected")
}

// Delete panics when the administrator group fixture receives an unsupported
// Delete call.
func (s *userRepoStubForGroupUpdate) Delete(context.Context, int64) error { panic("unexpected") }

// GetUserAvatar panics when the administrator group fixture receives an
// unsupported GetUserAvatar call.
func (s *userRepoStubForGroupUpdate) GetUserAvatar(context.Context, int64) (*identity.UserAvatar, error) {
	panic("unexpected")
}

// UpsertUserAvatar panics when the administrator group fixture receives an
// unsupported UpsertUserAvatar call.
func (s *userRepoStubForGroupUpdate) UpsertUserAvatar(context.Context, int64, identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	panic("unexpected")
}

// DeleteUserAvatar panics when the administrator group fixture receives an
// unsupported DeleteUserAvatar call.
func (s *userRepoStubForGroupUpdate) DeleteUserAvatar(context.Context, int64) error {
	panic("unexpected")
}

// List panics when the administrator group fixture receives an unsupported List
// call.
func (s *userRepoStubForGroupUpdate) List(context.Context, pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected")
}

// ListWithFilters panics when the administrator group fixture receives an
// unsupported ListWithFilters call.
func (s *userRepoStubForGroupUpdate) ListWithFilters(context.Context, pagination.PaginationParams, identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected")
}

// UpdateBalance panics when the administrator group fixture receives an
// unsupported UpdateBalance call.
func (s *userRepoStubForGroupUpdate) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected")
}

// AddBalance panics when the administrator group fixture receives an
// unsupported AddBalance call.
func (s *userRepoStubForGroupUpdate) AddBalance(context.Context, int64, float64) error {
	panic("unexpected")
}

// DeductBalance panics when the administrator group fixture receives an
// unsupported DeductBalance call.
func (s *userRepoStubForGroupUpdate) DeductBalance(context.Context, int64, float64) (float64, error) {
	panic("unexpected")
}

// AdjustBalance panics when the administrator group fixture receives an
// unsupported AdjustBalance call.
func (s *userRepoStubForGroupUpdate) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance panics when the administrator group fixture receives an
// unsupported SetBalance call.
func (s *userRepoStubForGroupUpdate) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// UpdateConcurrency panics when the administrator group fixture receives an
// unsupported UpdateConcurrency call.
func (s *userRepoStubForGroupUpdate) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected")
}

// BatchSetConcurrency panics when the administrator group fixture receives an
// unsupported BatchSetConcurrency call.
func (s *userRepoStubForGroupUpdate) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected")
}

// BatchAddConcurrency panics when the administrator group fixture receives an
// unsupported BatchAddConcurrency call.
func (s *userRepoStubForGroupUpdate) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected")
}

// BatchUpdateLimits acknowledges an unused fixture limit-update path with zero
// affected records.
func (s *userRepoStubForGroupUpdate) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}

// ExistsByEmail panics when the administrator group fixture receives an
// unsupported ExistsByEmail call.
func (s *userRepoStubForGroupUpdate) ExistsByEmail(context.Context, string) (bool, error) {
	panic("unexpected")
}

// ExistsByNormalizedEmail panics when the administrator group fixture receives
// an unsupported ExistsByNormalizedEmail call.
func (s *userRepoStubForGroupUpdate) ExistsByNormalizedEmail(context.Context, string) (bool, error) {
	panic("unexpected")
}

// LockRegistrationEmail panics when the administrator group fixture receives an
// unsupported LockRegistrationEmail call.
func (s *userRepoStubForGroupUpdate) LockRegistrationEmail(context.Context, string) error {
	panic("unexpected")
}

// RemoveGroupFromAllowedGroups panics when the administrator group fixture
// receives an unsupported RemoveGroupFromAllowedGroups call.
func (s *userRepoStubForGroupUpdate) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected")
}

// UpdateTotpSecret panics when the administrator group fixture receives an
// unsupported UpdateTotpSecret call.
func (s *userRepoStubForGroupUpdate) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected")
}

// EnableTotp panics when the administrator group fixture receives an
// unsupported EnableTotp call.
func (s *userRepoStubForGroupUpdate) EnableTotp(context.Context, int64) error { panic("unexpected") }

// DisableTotp panics when the administrator group fixture receives an
// unsupported DisableTotp call.
func (s *userRepoStubForGroupUpdate) DisableTotp(context.Context, int64) error { panic("unexpected") }

// GetByIDIncludeDeleted panics when the administrator group fixture receives an
// unsupported GetByIDIncludeDeleted call.
func (s *userRepoStubForGroupUpdate) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}

// ListUserAuthIdentities panics when the administrator group fixture receives
// an unsupported ListUserAuthIdentities call.
func (s *userRepoStubForGroupUpdate) ListUserAuthIdentities(context.Context, int64) ([]identity.UserAuthIdentityRecord, error) {
	panic("unexpected")
}

// UnbindUserAuthProvider panics when the administrator group fixture receives
// an unsupported UnbindUserAuthProvider call.
func (s *userRepoStubForGroupUpdate) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected")
}

// GetLatestUsedAtByUserIDs panics when the administrator group fixture receives
// an unsupported GetLatestUsedAtByUserIDs call.
func (s *userRepoStubForGroupUpdate) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	panic("unexpected")
}

// GetLatestUsedAtByUserID panics when the administrator group fixture receives
// an unsupported GetLatestUsedAtByUserID call.
func (s *userRepoStubForGroupUpdate) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	panic("unexpected")
}

// UpdateUserLastActiveAt panics when the administrator group fixture receives
// an unsupported UpdateUserLastActiveAt call.
func (s *userRepoStubForGroupUpdate) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	panic("unexpected")
}

// RemoveGroupFromUserAllowedGroups panics when the administrator group fixture
// receives an unsupported RemoveGroupFromUserAllowedGroups call.
func (s *userRepoStubForGroupUpdate) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected")
}

// GetByID returns a shallow configured Key copy, or its selected read error.
// Tests provide a nonnil record on success; nested pointer and collection
// storage remains shared and ID input is ignored.
func (s *apiKeyRepoStubForGroupUpdate) GetByID(_ context.Context, _ int64) (*apikey.APIKey, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	clone := *s.key
	return &clone, nil
}

// RotateCredential panics when the administrator group fixture receives an
// unsupported RotateCredential call.
func (s *apiKeyRepoStubForGroupUpdate) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update captures a shallow Key copy after a successful configured write
// result. A selected failure returns before capture; the fixture ignores the
// mask and does not replace its lookup record.
func (s *apiKeyRepoStubForGroupUpdate) Update(_ context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	clone := *key
	s.updated = &clone
	return nil
}

// Create panics when the administrator group fixture receives an unsupported
// Create call.
func (s *apiKeyRepoStubForGroupUpdate) Create(context.Context, *apikey.APIKey) error {
	panic("unexpected")
}

// GetKeyAndOwnerID panics when the administrator group fixture receives an
// unsupported GetKeyAndOwnerID call.
func (s *apiKeyRepoStubForGroupUpdate) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	panic("unexpected")
}

// GetByKey panics when the administrator group fixture receives an unsupported
// GetByKey call.
func (s *apiKeyRepoStubForGroupUpdate) GetByKey(context.Context, string) (*apikey.APIKey, error) {
	panic("unexpected")
}

// GetByKeyForAuth panics when the administrator group fixture receives an
// unsupported GetByKeyForAuth call.
func (s *apiKeyRepoStubForGroupUpdate) GetByKeyForAuth(context.Context, string) (*apikey.APIKey, error) {
	panic("unexpected")
}

// Delete panics when the administrator group fixture receives an unsupported
// Delete call.
func (s *apiKeyRepoStubForGroupUpdate) Delete(context.Context, int64) error { panic("unexpected") }

// DeleteWithAudit panics when the administrator group fixture receives an
// unsupported DeleteWithAudit call.
func (s *apiKeyRepoStubForGroupUpdate) DeleteWithAudit(context.Context, int64) error {
	panic("unexpected")
}

// ListByUserID panics when the administrator group fixture receives an
// unsupported ListByUserID call.
func (s *apiKeyRepoStubForGroupUpdate) ListByUserID(context.Context, int64, pagination.PaginationParams, apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}

// VerifyOwnership panics when the administrator group fixture receives an
// unsupported VerifyOwnership call.
func (s *apiKeyRepoStubForGroupUpdate) VerifyOwnership(context.Context, int64, []int64) ([]int64, error) {
	panic("unexpected")
}

// CountByUserID panics when the administrator group fixture receives an
// unsupported CountByUserID call.
func (s *apiKeyRepoStubForGroupUpdate) CountByUserID(context.Context, int64) (int64, error) {
	panic("unexpected")
}

// ExistsByKey panics when the administrator group fixture receives an
// unsupported ExistsByKey call.
func (s *apiKeyRepoStubForGroupUpdate) ExistsByKey(context.Context, string) (bool, error) {
	panic("unexpected")
}

// ListByGroupID panics when the administrator group fixture receives an
// unsupported ListByGroupID call.
func (s *apiKeyRepoStubForGroupUpdate) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}

// SearchAPIKeys panics when the administrator group fixture receives an
// unsupported SearchAPIKeys call.
func (s *apiKeyRepoStubForGroupUpdate) SearchAPIKeys(context.Context, int64, string, int) ([]apikey.APIKey, error) {
	panic("unexpected")
}

// ClearGroupIDByGroupID panics when the administrator group fixture receives an
// unsupported ClearGroupIDByGroupID call.
func (s *apiKeyRepoStubForGroupUpdate) ClearGroupIDByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}

// CountByGroupID panics when the administrator group fixture receives an
// unsupported CountByGroupID call.
func (s *apiKeyRepoStubForGroupUpdate) CountByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}

// ListKeysByUserID panics when the administrator group fixture receives an
// unsupported ListKeysByUserID call.
func (s *apiKeyRepoStubForGroupUpdate) ListKeysByUserID(context.Context, int64) ([]string, error) {
	panic("unexpected")
}

// ListKeysByGroupID panics when the administrator group fixture receives an
// unsupported ListKeysByGroupID call.
func (s *apiKeyRepoStubForGroupUpdate) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	panic("unexpected")
}

// UpdateLastUsed panics when the administrator group fixture receives an
// unsupported UpdateLastUsed call.
func (s *apiKeyRepoStubForGroupUpdate) UpdateLastUsed(context.Context, int64, time.Time) error {
	panic("unexpected")
}

// UpdateGroupIDByUserAndGroup panics when the administrator group fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *apiKeyRepoStubForGroupUpdate) UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error) {
	panic("unexpected")
}

// GetByID records the target identifier and returns its selected error or a
// shallow group copy. Tests supply a present group for success; nested group
// configuration remains shared until the wrapper clones it.
func (s *groupRepoStubForGroupUpdate) GetByID(_ context.Context, id int64) (*routing.Group, error) {
	s.lastGetByIDArg = id
	if s.getErr != nil {
		return nil, s.getErr
	}
	clone := *s.group
	return &clone, nil
}

// Create panics when the administrator group fixture receives an unsupported
// Create call.
func (s *groupRepoStubForGroupUpdate) Create(context.Context, *routing.Group) error {
	panic("unexpected")
}

// GetByIDLite panics when the administrator group fixture receives an
// unsupported GetByIDLite call.
func (s *groupRepoStubForGroupUpdate) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	panic("unexpected")
}

// Update panics when the administrator group fixture receives an unsupported
// Update call.
func (s *groupRepoStubForGroupUpdate) Update(context.Context, *routing.Group) error {
	panic("unexpected")
}

// Delete panics when the administrator group fixture receives an unsupported
// Delete call.
func (s *groupRepoStubForGroupUpdate) Delete(context.Context, int64) error { panic("unexpected") }

// DeleteCascade panics when the administrator group fixture receives an
// unsupported DeleteCascade call.
func (s *groupRepoStubForGroupUpdate) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected")
}

// List panics when the administrator group fixture receives an unsupported List
// call.
func (s *groupRepoStubForGroupUpdate) List(context.Context, pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}

// ListWithFilters panics when the administrator group fixture receives an
// unsupported ListWithFilters call.
func (s *groupRepoStubForGroupUpdate) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}

// ListActive panics when the administrator group fixture receives an
// unsupported ListActive call.
func (s *groupRepoStubForGroupUpdate) ListActive(context.Context) ([]routing.Group, error) {
	panic("unexpected")
}

// ListActiveByPlatform panics when the administrator group fixture receives an
// unsupported ListActiveByPlatform call.
func (s *groupRepoStubForGroupUpdate) ListActiveByPlatform(context.Context, string) ([]routing.Group, error) {
	panic("unexpected")
}

// ListActiveByPlatformLite panics when the administrator group fixture receives
// an unsupported ListActiveByPlatformLite call.
func (s *groupRepoStubForGroupUpdate) ListActiveByPlatformLite(context.Context, string) ([]routing.Group, error) {
	panic("unexpected")
}

// ExistsByName panics when the administrator group fixture receives an
// unsupported ExistsByName call.
func (s *groupRepoStubForGroupUpdate) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected")
}

// GetProviderCount panics when the administrator group fixture receives an
// unsupported GetProviderCount call.
func (s *groupRepoStubForGroupUpdate) GetProviderCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected")
}

// DeleteProviderGroupsByGroupID panics when the administrator group fixture
// receives an unsupported DeleteProviderGroupsByGroupID call.
func (s *groupRepoStubForGroupUpdate) DeleteProviderGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}

// GetProviderIDsByGroupIDs panics when the administrator group fixture receives
// an unsupported GetProviderIDsByGroupIDs call.
func (s *groupRepoStubForGroupUpdate) GetProviderIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected")
}

// BindProvidersToGroup panics when the administrator group fixture receives an
// unsupported BindProvidersToGroup call.
func (s *groupRepoStubForGroupUpdate) BindProvidersToGroup(context.Context, int64, []int64) error {
	panic("unexpected")
}

// UpdateSortOrders panics when the administrator group fixture receives an
// unsupported UpdateSortOrders call.
func (s *groupRepoStubForGroupUpdate) UpdateSortOrders(context.Context, []routing.GroupSortOrderUpdate) error {
	panic("unexpected")
}

// newKeyAdminForTest constructs administration with supplied fixtures and no
// Ent transaction client. Optional users, groups and invalidator are bound when
// present; exclusive mutations run their sequential storage fallback for these
// tests.
func newKeyAdminForTest(keys apikey.APIKeyRepository, groups *groupRepoStubForGroupUpdate, users *userRepoStubForGroupUpdate, invalidator *authCacheInvalidatorStub) *apikey.Admin {
	out := &apikey.Admin{Keys: keys, Mutations: &keypostgres.AdminGroupMutations{Keys: keys, Users: users}}
	if users != nil {
		out.Users = users
	}
	if groups != nil {
		out.Groups = keyGroupsFixture{source: groups}
	}
	if invalidator != nil {
		out.Invalidator = invalidator
	}
	return out
}

// GetByID delegates target lookup and clones the returned group through
// GroupFromRouting. A nil group result remains nil; the wrapper retains the fixture
// error and gives supported mutable group configuration independent storage.
func (p keyGroupsFixture) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := p.source.GetByID(ctx, id)
	return apikey.GroupFromRouting(value), err
}

// InvalidateAuthCacheByKey records the supplied secret for later administrative
// notification assertions.
func (s *authCacheInvalidatorStub) InvalidateAuthCacheByKey(_ context.Context, key string) {
	s.keys = append(s.keys, key)
}

// int64Ptr allocates an independent identifier pointer for administrative
// binding inputs.
func int64Ptr(value int64) *int64 { return &value }
