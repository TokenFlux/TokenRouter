//go:build unit

package billing_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/stretchr/testify/require"
)

// userGroupRateRepoStubForGroupRate 记录专属倍率与 RPM 配置操作。
type userGroupRateRepoStubForGroupRate struct {
	getByGroupIDData map[int64][]billing.UserGroupRateEntry
	getByGroupIDErr  error

	deletedGroupIDs  []int64
	deleteByGroupErr error

	syncedGroupID int64
	syncedEntries []billing.GroupRateMultiplierInput
	syncGroupErr  error

	rpmSyncedGroupID int64
	rpmSyncedEntries []billing.GroupRPMOverrideInput
	rpmSyncErr       error
}

func (s *userGroupRateRepoStubForGroupRate) GetByUserID(_ context.Context, _ int64) (map[int64]float64, error) {
	panic("unexpected GetByUserID call")
}

func (s *userGroupRateRepoStubForGroupRate) GetByUserAndGroup(_ context.Context, _, _ int64) (*float64, error) {
	panic("unexpected GetByUserAndGroup call")
}

func (s *userGroupRateRepoStubForGroupRate) GetRPMOverrideByUserAndGroup(_ context.Context, _, _ int64) (*int, error) {
	panic("unexpected GetRPMOverrideByUserAndGroup call")
}

func (s *userGroupRateRepoStubForGroupRate) GetByGroupID(_ context.Context, groupID int64) ([]billing.UserGroupRateEntry, error) {
	if s.getByGroupIDErr != nil {
		return nil, s.getByGroupIDErr
	}
	return s.getByGroupIDData[groupID], nil
}

func (s *userGroupRateRepoStubForGroupRate) SyncUserGroupRates(_ context.Context, _ int64, _ map[int64]*float64) error {
	panic("unexpected SyncUserGroupRates call")
}

func (s *userGroupRateRepoStubForGroupRate) SyncGroupRateMultipliers(_ context.Context, groupID int64, entries []billing.GroupRateMultiplierInput) error {
	s.syncedGroupID = groupID
	s.syncedEntries = entries
	return s.syncGroupErr
}

func (s *userGroupRateRepoStubForGroupRate) SyncGroupRPMOverrides(_ context.Context, groupID int64, entries []billing.GroupRPMOverrideInput) error {
	s.rpmSyncedGroupID = groupID
	s.rpmSyncedEntries = entries
	return s.rpmSyncErr
}

func (s *userGroupRateRepoStubForGroupRate) ClearGroupRPMOverrides(_ context.Context, _ int64) error {
	panic("unexpected ClearGroupRPMOverrides call")
}

func (s *userGroupRateRepoStubForGroupRate) DeleteByGroupID(_ context.Context, groupID int64) error {
	s.deletedGroupIDs = append(s.deletedGroupIDs, groupID)
	return s.deleteByGroupErr
}

func (s *userGroupRateRepoStubForGroupRate) DeleteByUserID(_ context.Context, _ int64) error {
	panic("unexpected DeleteByUserID call")
}

func TestAdminService_GetGroupRateMultipliers(t *testing.T) {
	t.Run("returns entries for group", func(t *testing.T) {
		aliceRate, bobRate := 1.5, 0.8
		repo := &userGroupRateRepoStubForGroupRate{
			getByGroupIDData: map[int64][]billing.UserGroupRateEntry{
				10: {
					{UserID: 1, UserName: "alice", UserEmail: "alice@test.com", RateMultiplier: &aliceRate},
					{UserID: 2, UserName: "bob", UserEmail: "bob@test.com", RateMultiplier: &bobRate},
				},
			},
		}
		svc := billing.NewGroupRateAdmin(repo, nil)

		entries, err := svc.GetGroupRateMultipliers(context.Background(), 10)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.Equal(t, int64(1), entries[0].UserID)
		require.Equal(t, "alice", entries[0].UserName)
		require.NotNil(t, entries[0].RateMultiplier)
		require.Equal(t, 1.5, *entries[0].RateMultiplier)
		require.Equal(t, int64(2), entries[1].UserID)
		require.NotNil(t, entries[1].RateMultiplier)
		require.Equal(t, 0.8, *entries[1].RateMultiplier)
	})

	t.Run("returns nil when repo is nil", func(t *testing.T) {
		svc := billing.NewGroupRateAdmin(nil, nil)

		entries, err := svc.GetGroupRateMultipliers(context.Background(), 10)
		require.NoError(t, err)
		require.Nil(t, entries)
	})

	t.Run("returns empty slice for group with no entries", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{
			getByGroupIDData: map[int64][]billing.UserGroupRateEntry{},
		}
		svc := billing.NewGroupRateAdmin(repo, nil)

		entries, err := svc.GetGroupRateMultipliers(context.Background(), 99)
		require.NoError(t, err)
		require.Nil(t, entries)
	})

	t.Run("propagates repo error", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{
			getByGroupIDErr: errors.New("db error"),
		}
		svc := billing.NewGroupRateAdmin(repo, nil)

		_, err := svc.GetGroupRateMultipliers(context.Background(), 10)
		require.Error(t, err)
		require.Contains(t, err.Error(), "db error")
	})
}

