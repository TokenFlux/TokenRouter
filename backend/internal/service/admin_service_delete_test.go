//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type userRepoStub struct {
	user             *User
	getErr           error
	createErr        error
	deleteErr        error
	exists           bool
	existsErr        error
	nextID           int64
	created          []*User
	updated          []*User
	deletedIDs       []int64
	usersByEmail     map[string]*User
	getByEmailErr    error
	domainCounts     map[string]int
	domainCountErr   error
	domainGuardCalls []string
}

func (s *userRepoStub) Create(ctx context.Context, user *User) error {
	if s.createErr != nil {
		return s.createErr
	}
	if s.nextID != 0 && user.ID == 0 {
		user.ID = s.nextID
	}
	s.created = append(s.created, user)
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*User)
	}
	s.usersByEmail[user.Email] = user
	s.user = user
	return nil
}

func (s *userRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string) error {
	return s.Create(ctx, user)
}

func (s *userRepoStub) CountUsersByEmailDomain(_ context.Context, domain string) (int, error) {
	if s.domainCountErr != nil {
		return 0, s.domainCountErr
	}
	return s.domainCounts[domain], nil
}

func (s *userRepoStub) CreateWithRegistrationEmailGuards(ctx context.Context, user *User, _ string, domain string) error {
	s.domainGuardCalls = append(s.domainGuardCalls, domain)
	if s.domainCounts[domain] > 0 {
		return ErrEmailDomainRegistrationLimit
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

func (s *userRepoStub) GetByID(ctx context.Context, id int64) (*User, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.user == nil {
		return nil, ErrUserNotFound
	}
	return s.user, nil
}

func (s *userRepoStub) GetByEmail(ctx context.Context, email string) (*User, error) {
	if s.getByEmailErr != nil {
		return nil, s.getByEmailErr
	}
	if s.usersByEmail != nil {
		if user, ok := s.usersByEmail[email]; ok {
			return user, nil
		}
	}
	if s.user != nil && s.user.Email == email {
		return s.user, nil
	}
	return nil, ErrUserNotFound
}

func (s *userRepoStub) GetFirstAdmin(ctx context.Context) (*User, error) {
	panic("unexpected GetFirstAdmin call")
}

func (s *userRepoStub) Update(ctx context.Context, user *User, fields UserUpdateFields) error {
	s.updated = append(s.updated, user)
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*User)
	}
	s.usersByEmail[user.Email] = user
	s.user = user
	return nil
}

func (s *userRepoStub) UpdateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string, fields UserUpdateFields) error {
	return s.Update(ctx, user, fields)
}

func (s *userRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

func (s *userRepoStub) GetUserAvatar(ctx context.Context, userID int64) (*UserAvatar, error) {
	panic("unexpected GetUserAvatar call")
}

func (s *userRepoStub) UpsertUserAvatar(ctx context.Context, userID int64, input UpsertUserAvatarInput) (*UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

func (s *userRepoStub) DeleteUserAvatar(ctx context.Context, userID int64) error {
	panic("unexpected DeleteUserAvatar call")
}

func (s *userRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *userRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *userRepoStub) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserIDs call")
}

func (s *userRepoStub) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserID call")
}

func (s *userRepoStub) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	panic("unexpected UpdateUserLastActiveAt call")
}

func (s *userRepoStub) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected UpdateBalance call")
}

func (s *userRepoStub) AddBalance(ctx context.Context, id int64, amount float64) error {
	if s.user != nil && s.user.ID == id {
		s.user.Balance += amount
	}
	for _, user := range s.created {
		if user != nil && user.ID == id {
			user.Balance += amount
		}
	}
	return nil
}

func (s *userRepoStub) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected DeductBalance call")
}

func (s *userRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (s *userRepoStub) SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error) {
	panic("unexpected SetBalance call")
}

func (s *userRepoStub) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	panic("unexpected UpdateConcurrency call")
}

func (s *userRepoStub) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

func (s *userRepoStub) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}

func (s *userRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
}

func (s *userRepoStub) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	if s.existsErr != nil {
		return false, s.existsErr
	}
	return s.exists, nil
}

func (s *userRepoStub) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	return s.ExistsByEmail(ctx, normalizedEmail)
}

func (s *userRepoStub) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	return nil
}

func (s *userRepoStub) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (s *userRepoStub) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (s *userRepoStub) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (s *userRepoStub) ListUserAuthIdentities(ctx context.Context, userID int64) ([]UserAuthIdentityRecord, error) {
	panic("unexpected ListUserAuthIdentities call")
}

