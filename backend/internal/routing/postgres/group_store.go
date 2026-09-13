// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	sql "database/sql"
	json "encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	errors "errors"
	fmt "fmt"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	group "github.com/TokenFlux/TokenRouter/ent/group"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	pq "github.com/lib/pq"
	sort "sort"
	strings "strings"
)

type GroupLinkParticipant interface {
	Clear(context.Context, int64) (sql.Result, error)
	Bind(context.Context, int64, []int64) error
	Copy(context.Context, int64, int64, bool) (sql.Result, error)
}
type GroupAccessParticipant interface {
	Delete(context.Context, int64) error
}
type GroupStoreOptions struct {
	Accounts func(postgresinfra.Executor) GroupLinkParticipant
	Users    func(postgresinfra.Executor) GroupAccessParticipant
	Enqueue  func(context.Context, postgresinfra.Executor, *int64) error
}

func NewGroupStore(client *dbent.Client, db postgresinfra.Executor, options GroupStoreOptions) *GroupStore {
	return &GroupStore{client: client, sql: db, options: options}
}

type GroupStore struct {
	options GroupStoreOptions
	client  *dbent.Client
	sql     postgresinfra.Executor
}

func (r *GroupStore) sqlExecutorFromContext(ctx context.Context) postgresinfra.Executor {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.sql
}

// LockGroupSortOrder 在当前事务内串行化末尾排序值的读取和分组创建。
func (r *GroupStore) LockGroupSortOrder(ctx context.Context) error {
	if dbent.TxFromContext(ctx) == nil {
		return errors.New("group sort order lock requires transaction")
	}
	sqlq := r.sqlExecutorFromContext(ctx)
	if sqlq == nil {
		return errors.New("sql executor is not configured")
	}
	_, err := sqlq.ExecContext(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtext($1))",
		"tokenrouter_group_sort_order",
	)
	return err
}

func (r *GroupStore) Create(ctx context.Context, groupIn *routing.Group) error {
	client := clientFromContext(ctx, r.client)
	sqlq := r.sqlExecutorFromContext(ctx)
	if err := createGroupRecord(ctx, client, groupIn); err != nil {
		return err
	}
	if err := r.options.Enqueue(ctx, sqlq, &groupIn.ID); err != nil {
		logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue group create failed: group=%d err=%v", groupIn.ID, err)
	}
	return nil
}

