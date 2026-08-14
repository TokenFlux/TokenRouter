package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/apikey"
	"github.com/TokenFlux/TokenRouter/ent/apikeycompositegroup"
	"github.com/TokenFlux/TokenRouter/ent/group"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/lib/pq"
)

type apiKeyRepository struct {
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
	return &apiKeyRepository{client: client, sql: sqlq}
}

func (r *apiKeyRepository) activeQuery() *dbent.APIKeyQuery {
	// 默认过滤已软删除记录，避免删除后仍被查询到。
	return r.client.APIKey.Query().Where(apikey.DeletedAtIsNil())
}

// apiKeyFastModePolicyForPersistence 兼容绕过服务层的旧夹具和内部调用。
func apiKeyFastModePolicyForPersistence(value string) string {
	policy, ok := service.NormalizeAPIKeyFastModePolicy(value)
	if ok {
		return policy
	}
	return value
}

// apiKeyBillingModeForPersistence 兼容绕过服务层的旧夹具和内部调用。
func apiKeyBillingModeForPersistence(value string) string {
	mode, ok := service.NormalizeAPIKeyBillingMode(value)
	if ok {
		return mode
	}
	return value
}

func (r *apiKeyRepository) Create(ctx context.Context, key *service.APIKey) error {
	if key == nil {
		return fmt.Errorf("api key is required")
	}

	// API Key 数量判断和插入必须共用事务；PostgreSQL 上的用户行锁会串行化同一用户的并发创建。
	txClient, sqlq, commit, rollback, err := r.beginAPIKeyCreateTransaction(ctx)
	if err != nil {
		return err
	}
	defer rollback()

	current, limit, err := r.lockAPIKeyOwnerAndCount(ctx, sqlq, key.UserID)
	if err != nil {
		return err
	}
	if limit > 0 && current >= int64(limit) {
		return service.NewAPIKeyLimitReachedError(current, limit)
	}

	created, err := createAPIKeyRecord(ctx, txClient, key)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrAPIKeyExists)
	}
	if err := commit(); err != nil {
		return err
	}

	key.ID = created.ID
	key.LastUsedAt = created.LastUsedAt
	key.CreatedAt = created.CreatedAt
	key.UpdatedAt = created.UpdatedAt
	return nil
}

// beginAPIKeyCreateTransaction 返回创建流程使用的 Ent client、SQL 执行器和事务收尾函数。
func (r *apiKeyRepository) beginAPIKeyCreateTransaction(ctx context.Context) (*dbent.Client, sqlExecutor, func() error, func(), error) {
	if existing := dbent.TxFromContext(ctx); existing != nil {
		client := existing.Client()
		return client, client, func() error { return nil }, func() {}, nil
	}

	tx, err := r.client.Tx(ctx)
	if err == nil {
		client := tx.Client()
		return client, client, tx.Commit, func() { _ = tx.Rollback() }, nil
	}
	if !errors.Is(err, dbent.ErrTxStarted) {
		return nil, nil, nil, nil, err
	}

	// 仓储可能已经绑定到调用方创建的事务，此时由调用方负责提交或回滚。
	if r.sql == nil {
		return nil, nil, nil, nil, fmt.Errorf("sql executor is not configured")
	}
	return r.client, r.sql, func() error { return nil }, func() {}, nil
}

// lockAPIKeyOwnerAndCount 锁定用户并统计所有未软删除的 API Key。
func (r *apiKeyRepository) lockAPIKeyOwnerAndCount(ctx context.Context, sqlq sqlExecutor, userID int64) (int64, int, error) {
	lockQuery := `SELECT api_key_limit FROM users WHERE id = $1 AND deleted_at IS NULL`
	if r.client.Driver().Dialect() == dialect.Postgres {
		lockQuery += ` FOR UPDATE`
	}

	var limit int
	if err := scanSingleRow(ctx, sqlq, lockQuery, []any{userID}, &limit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, service.ErrUserNotFound
		}
		return 0, 0, err
	}

	var current int64
	const countQuery = `SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND deleted_at IS NULL`
	if err := scanSingleRow(ctx, sqlq, countQuery, []any{userID}, &current); err != nil {
		return 0, 0, err
	}
	return current, limit, nil
}