func (s *userRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

func (s *userRepoStub) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	panic("unexpected UpdateTotpSecret call")
}

func (s *userRepoStub) EnableTotp(ctx context.Context, userID int64) error {
	panic("unexpected EnableTotp call")
}

func (s *userRepoStub) DisableTotp(ctx context.Context, userID int64) error {
	panic("unexpected DisableTotp call")
}

func (s *userRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*User, error) {
	return s.GetByID(ctx, id)
}

type groupRepoStub struct {
	affectedUserIDs []int64
	deleteErr       error
	deleteCalls     []int64
}

func (s *groupRepoStub) Create(ctx context.Context, group *Group) error {
	panic("unexpected Create call")
}

func (s *groupRepoStub) GetByID(ctx context.Context, id int64) (*Group, error) {
	panic("unexpected GetByID call")
}

func (s *groupRepoStub) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	panic("unexpected GetByIDLite call")
}

func (s *groupRepoStub) Update(ctx context.Context, group *Group) error {
	panic("unexpected Update call")
}

func (s *groupRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStub) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	s.deleteCalls = append(s.deleteCalls, id)
	return s.affectedUserIDs, s.deleteErr
}

func (s *groupRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStub) ListActive(ctx context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (s *groupRepoStub) ListActiveByPlatformLite(ctx context.Context, platform string) ([]Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStub) GetAccountCount(ctx context.Context, groupID int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStub) DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStub) BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStub) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStub) UpdateSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error {
	return nil
}

type deleteGroupAPIKeyRepoStub struct {
	apiKeyRepoStubForGroupUpdate
	keys         []string
	listErr      error
	listGroupIDs []int64
}

func (s *deleteGroupAPIKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	s.listGroupIDs = append(s.listGroupIDs, groupID)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.keys, nil
}

type proxyRepoStub struct {
	deleteErr    error
	countErr     error
	accountCount int64
	deletedIDs   []int64
}

func (s *proxyRepoStub) Create(ctx context.Context, proxy *Proxy) error {
	panic("unexpected Create call")
}

func (s *proxyRepoStub) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	panic("unexpected GetByID call")
}

func (s *proxyRepoStub) ListByIDs(ctx context.Context, ids []int64) ([]Proxy, error) {
	panic("unexpected ListByIDs call")
}

func (s *proxyRepoStub) Update(ctx context.Context, proxy *Proxy) error {
	panic("unexpected Update call")
}

func (s *proxyRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

func (s *proxyRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]Proxy, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *proxyRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]Proxy, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *proxyRepoStub) ListActive(ctx context.Context) ([]Proxy, error) {
	panic("unexpected ListActive call")
}

func (s *proxyRepoStub) ListActiveWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error) {
	panic("unexpected ListActiveWithAccountCount call")
}

func (s *proxyRepoStub) ListWithFiltersAndAccountCount(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]ProxyWithAccountCount, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFiltersAndAccountCount call")
}

func (s *proxyRepoStub) ExistsByHostPortAuth(ctx context.Context, host string, port int, username, password string) (bool, error) {
	panic("unexpected ExistsByHostPortAuth call")
}

func (s *proxyRepoStub) CountAccountsByProxyID(ctx context.Context, proxyID int64) (int64, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return s.accountCount, nil
}

