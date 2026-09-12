// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type legacyUserAdministration struct{ service.AdminService }

func (a legacyUserAdministration) ListUsers(ctx context.Context, page, pageSize int, filters identity.UserListFilters, sortBy, sortOrder string) ([]identity.User, int64, error) {
	v, total, e := a.AdminService.ListUsers(ctx, page, pageSize, filters, sortBy, sortOrder)
	if v == nil {
		return nil, total, e
	}
	out := make([]identity.User, len(v))
	for i := range v {
		out[i] = *service.IdentityUser(&v[i])
	}
	return out, total, e
}
func (a legacyUserAdministration) GetUser(ctx context.Context, id int64) (*identity.User, error) {
	v, e := a.AdminService.GetUser(ctx, id)
	return service.IdentityUser(v), e
}
func (a legacyUserAdministration) GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	v, e := a.AdminService.GetUserIncludeDeleted(ctx, id)
	return service.IdentityUser(v), e
}
func (a legacyUserAdministration) CreateUser(ctx context.Context, input *identity.CreateUserInput) (*identity.User, error) {
	v, e := a.AdminService.CreateUser(ctx, input)
	return service.IdentityUser(v), e
}
func (a legacyUserAdministration) UpdateUser(ctx context.Context, id int64, input *identity.UpdateUserInput) (*identity.User, error) {
	v, e := a.AdminService.UpdateUser(ctx, id, input)
	return service.IdentityUser(v), e
}
func (a legacyUserAdministration) DeleteUser(ctx context.Context, id int64) error {
	return a.AdminService.DeleteUser(ctx, id)
}
func (a legacyUserAdministration) BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error) {
	return a.AdminService.BatchUpdateConcurrency(ctx, userIDs, value, mode)
}
func (a legacyUserAdministration) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	return a.AdminService.BatchUpdateLimits(ctx, userIDs, concurrency, rpmLimit)
}
func (a legacyUserAdministration) UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*identity.User, error) {
	v, e := a.AdminService.UpdateUserBalance(ctx, userID, balance, operation, notes)
	return service.IdentityUser(v), e
}
func (a legacyUserAdministration) GetUserRPMStatus(ctx context.Context, userID int64) (*identity.UserRPMStatus, error) {
	return a.AdminService.GetUserRPMStatus(ctx, userID)
}
func (a legacyUserAdministration) GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error) {
	return a.AdminService.GetUserUsageStats(ctx, userID, period)
}
func (a legacyUserAdministration) GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]identity.RedeemCode, int64, float64, error) {
	return a.AdminService.GetUserBalanceHistory(ctx, userID, page, pageSize, codeType)
}
func (a legacyUserAdministration) BindUserAuthIdentity(ctx context.Context, userID int64, input identity.AdminBindAuthIdentityInput) (*identity.AdminBoundAuthIdentity, error) {
	return a.AdminService.BindUserAuthIdentity(ctx, userID, input)
}
func (a legacyUserAdministration) ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*identity.ReplaceUserGroupResult, error) {
	return a.AdminService.ReplaceUserGroup(ctx, userID, oldGroupID, newGroupID)
}

// legacyUserAdministration 只用于尚未迁出的构造器或测试替身，生产优先取得已固定的 identity 用例。
func newIdentityUserAdministration(s service.AdminService) identityhttp.UserAdministration {
	if actual, ok := s.(interface{ IdentityAdministration() *identity.UserAdmin }); ok {
		return actual.IdentityAdministration()
	}
	return legacyUserAdministration{s}
}