// createAPIKeyRecord 使用指定事务 client 插入 API Key 记录。
func createAPIKeyRecord(ctx context.Context, client *dbent.Client, key *service.APIKey) (*dbent.APIKey, error) {
	builder := client.APIKey.Create().
		SetUserID(key.UserID).
		SetNillableTeamID(key.TeamID).
		SetKey(key.Key).
		SetName(key.Name).
		SetStatus(key.Status).
		SetIsComposite(key.IsComposite).
		SetFastModePolicy(apiKeyFastModePolicyForPersistence(key.FastModePolicy)).
		SetBillingMode(apiKeyBillingModeForPersistence(key.BillingMode)).
		SetNillablePreferredSubscriptionID(key.PreferredSubscriptionID).
		SetModelMapping(service.CloneModelMapping(key.ModelMapping)).
		SetNillableGroupID(key.GroupID).
		SetNillableLastUsedAt(key.LastUsedAt).
		SetQuota(key.Quota).
		SetQuotaUsed(key.QuotaUsed).
		SetNillableExpiresAt(key.ExpiresAt).
		SetRateLimit5h(key.RateLimit5h).
		SetRateLimit1d(key.RateLimit1d).
		SetRateLimit7d(key.RateLimit7d).
		SetFallbackToDefaultGroupWhenUnavailable(key.FallbackToDefaultGroupWhenUnavailable).
		SetDataSharingNoticeVersion(key.DataSharingNoticeVersion).
		SetNillableDataSharingConfirmedGroupID(key.DataSharingConfirmedGroupID).
		SetNillableDataSharingConfirmedAt(key.DataSharingConfirmedAt)

	if len(key.IPWhitelist) > 0 {
		builder.SetIPWhitelist(key.IPWhitelist)
	}
	if len(key.IPBlacklist) > 0 {
		builder.SetIPBlacklist(key.IPBlacklist)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := replaceCompositeGroupRecords(ctx, client, created.ID, key.CompositeGroups); err != nil {
		return nil, err
	}
	return created, nil
}

// replaceCompositeGroupRecords 在调用方事务内完整替换复合 Key 的分组映射。
func replaceCompositeGroupRecords(ctx context.Context, client *dbent.Client, apiKeyID int64, bindings []service.APIKeyCompositeGroup) error {
	if _, err := client.APIKeyCompositeGroup.Delete().
		Where(apikeycompositegroup.APIKeyIDEQ(apiKeyID)).
		Exec(ctx); err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	builders := make([]*dbent.APIKeyCompositeGroupCreate, 0, len(bindings))
	for _, binding := range bindings {
		builders = append(builders, client.APIKeyCompositeGroup.Create().
			SetAPIKeyID(apiKeyID).
			SetGroupID(binding.GroupID).
			SetPrefix(binding.Prefix).
			SetNormalizedPrefix(binding.NormalizedPrefix).
			SetSortOrder(binding.SortOrder).
			SetDataSharingNoticeVersion(binding.DataSharingNoticeVersion).
			SetNillableDataSharingConfirmedAt(binding.DataSharingConfirmedAt))
	}
	return client.APIKeyCompositeGroup.CreateBulk(builders...).Exec(ctx)
}

// withAPIKeyCompositeGroups 统一按用户配置顺序加载复合映射及分组。
func withAPIKeyCompositeGroups(query *dbent.APIKeyQuery) *dbent.APIKeyQuery {
	return query.WithCompositeGroups(func(q *dbent.APIKeyCompositeGroupQuery) {
		q.Order(dbent.Asc(apikeycompositegroup.FieldSortOrder), dbent.Asc(apikeycompositegroup.FieldID)).
			WithGroup()
	})
}

func (r *apiKeyRepository) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	m, err := withAPIKeyCompositeGroups(r.activeQuery()).
		Where(apikey.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return apiKeyEntityToService(m), nil
}

// GetKeyAndOwnerID 根据 API Key ID 获取其 key 与所有者（用户）ID。
// 相比 GetByID，此方法性能更优，因为：
//   - 使用 Select() 只查询必要字段，减少数据传输量
//   - 不加载完整的 API Key 实体及其关联数据（User、Group 等）
//   - 适用于删除等只需 key 与用户 ID 的场景
func (r *apiKeyRepository) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	m, err := r.activeQuery().
		Where(apikey.IDEQ(id)).
		Select(apikey.FieldKey, apikey.FieldUserID).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", 0, service.ErrAPIKeyNotFound
		}
		return "", 0, err
	}
	return m.Key, m.UserID, nil
}

func (r *apiKeyRepository) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	m, err := withAPIKeyCompositeGroups(r.activeQuery()).
		Where(apikey.KeyEQ(key)).
		WithUser(func(q *dbent.UserQuery) {
			q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
				gq.Select(group.FieldID)
			})
			q.WithUserDisabledPublicGroups()
		}).
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return apiKeyEntityToService(m), nil
}

func (r *apiKeyRepository) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	m, err := r.activeQuery().
		Where(apikey.KeyEQ(key)).
		Select(
			apikey.FieldID,
			apikey.FieldUserID,
			apikey.FieldTeamID,
			apikey.FieldTeamOwnerDisabled,
			apikey.FieldCreatedAt,
			apikey.FieldGroupID,
			apikey.FieldIsComposite,
			apikey.FieldName,
			apikey.FieldStatus,
			apikey.FieldFastModePolicy,
			apikey.FieldBillingMode,
			apikey.FieldPreferredSubscriptionID,
			apikey.FieldModelMapping,
			apikey.FieldIPWhitelist,
			apikey.FieldIPBlacklist,
			apikey.FieldQuota,
			apikey.FieldQuotaUsed,
			apikey.FieldExpiresAt,
			apikey.FieldRateLimit5h,
			apikey.FieldRateLimit1d,
			apikey.FieldRateLimit7d,
			apikey.FieldFallbackToDefaultGroupWhenUnavailable,
		).
		WithUser(func(q *dbent.UserQuery) {
			q.Select(
				user.FieldID,
				user.FieldEmail,
				user.FieldUsername,
				user.FieldStatus,
				user.FieldRole,
				user.FieldBalance,
				user.FieldConcurrency,
				user.FieldBalanceNotifyEnabled,
				user.FieldBalanceNotifyThresholdType,
				user.FieldBalanceNotifyThreshold,
				user.FieldBalanceNotifyExtraEmails,
				user.FieldTotalRecharged,
				user.FieldSignupSource,
				user.FieldLastLoginAt,
				user.FieldLastActiveAt,
				user.FieldRpmLimit,
			)
			q.WithUserDisabledPublicGroups()
			q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
				gq.Select(group.FieldID)
			})
		}).
		WithGroup(func(q *dbent.GroupQuery) {
			q.Select(
				group.FieldID,
				group.FieldName,
				group.FieldPlatform,
				group.FieldSchedulerType,
				group.FieldAdvancedSchedulerOverrides,
				group.FieldIsExclusive,
				group.FieldStatus,
				group.FieldRateMultiplier,
				group.FieldAllowImageGeneration,
				group.FieldAllowBatchImageGeneration,
				group.FieldImageRateIndependent,
				group.FieldImageRateMultiplier,
				group.FieldImagePrice1k,
				group.FieldImagePrice2k,
				group.FieldImagePrice4k,
				group.FieldVideoRateIndependent,
				group.FieldVideoRateMultiplier,
				group.FieldVideoPrice480p,
				group.FieldVideoPrice720p,
				group.FieldVideoPrice1080p,
				group.FieldVideoModelPrices,
				group.FieldWebSearchPricePerCall,
				group.FieldSearchPricePer1k,
				group.FieldAudioRealtimePricePerMin,
				group.FieldAudioTtsPricePerMillionChars,
				group.FieldAudioSttPricePerHour,
				group.FieldLongContextPricingEnabled,
				group.FieldModelPricing,
				group.FieldClaudeCodeOnly,
				group.FieldFallbackGroupID,
				group.FieldFallbackGroupIDOnInvalidRequest,
				group.FieldModelRoutingEnabled,
				group.FieldModelRouting,
				group.FieldMcpXMLInject,
				group.FieldSupportedModelScopes,
				group.FieldAllowedClientProtocols,
				group.FieldAllowLive,
				group.FieldDefaultMappedModel,
				group.FieldMessagesDispatchModelConfig,
				group.FieldModelsListConfig,
				group.FieldRpmLimit,
				group.FieldMaxReasoningEffort,
				group.FieldReasoningEffortMappings,
				group.FieldDataSharingEnabled,
				group.FieldSessionIsolationEnabled,
				group.FieldPeakRateEnabled,
				group.FieldPeakStart,
				group.FieldPeakEnd,
				group.FieldPeakRateMultiplier,
			)
		}).
		WithCompositeGroups(func(q *dbent.APIKeyCompositeGroupQuery) {
			q.Order(dbent.Asc(apikeycompositegroup.FieldSortOrder), dbent.Asc(apikeycompositegroup.FieldID)).
				WithGroup()
		}).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return apiKeyEntityToService(m), nil
}