func (s *proxyRepoStub) ListAccountSummariesByProxyID(ctx context.Context, proxyID int64) ([]ProxyAccountSummary, error) {
	panic("unexpected ListAccountSummariesByProxyID call")
}
func (s *proxyRepoStub) SweepExpiredProxies(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (s *proxyRepoStub) ListAllForFallback(_ context.Context) ([]Proxy, error) {
	return nil, nil
}
func (s *proxyRepoStub) CountExpired(_ context.Context) (int64, error) {
	return 0, nil
}
func (s *proxyRepoStub) CountExpiringSoon(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

type redeemRepoStub struct {
	getErrByID    map[int64]error
	codesByID     map[int64]*RedeemCode
	deleteErrByID map[int64]error
	updateErr     error
	updatedCodes  []*RedeemCode
	deletedIDs    []int64
	lockedGetIDs  []int64

	batchUpdateIDs    []int64
	batchUpdateFields RedeemCodeBatchUpdateFields
	batchUpdateResult int64
	batchUpdateErr    error
	batchUpdateCalled bool
}

func (s *redeemRepoStub) Create(ctx context.Context, code *RedeemCode) error {
	panic("unexpected Create call")
}

func (s *redeemRepoStub) CreateBatch(ctx context.Context, codes []RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (s *redeemRepoStub) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	if s.getErrByID != nil {
		if err, ok := s.getErrByID[id]; ok {
			return nil, err
		}
	}
	if s.codesByID != nil {
		if code, ok := s.codesByID[id]; ok {
			return code, nil
		}
	}
	return &RedeemCode{ID: id}, nil
}

func (s *redeemRepoStub) GetByIDForUpdate(ctx context.Context, id int64) (*RedeemCode, error) {
	s.lockedGetIDs = append(s.lockedGetIDs, id)
	return s.GetByID(ctx, id)
}

func (s *redeemRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	panic("unexpected GetByCode call")
}

func (s *redeemRepoStub) GetByCodeForUpdate(ctx context.Context, code string) (*RedeemCode, error) {
	panic("unexpected GetByCodeForUpdate call")
}

func (s *redeemRepoStub) Update(ctx context.Context, code *RedeemCode) error {
	s.updatedCodes = append(s.updatedCodes, code)
	if s.codesByID == nil {
		s.codesByID = make(map[int64]*RedeemCode)
	}
	cloned := *code
	s.codesByID[code.ID] = &cloned
	return s.updateErr
}

func (s *redeemRepoStub) BatchUpdate(ctx context.Context, ids []int64, fields RedeemCodeBatchUpdateFields) (int64, error) {
	s.batchUpdateCalled = true
	s.batchUpdateIDs = append([]int64(nil), ids...)
	s.batchUpdateFields = fields
	if s.batchUpdateErr != nil {
		return 0, s.batchUpdateErr
	}
	if s.batchUpdateResult != 0 {
		return s.batchUpdateResult, nil
	}
	return int64(len(ids)), nil
}

func (s *redeemRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	if s.deleteErrByID != nil {
		if err, ok := s.deleteErrByID[id]; ok {
			return err
		}
	}
	return nil
}

func (s *redeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	panic("unexpected Use call")
}

func (s *redeemRepoStub) CreateUsage(ctx context.Context, usage *RedeemCodeUsage) error {
	return nil
}

func (s *redeemRepoStub) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*RedeemCodeUsage, error) {
	return nil, nil
}

func (s *redeemRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *redeemRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *redeemRepoStub) ListByUser(ctx context.Context, userID int64, limit int) ([]RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (s *redeemRepoStub) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *redeemRepoStub) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func TestAdminService_DeleteUser_Success(t *testing.T) {
	repo := &userRepoStub{user: &User{ID: 7, Role: RoleUser}}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.DeleteUser(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, repo.deletedIDs)
}

func TestAdminService_DeleteUser_DeletesOwnedAPIKeys(t *testing.T) {
	repo := &userRepoStub{user: &User{ID: 7, Role: RoleUser}}
	apiKeyRepo := &apiKeyRepoStub{
		allowListByUserID: true,
		listByUserIDKeys: []APIKey{
			{ID: 11, UserID: 7, Key: "sk-user-1"},
			{ID: 12, UserID: 7, Key: "sk-user-2"},
		},
	}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		apiKeyRepo:           apiKeyRepo,
		authCacheInvalidator: invalidator,
	}

	err := svc.DeleteUser(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, repo.deletedIDs)
	require.Equal(t, []int64{7}, apiKeyRepo.listByUserIDCalls)
	require.Equal(t, []int64{11, 12}, apiKeyRepo.deletedIDs)
	require.ElementsMatch(t, []string{"sk-user-1", "sk-user-2"}, invalidator.keys)
	require.Equal(t, []int64{7}, invalidator.userIDs)
}

func TestAdminService_DeleteUser_NotFound(t *testing.T) {
	repo := &userRepoStub{getErr: ErrUserNotFound}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.DeleteUser(context.Background(), 404)
	require.ErrorIs(t, err, ErrUserNotFound)
	require.Empty(t, repo.deletedIDs)
}

func TestAdminService_DeleteUser_AdminGuard(t *testing.T) {
	repo := &userRepoStub{user: &User{ID: 1, Role: RoleAdmin}}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.DeleteUser(context.Background(), 1)
	require.Error(t, err)
	require.ErrorContains(t, err, "cannot delete admin user")
	require.Empty(t, repo.deletedIDs)
}