func TestAdminService_ClearGroupRateMultipliers(t *testing.T) {
	t.Run("deletes by group ID", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{}
		svc := billing.NewGroupRateAdmin(repo, nil)

		err := svc.ClearGroupRateMultipliers(context.Background(), 42)
		require.NoError(t, err)
		require.Equal(t, []int64{42}, repo.deletedGroupIDs)
	})

	t.Run("returns nil when repo is nil", func(t *testing.T) {
		svc := billing.NewGroupRateAdmin(nil, nil)

		err := svc.ClearGroupRateMultipliers(context.Background(), 42)
		require.NoError(t, err)
	})

	t.Run("propagates repo error", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{
			deleteByGroupErr: errors.New("delete failed"),
		}
		svc := billing.NewGroupRateAdmin(repo, nil)

		err := svc.ClearGroupRateMultipliers(context.Background(), 42)
		require.Error(t, err)
		require.Contains(t, err.Error(), "delete failed")
	})
}

func TestAdminService_BatchSetGroupRateMultipliers(t *testing.T) {
	t.Run("syncs entries to repo", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{}
		svc := billing.NewGroupRateAdmin(repo, nil)

		entries := []billing.GroupRateMultiplierInput{
			{UserID: 1, RateMultiplier: 1.5},
			{UserID: 2, RateMultiplier: 0.8},
		}
		err := svc.BatchSetGroupRateMultipliers(context.Background(), 10, entries)
		require.NoError(t, err)
		require.Equal(t, int64(10), repo.syncedGroupID)
		require.Equal(t, entries, repo.syncedEntries)
	})

	t.Run("returns nil when repo is nil", func(t *testing.T) {
		svc := billing.NewGroupRateAdmin(nil, nil)

		err := svc.BatchSetGroupRateMultipliers(context.Background(), 10, nil)
		require.NoError(t, err)
	})

	t.Run("propagates repo error", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{
			syncGroupErr: errors.New("sync failed"),
		}
		svc := billing.NewGroupRateAdmin(repo, nil)

		err := svc.BatchSetGroupRateMultipliers(context.Background(), 10, []billing.GroupRateMultiplierInput{
			{UserID: 1, RateMultiplier: 1.0},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "sync failed")
	})
}

func TestAdminService_BatchSetGroupRPMOverrides(t *testing.T) {
	t.Run("syncs entries to repo", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{}
		svc := billing.NewGroupRateAdmin(repo, nil)
		override := 20
		entries := []billing.GroupRPMOverrideInput{{UserID: 2, RPMOverride: &override}}

		err := svc.BatchSetGroupRPMOverrides(context.Background(), 10, entries)
		require.NoError(t, err)
		require.Equal(t, int64(10), repo.rpmSyncedGroupID)
		require.Equal(t, entries, repo.rpmSyncedEntries)
	})

	t.Run("rejects negative override as bad request", func(t *testing.T) {
		repo := &userGroupRateRepoStubForGroupRate{}
		svc := billing.NewGroupRateAdmin(repo, nil)
		negative := -1

		err := svc.BatchSetGroupRPMOverrides(context.Background(), 10, []billing.GroupRPMOverrideInput{
			{UserID: 2, RPMOverride: &negative},
		})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
		require.Zero(t, repo.rpmSyncedGroupID)
	})
}