func (r *apiKeyRepository) Update(ctx context.Context, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	// 空掩码代表调用方不改任何列，直接返回，避免产生一次无意义的整行写。
	if fields.IsEmpty() {
		return nil
	}

	// 只有复合配置会改关联表，需要与 API Key 主记录放在同一事务中。
	if fields.CompositeConfiguration && dbent.TxFromContext(ctx) == nil {
		tx, err := r.client.Tx(ctx)
		if err == nil {
			defer func() { _ = tx.Rollback() }()
			opCtx := dbent.NewTxContext(ctx, tx)
			if err := r.Update(opCtx, key, fields); err != nil {
				return err
			}
			return tx.Commit()
		}
		if !errors.Is(err, dbent.ErrTxStarted) {
			return err
		}
	}
	// 使用原子操作：将软删除检查与更新合并到同一语句，避免竞态条件。
	// 之前的实现先检查 Exist 再 UpdateOneID，若在两步之间发生软删除，
	// 则会更新已删除的记录。
	// 这里选择 Update().Where()，确保只有未软删除记录能被更新。
	// 同时显式设置 updated_at，避免二次查询带来的并发可见性问题。
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	builder := client.APIKey.Update().
		Where(apikey.IDEQ(key.ID), apikey.DeletedAtIsNil()).
		SetUpdatedAt(now)
	if fields.Name {
		builder.SetName(key.Name)
	}
	if fields.Status {
		builder.SetStatus(key.Status)
	}
	if fields.CompositeConfiguration {
		builder.SetIsComposite(key.IsComposite)
	}
	if fields.FastModePolicy {
		builder.SetFastModePolicy(apiKeyFastModePolicyForPersistence(key.FastModePolicy))
	}
	if fields.BillingConfiguration {
		builder.SetBillingMode(apiKeyBillingModeForPersistence(key.BillingMode))
		if key.PreferredSubscriptionID == nil {
			builder.ClearPreferredSubscriptionID()
		} else {
			builder.SetPreferredSubscriptionID(*key.PreferredSubscriptionID)
		}
	}
	if fields.ModelMapping {
		builder.SetModelMapping(service.CloneModelMapping(key.ModelMapping))
	}
	if fields.FallbackToDefaultGroupWhenUnavailable {
		builder.SetFallbackToDefaultGroupWhenUnavailable(key.FallbackToDefaultGroupWhenUnavailable)
	}
	if fields.Quota {
		builder.SetQuota(key.Quota)
	}
	if fields.QuotaUsed {
		builder.SetQuotaUsed(key.QuotaUsed)
	}
	if fields.RateLimits {
		builder.
			SetRateLimit5h(key.RateLimit5h).
			SetRateLimit1d(key.RateLimit1d).
			SetRateLimit7d(key.RateLimit7d)
	}
	if fields.RateLimitUsage {
		builder.
			SetUsage5h(key.Usage5h).
			SetUsage1d(key.Usage1d).
			SetUsage7d(key.Usage7d)

		// Rate limit window start times
		if key.Window5hStart != nil {
			builder.SetWindow5hStart(*key.Window5hStart)
		} else {
			builder.ClearWindow5hStart()
		}
		if key.Window1dStart != nil {
			builder.SetWindow1dStart(*key.Window1dStart)
		} else {
			builder.ClearWindow1dStart()
		}
		if key.Window7dStart != nil {
			builder.SetWindow7dStart(*key.Window7dStart)
		} else {
			builder.ClearWindow7dStart()
		}
	}
	if fields.GroupID {
		if key.GroupID != nil {
			builder.SetGroupID(*key.GroupID)
		} else {
			builder.ClearGroupID()
		}
	}

	if fields.DataSharingConfirmation {
		// 数据共享确认信息与分组切换同一次主记录更新保存。
		builder.SetDataSharingNoticeVersion(key.DataSharingNoticeVersion)
		if key.DataSharingConfirmedGroupID != nil {
			builder.SetDataSharingConfirmedGroupID(*key.DataSharingConfirmedGroupID)
		} else {
			builder.ClearDataSharingConfirmedGroupID()
		}
		if key.DataSharingConfirmedAt != nil {
			builder.SetDataSharingConfirmedAt(*key.DataSharingConfirmedAt)
		} else {
			builder.ClearDataSharingConfirmedAt()
		}
	}

	// Expiration time
	if fields.ExpiresAt {
		if key.ExpiresAt != nil {
			builder.SetExpiresAt(*key.ExpiresAt)
		} else {
			builder.ClearExpiresAt()
		}
	}

	// IP 限制字段
	if fields.IPRules {
		if len(key.IPWhitelist) > 0 {
			builder.SetIPWhitelist(key.IPWhitelist)
		} else {
			builder.ClearIPWhitelist()
		}
		if len(key.IPBlacklist) > 0 {
			builder.SetIPBlacklist(key.IPBlacklist)
		} else {
			builder.ClearIPBlacklist()
		}
	}

	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// 更新影响行数为 0，说明记录不存在或已被软删除。
		return service.ErrAPIKeyNotFound
	}
	if fields.CompositeConfiguration {
		if err := replaceCompositeGroupRecords(ctx, client, key.ID, key.CompositeGroups); err != nil {
			return err
		}
	}

	// 使用同一时间戳回填，避免并发删除导致二次查询失败。
	key.UpdatedAt = now
	return nil
}