func TestAdminService_DeleteUser_DeleteError(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &userRepoStub{
		user:      &User{ID: 9, Role: RoleUser},
		deleteErr: deleteErr,
	}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.DeleteUser(context.Background(), 9)
	require.ErrorIs(t, err, deleteErr)
	require.Equal(t, []int64{9}, repo.deletedIDs)
}

func TestAdminService_DeleteGroup_Success(t *testing.T) {
	repo := &groupRepoStub{affectedUserIDs: []int64{11, 12}}
	svc := &adminServiceImpl{
		groupRepo: repo,
	}

	err := svc.DeleteGroup(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, repo.deleteCalls)
}

func TestAdminService_DeleteGroup_InvalidatesAuthCacheForBoundKeys(t *testing.T) {
	repo := &groupRepoStub{}
	apiKeyRepo := &deleteGroupAPIKeyRepoStub{keys: []string{"k1", "k2"}}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		groupRepo:            repo,
		apiKeyRepo:           apiKeyRepo,
		authCacheInvalidator: invalidator,
	}

	err := svc.DeleteGroup(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, repo.deleteCalls)
	require.Equal(t, []int64{5}, apiKeyRepo.listGroupIDs)
	require.Equal(t, []string{"k1", "k2"}, invalidator.keys)
}

func TestAdminService_DeleteGroup_NotFound(t *testing.T) {
	repo := &groupRepoStub{deleteErr: ErrGroupNotFound}
	svc := &adminServiceImpl{groupRepo: repo}

	err := svc.DeleteGroup(context.Background(), 99)
	require.ErrorIs(t, err, ErrGroupNotFound)
}

func TestAdminService_DeleteGroup_Error(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &groupRepoStub{deleteErr: deleteErr}
	svc := &adminServiceImpl{groupRepo: repo}

	err := svc.DeleteGroup(context.Background(), 42)
	require.ErrorIs(t, err, deleteErr)
}

func TestAdminService_DeleteProxy_Success(t *testing.T) {
	repo := &proxyRepoStub{}
	svc := &adminServiceImpl{proxyRepo: repo}

	err := svc.DeleteProxy(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, repo.deletedIDs)
}

func TestAdminService_DeleteProxy_Idempotent(t *testing.T) {
	repo := &proxyRepoStub{}
	svc := &adminServiceImpl{proxyRepo: repo}

	err := svc.DeleteProxy(context.Background(), 404)
	require.NoError(t, err)
	require.Equal(t, []int64{404}, repo.deletedIDs)
}

func TestAdminService_DeleteProxy_InUse(t *testing.T) {
	repo := &proxyRepoStub{accountCount: 2}
	svc := &adminServiceImpl{proxyRepo: repo}

	err := svc.DeleteProxy(context.Background(), 77)
	require.ErrorIs(t, err, ErrProxyInUse)
	require.Empty(t, repo.deletedIDs)
}

func TestAdminService_DeleteProxy_Error(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &proxyRepoStub{deleteErr: deleteErr}
	svc := &adminServiceImpl{proxyRepo: repo}

	err := svc.DeleteProxy(context.Background(), 33)
	require.ErrorIs(t, err, deleteErr)
}

