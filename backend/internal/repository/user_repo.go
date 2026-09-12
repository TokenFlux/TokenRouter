// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	predicate "github.com/TokenFlux/TokenRouter/ent/predicate"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

const normalizedUserEmailSQL = identitypostgres.IdentityNormalizedUserEmailSQL

type userRepository struct {
	*identitypostgres.UserStore
	client *dbent.Client
	sql    sqlExecutor
}

func NewUserRepository(client *dbent.Client, sqlDB *sql.DB) service.UserRepository {
	return newUserRepositoryWithSQL(client, sqlDB)
}

func newUserRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor) *userRepository {
	return &userRepository{UserStore: identitypostgres.NewUserStoreWithSQL(client, sqlq), client: client, sql: sqlq}
}

// Create 转接身份存储，原事务 context 原样传递。
func (r *userRepository) Create(ctx context.Context, userIn *service.User) error {
	projected := service.IdentityUser(userIn)
	err := r.UserStore.Create(ctx, projected)
	service.ApplyIdentityUser(userIn, projected)
	return err
}

// CreateWithNormalizedEmailGuard 转接身份存储，原事务 context 原样传递。
func (r *userRepository) CreateWithNormalizedEmailGuard(ctx context.Context, userIn *service.User, normalizedEmail string) error {
	projected := service.IdentityUser(userIn)
	err := r.UserStore.CreateWithNormalizedEmailGuard(ctx, projected, normalizedEmail)
	service.ApplyIdentityUser(userIn, projected)
	return err
}

// CountUsersByEmailDomain 转接身份存储，原事务 context 原样传递。
func (r *userRepository) CountUsersByEmailDomain(ctx context.Context, domain string) (int, error) {
	return r.UserStore.CountUsersByEmailDomain(ctx, domain)
}

// CreateWithRegistrationEmailGuards 转接身份存储，原事务 context 原样传递。
func (r *userRepository) CreateWithRegistrationEmailGuards(ctx context.Context, userIn *service.User, normalizedEmail, domain string) error {
	projected := service.IdentityUser(userIn)
	err := r.UserStore.CreateWithRegistrationEmailGuards(ctx, projected, normalizedEmail, domain)
	service.ApplyIdentityUser(userIn, projected)
	return err
}

// GetByID 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetByID(ctx context.Context, id int64) (*service.User, error) {
	value, err := r.UserStore.GetByID(ctx, id)
	return service.UserFromIdentity(value), err
}

// GetByIDIncludeDeleted 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetByIDIncludeDeleted(ctx context.Context, id int64) (*service.User, error) {
	value, err := r.UserStore.GetByIDIncludeDeleted(ctx, id)
	return service.UserFromIdentity(value), err
}

// GetByEmail 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetByEmail(ctx context.Context, email string) (*service.User, error) {
	value, err := r.UserStore.GetByEmail(ctx, email)
	return service.UserFromIdentity(value), err
}

// Update 转接身份存储，原事务 context 原样传递。
func (r *userRepository) Update(ctx context.Context, userIn *service.User, fields service.UserUpdateFields) error {
	projected := service.IdentityUser(userIn)
	err := r.UserStore.Update(ctx, projected, fields)
	service.ApplyIdentityUser(userIn, projected)
	return err
}

// UpdateWithNormalizedEmailGuard 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateWithNormalizedEmailGuard(ctx context.Context, userIn *service.User, normalizedEmail string, fields service.UserUpdateFields) error {
	projected := service.IdentityUser(userIn)
	err := r.UserStore.UpdateWithNormalizedEmailGuard(ctx, projected, normalizedEmail, fields)
	service.ApplyIdentityUser(userIn, projected)
	return err
}

// Delete 转接身份存储，原事务 context 原样传递。
func (r *userRepository) Delete(ctx context.Context, id int64) error {
	return r.UserStore.Delete(ctx, id)
}

// List 转接身份存储，原事务 context 原样传递。
func (r *userRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.User, *pagination.PaginationResult, error) {
	values, page, err := r.UserStore.List(ctx, params)
	if values == nil {
		return nil, page, err
	}
	out := make([]service.User, len(values))
	for i := range values {
		out[i] = *service.UserFromIdentity(&values[i])
	}
	return out, page, err
}

// ListWithFilters 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters service.UserListFilters) ([]service.User, *pagination.PaginationResult, error) {
	values, page, err := r.UserStore.ListWithFilters(ctx, params, filters)
	if values == nil {
		return nil, page, err
	}
	out := make([]service.User, len(values))
	for i := range values {
		out[i] = *service.UserFromIdentity(&values[i])
	}
	return out, page, err
}

// GetLatestUsedAtByUserIDs 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	return r.UserStore.GetLatestUsedAtByUserIDs(ctx, userIDs)
}

// GetLatestUsedAtByUserID 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	return r.UserStore.GetLatestUsedAtByUserID(ctx, userID)
}

// UpdateBalance 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	return r.UserStore.UpdateBalance(ctx, id, amount)
}

// AddBalance 转接身份存储，原事务 context 原样传递。
func (r *userRepository) AddBalance(ctx context.Context, id int64, amount float64) error {
	return r.UserStore.AddBalance(ctx, id, amount)
}

// ApplyRedeemBalanceAdjustment 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ApplyRedeemBalanceAdjustment(ctx context.Context, id int64, delta float64) error {
	return r.UserStore.ApplyRedeemBalanceAdjustment(ctx, id, delta)
}