func (r *apiKeyRepository) Delete(ctx context.Context, id int64) error {
	// 存在唯一键约束 生成tombstone key 用来释放原key，长度远小于 128，满足 schema 限制
	tombstoneKey := fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano())
	// 显式软删除：避免依赖 Hook 行为，确保 deleted_at 一定被设置。
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetKey(tombstoneKey).
		SetDeletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrAPIKeyNotFound
		}
		return err
	}
	if affected == 0 {
		exists, err := r.client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		return service.ErrAPIKeyNotFound
	}
	return nil
}

// DeleteWithAudit 为兼容滚动升级保留历史方法名。
// 该方法以原子方式写入墓碑并软删除 Key，不保留凭据材料；墓碑会释放唯一键值以便安全复用。
func (r *apiKeyRepository) DeleteWithAudit(ctx context.Context, id int64) error {
	tombstoneKey := fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano())

	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return r.deleteWithTombstone(ctx, existingTx.Client(), id, tombstoneKey)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}
	exec := r.client
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		exec = tx.Client()
	}

	if err := r.deleteWithTombstone(ctx, exec, id, tombstoneKey); err != nil {
		return err
	}

	if tx != nil {
		return tx.Commit()
	}
	return nil
}

func (r *apiKeyRepository) deleteWithTombstone(ctx context.Context, exec *dbent.Client, id int64, tombstoneKey string) error {
	res, err := exec.ExecContext(ctx, `
		UPDATE api_keys
		SET key = $1, deleted_at = NOW(), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`, tombstoneKey, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// 并发/重复删除:记录已存在(已软删)则幂等返回 nil(defer 回滚空事务),否则 NotFound。
		exists, existErr := r.client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if existErr != nil {
			return existErr
		}
		if exists {
			return nil
		}
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func (r *apiKeyRepository) apiKeyListByUserIDQuery(userID int64, filters service.APIKeyListFilters) *dbent.APIKeyQuery {
	q := r.activeQuery().Where(apikey.UserIDEQ(userID))

	if filters.Search != "" {
		q = q.Where(apikey.Or(
			apikey.NameContainsFold(filters.Search),
			apikey.KeyContainsFold(filters.Search),
		))
	}
	if filters.Status != "" {
		q = q.Where(apikey.StatusEQ(filters.Status))
	}
	if filters.GroupID != nil {
		if *filters.GroupID == 0 {
			q = q.Where(apikey.GroupIDIsNil(), apikey.IsCompositeEQ(false))
		} else {
			q = q.Where(apikey.Or(
				apikey.GroupIDEQ(*filters.GroupID),
				apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(*filters.GroupID)),
			))
		}
	}
	// scope 只接受已定义的个人和团队范围，空值表示不限制。
	switch filters.Scope {
	case "personal":
		q = q.Where(apikey.TeamIDIsNil())
	case "team":
		q = q.Where(apikey.TeamIDNotNil())
	}

	return q
}

func (r *apiKeyRepository) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.apiKeyListByUserIDQuery(userID, filters)

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	if strings.EqualFold(strings.TrimSpace(params.SortBy), "usage") {
		return r.listByUserIDWithUsageSort(ctx, q, params, total)
	}

	keysQuery := withAPIKeyCompositeGroups(q.WithGroup())
	keysQuery = keysQuery.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	if err := r.attachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, nil, err
	}

	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

// ListAllByUserID 返回经过筛选的全部 API Key，供依赖运行时数据的排序逻辑使用。
func (r *apiKeyRepository) ListAllByUserID(ctx context.Context, userID int64, filters service.APIKeyListFilters) ([]service.APIKey, error) {
	keys, err := withAPIKeyCompositeGroups(r.apiKeyListByUserIDQuery(userID, filters).WithGroup()).
		Order(dbent.Asc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	if err := r.attachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, err
	}
	return outKeys, nil
}

