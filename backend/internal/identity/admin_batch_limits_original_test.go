//go:build unit

package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/stretchr/testify/require"
)

type batchLimitsUserRepoStub struct {
	identity.UserRepository
	calls       int
	userIDs     []int64
	concurrency *int
	rpmLimit    *int
	affected    int
	err         error
}

func (s *batchLimitsUserRepoStub) BatchUpdateLimits(_ context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	s.calls++
	s.userIDs = append([]int64(nil), userIDs...)
	s.concurrency = cloneBatchLimitValue(concurrency)
	s.rpmLimit = cloneBatchLimitValue(rpmLimit)
	return s.affected, s.err
}

func cloneBatchLimitValue(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

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

func TestAdminServiceBatchUpdateLimitsRequiresAField(t *testing.T) {
	repo := &batchLimitsUserRepoStub{}
	service := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: &batchLimitsInvalidator{}})

	affected, err := service.BatchUpdateLimits(context.Background(), []int64{1}, nil, nil)

	require.Error(t, err)
	require.Zero(t, affected)
	require.Zero(t, repo.calls)
}

func pointerToInt(value int) *int {
	return &value
}

// batchLimitsInvalidator 只记录本批操作的成功后认证失效。
type batchLimitsInvalidator struct{ userIDs []int64 }

func (s *batchLimitsInvalidator) InvalidateAuthCacheByUserID(_ context.Context, id int64) {
	s.userIDs = append(s.userIDs, id)
}
func (s *batchLimitsInvalidator) InvalidateAuthCacheByKey(context.Context, string) {
	panic("unexpected key invalidation")
}