func createGroupRecord(ctx context.Context, client *dbent.Client, groupIn *routing.Group) error {
	if groupIn == nil {
		return errors.New("group is nil")
	}
	schedulerType, err := routing.NormalizeGroupSchedulerType(string(groupIn.SchedulerType))
	if err != nil {
		return err
	}
	groupIn.SchedulerType = schedulerType
	modelPricing, err := json.Marshal(groupIn.ModelPricing)
	if err != nil {
		return fmt.Errorf("marshal group model pricing: %w", err)
	}
	builder := client.Group.Create().
		SetName(groupIn.Name).
		SetDescription(groupIn.Description).
		SetPlatform(groupIn.Platform).
		SetSchedulerType(string(groupIn.SchedulerType)).
		SetAdvancedSchedulerOverrides(groupIn.AdvancedSchedulerOverrides).
		SetDisplayBrand(groupIn.DisplayBrand).
		SetRateMultiplier(groupIn.RateMultiplier).
		SetSortOrder(groupIn.SortOrder).
		SetIsExclusive(groupIn.IsExclusive).
		SetIsDefault(groupIn.IsDefault).
		SetStatus(groupIn.Status).
		SetSessionIsolationEnabled(groupIn.SessionIsolationEnabled).
		SetAllowImageGeneration(groupIn.AllowImageGeneration).
		SetAllowBatchImageGeneration(groupIn.AllowBatchImageGeneration).
		SetBatchImageDiscountMultiplier(groupIn.BatchImageDiscountMultiplier).
		SetBatchImageHoldMultiplier(groupIn.BatchImageHoldMultiplier).
		SetNillableWebSearchPricePerCall(groupIn.WebSearchPricePerCall).
		SetNillableSearchPricePer1k(groupIn.SearchPricePer1k).
		SetNillableAudioRealtimePricePerMin(groupIn.AudioRealtimePricePerMin).
		SetNillableAudioTtsPricePerMillionChars(groupIn.AudioTTSPricePerMillionChars).
		SetNillableAudioSttPricePerHour(groupIn.AudioSTTPricePerHour).
		SetLongContextPricingEnabled(groupIn.LongContextPricingEnabled).
		SetModelPricing(modelPricing).
		SetClaudeCodeOnly(groupIn.ClaudeCodeOnly).
		SetNillableFallbackGroupID(groupIn.FallbackGroupID).
		SetNillableFallbackGroupIDOnInvalidRequest(groupIn.FallbackGroupIDOnInvalidRequest).
		SetNillableUnavailableFallbackGroupID(groupIn.UnavailableFallbackGroupID).
		SetModelRoutingEnabled(groupIn.ModelRoutingEnabled).
		SetMcpXMLInject(groupIn.MCPXMLInject).
		SetAllowedProtocols(groupIn.AllowedProtocols).
		SetProtocolFallbacks(groupIn.ProtocolFallbacks).
		SetResponsesImagePolicy(groupIn.ResponsesImagePolicy).
		SetAllowMessagesDispatch(groupIn.AllowMessagesDispatch).
		SetAllowLive(groupIn.AllowLive).
		SetForceOpenaiFast(groupIn.ForceOpenAIFast).
		SetOpenaiFastPolicy(groupIn.EffectiveOpenAIFastPolicy()).
		SetFreeOpenaiFast(groupIn.FreeOpenAIFast).
		SetRequireOauthOnly(groupIn.RequireOAuthOnly).
		SetRequirePrivacySet(groupIn.RequirePrivacySet).
		SetDefaultMappedModel(groupIn.DefaultMappedModel).
		SetMessagesDispatchModelConfig(groupIn.MessagesDispatchModelConfig).
		SetModelsListConfig(groupIn.ModelsListConfig).
		SetAvailabilityProbeConfig(groupIn.AvailabilityProbeConfig).
		SetRpmLimit(groupIn.RPMLimit).
		SetMaxReasoningEffort(groupIn.MaxReasoningEffort).
		SetMaxReasoningEffortOverLimit(groupIn.MaxReasoningEffortOverLimit).
		SetReasoningEffortMappings(groupIn.ReasoningEffortMappings).
		SetPeakRateEnabled(groupIn.PeakRateEnabled).
		SetPeakStart(groupIn.PeakStart).
		SetPeakEnd(groupIn.PeakEnd).
		SetPeakRateMultiplier(groupIn.PeakRateMultiplier)
	if groupIn.DuplicateOperationID != "" {
		builder = builder.SetDuplicateOperationID(groupIn.DuplicateOperationID)
	}

	// 设置模型路由配置
	if groupIn.ModelRouting != nil {
		builder = builder.SetModelRouting(groupIn.ModelRouting)
	}

	// 设置支持的模型系列（始终设置，空数组表示不限制）
	builder = builder.SetSupportedModelScopes(groupIn.SupportedModelScopes)

	created, err := builder.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, routing.ErrGroupExists)
	}
	groupIn.ID = created.ID
	groupIn.CreatedAt = created.CreatedAt
	groupIn.UpdatedAt = created.UpdatedAt
	return nil
}

func (r *GroupStore) FindByDuplicateOperationID(ctx context.Context, operationID string) (*routing.Group, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, nil
	}
	row, err := r.client.Group.Query().
		Where(group.DuplicateOperationIDEQ(operationID)).
		Order(dbent.Asc(group.FieldID)).
		First(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find group duplicate operation: %w", err)
	}
	return GroupFromEnt(row), nil
}

// CreateFromSource 原子保存分组副本、源账号绑定和调度事件。
func (r *GroupStore) CreateFromSource(ctx context.Context, groupIn *routing.Group, sourceGroupID int64) error {
	if groupIn == nil {
		return errors.New("group is nil")
	}

	baseClient := clientFromContext(ctx, r.client)
	tx, err := baseClient.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}

	txClient := baseClient
	txCtx := ctx
	ownsTx := err == nil
	if ownsTx {
		defer func() { _ = tx.Rollback() }()
		txClient = tx.Client()
		txCtx = dbent.NewTxContext(ctx, tx)
	}

	if err := createGroupRecord(txCtx, txClient, groupIn); err != nil {
		return err
	}
	result, err := r.options.Accounts(txClient).Copy(txCtx, groupIn.ID, sourceGroupID, groupIn.RequireOAuthOnly)
	if err != nil {
		return err
	}
	if count, countErr := result.RowsAffected(); countErr == nil {
		groupIn.AccountCount = count
	}
	if err := r.options.Enqueue(txCtx, txClient, &groupIn.ID); err != nil {
		return err
	}

	if ownsTx {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (r *GroupStore) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	out, err := r.GetByIDLite(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := r.loadAccountCounts(ctx, []int64{out.ID})
	if err == nil {
		c := counts[out.ID]
		out.AccountCount = c.Total
		out.ActiveAccountCount = c.Active
		out.RateLimitedAccountCount = c.RateLimited
	}
	return out, nil
}

func (r *GroupStore) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	client := clientFromContext(ctx, r.client)

	// AccountCount is intentionally not loaded here; use GetByID when needed.
	m, err := client.Group.Query().
		Where(group.IDEQ(id)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, routing.ErrGroupNotFound, nil)
	}
	return GroupFromEnt(m), nil
}