// attachLastUsedIPs 为当前页 API Key 批量附加最近一条非空使用 IP。
func (r *apiKeyRepository) attachLastUsedIPs(ctx context.Context, keys []service.APIKey) error {
	if len(keys) == 0 || r.sql == nil {
		return nil
	}

	apiKeyIDs := make([]int64, 0, len(keys))
	for i := range keys {
		apiKeyIDs = append(apiKeyIDs, keys[i].ID)
	}

	lastUsedIPs, err := r.latestUsageLogIPs(ctx, apiKeyIDs)
	if err != nil {
		return err
	}
	for i := range keys {
		if ipAddress, ok := lastUsedIPs[keys[i].ID]; ok {
			keys[i].LastUsedIP = &ipAddress
		}
	}
	return nil
}

// latestUsageLogIPs 从 usage_logs 查询每个 API Key 最新的非空 IP。
func (r *apiKeyRepository) latestUsageLogIPs(ctx context.Context, apiKeyIDs []int64) (result map[int64]string, err error) {
	if len(apiKeyIDs) == 0 || r.sql == nil {
		return map[int64]string{}, nil
	}

	query, args := latestUsageLogIPsQuery(apiKeyIDs, r.client.Driver().Dialect())
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	out := make(map[int64]string, len(apiKeyIDs))
	for rows.Next() {
		var apiKeyID int64
		var ipAddress string
		if err := rows.Scan(&apiKeyID, &ipAddress); err != nil {
			return nil, err
		}
		out[apiKeyID] = ipAddress
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// latestUsageLogIPsQuery 按数据库方言生成批量查询：PostgreSQL 使用数组，
// 其它方言使用逐项占位符，便于 SQLite 回归测试覆盖真实 SQL。
func latestUsageLogIPsQuery(apiKeyIDs []int64, dialectName string) (string, []any) {
	if dialectName == dialect.Postgres {
		// 每个 Key 只做一次有序索引探测，避免为整段历史记录计算窗口排名。
		return `
		SELECT requested.api_key_id, latest.ip_address
		FROM unnest($1::bigint[]) AS requested(api_key_id)
		CROSS JOIN LATERAL (
			SELECT ul.ip_address
			FROM usage_logs AS ul
			WHERE ul.api_key_id = requested.api_key_id
				AND ul.ip_address IS NOT NULL
				AND ul.ip_address <> ''
			ORDER BY ul.created_at DESC, ul.id DESC
			LIMIT 1
		) AS latest`, []any{pq.Array(apiKeyIDs)}
	}

	placeholders := make([]string, len(apiKeyIDs))
	args := make([]any, len(apiKeyIDs))
	for i, id := range apiKeyIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	return fmt.Sprintf(`
		SELECT api_key_id, ip_address
		FROM (
			SELECT api_key_id, ip_address,
				ROW_NUMBER() OVER (PARTITION BY api_key_id ORDER BY created_at DESC, id DESC) AS rn
			FROM usage_logs
			WHERE api_key_id IN (%s)
				AND ip_address IS NOT NULL
				AND ip_address <> ''
		) ranked
		WHERE rn = 1`, strings.Join(placeholders, ", ")), args
}

func (r *apiKeyRepository) listByUserIDWithUsageSort(ctx context.Context, q *dbent.APIKeyQuery, params pagination.PaginationParams, total int) ([]service.APIKey, *pagination.PaginationResult, error) {
	keys, err := withAPIKeyCompositeGroups(q.WithGroup()).
		Order(dbent.Desc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	keyIDs := make([]int64, 0, len(keys))
	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		key := apiKeyEntityToService(keys[i])
		outKeys = append(outKeys, *key)
		keyIDs = append(keyIDs, key.ID)
	}

	usageTotals, err := r.loadAPIKeyUsageTotals(ctx, keyIDs)
	if err != nil {
		return nil, nil, err
	}

	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)
	sort.SliceStable(outKeys, func(i, j int) bool {
		left := usageTotals[outKeys[i].ID]
		right := usageTotals[outKeys[j].ID]
		if left == right {
			if sortOrder == pagination.SortOrderAsc {
				return outKeys[i].ID < outKeys[j].ID
			}
			return outKeys[i].ID > outKeys[j].ID
		}
		if sortOrder == pagination.SortOrderAsc {
			return left < right
		}
		return left > right
	})

	pageKeys := paginateSlice(outKeys, params)
	if err := r.attachLastUsedIPs(ctx, pageKeys); err != nil {
		return nil, nil, err
	}
	return pageKeys, paginationResultFromTotal(int64(total), params), nil
}

func (r *apiKeyRepository) loadAPIKeyUsageTotals(ctx context.Context, keyIDs []int64) (map[int64]float64, error) {
	result := make(map[int64]float64, len(keyIDs))
	if len(keyIDs) == 0 {
		return result, nil
	}
	for _, id := range keyIDs {
		result[id] = 0
	}

	now := time.Now()
	start := now.AddDate(0, 0, -30)
	if r.preAggregation != nil {
		usageRepo := &usageLogRepository{sql: r.sql, preAggregation: r.preAggregation}
		if stats, ok, err := usageRepo.getBatchAPIKeyUsageStatsFromAnalytics(ctx, keyIDs, start, now); err == nil && ok {
			for keyID, stat := range stats {
				if stat != nil {
					result[keyID] = stat.TotalActualCost
				}
			}
			return result, nil
		} else if err != nil {
			usageRepo.logUsageAnalyticsFallback("api_key_list_usage", err)
		}
	}

	query := `
		SELECT api_key_id, COALESCE(SUM(actual_cost), 0)
		FROM usage_logs
		WHERE api_key_id = ANY($1)
		  AND created_at >= $2
		  AND created_at < $3
		GROUP BY api_key_id
	`
	rows, err := r.sql.QueryContext(ctx, query, pq.Array(keyIDs), now.AddDate(0, 0, -30), now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var keyID int64
		var total float64
		if err := rows.Scan(&keyID, &total); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result[keyID] = total
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (r *apiKeyRepository) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	ids, err := r.client.APIKey.Query().
		Where(apikey.UserIDEQ(userID), apikey.IDIn(apiKeyIDs...), apikey.DeletedAtIsNil()).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *apiKeyRepository) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	count, err := r.activeQuery().Where(apikey.UserIDEQ(userID)).Count(ctx)
	return int64(count), err
}

func (r *apiKeyRepository) ExistsByKey(ctx context.Context, key string) (bool, error) {
	count, err := r.activeQuery().Where(apikey.KeyEQ(key)).Count(ctx)
	return count > 0, err
}

func (r *apiKeyRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.activeQuery().Where(apikey.Or(
		apikey.GroupIDEQ(groupID),
		apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
	))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := withAPIKeyCompositeGroups(q.WithUser().WithGroup())
	keysQuery = keysQuery.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}

	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

func apiKeyListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	var field string
	switch sortBy {
	case "name":
		field = apikey.FieldName
	case "status":
		field = apikey.FieldStatus
	case "expires_at":
		field = apikey.FieldExpiresAt
	case "last_used_at":
		field = apikey.FieldLastUsedAt
	case "created_at":
		field = apikey.FieldCreatedAt
	case "id":
		field = apikey.FieldID
	default:
		field = apikey.FieldID
	}

	if sortOrder == pagination.SortOrderAsc {
		orders := []func(*entsql.Selector){dbent.Asc(field)}
		if field != apikey.FieldID {
			orders = append(orders, dbent.Asc(apikey.FieldID))
		}
		return orders
	}
	orders := []func(*entsql.Selector){dbent.Desc(field)}
	if field != apikey.FieldID {
		orders = append(orders, dbent.Desc(apikey.FieldID))
	}
	return orders
}

