// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	usagequery "github.com/TokenFlux/TokenRouter/internal/usage/postgres/query"

	context "context"

	sql "database/sql"

	dialect "entgo.io/ent/dialect"

	entsql "entgo.io/ent/dialect/sql"

	errors "errors"

	fmt "fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"

	apikey "github.com/TokenFlux/TokenRouter/ent/apikey"

	apikeycompositegroup "github.com/TokenFlux/TokenRouter/ent/apikeycompositegroup"

	group "github.com/TokenFlux/TokenRouter/ent/group"

	mixins "github.com/TokenFlux/TokenRouter/ent/schema/mixins"

	user "github.com/TokenFlux/TokenRouter/ent/user"

	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	sort "sort"

	strings "strings"

	time "time"
)

type KeyStore struct {
	client      *dbent.Client
	sql         SQLExecutor
	usageTotals UsageTotalsReader
}

func NewKeyStore(client *dbent.Client, sqlDB *sql.DB, usage UsageTotalsReader) *KeyStore {
	return NewKeyStoreWithSQL(client, sqlDB, usage)
}

func NewKeyStoreWithSQL(client *dbent.Client, sqlq SQLExecutor, usage UsageTotalsReader) *KeyStore {
	return &KeyStore{client: client, sql: sqlq, usageTotals: usage}
}

func (r *KeyStore) KeyActiveQuery() *dbent.APIKeyQuery {
	// 默认过滤已软删除记录，避免删除后仍被查询到。
	return r.client.APIKey.Query().Where(apikey.DeletedAtIsNil())
}

// KeyApiKeyFastModePolicyForPersistence 兼容绕过服务层的旧夹具和内部调用。
func KeyApiKeyFastModePolicyForPersistence(value string) string {
	policy, ok := keycore.NormalizeAPIKeyFastModePolicy(value)
	if ok {
		return policy
	}
	return value
}

// KeyApiKeyBillingModeForPersistence 兼容绕过服务层的旧夹具和内部调用。
func KeyApiKeyBillingModeForPersistence(value string) string {
	mode, ok := keycore.NormalizeAPIKeyBillingMode(value)
	if ok {
		return mode
	}
	return value
}

