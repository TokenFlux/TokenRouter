//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

// rpmUserRepoStub 复用 admin_service_update_balance_test.go 的基础 stub 结构，
// 只在 Update 时把入参克隆一份，便于断言修改后的 RPMLimit。
type rpmUserRepoStub struct {
	*userRepoStub
	lastUpdated *identity.User
	lastFields  identity.UserUpdateFields
}

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
