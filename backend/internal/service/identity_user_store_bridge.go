// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	_ "image/png"
	time "time"
)

// legacyIdentityUsers 只转换旧用户字段；旧消费者清零后删除。
type legacyIdentityUsers struct{ Repository UserRepository }

func (p legacyIdentityUsers) Create(ctx context.Context, user *identity.User) error {
	legacy := UserFromIdentity(user)
	err := p.Repository.Create(ctx, legacy)
	if legacy != nil && user != nil {
		*user = *IdentityUser(legacy)
	}
	return err
}
func (p legacyIdentityUsers) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	legacy := UserFromIdentity(user)
	err := p.Repository.CreateWithNormalizedEmailGuard(ctx, legacy, normalizedEmail)
	if legacy != nil && user != nil {
		*user = *IdentityUser(legacy)
	}
	return err
}
func (p legacyIdentityUsers) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	v, err := p.Repository.GetByID(ctx, id)
	return IdentityUser(v), err
}
func (p legacyIdentityUsers) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	v, err := p.Repository.GetByIDIncludeDeleted(ctx, id)
	return IdentityUser(v), err
}
func (p legacyIdentityUsers) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	v, err := p.Repository.GetByEmail(ctx, email)
	return IdentityUser(v), err
}
func (p legacyIdentityUsers) GetFirstAdmin(ctx context.Context) (*identity.User, error) {
	v, err := p.Repository.GetFirstAdmin(ctx)
	return IdentityUser(v), err
}
func (p legacyIdentityUsers) Update(ctx context.Context, user *identity.User, fields UserUpdateFields) error {
	legacy := UserFromIdentity(user)
	err := p.Repository.Update(ctx, legacy, fields)
	if legacy != nil && user != nil {
		*user = *IdentityUser(legacy)
	}
	return err
}
func (p legacyIdentityUsers) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string, fields UserUpdateFields) error {
	legacy := UserFromIdentity(user)
	err := p.Repository.UpdateWithNormalizedEmailGuard(ctx, legacy, normalizedEmail, fields)
	if legacy != nil && user != nil {
		*user = *IdentityUser(legacy)
	}
	return err
}
func (p legacyIdentityUsers) Delete(ctx context.Context, id int64) error {
	return p.Repository.Delete(ctx, id)
}
func (p legacyIdentityUsers) GetUserAvatar(ctx context.Context, userID int64) (*UserAvatar, error) {
	return p.Repository.GetUserAvatar(ctx, userID)
}
func (p legacyIdentityUsers) UpsertUserAvatar(ctx context.Context, userID int64, input UpsertUserAvatarInput) (*UserAvatar, error) {
	return p.Repository.UpsertUserAvatar(ctx, userID, input)
}
func (p legacyIdentityUsers) DeleteUserAvatar(ctx context.Context, userID int64) error {
	return p.Repository.DeleteUserAvatar(ctx, userID)
}
func (p legacyIdentityUsers) List(ctx context.Context, params pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	v, page, err := p.Repository.List(ctx, params)
	if v == nil {
		return nil, page, err
	}
	out := make([]identity.User, len(v))
	for i := range v {
		out[i] = *IdentityUser(&v[i])
	}
	return out, page, err
}
func (p legacyIdentityUsers) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	v, page, err := p.Repository.ListWithFilters(ctx, params, filters)
	if v == nil {
		return nil, page, err
	}
	out := make([]identity.User, len(v))
	for i := range v {
		out[i] = *IdentityUser(&v[i])
	}
	return out, page, err
}
func (p legacyIdentityUsers) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	return p.Repository.GetLatestUsedAtByUserIDs(ctx, userIDs)
}
func (p legacyIdentityUsers) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	return p.Repository.GetLatestUsedAtByUserID(ctx, userID)
}
func (p legacyIdentityUsers) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	return p.Repository.UpdateUserLastActiveAt(ctx, userID, activeAt)
}
func (p legacyIdentityUsers) AddBalance(ctx context.Context, id int64, amount float64) error {
	return p.Repository.AddBalance(ctx, id, amount)
}
func (p legacyIdentityUsers) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	return p.Repository.UpdateBalance(ctx, id, amount)
}
func (p legacyIdentityUsers) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return p.Repository.DeductBalance(ctx, id, amount)
}
func (p legacyIdentityUsers) AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error) {
	return p.Repository.AdjustBalance(ctx, id, delta)
}
func (p legacyIdentityUsers) SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error) {
	return p.Repository.SetBalance(ctx, id, value)
}
func (p legacyIdentityUsers) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	return p.Repository.UpdateConcurrency(ctx, id, amount)
}
func (p legacyIdentityUsers) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	return p.Repository.BatchSetConcurrency(ctx, userIDs, value)
}
func (p legacyIdentityUsers) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	return p.Repository.BatchAddConcurrency(ctx, userIDs, delta)
}
func (p legacyIdentityUsers) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	return p.Repository.BatchUpdateLimits(ctx, userIDs, concurrency, rpmLimit)
}
func (p legacyIdentityUsers) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return p.Repository.ExistsByEmail(ctx, email)
}
func (p legacyIdentityUsers) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	return p.Repository.ExistsByNormalizedEmail(ctx, normalizedEmail)
}
func (p legacyIdentityUsers) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	return p.Repository.LockRegistrationEmail(ctx, normalizedEmail)
}
func (p legacyIdentityUsers) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	return p.Repository.RemoveGroupFromAllowedGroups(ctx, groupID)
}
func (p legacyIdentityUsers) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	return p.Repository.AddGroupToAllowedGroups(ctx, userID, groupID)
}
func (p legacyIdentityUsers) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	return p.Repository.RemoveGroupFromUserAllowedGroups(ctx, userID, groupID)
}
func (p legacyIdentityUsers) ListUserAuthIdentities(ctx context.Context, userID int64) ([]UserAuthIdentityRecord, error) {
	return p.Repository.ListUserAuthIdentities(ctx, userID)
}
func (p legacyIdentityUsers) UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) error {
	return p.Repository.UnbindUserAuthProvider(ctx, userID, provider)
}
func (p legacyIdentityUsers) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	return p.Repository.UpdateTotpSecret(ctx, userID, encryptedSecret)
}
func (p legacyIdentityUsers) EnableTotp(ctx context.Context, userID int64) error {
	return p.Repository.EnableTotp(ctx, userID)
}
func (p legacyIdentityUsers) DisableTotp(ctx context.Context, userID int64) error {
	return p.Repository.DisableTotp(ctx, userID)
}
func (p legacyIdentityUsers) WithUserProfileIdentityTx(ctx context.Context, fn func(context.Context) error) error {
	if tx, ok := p.Repository.(interface {
		WithUserProfileIdentityTx(context.Context, func(context.Context) error) error
	}); ok {
		return tx.WithUserProfileIdentityTx(ctx, fn)
	}
	return fn(ctx)
}