func (r *KeyStore) Create(ctx context.Context, key *keycore.APIKey) error {
	if key == nil {
		return fmt.Errorf("api key is required")
	}

	// API Key 数量判断和插入必须共用事务；PostgreSQL 上的用户行锁会串行化同一用户的并发创建。
	txClient, sqlq, commit, rollback, err := r.KeyBeginAPIKeyCreateTransaction(ctx)
	if err != nil {
		return err
	}
	defer rollback()

	current, limit, err := r.KeyLockAPIKeyOwnerAndCount(ctx, sqlq, key.UserID)
	if err != nil {
		return err
	}
	if limit > 0 && current >= int64(limit) {
		return keycore.NewAPIKeyLimitReachedError(current, limit)
	}

	created, err := KeyCreateAPIKeyRecord(ctx, txClient, key)
	if err != nil {
		return translatePersistenceError(err, nil, keycore.ErrAPIKeyExists)
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

// KeyBeginAPIKeyCreateTransaction 返回创建流程使用的 Ent client、SQL 执行器和事务收尾函数。
func (r *KeyStore) KeyBeginAPIKeyCreateTransaction(ctx context.Context) (*dbent.Client, SQLExecutor, func() error, func(), error) {
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

// KeyLockAPIKeyOwnerAndCount 锁定用户并统计所有未软删除的 API Key。
func (r *KeyStore) KeyLockAPIKeyOwnerAndCount(ctx context.Context, sqlq SQLExecutor, userID int64) (int64, int, error) {
	lockQuery := `SELECT api_key_limit FROM users WHERE id = $1 AND deleted_at IS NULL`
	if r.client.Driver().Dialect() == dialect.Postgres {
		lockQuery += ` FOR UPDATE`
	}

	var limit int
	if err := scanSingleRow(ctx, sqlq, lockQuery, []any{userID}, &limit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, keycore.ErrUserNotFound
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

// KeyCreateAPIKeyRecord 使用指定事务 client 插入 API Key 记录。
func KeyCreateAPIKeyRecord(ctx context.Context, client *dbent.Client, key *keycore.APIKey) (*dbent.APIKey, error) {
	builder := client.APIKey.Create().
		SetUserID(key.UserID).
		SetNillableTeamID(key.TeamID).
		SetKey(key.Key).
		SetName(key.Name).
		SetStatus(key.Status).
		SetIsComposite(key.IsComposite).
		SetFastModePolicy(KeyApiKeyFastModePolicyForPersistence(key.FastModePolicy)).
		SetBillingMode(KeyApiKeyBillingModeForPersistence(key.BillingMode)).
		SetNillablePreferredSubscriptionID(key.PreferredSubscriptionID).
		SetModelMapping(keycore.CloneModelMapping(key.ModelMapping)).
		SetNillableGroupID(key.GroupID).
		SetNillableLastUsedAt(key.LastUsedAt).
		SetNillableManagedBy(key.ManagedBy).
		SetQuota(key.Quota).
		SetQuotaUsed(key.QuotaUsed).
		SetNillableExpiresAt(key.ExpiresAt).
		SetRateLimit5h(key.RateLimit5h).
		SetRateLimit1d(key.RateLimit1d).
		SetRateLimit7d(key.RateLimit7d).
		SetFallbackToDefaultGroupWhenUnavailable(key.FallbackToDefaultGroupWhenUnavailable)

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
	if err := KeyReplaceCompositeGroupRecords(ctx, client, created.ID, key.CompositeGroups); err != nil {
		return nil, err
	}
	return created, nil
}

// KeyReplaceCompositeGroupRecords 在调用方事务内完整替换复合 Key 的分组映射。
func KeyReplaceCompositeGroupRecords(ctx context.Context, client *dbent.Client, apiKeyID int64, bindings []keycore.APIKeyCompositeGroup) error {
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
			SetSortOrder(binding.SortOrder))
	}
	return client.APIKeyCompositeGroup.CreateBulk(builders...).Exec(ctx)
}

// KeyWithAPIKeyCompositeGroups 统一按用户配置顺序加载复合映射及分组。
func KeyWithAPIKeyCompositeGroups(query *dbent.APIKeyQuery) *dbent.APIKeyQuery {
	return query.WithCompositeGroups(func(q *dbent.APIKeyCompositeGroupQuery) {
		q.Order(dbent.Asc(apikeycompositegroup.FieldSortOrder), dbent.Asc(apikeycompositegroup.FieldID)).
			WithGroup()
	})
}

func (r *KeyStore) GetByID(ctx context.Context, id int64) (*keycore.APIKey, error) {
	m, err := KeyWithAPIKeyCompositeGroups(r.KeyActiveQuery()).
		Where(apikey.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

// GetManagedKeyByUserAndGroup 查找某用户 + 分组的服务端托管隐藏 Key（如创作台执行 Key）。
func (r *KeyStore) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*keycore.APIKey, error) {
	m, err := r.KeyActiveQuery().
		Where(
			apikey.UserIDEQ(userID),
			apikey.GroupIDEQ(groupID),
			apikey.ManagedByEQ(managedBy),
		).
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

// CreateManagedKey 创建服务端托管的隐藏 Key；与普通 Key 共用创建路径（含数量限制），
// managed_by 由调用方在 key.ManagedBy 上显式标记。
func (r *KeyStore) CreateManagedKey(ctx context.Context, key *keycore.APIKey) error {
	return r.Create(ctx, key)
}

// GetKeyAndOwnerID 根据 API Key ID 获取其 key 与所有者（用户）ID。
// 相比 GetByID，此方法性能更优，因为：
//   - 使用 Select() 只查询必要字段，减少数据传输量
//   - 不加载完整的 API Key 实体及其关联数据（User、Group 等）
//   - 适用于删除等只需 key 与用户 ID 的场景
func (r *KeyStore) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	m, err := r.KeyActiveQuery().
		Where(apikey.IDEQ(id)).
		Select(apikey.FieldKey, apikey.FieldUserID).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", 0, keycore.ErrAPIKeyNotFound
		}
		return "", 0, err
	}
	return m.Key, m.UserID, nil
}

func (r *KeyStore) GetByKey(ctx context.Context, key string) (*keycore.APIKey, error) {
	m, err := KeyWithAPIKeyCompositeGroups(r.KeyActiveQuery()).
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
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

func (r *KeyStore) GetByKeyForAuth(ctx context.Context, key string) (*keycore.APIKey, error) {
	m, err := r.KeyActiveQuery().
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
				group.FieldAllowedProtocols,
				group.FieldProtocolFallbacks,
				group.FieldResponsesImagePolicy,
				group.FieldAllowLive,
				group.FieldForceOpenaiFast,
				group.FieldOpenaiFastPolicy,
				group.FieldFreeOpenaiFast,
				group.FieldDefaultMappedModel,
				group.FieldMessagesDispatchModelConfig,
				group.FieldModelsListConfig,
				group.FieldRpmLimit,
				group.FieldMaxReasoningEffort,
				group.FieldMaxReasoningEffortOverLimit,
				group.FieldReasoningEffortMappings,
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
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

func (r *KeyStore) Update(ctx context.Context, key *keycore.APIKey, fields keycore.APIKeyUpdateFields) error {
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
		builder.SetFastModePolicy(KeyApiKeyFastModePolicyForPersistence(key.FastModePolicy))
	}
	if fields.BillingConfiguration {
		builder.SetBillingMode(KeyApiKeyBillingModeForPersistence(key.BillingMode))
		if key.PreferredSubscriptionID == nil {
			builder.ClearPreferredSubscriptionID()
		} else {
			builder.SetPreferredSubscriptionID(*key.PreferredSubscriptionID)
		}
	}
	if fields.ModelMapping {
		builder.SetModelMapping(keycore.CloneModelMapping(key.ModelMapping))
	}
	if fields.FallbackToDefaultGroupWhenUnavailable {
		builder.SetFallbackToDefaultGroupWhenUnavailable(key.FallbackToDefaultGroupWhenUnavailable)
	}
	if fields.Quota {
		builder.SetQuota(key.Quota)
	}
	if fields.RateLimits {
		builder.
			SetRateLimit5h(key.RateLimit5h).
			SetRateLimit1d(key.RateLimit1d).
			SetRateLimit7d(key.RateLimit7d)
	}
	if fields.GroupID {
		if key.GroupID != nil {
			builder.SetGroupID(*key.GroupID)
		} else {
			builder.ClearGroupID()
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

	billingpostgres.ApplyKeyUsageReset(builder, billing.KeyUsageReset{ResetQuota: fields.QuotaUsed, ResetWindows: fields.RateLimitUsage, QuotaUsed: key.QuotaUsed, Windows: billing.APIKeyRateLimitData{Usage5h: key.Usage5h, Usage1d: key.Usage1d, Usage7d: key.Usage7d, Window5hStart: key.Window5hStart, Window1dStart: key.Window1dStart, Window7dStart: key.Window7dStart}})
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// 更新影响行数为 0，说明记录不存在或已被软删除。
		return keycore.ErrAPIKeyNotFound
	}
	if fields.CompositeConfiguration {
		if err := KeyReplaceCompositeGroupRecords(ctx, client, key.ID, key.CompositeGroups); err != nil {
			return err
		}
	}

	// 使用同一时间戳回填，避免并发删除导致二次查询失败。
	key.UpdatedAt = now
	return nil
}

func (r *KeyStore) Delete(ctx context.Context, id int64) error {
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
			return keycore.ErrAPIKeyNotFound
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
		return keycore.ErrAPIKeyNotFound
	}
	return nil
}

// DeleteWithAudit 为兼容滚动升级保留历史方法名。
// 该方法以原子方式写入墓碑并软删除 Key，不保留凭据材料；墓碑会释放唯一键值以便安全复用。
func (r *KeyStore) DeleteWithAudit(ctx context.Context, id int64) error {
	tombstoneKey := fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano())

	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return r.KeyDeleteWithTombstone(ctx, existingTx.Client(), id, tombstoneKey)
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

	if err := r.KeyDeleteWithTombstone(ctx, exec, id, tombstoneKey); err != nil {
		return err
	}

	if tx != nil {
		return tx.Commit()
	}
	return nil
}

func (r *KeyStore) KeyDeleteWithTombstone(ctx context.Context, exec *dbent.Client, id int64, tombstoneKey string) error {
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
		return keycore.ErrAPIKeyNotFound
	}
	return nil
}

func (r *KeyStore) KeyApiKeyListByUserIDQuery(userID int64, filters keycore.APIKeyListFilters) *dbent.APIKeyQuery {
	q := r.KeyActiveQuery().Where(apikey.UserIDEQ(userID))
	// 创作台等场景的服务端托管隐藏 Key 不出现在普通 Key 列表中。
	q = q.Where(apikey.ManagedByIsNil())

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

func (r *KeyStore) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters keycore.APIKeyListFilters) ([]keycore.APIKey, *pagination.PaginationResult, error) {
	q := r.KeyApiKeyListByUserIDQuery(userID, filters)

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	if strings.EqualFold(strings.TrimSpace(params.SortBy), "usage") {
		return r.KeyListByUserIDWithUsageSort(ctx, q, params, total)
	}

	keysQuery := KeyWithAPIKeyCompositeGroups(q.WithGroup())
	keysQuery = keysQuery.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range KeyApiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}
	if err := r.KeyAttachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, nil, err
	}

	return outKeys, pagination.ResultFromTotal(int64(total), params), nil
}