// SearchAPIKeys searches API keys by user ID and/or keyword (name)
func (r *apiKeyRepository) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]service.APIKey, error) {
	q := r.activeQuery()
	if userID > 0 {
		q = q.Where(apikey.UserIDEQ(userID))
	}

	if keyword != "" {
		q = q.Where(apikey.NameContainsFold(keyword))
	}

	keys, err := withAPIKeyCompositeGroups(q.WithGroup()).
		Limit(limit).
		Order(dbent.Desc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	return outKeys, nil
}

// ClearGroupIDByGroupID 将指定分组的所有 API Key 的 group_id 设为 nil
func (r *apiKeyRepository) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.APIKey.Update().
		Where(apikey.GroupIDEQ(groupID), apikey.DeletedAtIsNil()).
		ClearGroupID().
		Save(ctx)
	if err != nil {
		return 0, err
	}
	deleted, err := client.APIKeyCompositeGroup.Delete().
		Where(apikeycompositegroup.GroupIDEQ(groupID)).
		Exec(ctx)
	return int64(n + deleted), err
}

// UpdateGroupIDByUserAndGroup 将用户下绑定 oldGroupID 的所有 Key 迁移到 newGroupID
func (r *apiKeyRepository) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.APIKey.Update().
		Where(apikey.UserIDEQ(userID), apikey.GroupIDEQ(oldGroupID), apikey.DeletedAtIsNil()).
		SetGroupID(newGroupID).
		Save(ctx)
	if err != nil {
		return 0, err
	}
	// 目标分组已存在时保留其原前缀，并删除旧分组映射避免唯一约束冲突。
	bindings, err := client.APIKeyCompositeGroup.Query().
		Where(apikeycompositegroup.GroupIDEQ(oldGroupID), apikeycompositegroup.HasAPIKeyWith(apikey.UserIDEQ(userID), apikey.DeletedAtIsNil())).
		All(ctx)
	if err != nil {
		return 0, err
	}
	for _, binding := range bindings {
		exists, err := client.APIKeyCompositeGroup.Query().
			Where(apikeycompositegroup.APIKeyIDEQ(binding.APIKeyID), apikeycompositegroup.GroupIDEQ(newGroupID)).
			Exist(ctx)
		if err != nil {
			return 0, err
		}
		if exists {
			if err := client.APIKeyCompositeGroup.DeleteOneID(binding.ID).Exec(ctx); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := client.APIKeyCompositeGroup.UpdateOneID(binding.ID).SetGroupID(newGroupID).Save(ctx); err != nil {
			return 0, err
		}
	}
	return int64(n + len(bindings)), nil
}

// CountByGroupID 获取分组的 API Key 数量
func (r *apiKeyRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	count, err := r.activeQuery().Where(apikey.Or(
		apikey.GroupIDEQ(groupID),
		apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
	)).Count(ctx)
	return int64(count), err
}

func (r *apiKeyRepository) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	keys, err := r.activeQuery().
		Where(apikey.UserIDEQ(userID)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (r *apiKeyRepository) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	keys, err := r.activeQuery().
		Where(apikey.Or(
			apikey.GroupIDEQ(groupID),
			apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
		)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// IncrementQuotaUsed 使用 Ent 原子递增 quota_used 字段并返回新值
func (r *apiKeyRepository) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	updated, err := r.client.APIKey.UpdateOneID(id).
		Where(apikey.DeletedAtIsNil()).
		AddQuotaUsed(amount).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return 0, service.ErrAPIKeyNotFound
		}
		return 0, err
	}
	return updated.QuotaUsed, nil
}