// DeductBalance 转接身份存储，原事务 context 原样传递。
func (r *userRepository) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return r.UserStore.DeductBalance(ctx, id, amount)
}

// AdjustBalance 转接身份存储，原事务 context 原样传递。
func (r *userRepository) AdjustBalance(ctx context.Context, id int64, delta float64) (service.BalanceChange, error) {
	return r.UserStore.AdjustBalance(ctx, id, delta)
}

// SetBalance 转接身份存储，原事务 context 原样传递。
func (r *userRepository) SetBalance(ctx context.Context, id int64, value float64) (service.BalanceChange, error) {
	return r.UserStore.SetBalance(ctx, id, value)
}

// UpdateConcurrency 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	return r.UserStore.UpdateConcurrency(ctx, id, amount)
}

// ApplyRedeemConcurrencyAdjustment 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ApplyRedeemConcurrencyAdjustment(ctx context.Context, id int64, delta int) error {
	return r.UserStore.ApplyRedeemConcurrencyAdjustment(ctx, id, delta)
}

// BatchSetConcurrency 转接身份存储，原事务 context 原样传递。
func (r *userRepository) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	return r.UserStore.BatchSetConcurrency(ctx, userIDs, value)
}

// BatchAddConcurrency 转接身份存储，原事务 context 原样传递。
func (r *userRepository) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	return r.UserStore.BatchAddConcurrency(ctx, userIDs, delta)
}

// BatchUpdateLimits 转接身份存储，原事务 context 原样传递。
func (r *userRepository) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	return r.UserStore.BatchUpdateLimits(ctx, userIDs, concurrency, rpmLimit)
}

// ExistsByEmail 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return r.UserStore.ExistsByEmail(ctx, email)
}

// ExistsByEmailAlias 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ExistsByEmailAlias(ctx context.Context, email string) (bool, error) {
	return r.UserStore.ExistsByEmailAlias(ctx, email)
}

// EmailAliasOwnerID 转接身份存储，原事务 context 原样传递。
func (r *userRepository) EmailAliasOwnerID(ctx context.Context, email string, currentUserID int64) (int64, bool, error) {
	return r.UserStore.EmailAliasOwnerID(ctx, email, currentUserID)
}

// UpdateEmailWithAliasGuard 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateEmailWithAliasGuard(
	ctx context.Context,
	userID int64,
	email string,
	passwordHash string,
) error {
	return r.UserStore.UpdateEmailWithAliasGuard(ctx, userID, email, passwordHash)
}

// ExistsByNormalizedEmail 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	return r.UserStore.ExistsByNormalizedEmail(ctx, normalizedEmail)
}

// ExistsByNormalizedEmailExcluding 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ExistsByNormalizedEmailExcluding(ctx context.Context, normalizedEmail string, excludedUserID int64) (bool, error) {
	return r.UserStore.ExistsByNormalizedEmailExcluding(ctx, normalizedEmail, excludedUserID)
}

// LockRegistrationEmail 转接身份存储，原事务 context 原样传递。
func (r *userRepository) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	return r.UserStore.LockRegistrationEmail(ctx, normalizedEmail)
}

// userEmailLookupPredicate 转接身份存储，原事务 context 原样传递。
func userEmailLookupPredicate(email string) predicate.User {
	return identitypostgres.IdentityUserEmailLookupPredicate(email)
}

// AddGroupToAllowedGroups 转接身份存储，原事务 context 原样传递。
func (r *userRepository) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	return r.UserStore.AddGroupToAllowedGroups(ctx, userID, groupID)
}

// RemoveGroupFromAllowedGroups 转接身份存储，原事务 context 原样传递。
func (r *userRepository) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	return r.UserStore.RemoveGroupFromAllowedGroups(ctx, groupID)
}

// RemoveGroupFromUserAllowedGroups 转接身份存储，原事务 context 原样传递。
func (r *userRepository) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	return r.UserStore.RemoveGroupFromUserAllowedGroups(ctx, userID, groupID)
}

// GetFirstAdmin 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetFirstAdmin(ctx context.Context) (*service.User, error) {
	value, err := r.UserStore.GetFirstAdmin(ctx)
	return service.UserFromIdentity(value), err
}

// UpdateTotpSecret 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	return r.UserStore.UpdateTotpSecret(ctx, userID, encryptedSecret)
}

// EnableTotp 转接身份存储，原事务 context 原样传递。
func (r *userRepository) EnableTotp(ctx context.Context, userID int64) error {
	return r.UserStore.EnableTotp(ctx, userID)
}

// DisableTotp 转接身份存储，原事务 context 原样传递。
func (r *userRepository) DisableTotp(ctx context.Context, userID int64) error {
	return r.UserStore.DisableTotp(ctx, userID)
}

// IdentityRepository 交付同一生产存储实例，不再经过旧用户字段转换。
func (r *userRepository) IdentityRepository() identity.UserRepository { return r.UserStore }

// WrapUserStore 让旧消费者共用 app 创建的身份存储，不重新打开连接或复制实现。
func WrapUserStore(client *dbent.Client, sqlDB *sql.DB, store *identitypostgres.UserStore) service.UserRepository {
	return &userRepository{UserStore: store, client: client, sql: sqlDB}
}