func (r *GroupStore) Update(ctx context.Context, groupIn *routing.Group) error {
	if groupIn == nil {
		return errors.New("group is nil")
	}
	schedulerType, err := routing.NormalizeGroupSchedulerType(string(groupIn.SchedulerType))
	if err != nil {
		return err
	}
	groupIn.SchedulerType = schedulerType
	client := clientFromContext(ctx, r.client)
	sqlq := r.sqlExecutorFromContext(ctx)
	modelPricing, err := json.Marshal(groupIn.ModelPricing)
	if err != nil {
		return fmt.Errorf("marshal group model pricing: %w", err)
	}

	builder := client.Group.UpdateOneID(groupIn.ID).
		SetName(groupIn.Name).
		SetDescription(groupIn.Description).
		SetPlatform(groupIn.Platform).
		SetSchedulerType(string(schedulerType)).
		SetAdvancedSchedulerOverrides(groupIn.AdvancedSchedulerOverrides).
		SetDisplayBrand(groupIn.DisplayBrand).
		SetRateMultiplier(groupIn.RateMultiplier).
		SetSortOrder(groupIn.SortOrder).
		SetIsExclusive(groupIn.IsExclusive).
		SetIsDefault(groupIn.IsDefault).
		SetStatus(groupIn.Status).
		SetSessionIsolationEnabled(groupIn.SessionIsolationEnabled).
		SetAllowImageGeneration(groupIn.AllowImageGeneration).
		SetAllowBatchImageGeneration(groupIn.AllowBatchImageGeneration).
		SetBatchImageDiscountMultiplier(groupIn.BatchImageDiscountMultiplier).
		SetBatchImageHoldMultiplier(groupIn.BatchImageHoldMultiplier).
		SetLongContextPricingEnabled(groupIn.LongContextPricingEnabled).
		SetModelPricing(modelPricing).
		SetClaudeCodeOnly(groupIn.ClaudeCodeOnly).
		SetModelRoutingEnabled(groupIn.ModelRoutingEnabled).
		SetMcpXMLInject(groupIn.MCPXMLInject).
		SetAllowedProtocols(groupIn.AllowedProtocols).
		SetProtocolFallbacks(groupIn.ProtocolFallbacks).
		SetResponsesImagePolicy(groupIn.ResponsesImagePolicy).
		SetAllowMessagesDispatch(groupIn.AllowMessagesDispatch).
		SetAllowLive(groupIn.AllowLive).
		SetForceOpenaiFast(groupIn.ForceOpenAIFast).
		SetOpenaiFastPolicy(groupIn.EffectiveOpenAIFastPolicy()).
		SetFreeOpenaiFast(groupIn.FreeOpenAIFast).
		SetRequireOauthOnly(groupIn.RequireOAuthOnly).
		SetRequirePrivacySet(groupIn.RequirePrivacySet).
		SetDefaultMappedModel(groupIn.DefaultMappedModel).
		SetMessagesDispatchModelConfig(groupIn.MessagesDispatchModelConfig).
		SetModelsListConfig(groupIn.ModelsListConfig).
		SetAvailabilityProbeConfig(groupIn.AvailabilityProbeConfig).
		SetRpmLimit(groupIn.RPMLimit).
		SetMaxReasoningEffort(groupIn.MaxReasoningEffort).
		SetMaxReasoningEffortOverLimit(groupIn.MaxReasoningEffortOverLimit).
		SetReasoningEffortMappings(groupIn.ReasoningEffortMappings).
		SetPeakRateEnabled(groupIn.PeakRateEnabled).
		SetPeakStart(groupIn.PeakStart).
		SetPeakEnd(groupIn.PeakEnd).
		SetPeakRateMultiplier(groupIn.PeakRateMultiplier)

	if groupIn.WebSearchPricePerCall != nil {
		builder = builder.SetWebSearchPricePerCall(*groupIn.WebSearchPricePerCall)
	} else {
		builder = builder.ClearWebSearchPricePerCall()
	}
	if groupIn.SearchPricePer1k != nil {
		builder = builder.SetSearchPricePer1k(*groupIn.SearchPricePer1k)
	} else {
		builder = builder.ClearSearchPricePer1k()
	}
	if groupIn.AudioRealtimePricePerMin != nil {
		builder = builder.SetAudioRealtimePricePerMin(*groupIn.AudioRealtimePricePerMin)
	} else {
		builder = builder.ClearAudioRealtimePricePerMin()
	}
	if groupIn.AudioTTSPricePerMillionChars != nil {
		builder = builder.SetAudioTtsPricePerMillionChars(*groupIn.AudioTTSPricePerMillionChars)
	} else {
		builder = builder.ClearAudioTtsPricePerMillionChars()
	}
	if groupIn.AudioSTTPricePerHour != nil {
		builder = builder.SetAudioSttPricePerHour(*groupIn.AudioSTTPricePerHour)
	} else {
		builder = builder.ClearAudioSttPricePerHour()
	}

	// 处理 FallbackGroupID：nil 时清除，否则设置
	if groupIn.FallbackGroupID != nil {
		builder = builder.SetFallbackGroupID(*groupIn.FallbackGroupID)
	} else {
		builder = builder.ClearFallbackGroupID()
	}
	// 处理 FallbackGroupIDOnInvalidRequest：nil 时清除，否则设置
	if groupIn.FallbackGroupIDOnInvalidRequest != nil {
		builder = builder.SetFallbackGroupIDOnInvalidRequest(*groupIn.FallbackGroupIDOnInvalidRequest)
	} else {
		builder = builder.ClearFallbackGroupIDOnInvalidRequest()
	}
	// 处理 UnavailableFallbackGroupID：nil 时清除，否则设置
	if groupIn.UnavailableFallbackGroupID != nil {
		builder = builder.SetUnavailableFallbackGroupID(*groupIn.UnavailableFallbackGroupID)
	} else {
		builder = builder.ClearUnavailableFallbackGroupID()
	}

	// 处理 ModelRouting：nil 时清除，否则设置
	if groupIn.ModelRouting != nil {
		builder = builder.SetModelRouting(groupIn.ModelRouting)
	} else {
		builder = builder.ClearModelRouting()
	}

	// 处理 SupportedModelScopes（始终设置，空数组表示不限制）
	builder = builder.SetSupportedModelScopes(groupIn.SupportedModelScopes)

	updated, err := builder.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, routing.ErrGroupNotFound, routing.ErrGroupExists)
	}
	groupIn.UpdatedAt = updated.UpdatedAt
	if err := r.options.Enqueue(ctx, sqlq, &groupIn.ID); err != nil {
		logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue group update failed: group=%d err=%v", groupIn.ID, err)
	}
	return nil
}