// ListAllByUserID 返回经过筛选的全部 API Key，供依赖运行时数据的排序逻辑使用。
func (r *KeyStore) ListAllByUserID(ctx context.Context, userID int64, filters keycore.APIKeyListFilters) ([]keycore.APIKey, error) {
	keys, err := KeyWithAPIKeyCompositeGroups(r.KeyApiKeyListByUserIDQuery(userID, filters).WithGroup()).
		Order(dbent.Asc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}
	if err := r.KeyAttachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, err
	}
	return outKeys, nil
}

// KeyAttachLastUsedIPs 为当前页 API Key 批量附加最近一条非空使用 IP。
func (r *KeyStore) KeyAttachLastUsedIPs(ctx context.Context, keys []keycore.APIKey) error {
	if len(keys) == 0 || r.sql == nil {
		return nil
	}

	apiKeyIDs := make([]int64, 0, len(keys))
	for i := range keys {
		apiKeyIDs = append(apiKeyIDs, keys[i].ID)
	}

	lastUsedIPs, err := r.KeyLatestUsageLogIPs(ctx, apiKeyIDs)
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

func (r *KeyStore) KeyLatestUsageLogIPs(ctx context.Context, apiKeyIDs []int64) (result map[int64]string, err error) {
	// 保留原空批次/无执行器的提前返回，避免迁移后提前读取 dialect。
	if len(apiKeyIDs) == 0 || r.sql == nil {
		return map[int64]string{}, nil
	}
	return usagequery.KeyLatestUsageLogIPs(ctx, r.sql, r.client.Driver().Dialect(), apiKeyIDs)
}

func KeyLatestUsageLogIPsQuery(apiKeyIDs []int64, dialectName string) (string, []any) {
	return usagequery.KeyLatestUsageLogIPsQuery(apiKeyIDs, dialectName)
}

func (r *KeyStore) KeyListByUserIDWithUsageSort(ctx context.Context, q *dbent.APIKeyQuery, params pagination.PaginationParams, total int) ([]keycore.APIKey, *pagination.PaginationResult, error) {
	keys, err := KeyWithAPIKeyCompositeGroups(q.WithGroup()).
		Order(dbent.Desc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	keyIDs := make([]int64, 0, len(keys))
	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		key := KeyApiKeyEntityToService(keys[i])
		outKeys = append(outKeys, *key)
		keyIDs = append(keyIDs, key.ID)
	}

	usageTotals, err := r.KeyLoadAPIKeyUsageTotals(ctx, keyIDs)
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

	pageKeys := paginateKeyRows(outKeys, params)
	if err := r.KeyAttachLastUsedIPs(ctx, pageKeys); err != nil {
		return nil, nil, err
	}
	return pageKeys, pagination.ResultFromTotal(int64(total), params), nil
}

func (r *KeyStore) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
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

func (r *KeyStore) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	count, err := r.KeyActiveQuery().Where(apikey.UserIDEQ(userID)).Count(ctx)
	return int64(count), err
}