// IncrementQuotaUsedAndGetState atomically increments quota_used, conditionally marks the key
// as quota_exhausted, and returns the latest quota state in one round trip.
func (r *apiKeyRepository) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*service.APIKeyQuotaUsageState, error) {
	query := `
		UPDATE api_keys
		SET
			quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0 AND quota_used + $1 >= quota THEN $2
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL
		RETURNING quota_used, quota, key, status
	`

	state := &service.APIKeyQuotaUsageState{}
	if err := scanSingleRow(ctx, r.sql, query, []any{amount, service.StatusAPIKeyQuotaExhausted, id}, &state.QuotaUsed, &state.Quota, &state.Key, &state.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return state, nil
}

func (r *apiKeyRepository) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetLastUsedAt(usedAt).
		SetUpdatedAt(usedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

// IncrementRateLimitUsage atomically increments all rate limit usage counters and initializes
// window start times via COALESCE if not already set.
func (r *apiKeyRepository) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`,
		cost, id)
	return err
}

// ResetRateLimitWindows resets expired rate limit windows atomically.
func (r *apiKeyRepository) ResetRateLimitWindows(ctx context.Context, id int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN 0 ELSE usage_5h END,
			window_5h_start = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN 0 ELSE usage_1d END,
			window_1d_start = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN 0 ELSE usage_7d END,
			window_7d_start = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`,
		id)
	return err
}

