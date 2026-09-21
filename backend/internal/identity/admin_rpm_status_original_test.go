//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

type rpmStatusUserRepoStub struct {
	identity.UserRepository

	user *identity.User
}

func (s *rpmStatusUserRepoStub) GetByID(_ context.Context, _ int64) (*identity.User, error) {
	return s.user, nil
}

type rpmStatusAPIKeyRepoStub struct {
	identity.AdminKeyReader
	keys []identity.AdminKeySummary
}

func (s *rpmStatusAPIKeyRepoStub) List(_ context.Context, _ int64, _, _ int, _, _ string) ([]identity.AdminKeySummary, int64, error) {
	return s.keys, int64(len(s.keys)), nil
}

type rpmStatusGroupRepoStub struct {
	identity.AdminGroupReader

	groups map[int64]*identity.AdminGroup
}

func (s *rpmStatusGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*identity.AdminGroup, error) {
	return s.groups[id], nil
}

type rpmStatusRateRepoStub struct {
	billing.UserGroupRateRepository
	overrides map[int64]*int
}

func (s *rpmStatusRateRepoStub) GetRPMOverrideByUserAndGroup(_ context.Context, _, groupID int64) (*int, error) {
	return s.overrides[groupID], nil
}

type rpmStatusCacheStub struct {
	scheduler.UserRPMCache
	userUsed  int
	groupUsed map[int64]int
}

func (s *rpmStatusCacheStub) IncrementUserGroupRPM(context.Context, int64, int64) (int, error) {
	return 0, nil
}

func (s *rpmStatusCacheStub) IncrementUserRPM(context.Context, int64) (int, error) {
	return 0, nil
}

func (s *rpmStatusCacheStub) GetUserGroupRPM(_ context.Context, _, groupID int64) (int, error) {
	return s.groupUsed[groupID], nil
}

func (s *rpmStatusCacheStub) GetUserRPM(context.Context, int64) (int, error) {
	return s.userUsed, nil
}

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
