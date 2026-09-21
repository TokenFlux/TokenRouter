//go:build unit

package routing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

type groupRepoStub struct {
	affectedUserIDs []int64
	deleteErr       error
	deleteCalls     []int64
}

func (s *groupRepoStub) Create(ctx context.Context, group *routing.Group) error {
	panic("unexpected Create call")
}

func (s *groupRepoStub) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	panic("unexpected GetByID call")
}

func (s *groupRepoStub) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	panic("unexpected GetByIDLite call")
}

func (s *groupRepoStub) Update(ctx context.Context, group *routing.Group) error {
	panic("unexpected Update call")
}

func (s *groupRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStub) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	s.deleteCalls = append(s.deleteCalls, id)
	return s.affectedUserIDs, s.deleteErr
}

func (s *groupRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStub) ListActive(ctx context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStub) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
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

func (s *groupRepoStub) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	return nil
}

type deleteGroupAPIKeyRepoStub struct {
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

func TestAdminService_DeleteGroup_Success(t *testing.T) {
	repo := &groupRepoStub{affectedUserIDs: []int64{11, 12}}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	err := svc.DeleteGroup(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, repo.deleteCalls)
}

func TestAdminService_DeleteGroup_InvalidatesAuthCacheForBoundKeys(t *testing.T) {
	repo := &groupRepoStub{}
	apiKeyRepo := &deleteGroupAPIKeyRepoStub{keys: []string{"k1", "k2"}}
	invalidator := &authCacheInvalidatorStub{}
	svc := newOriginalGroupAdminPorts(repo, nil, nil, nil, nil, invalidator, nil, apiKeyRepo)

	err := svc.DeleteGroup(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, repo.deleteCalls)
	require.Equal(t, []int64{5}, apiKeyRepo.listGroupIDs)
	require.Equal(t, []string{"k1", "k2"}, invalidator.keys)
}

func TestAdminService_DeleteGroup_NotFound(t *testing.T) {
	repo := &groupRepoStub{deleteErr: routing.ErrGroupNotFound}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	err := svc.DeleteGroup(context.Background(), 99)
	require.ErrorIs(t, err, routing.ErrGroupNotFound)
}

func TestAdminService_DeleteGroup_Error(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &groupRepoStub{deleteErr: deleteErr}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	err := svc.DeleteGroup(context.Background(), 42)
	require.ErrorIs(t, err, deleteErr)
}
