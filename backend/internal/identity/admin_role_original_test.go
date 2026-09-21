//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

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

func TestAdminService_UpdateUser_RoleOmittedKeepsExisting(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleAdmin}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	newName := "renamed"
	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Username: &newName})
	require.NoError(t, err)
	require.Equal(t, identity.RoleAdmin, updated.Role, "未提供 role 时不应改变现有角色")
}

func TestAdminService_UpdateUser_InvalidRoleRejected(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: "root"})
	require.Error(t, err)
	require.Nil(t, repo.lastUpdated, "非法角色不应触发持久化")
}

// roleGuardUserRepoStub 在 rpmUserRepoStub 之上提供可控的管理员计数，
// 用于测试"最后一个管理员不可降级"守卫。
type roleGuardUserRepoStub struct {
	*rpmUserRepoStub
	adminTotal int64
	listCalls  int
}

func (s *roleGuardUserRepoStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _ identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	s.listCalls++
	return nil, &pagination.PaginationResult{Total: s.adminTotal}, nil
}

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

func TestAdminService_UpdateUser_PromoteDoesNotCountAdmins(t *testing.T) {
	base := &userRepoStub{user: &identity.User{ID: 42, Email: "u@example.com", Role: identity.RoleUser}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Invalidator: &batchLimitsInvalidator{}})

	updated, err := svc.UpdateUser(context.Background(), 42, &identity.UpdateUserInput{Role: identity.RoleAdmin})
	require.NoError(t, err)
	require.Equal(t, identity.RoleAdmin, updated.Role)
	require.Equal(t, 0, repo.listCalls, "升级路径不应触发管理员计数")
}