func (r *GroupStore) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	sqlq := r.sqlExecutorFromContext(ctx)

	_, err := client.Group.Delete().Where(group.IDEQ(id)).Exec(ctx)
	if err != nil {
		return translatePersistenceError(err, routing.ErrGroupNotFound, nil)
	}
	if err := r.options.Enqueue(ctx, sqlq, &id); err != nil {
		logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue group delete failed: group=%d err=%v", id, err)
	}
	return nil
}

func (r *GroupStore) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "", nil)
}

func (r *GroupStore) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.Group.Query()

	if platform != "" {
		q = q.Where(group.PlatformEQ(platform))
	}
	if status != "" {
		q = q.Where(group.StatusEQ(status))
	}
	if search != "" {
		q = q.Where(group.Or(
			group.NameContainsFold(search),
			group.DescriptionContainsFold(search),
			group.DisplayBrandContainsFold(search),
		))
	}
	if isExclusive != nil {
		q = q.Where(group.IsExclusiveEQ(*isExclusive))
	}

	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	if strings.EqualFold(strings.TrimSpace(params.SortBy), "account_count") {
		return r.listWithAccountCountSort(ctx, q, params, total)
	}

	groupsQuery := q.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range groupListOrder(params) {
		groupsQuery = groupsQuery.Order(order)
	}

	groups, err := groupsQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	groupIDs := make([]int64, 0, len(groups))
	outGroups := make([]routing.Group, 0, len(groups))
	for i := range groups {
		g := GroupFromEnt(groups[i])
		outGroups = append(outGroups, *g)
		groupIDs = append(groupIDs, g.ID)
	}

	counts, err := r.loadAccountCounts(ctx, groupIDs)
	if err == nil {
		for i := range outGroups {
			c := counts[outGroups[i].ID]
			outGroups[i].AccountCount = c.Total
			outGroups[i].ActiveAccountCount = c.Active
			outGroups[i].RateLimitedAccountCount = c.RateLimited
		}
	}

	return outGroups, pagination.ResultFromTotal(int64(total), params), nil
}