func (r *KeyStore) ExistsByKey(ctx context.Context, key string) (bool, error) {
	count, err := r.KeyActiveQuery().Where(apikey.KeyEQ(key)).Count(ctx)
	return count > 0, err
}

func (r *KeyStore) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]keycore.APIKey, *pagination.PaginationResult, error) {
	q := r.KeyActiveQuery().Where(apikey.Or(
		apikey.GroupIDEQ(groupID),
		apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
	))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := KeyWithAPIKeyCompositeGroups(q.WithUser().WithGroup())
	keysQuery = keysQuery.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range KeyApiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}

	return outKeys, pagination.ResultFromTotal(int64(total), params), nil
}

func KeyApiKeyListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
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
func (r *KeyStore) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]keycore.APIKey, error) {
	q := r.KeyActiveQuery()
	if userID > 0 {
		q = q.Where(apikey.UserIDEQ(userID))
	}

	if keyword != "" {
		q = q.Where(apikey.NameContainsFold(keyword))
	}

	keys, err := KeyWithAPIKeyCompositeGroups(q.WithGroup()).
		Limit(limit).
		Order(dbent.Desc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}
	return outKeys, nil
}

// ClearGroupIDByGroupID 将指定分组的所有 API Key 的 group_id 设为 nil
func (r *KeyStore) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
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
func (r *KeyStore) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
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
func (r *KeyStore) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	count, err := r.KeyActiveQuery().Where(apikey.Or(
		apikey.GroupIDEQ(groupID),
		apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
	)).Count(ctx)
	return int64(count), err
}