// GetRateLimitData returns the current rate limit usage and window start times for an API key.
func (r *apiKeyRepository) GetRateLimitData(ctx context.Context, id int64) (result *service.APIKeyRateLimitData, err error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start
		FROM api_keys
		WHERE id = $1 AND deleted_at IS NULL`,
		id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		return nil, service.ErrAPIKeyNotFound
	}
	data := &service.APIKeyRateLimitData{}
	if err := rows.Scan(&data.Usage5h, &data.Usage1d, &data.Usage7d, &data.Window5hStart, &data.Window1dStart, &data.Window7dStart); err != nil {
		return nil, err
	}
	return data, rows.Err()
}

func apiKeyEntityToService(m *dbent.APIKey) *service.APIKey {
	if m == nil {
		return nil
	}
	out := &service.APIKey{
		ID:                                    m.ID,
		UserID:                                m.UserID,
		TeamID:                                m.TeamID,
		TeamOwnerDisabled:                     m.TeamOwnerDisabled,
		Key:                                   m.Key,
		Name:                                  m.Name,
		Status:                                m.Status,
		FastModePolicy:                        m.FastModePolicy,
		BillingMode:                           m.BillingMode,
		PreferredSubscriptionID:               m.PreferredSubscriptionID,
		ModelMapping:                          service.CloneModelMapping(m.ModelMapping),
		IPWhitelist:                           m.IPWhitelist,
		IPBlacklist:                           m.IPBlacklist,
		LastUsedAt:                            m.LastUsedAt,
		CreatedAt:                             m.CreatedAt,
		UpdatedAt:                             m.UpdatedAt,
		GroupID:                               m.GroupID,
		IsComposite:                           m.IsComposite,
		Quota:                                 m.Quota,
		QuotaUsed:                             m.QuotaUsed,
		ExpiresAt:                             m.ExpiresAt,
		RateLimit5h:                           m.RateLimit5h,
		RateLimit1d:                           m.RateLimit1d,
		RateLimit7d:                           m.RateLimit7d,
		Usage5h:                               m.Usage5h,
		Usage1d:                               m.Usage1d,
		Usage7d:                               m.Usage7d,
		Window5hStart:                         m.Window5hStart,
		Window1dStart:                         m.Window1dStart,
		Window7dStart:                         m.Window7dStart,
		FallbackToDefaultGroupWhenUnavailable: m.FallbackToDefaultGroupWhenUnavailable,
		// 数据共享确认信息随 API Key 返回，用户端可判断是否需要重新确认须知。
		DataSharingNoticeVersion:    m.DataSharingNoticeVersion,
		DataSharingConfirmedGroupID: m.DataSharingConfirmedGroupID,
		DataSharingConfirmedAt:      m.DataSharingConfirmedAt,
	}
	if m.Edges.User != nil {
		out.User = userEntityToService(m.Edges.User)
		out.ActorUser = out.User
		if allowed := m.Edges.User.Edges.AllowedGroups; len(allowed) > 0 {
			out.User.AllowedGroups = make([]int64, 0, len(allowed))
			for _, g := range allowed {
				if g != nil {
					out.User.AllowedGroups = append(out.User.AllowedGroups, g.ID)
				}
			}
			sort.Slice(out.User.AllowedGroups, func(i, j int) bool {
				return out.User.AllowedGroups[i] < out.User.AllowedGroups[j]
			})
		}
		if disabledPublicRows, err := m.Edges.User.Edges.UserDisabledPublicGroupsOrErr(); err == nil {
			out.User.DisabledPublicGroups = make([]int64, 0, len(disabledPublicRows))
			for _, row := range disabledPublicRows {
				out.User.DisabledPublicGroups = append(out.User.DisabledPublicGroups, row.GroupID)
			}
			sort.Slice(out.User.DisabledPublicGroups, func(i, j int) bool {
				return out.User.DisabledPublicGroups[i] < out.User.DisabledPublicGroups[j]
			})
			out.User.GroupRestrictionsLoaded = true
		}
	}
	if m.Edges.Group != nil {
		out.Group = groupEntityToService(m.Edges.Group)
	}
	if rows, err := m.Edges.CompositeGroupsOrErr(); err == nil {
		out.CompositeGroups = make([]service.APIKeyCompositeGroup, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			binding := service.APIKeyCompositeGroup{
				ID:                       row.ID,
				APIKeyID:                 row.APIKeyID,
				GroupID:                  row.GroupID,
				Prefix:                   row.Prefix,
				NormalizedPrefix:         row.NormalizedPrefix,
				SortOrder:                row.SortOrder,
				DataSharingNoticeVersion: row.DataSharingNoticeVersion,
				DataSharingConfirmedAt:   row.DataSharingConfirmedAt,
			}
			if row.Edges.Group != nil {
				binding.Group = groupEntityToService(row.Edges.Group)
			}
			out.CompositeGroups = append(out.CompositeGroups, binding)
		}
	}
	return out
}

func userEntityToService(u *dbent.User) *service.User {
	if u == nil {
		return nil
	}
	out := &service.User{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Notes:                      u.Notes,
		PasswordHash:               u.PasswordHash,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		SignupSource:               u.SignupSource,
		LastLoginAt:                u.LastLoginAt,
		LastActiveAt:               u.LastActiveAt,
		TotpSecretEncrypted:        u.TotpSecretEncrypted,
		TotpEnabled:                u.TotpEnabled,
		TotpEnabledAt:              u.TotpEnabledAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RpmLimit,
		APIKeyLimit:                u.APIKeyLimit,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		DeletedAt:                  u.DeletedAt,
	}
	// Parse extra emails JSON (supports both old []string and new []NotifyEmailEntry format)
	if u.BalanceNotifyExtraEmails != "" && u.BalanceNotifyExtraEmails != "[]" {
		out.BalanceNotifyExtraEmails = service.ParseNotifyEmails(u.BalanceNotifyExtraEmails)
	}
	return out
}

func groupEntityToService(g *dbent.Group) *service.Group {
	if g == nil {
		return nil
	}
	var modelPricing []service.ChannelModelPricing
	if len(g.ModelPricing) > 0 {
		if err := json.Unmarshal(g.ModelPricing, &modelPricing); err != nil {
			slog.Warn("group model_pricing unmarshal failed; falling back to channel/builtin pricing",
				"group_id", g.ID, "error", err)
			modelPricing = nil
		}
	}
	return &service.Group{
		ID:                              g.ID,
		Name:                            g.Name,
		Description:                     derefString(g.Description),
		Platform:                        g.Platform,
		SchedulerType:                   service.GroupSchedulerType(g.SchedulerType),
		AdvancedSchedulerOverrides:      service.CloneGroupAdvancedSchedulerOverrides(g.AdvancedSchedulerOverrides),
		DisplayBrand:                    g.DisplayBrand,
		RateMultiplier:                  g.RateMultiplier,
		IsExclusive:                     g.IsExclusive,
		IsDefault:                       g.IsDefault,
		Status:                          g.Status,
		Hydrated:                        true,
		DuplicateOperationID:            derefString(g.DuplicateOperationID),
		DataSharingEnabled:              g.DataSharingEnabled,
		SessionIsolationEnabled:         g.SessionIsolationEnabled,
		AllowImageGeneration:            g.AllowImageGeneration,
		AllowBatchImageGeneration:       g.AllowBatchImageGeneration,
		ImageRateIndependent:            g.ImageRateIndependent,
		ImageRateMultiplier:             g.ImageRateMultiplier,
		ImagePrice1K:                    g.ImagePrice1k,
		ImagePrice2K:                    g.ImagePrice2k,
		ImagePrice4K:                    g.ImagePrice4k,
		BatchImageDiscountMultiplier:    g.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        g.BatchImageHoldMultiplier,
		VideoRateIndependent:            g.VideoRateIndependent,
		VideoRateMultiplier:             g.VideoRateMultiplier,
		VideoPrice480P:                  g.VideoPrice480p,
		VideoPrice720P:                  g.VideoPrice720p,
		VideoPrice1080P:                 g.VideoPrice1080p,
		VideoModelPrices:                service.NormalizeVideoModelPrices(g.VideoModelPrices),
		WebSearchPricePerCall:           g.WebSearchPricePerCall,
		SearchPricePer1k:                g.SearchPricePer1k,
		AudioRealtimePricePerMin:        g.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    g.AudioTtsPricePerMillionChars,
		AudioSTTPricePerHour:            g.AudioSttPricePerHour,
		LongContextPricingEnabled:       g.LongContextPricingEnabled,
		ModelPricing:                    modelPricing,
		ClaudeCodeOnly:                  g.ClaudeCodeOnly,
		FallbackGroupID:                 g.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest,
		UnavailableFallbackGroupID:      g.UnavailableFallbackGroupID,
		ModelRouting:                    g.ModelRouting,
		ModelRoutingEnabled:             g.ModelRoutingEnabled,
		MCPXMLInject:                    g.McpXMLInject,
		SupportedModelScopes:            g.SupportedModelScopes,
		SortOrder:                       g.SortOrder,
		AllowedClientProtocols:          g.AllowedClientProtocols,
		AllowMessagesDispatch:           g.AllowMessagesDispatch,
		AllowLive:                       g.AllowLive,
		RequireOAuthOnly:                g.RequireOauthOnly,
		RequirePrivacySet:               g.RequirePrivacySet,
		DefaultMappedModel:              g.DefaultMappedModel,
		MessagesDispatchModelConfig:     g.MessagesDispatchModelConfig,
		ModelsListConfig:                g.ModelsListConfig,
		AvailabilityProbeConfig:         g.AvailabilityProbeConfig,
		RPMLimit:                        g.RpmLimit,
		MaxReasoningEffort:              g.MaxReasoningEffort,
		ReasoningEffortMappings:         g.ReasoningEffortMappings,
		PeakRateEnabled:                 g.PeakRateEnabled,
		PeakStart:                       g.PeakStart,
		PeakEnd:                         g.PeakEnd,
		PeakRateMultiplier:              g.PeakRateMultiplier,
		CreatedAt:                       g.CreatedAt,
		UpdatedAt:                       g.UpdatedAt,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