func TestAdminService_DeleteRedeemCode_Success(t *testing.T) {
	repo := &redeemRepoStub{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	err := svc.DeleteRedeemCode(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, []int64{10}, repo.deletedIDs)
}

func TestAdminService_DeleteRedeemCode_Idempotent(t *testing.T) {
	repo := &redeemRepoStub{getErrByID: map[int64]error{999: ErrRedeemCodeNotFound}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	err := svc.DeleteRedeemCode(context.Background(), 999)
	require.ErrorIs(t, err, ErrRedeemCodeNotFound)
	require.Empty(t, repo.deletedIDs)
}

func TestAdminService_DeleteRedeemCode_Error(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &redeemRepoStub{deleteErrByID: map[int64]error{1: deleteErr}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	err := svc.DeleteRedeemCode(context.Background(), 1)
	require.ErrorIs(t, err, deleteErr)
	require.Equal(t, []int64{1}, repo.deletedIDs)
}

func TestAdminService_BatchDeleteRedeemCodes_Success(t *testing.T) {
	repo := &redeemRepoStub{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	deleted, err := svc.BatchDeleteRedeemCodes(context.Background(), []int64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.Equal(t, []int64{1, 2, 3}, repo.deletedIDs)
}

func TestAdminService_BatchDeleteRedeemCodes_PartialFailures(t *testing.T) {
	repo := &redeemRepoStub{
		deleteErrByID: map[int64]error{
			2: errors.New("db error"),
		},
	}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	deleted, err := svc.BatchDeleteRedeemCodes(context.Background(), []int64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	require.Equal(t, []int64{1, 2, 3}, repo.deletedIDs)
}

func TestAdminService_UpdateRedeemCode_UnusedBalanceUpdatesValueLimitAndExpiry(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	value := 25.5
	maxUses := 3
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {ID: 1, Code: "R-1", Type: RedeemTypeBalance, Value: 10, Status: StatusUnused, MaxUses: 1},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{
		Value:        &value,
		MaxUses:      &maxUses,
		ExpiresAt:    &expiresAt,
		ExpiresAtSet: true,
	})

	require.NoError(t, err)
	require.Equal(t, value, updated.Value)
	require.Equal(t, maxUses, updated.MaxUses)
	require.Equal(t, &expiresAt, updated.ExpiresAt)
	require.Equal(t, StatusUnused, updated.Status)
	require.Len(t, repo.updatedCodes, 1)
	require.Equal(t, []int64{1}, repo.lockedGetIDs)
}

func TestAdminService_UpdateRedeemCode_UsedValueLocked(t *testing.T) {
	value := 50.0
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {ID: 1, Code: "R-1", Type: RedeemTypeBalance, Value: 10, Status: StatusUsed, MaxUses: 1, UsedCount: 1},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	_, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{Value: &value})

	require.Error(t, err)
	require.ErrorContains(t, err, "value or plan cannot be updated")
	require.Empty(t, repo.updatedCodes)
}

func TestAdminService_UpdateRedeemCode_RejectsMaxUsesBelowUsedCount(t *testing.T) {
	maxUses := 1
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {ID: 1, Code: "R-1", Type: RedeemTypeBalance, Value: 10, Status: StatusActive, MaxUses: 3, UsedCount: 2},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	_, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{MaxUses: &maxUses})

	require.Error(t, err)
	require.ErrorContains(t, err, "max_uses cannot be less than used_count")
	require.Empty(t, repo.updatedCodes)
}

func TestAdminService_UpdateRedeemCode_RestoresExhaustedCodeWhenLimitIncreases(t *testing.T) {
	maxUses := 2
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {ID: 1, Code: "R-1", Type: RedeemTypeBalance, Value: 10, Status: StatusUsed, MaxUses: 1, UsedCount: 1},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{MaxUses: &maxUses})

	require.NoError(t, err)
	require.Equal(t, StatusActive, updated.Status)
	require.Equal(t, maxUses, updated.MaxUses)
}

func TestAdminService_UpdateRedeemCode_RestoresExpiredCodeWhenExpiryCleared(t *testing.T) {
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {
			ID:        1,
			Code:      "R-1",
			Type:      RedeemTypeBalance,
			Value:     10,
			Status:    StatusExpired,
			MaxUses:   3,
			UsedCount: 1,
			ExpiresAt: ptrTime(time.Now().Add(-time.Hour)),
		},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{ExpiresAtSet: true})

	require.NoError(t, err)
	require.Nil(t, updated.ExpiresAt)
	require.Equal(t, StatusActive, updated.Status)
}

func TestAdminService_UpdateRedeemCode_InvitationKeepsExpiry(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	maxUses := 0
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {ID: 1, Code: "INVITE-1", Type: RedeemTypeInvitation, Status: StatusUnused, MaxUses: 1},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{
		MaxUses:      &maxUses,
		ExpiresAt:    &expiresAt,
		ExpiresAtSet: true,
	})

	require.NoError(t, err)
	require.Equal(t, 1, updated.MaxUses)
	require.Equal(t, &expiresAt, updated.ExpiresAt)
	require.Equal(t, StatusUnused, updated.Status)
}

func TestAdminService_UpdateRedeemCode_RejectsSystemRecords(t *testing.T) {
	maxUses := 2
	repo := &redeemRepoStub{codesByID: map[int64]*RedeemCode{
		1: {ID: 1, Code: "AFF-1", Type: RedeemTypeAffiliateBalance, Value: 10, Status: StatusUsed, MaxUses: 1, UsedCount: 1},
	}}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	_, err := svc.UpdateRedeemCode(context.Background(), 1, &UpdateRedeemCodeInput{MaxUses: &maxUses})

	require.Error(t, err)
	require.ErrorContains(t, err, "system redeem records cannot be updated")
	require.Empty(t, repo.updatedCodes)
}