func (r *KeyStore) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	keys, err := r.KeyActiveQuery().
		Where(apikey.UserIDEQ(userID)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (r *KeyStore) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	keys, err := r.KeyActiveQuery().
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

func (r *KeyStore) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetLastUsedAt(usedAt).
		SetUpdatedAt(usedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return keycore.ErrAPIKeyNotFound
	}
	return nil
}

func KeyApiKeyEntityToService(m *dbent.APIKey) *keycore.APIKey {
	if m == nil {
		return nil
	}
	out := &keycore.APIKey{
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
		ModelMapping:                          keycore.CloneModelMapping(m.ModelMapping),
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
		ManagedBy:                             m.ManagedBy,
	}
	if m.Edges.User != nil {
		out.User = userEntityToKeyView(m.Edges.User)
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
		out.Group = groupEntityToKeyView(m.Edges.Group)
	}
	if rows, err := m.Edges.CompositeGroupsOrErr(); err == nil {
		out.CompositeGroups = make([]keycore.APIKeyCompositeGroup, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			binding := keycore.APIKeyCompositeGroup{
				ID:               row.ID,
				APIKeyID:         row.APIKeyID,
				GroupID:          row.GroupID,
				Prefix:           row.Prefix,
				NormalizedPrefix: row.NormalizedPrefix,
				SortOrder:        row.SortOrder,
			}
			if row.Edges.Group != nil {
				binding.Group = groupEntityToKeyView(row.Edges.Group)
			}
			out.CompositeGroups = append(out.CompositeGroups, binding)
		}
	}
	return out
}

func KeyDerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func (r *KeyStore) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).IncrementQuotaUsed(ctx, id, amount)
}

func (r *KeyStore) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*keycore.APIKeyQuotaUsageState, error) {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).IncrementQuotaUsedAndGetState(ctx, id, amount)
}

func (r *KeyStore) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).IncrementRateLimitUsage(ctx, id, cost)
}

func (r *KeyStore) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).ResetRateLimitWindows(ctx, id)
}

func (r *KeyStore) GetRateLimitData(ctx context.Context, id int64) (result *keycore.APIKeyRateLimitData, err error) {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).GetRateLimitData(ctx, id)
}

// UsageTotalsReader 保留用量排序整段查询的原过滤和回退，S08 改绑。
type UsageTotalsReader func(context.Context, []int64) (map[int64]float64, error)

func (r *KeyStore) KeyLoadAPIKeyUsageTotals(ctx context.Context, ids []int64) (map[int64]float64, error) {
	if r.usageTotals == nil {
		return nil, fmt.Errorf("API key usage totals reader is not configured")
	}
	return r.usageTotals(ctx, ids)
}