func (r *GroupStore) listWithAccountCountSort(ctx context.Context, q *dbent.GroupQuery, params pagination.PaginationParams, total int) ([]routing.Group, *pagination.PaginationResult, error) {
	// 第一步：只查 ID + sort_order（轻量，不做分页 — 需要全量排序 account_count）。
	rows, err := q.Clone().
		Select(group.FieldID, group.FieldSortOrder).
		Order(dbent.Asc(group.FieldSortOrder), dbent.Asc(group.FieldID)).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	type sortEntry struct {
		id           int64
		sortOrder    int
		accountCount int64
	}
	entries := make([]sortEntry, 0, len(rows))
	groupIDs := make([]int64, len(rows))
	for i, r := range rows {
		groupIDs[i] = r.ID
		entries = append(entries, sortEntry{id: r.ID, sortOrder: r.SortOrder})
	}

	// 第二步：批量加载 account counts（一次 SQL）。
	counts, err := r.loadAccountCounts(ctx, groupIDs)
	if err != nil {
		return nil, nil, err
	}
	for i := range entries {
		c := counts[entries[i].id]
		if c.Total > 0 {
			entries[i].accountCount = c.Total
		}
	}

	// 第三步：Go 侧排序（数据量 = Group 总数，通常 < 200，安全）。
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)
	tieCmp := func(a, b sortEntry) bool {
		if a.sortOrder == b.sortOrder {
			return a.id < b.id
		}
		return a.sortOrder < b.sortOrder
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].accountCount == entries[j].accountCount {
			return tieCmp(entries[i], entries[j])
		}
		if sortOrder == pagination.SortOrderAsc {
			return entries[i].accountCount < entries[j].accountCount
		}
		return entries[i].accountCount > entries[j].accountCount
	})

	// 第四步：分页，只加载当前页需要的完整 Group。
	page := pagination.Slice(entries, params)
	if len(page) == 0 {
		return nil, pagination.ResultFromTotal(int64(total), params), nil
	}

	pageIDs := make([]int64, len(page))
	pageIdx := make(map[int64]int, len(page))
	for i, e := range page {
		pageIDs[i] = e.id
		pageIdx[e.id] = i
	}

	groups, err := r.client.Group.Query().
		Where(group.IDIn(pageIDs...)).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outGroups := make([]routing.Group, len(page))
	for i := range groups {
		g := GroupFromEnt(groups[i])
		c := counts[g.ID]
		g.AccountCount = c.Total
		g.ActiveAccountCount = c.Active
		g.RateLimitedAccountCount = c.RateLimited
		if idx, ok := pageIdx[g.ID]; ok {
			outGroups[idx] = *g
		}
	}

	return outGroups, pagination.ResultFromTotal(int64(total), params), nil
}

func groupListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderAsc)

	var field string
	tieField := group.FieldID
	defaultOrder := true
	switch sortBy {
	case "", "sort_order":
		field = group.FieldSortOrder
	case "name":
		field = group.FieldName
		defaultOrder = false
	case "platform":
		field = group.FieldPlatform
		defaultOrder = false
	case "display_brand":
		field = group.FieldDisplayBrand
		defaultOrder = false
	case "rate_multiplier":
		field = group.FieldRateMultiplier
		defaultOrder = false
	case "is_exclusive":
		field = group.FieldIsExclusive
		defaultOrder = false
	case "session_isolation_enabled":
		field = group.FieldSessionIsolationEnabled
		defaultOrder = false
	case "status":
		field = group.FieldStatus
		defaultOrder = false
	case "created_at":
		field = group.FieldCreatedAt
		defaultOrder = false
	case "id":
		field = group.FieldID
		defaultOrder = false
		tieField = ""
	default:
		field = group.FieldSortOrder
	}

	if sortOrder == pagination.SortOrderDesc && sortBy != "" {
		if tieField == "" {
			return []func(*entsql.Selector){dbent.Desc(field)}
		}
		return []func(*entsql.Selector){dbent.Desc(field), dbent.Desc(tieField)}
	}
	if defaultOrder {
		return []func(*entsql.Selector){dbent.Asc(group.FieldSortOrder), dbent.Asc(group.FieldID)}
	}
	if tieField == "" {
		return []func(*entsql.Selector){dbent.Asc(field)}
	}
	return []func(*entsql.Selector){dbent.Asc(field), dbent.Asc(tieField)}
}

