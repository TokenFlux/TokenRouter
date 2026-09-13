// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

type apiKeyRepository struct {
	*keypostgres.KeyStore
	client         *dbent.Client
	sql            sqlExecutor
	preAggregation *service.PreAggregationSettingsService
}

// ProvideAPIKeyRepository 注入统一预聚合配置，供用量排序复用多维聚合表。
func ProvideAPIKeyRepository(client *dbent.Client, sqlDB *sql.DB, preAggregation *service.PreAggregationSettingsService) service.APIKeyRepository {
	repo := newAPIKeyRepositoryWithSQL(client, sqlDB)
	repo.preAggregation = preAggregation
	return repo
}

func NewAPIKeyRepository(client *dbent.Client, sqlDB *sql.DB) service.APIKeyRepository {
	return newAPIKeyRepositoryWithSQL(client, sqlDB)
}

func newAPIKeyRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor) *apiKeyRepository {
	r := &apiKeyRepository{client: client, sql: sqlq}
	r.KeyStore = keypostgres.NewKeyStoreWithSQL(client, sqlq, func(ctx context.Context, ids []int64) (map[int64]float64, error) {
		return ReadAPIKeyUsageTotals(ctx, sqlq, r.preAggregation, ids)
	})
	return r
}

// Create 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) Create(ctx context.Context, key *service.APIKey) error {
	keyView := service.APIKeyView(key)
	err := r.KeyStore.Create(ctx, keyView)
	service.ApplyAPIKeyView(key, keyView)
	return err
}

// GetByID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	v, e := r.KeyStore.GetByID(ctx, id)
	return service.APIKeyFromView(v), e
}

// GetManagedKeyByUserAndGroup 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*service.APIKey, error) {
	v, e := r.KeyStore.GetManagedKeyByUserAndGroup(ctx, userID, groupID, managedBy)
	return service.APIKeyFromView(v), e
}

// CreateManagedKey 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) CreateManagedKey(ctx context.Context, key *service.APIKey) error {
	keyView := service.APIKeyView(key)
	err := r.KeyStore.CreateManagedKey(ctx, keyView)
	service.ApplyAPIKeyView(key, keyView)
	return err
}

// GetKeyAndOwnerID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	return r.KeyStore.GetKeyAndOwnerID(ctx, id)
}

// GetByKey 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	v, e := r.KeyStore.GetByKey(ctx, key)
	return service.APIKeyFromView(v), e
}

// GetByKeyForAuth 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	v, e := r.KeyStore.GetByKeyForAuth(ctx, key)
	return service.APIKeyFromView(v), e
}

// Update 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) Update(ctx context.Context, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	keyView := service.APIKeyView(key)
	err := r.KeyStore.Update(ctx, keyView, fields)
	service.ApplyAPIKeyView(key, keyView)
	return err
}

// Delete 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) Delete(ctx context.Context, id int64) error {
	return r.KeyStore.Delete(ctx, id)
}

// DeleteWithAudit 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) DeleteWithAudit(ctx context.Context, id int64) error {
	return r.KeyStore.DeleteWithAudit(ctx, id)
}

// ListByUserID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	v, page, e := r.KeyStore.ListByUserID(ctx, userID, params, filters)
	return repositoryKeysFromView(v), page, e
}

// ListAllByUserID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ListAllByUserID(ctx context.Context, userID int64, filters service.APIKeyListFilters) ([]service.APIKey, error) {
	v, e := r.KeyStore.ListAllByUserID(ctx, userID, filters)
	return repositoryKeysFromView(v), e
}

// latestUsageLogIPsQuery 转接 Key 存储，不自行提交事务。
func latestUsageLogIPsQuery(apiKeyIDs []int64, dialectName string) (string, []any) {
	return keypostgres.KeyLatestUsageLogIPsQuery(apiKeyIDs, dialectName)
}

// VerifyOwnership 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return r.KeyStore.VerifyOwnership(ctx, userID, apiKeyIDs)
}

// CountByUserID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	return r.KeyStore.CountByUserID(ctx, userID)
}

// ExistsByKey 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return r.KeyStore.ExistsByKey(ctx, key)
}

// ListByGroupID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	v, page, e := r.KeyStore.ListByGroupID(ctx, groupID, params)
	return repositoryKeysFromView(v), page, e
}

// SearchAPIKeys 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]service.APIKey, error) {
	v, e := r.KeyStore.SearchAPIKeys(ctx, userID, keyword, limit)
	return repositoryKeysFromView(v), e
}

// ClearGroupIDByGroupID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return r.KeyStore.ClearGroupIDByGroupID(ctx, groupID)
}

// UpdateGroupIDByUserAndGroup 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	return r.KeyStore.UpdateGroupIDByUserAndGroup(ctx, userID, oldGroupID, newGroupID)
}

// CountByGroupID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return r.KeyStore.CountByGroupID(ctx, groupID)
}

// ListKeysByUserID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return r.KeyStore.ListKeysByUserID(ctx, userID)
}

// ListKeysByGroupID 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return r.KeyStore.ListKeysByGroupID(ctx, groupID)
}

// IncrementQuotaUsed 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return r.KeyStore.IncrementQuotaUsed(ctx, id, amount)
}

// IncrementQuotaUsedAndGetState 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*service.APIKeyQuotaUsageState, error) {
	return r.KeyStore.IncrementQuotaUsedAndGetState(ctx, id, amount)
}

// UpdateLastUsed 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	return r.KeyStore.UpdateLastUsed(ctx, id, usedAt)
}

// IncrementRateLimitUsage 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return r.KeyStore.IncrementRateLimitUsage(ctx, id, cost)
}

// ResetRateLimitWindows 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return r.KeyStore.ResetRateLimitWindows(ctx, id)
}

// GetRateLimitData 转接 Key 存储，不自行提交事务。
func (r *apiKeyRepository) GetRateLimitData(ctx context.Context, id int64) (result *service.APIKeyRateLimitData, err error) {
	return r.KeyStore.GetRateLimitData(ctx, id)
}

// apiKeyEntityToService 转接 Key 存储，不自行提交事务。
func apiKeyEntityToService(m *dbent.APIKey) *service.APIKey {
	v := keypostgres.KeyApiKeyEntityToService(m)
	return service.APIKeyFromView(v)
}

func userEntityToService(u *dbent.User) *service.User {
	return service.UserFromIdentity(identitypostgres.UserFromEntity(u))
}

func groupEntityToService(g *dbent.Group) *service.Group {
	return service.GroupFromRouting(routingpostgres.GroupFromEnt(g))
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *apiKeyRepository) APIKeyRepository() keycore.APIKeyRepository { return r.KeyStore }

func repositoryKeysFromView(v []keycore.APIKey) []service.APIKey {
	if v == nil {
		return nil
	}
	out := make([]service.APIKey, len(v))
	for i := range v {
		out[i] = *service.APIKeyFromView(&v[i])
	}
	return out
}

// WrapKeyStore 只兼容旧返回形状；数据写入和事务由传入的唯一存储拥有。
func WrapKeyStore(store *keypostgres.KeyStore, client *dbent.Client, db *sql.DB, settings *service.PreAggregationSettingsService) service.APIKeyRepository {
	return &apiKeyRepository{KeyStore: store, client: client, sql: db, preAggregation: settings}
}