func (r *GroupStore) ListActive(ctx context.Context) ([]routing.Group, error) {
	client := clientFromContext(ctx, r.client)

	groups, err := client.Group.Query().
		Where(group.StatusEQ(routing.StatusActive)).
		Order(dbent.Asc(group.FieldSortOrder), dbent.Asc(group.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	groupIDs := make([]int64, 0, len(groups))
	outGroups := make([]routing.Group, 0, len(groups))
	for i := range groups {
		g := GroupFromEnt(groups[i])
		outGroups = append(outGroups, *g)
		groupIDs = append(groupIDs, g.ID)
	}

	counts, err := r.loadAccountCounts(ctx, groupIDs)
	if err == nil {
		for i := range outGroups {
			c := counts[outGroups[i].ID]
			outGroups[i].AccountCount = c.Total
			outGroups[i].ActiveAccountCount = c.Active
			outGroups[i].RateLimitedAccountCount = c.RateLimited
		}
	}

	return outGroups, nil
}

// ListActiveIDs 仅加载活跃分组 ID，供容量汇总等不需要完整分组数据的路径使用。
func (r *GroupStore) ListActiveIDs(ctx context.Context) ([]int64, error) {
	if r.sql != nil {
		rows, err := r.sql.QueryContext(ctx, `
			SELECT id
			FROM groups
			WHERE status = $1
			  AND deleted_at IS NULL
			ORDER BY sort_order ASC, id ASC
		`, routing.StatusActive)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()

		ids := make([]int64, 0)
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return ids, nil
	}

	groups, err := r.client.Group.Query().
		Where(group.StatusEQ(routing.StatusActive)).
		Select(group.FieldID).
		Order(dbent.Asc(group.FieldSortOrder), dbent.Asc(group.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}
	return ids, nil
}

func (r *GroupStore) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	outGroups, err := r.ListActiveByPlatformLite(ctx, platform)
	if err != nil {
		return nil, err
	}

	groupIDs := make([]int64, 0, len(outGroups))
	for i := range outGroups {
		groupIDs = append(groupIDs, outGroups[i].ID)
	}

	counts, err := r.loadAccountCounts(ctx, groupIDs)
	if err == nil {
		for i := range outGroups {
			c := counts[outGroups[i].ID]
			outGroups[i].AccountCount = c.Total
			outGroups[i].ActiveAccountCount = c.Active
			outGroups[i].RateLimitedAccountCount = c.RateLimited
		}
	}

	return outGroups, nil
}

// ListActiveByPlatformLite 返回指定平台的活跃分组，但不加载账号统计。
func (r *GroupStore) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	client := clientFromContext(ctx, r.client)

	groups, err := client.Group.Query().
		Where(group.StatusEQ(routing.StatusActive), group.PlatformEQ(platform)).
		Order(dbent.Asc(group.FieldSortOrder), dbent.Asc(group.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outGroups := make([]routing.Group, 0, len(groups))
	for i := range groups {
		g := GroupFromEnt(groups[i])
		outGroups = append(outGroups, *g)
	}

	return outGroups, nil
}

func (r *GroupStore) ExistsByName(ctx context.Context, name string) (bool, error) {
	client := clientFromContext(ctx, r.client)
	return client.Group.Query().Where(group.NameEQ(name)).Exist(ctx)
}

// ExistsByIDs 批量检查分组是否存在（仅检查未软删除记录）。
// 返回结构：map[groupID]exists。
func (r *GroupStore) ExistsByIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	result := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
		result[id] = false
	}
	if len(uniqueIDs) == 0 {
		return result, nil
	}

	rows, err := sqlq.QueryContext(ctx, `
		SELECT id
		FROM groups
		WHERE id = ANY($1) AND deleted_at IS NULL
	`, pq.Array(uniqueIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *GroupStore) GetAccountCount(ctx context.Context, groupID int64) (total int64, active int64, err error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	var rateLimited int64
	err = postgresinfra.ScanSingleRow(ctx, sqlq,
		fmt.Sprintf(`SELECT
			COUNT(*) FILTER (WHERE a.deleted_at IS NULL),
			COUNT(*) FILTER (WHERE %s),
			COUNT(*) FILTER (WHERE %s)
		FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = $1`, groupAccountAvailableSQL, groupAccountTemporarilyLimitedSQL),
		[]any{groupID}, &total, &active, &rateLimited)
	return
}

func (r *GroupStore) DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	sqlq := r.sqlExecutorFromContext(ctx)

	res, err := r.options.Accounts(sqlq).Clear(ctx, groupID)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	if err := r.options.Enqueue(ctx, sqlq, &groupID); err != nil {
		logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue group account clear failed: group=%d err=%v", groupID, err)
	}
	return affected, nil
}

func (r *GroupStore) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	g, err := r.client.Group.Query().Where(group.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, routing.ErrGroupNotFound, nil)
	}
	_ = GroupFromEnt(g)

	// 使用 ent 事务统一包裹：避免手工基于 *sql.Tx 构造 ent client 带来的驱动断言问题，
	// 同时保证级联删除的原子性。
	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return nil, err
	}
	exec := r.client
	txClient := r.client
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		exec = tx.Client()
		txClient = exec
	}
	// err 为 dbent.ErrTxStarted 时，复用当前 client 参与同一事务。

	// 锁定分组行，避免级联删除期间出现并发写入。
	// 这里使用 exec.QueryContext 手动扫描，确保同一事务内加锁并能区分"未找到"与其他错误。
	rows, err := exec.QueryContext(ctx, "SELECT id FROM groups WHERE id = $1 AND deleted_at IS NULL FOR UPDATE", id)
	if err != nil {
		return nil, err
	}
	var lockedID int64
	if rows.Next() {
		if err := rows.Scan(&lockedID); err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if lockedID == 0 {
		return nil, routing.ErrGroupNotFound
	}

	var affectedUserIDs []int64

	if err := r.options.Users(exec).Delete(ctx, id); err != nil {
		return nil, err
	}
	if _, err := r.options.Accounts(exec).Clear(ctx, id); err != nil {
		return nil, err
	}

	// 5. 软删除分组自身。
	if _, err := txClient.Group.Delete().Where(group.IDEQ(id)).Exec(ctx); err != nil {
		return nil, err
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	if err := r.options.Enqueue(ctx, r.sql, &id); err != nil {
		logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue group cascade delete failed: group=%d err=%v", id, err)
	}

	return affectedUserIDs, nil
}

type groupAccountCounts struct {
	Total       int64
	Active      int64
	RateLimited int64
}

const (
	// 分组页的"可用"账号数必须与账号仓储的 ListSchedulableByGroupID 过滤口径一致。
	groupAccountAvailableSQL = `a.deleted_at IS NULL
				AND a.status = 'active'
				AND a.schedulable = true
				AND (a.expires_at IS NULL OR a.expires_at > NOW() OR a.auto_pause_on_expired = FALSE)
				AND (a.rate_limit_reset_at IS NULL OR a.rate_limit_reset_at <= NOW())
				AND (a.overload_until IS NULL OR a.overload_until <= NOW())
				AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW())`

	// 这里沿用历史字段名 RateLimitedAccountCount，但统计的是会让账号暂时退出调度的时间窗口。
	groupAccountTemporarilyLimitedSQL = `a.deleted_at IS NULL
				AND a.status = 'active'
				AND a.schedulable = true
				AND (a.expires_at IS NULL OR a.expires_at > NOW() OR a.auto_pause_on_expired = FALSE)
				AND (
					a.rate_limit_reset_at > NOW() OR
					a.overload_until > NOW() OR
					a.temp_unschedulable_until > NOW()
				)`
)

func (r *GroupStore) loadAccountCounts(ctx context.Context, groupIDs []int64) (counts map[int64]groupAccountCounts, err error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	counts = make(map[int64]groupAccountCounts, len(groupIDs))
	if len(groupIDs) == 0 {
		return counts, nil
	}

	rows, err := sqlq.QueryContext(
		ctx,
		fmt.Sprintf(`SELECT ag.group_id,
			COUNT(*) FILTER (WHERE a.deleted_at IS NULL) AS total,
			COUNT(*) FILTER (WHERE %s) AS active,
			COUNT(*) FILTER (WHERE %s) AS rate_limited
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = ANY($1)
		GROUP BY ag.group_id`, groupAccountAvailableSQL, groupAccountTemporarilyLimitedSQL),
		pq.Array(groupIDs),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			counts = nil
		}
	}()

	for rows.Next() {
		var groupID int64
		var c groupAccountCounts
		if err = rows.Scan(&groupID, &c.Total, &c.Active, &c.RateLimited); err != nil {
			return nil, err
		}
		counts[groupID] = c
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return counts, nil
}

// GetAccountIDsByGroupIDs 获取多个分组的所有账号 ID（去重）
func (r *GroupStore) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	if len(groupIDs) == 0 {
		return nil, nil
	}

	rows, err := sqlq.QueryContext(
		ctx,
		"SELECT DISTINCT account_id FROM account_groups WHERE group_id = ANY($1) ORDER BY account_id",
		pq.Array(groupIDs),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var accountIDs []int64
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return accountIDs, nil
}

// BindAccountsToGroup 将多个账号绑定到指定分组（批量插入，忽略已存在的绑定）
func (r *GroupStore) BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error {
	sqlq := r.sqlExecutorFromContext(ctx)
	if len(accountIDs) == 0 {
		return nil
	}

	// 使用 INSERT ... ON CONFLICT DO NOTHING 忽略已存在的绑定
	err := r.options.Accounts(sqlq).Bind(ctx, groupID, accountIDs)
	if err != nil {
		return err
	}

	// 发送调度器事件
	if err := r.options.Enqueue(ctx, sqlq, &groupID); err != nil {
		logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue bind accounts to group failed: group=%d err=%v", groupID, err)
	}

	return nil
}

// UpdateSortOrders 批量更新分组排序
func (r *GroupStore) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	sqlq := r.sqlExecutorFromContext(ctx)
	if len(updates) == 0 {
		return nil
	}

	// 去重后保留最后一次排序值，避免重复 ID 造成 CASE 分支冲突。
	sortOrderByID := make(map[int64]int, len(updates))
	groupIDs := make([]int64, 0, len(updates))
	for _, u := range updates {
		if u.ID <= 0 {
			continue
		}
		if _, exists := sortOrderByID[u.ID]; !exists {
			groupIDs = append(groupIDs, u.ID)
		}
		sortOrderByID[u.ID] = u.SortOrder
	}
	if len(groupIDs) == 0 {
		return nil
	}

	// 与旧实现保持一致：任何不存在/已删除的分组都返回 not found，且不执行更新。
	var existingCount int
	if err := postgresinfra.ScanSingleRow(
		ctx,
		sqlq,
		`SELECT COUNT(*) FROM groups WHERE deleted_at IS NULL AND id = ANY($1)`,
		[]any{pq.Array(groupIDs)},
		&existingCount,
	); err != nil {
		return err
	}
	if existingCount != len(groupIDs) {
		return routing.ErrGroupNotFound
	}

	args := make([]any, 0, len(groupIDs)*2+1)
	caseClauses := make([]string, 0, len(groupIDs))
	placeholder := 1
	for _, id := range groupIDs {
		caseClauses = append(caseClauses, fmt.Sprintf("WHEN $%d THEN $%d", placeholder, placeholder+1))
		args = append(args, id, sortOrderByID[id])
		placeholder += 2
	}
	args = append(args, pq.Array(groupIDs))

	query := fmt.Sprintf(`
		UPDATE groups
		SET sort_order = CASE id
			%s
			ELSE sort_order
		END
		WHERE deleted_at IS NULL AND id = ANY($%d)
	`, strings.Join(caseClauses, "\n\t\t\t"), placeholder)

	result, err := sqlq.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != int64(len(groupIDs)) {
		return routing.ErrGroupNotFound
	}

	for _, id := range groupIDs {
		if err := r.options.Enqueue(ctx, sqlq, &id); err != nil {
			logger.LegacyPrintf("repository.group", "[SchedulerOutbox] enqueue group sort update failed: group=%d err=%v", id, err)
		}
	}
	return nil
}
