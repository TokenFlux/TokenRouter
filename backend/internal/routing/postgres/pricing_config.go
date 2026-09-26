// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/lib/pq"
)

type PricingConfigStore struct {
	db *sql.DB
}

// NewPricingConfigStore 创建价格配置数据访问实例
func NewPricingConfigStore(db *sql.DB) *PricingConfigStore {
	return &PricingConfigStore{db: db}
}

// runInTx 在事务中执行 fn，成功 commit，失败 rollback。
func (r *PricingConfigStore) runInTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PricingConfigStore) Create(ctx context.Context, pricingConfig *routing.PricingConfig) error {
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`INSERT INTO pricing_configs (name, description, status, billing_model_source) VALUES ($1, $2, $3, $4)
			 RETURNING id, created_at, updated_at`,
			pricingConfig.Name, pricingConfig.Description, pricingConfig.Status, pricingConfig.BillingModelSource,
		).Scan(&pricingConfig.ID, &pricingConfig.CreatedAt, &pricingConfig.UpdatedAt)
		if err != nil {
			if isUniqueViolation(err) {
				return routing.ErrPricingConfigExists
			}
			return fmt.Errorf("insert price configuration: %w", err)
		}

		// 设置分组关联
		if len(pricingConfig.GroupIDs) > 0 {
			if err := setGroupIDsTx(ctx, tx, pricingConfig.ID, pricingConfig.GroupIDs); err != nil {
				return err
			}
		}

		// 设置模型定价
		if len(pricingConfig.ModelPricing) > 0 {
			if err := replaceModelPricingTx(ctx, tx, pricingConfig.ID, pricingConfig.ModelPricing); err != nil {
				return err
			}
		}

		// 设置账号统计定价规则
		if len(pricingConfig.AccountStatsPricingRules) > 0 {
			if err := replaceAccountStatsPricingRulesTx(ctx, tx, pricingConfig.ID, pricingConfig.AccountStatsPricingRules); err != nil {
				return err
			}
		}

		return nil
	})
}

func (r *PricingConfigStore) GetByID(ctx context.Context, id int64) (*routing.PricingConfig, error) {
	ch := &routing.PricingConfig{}

	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, description, status, billing_model_source, created_at, updated_at
		 FROM pricing_configs WHERE id = $1`, id,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Status, &ch.BillingModelSource, &ch.CreatedAt, &ch.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, routing.ErrPricingConfigNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get price configuration: %w", err)
	}

	groupIDs, err := r.GetGroupIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	ch.GroupIDs = groupIDs

	pricing, err := r.ListModelPricing(ctx, id)
	if err != nil {
		return nil, err
	}
	ch.ModelPricing = pricing

	statsPricingRules, err := r.loadAccountStatsPricingRules(ctx, id)
	if err != nil {
		return nil, err
	}
	ch.AccountStatsPricingRules = statsPricingRules

	return ch, nil
}

func (r *PricingConfigStore) Update(ctx context.Context, pricingConfig *routing.PricingConfig) error {
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE pricing_configs SET name = $1, description = $2, status = $3, billing_model_source = $4, updated_at = NOW()
			 WHERE id = $5`,
			pricingConfig.Name, pricingConfig.Description, pricingConfig.Status, pricingConfig.BillingModelSource, pricingConfig.ID,
		)
		if err != nil {
			if isUniqueViolation(err) {
				return routing.ErrPricingConfigExists
			}
			return fmt.Errorf("update price configuration: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return routing.ErrPricingConfigNotFound
		}

		// 更新分组关联
		if pricingConfig.GroupIDs != nil {
			if err := setGroupIDsTx(ctx, tx, pricingConfig.ID, pricingConfig.GroupIDs); err != nil {
				return err
			}
		}

		// 更新模型定价
		if pricingConfig.ModelPricing != nil {
			if err := replaceModelPricingTx(ctx, tx, pricingConfig.ID, pricingConfig.ModelPricing); err != nil {
				return err
			}
		}

		// 更新账号统计定价规则
		if pricingConfig.AccountStatsPricingRules != nil {
			if err := replaceAccountStatsPricingRulesTx(ctx, tx, pricingConfig.ID, pricingConfig.AccountStatsPricingRules); err != nil {
				return err
			}
		}

		return nil
	})
}

func (r *PricingConfigStore) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM pricing_configs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete price configuration: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return routing.ErrPricingConfigNotFound
	}
	return nil
}

func (r *PricingConfigStore) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]routing.PricingConfig, *pagination.PaginationResult, error) {
	where := []string{"1=1"}
	args := []any{}
	argIdx := 1

	if status != "" {
		where = append(where, fmt.Sprintf("c.status = $%d", argIdx))
		args = append(args, status)
		argIdx++
	}
	if search != "" {
		where = append(where, fmt.Sprintf("(c.name ILIKE $%d OR c.description ILIKE $%d)", argIdx, argIdx))
		args = append(args, "%"+escapeLike(search)+"%")
		argIdx++
	}

	whereClause := strings.Join(where, " AND ")

	// 计数
	var total int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM pricing_configs c WHERE %s", whereClause)
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count price configurations: %w", err)
	}

	pageSize := params.Limit() // 约束在 [1, 100]
	page := params.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// 查询 pricingConfig 列表
	dataQuery := fmt.Sprintf(
		`SELECT c.id, c.name, c.description, c.status, c.billing_model_source, c.created_at, c.updated_at
		 FROM pricing_configs c WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		whereClause, pricingConfigListOrderBy(params), argIdx, argIdx+1,
	)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, dataQuery, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query price configurations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pricingConfigs []routing.PricingConfig
	var pricingConfigIDs []int64
	for rows.Next() {
		var ch routing.PricingConfig

		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Status, &ch.BillingModelSource, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan price configuration: %w", err)
		}

		pricingConfigs = append(pricingConfigs, ch)
		pricingConfigIDs = append(pricingConfigIDs, ch.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate price configurations: %w", err)
	}

	// 批量加载分组 ID 和模型定价（避免 N+1）
	if len(pricingConfigIDs) > 0 {
		groupMap, err := r.batchLoadGroupIDs(ctx, pricingConfigIDs)
		if err != nil {
			return nil, nil, err
		}
		pricingMap, err := r.batchLoadModelPricing(ctx, pricingConfigIDs)
		if err != nil {
			return nil, nil, err
		}
		statsRulesMap, err := r.batchLoadAccountStatsPricingRules(ctx, pricingConfigIDs)
		if err != nil {
			return nil, nil, err
		}
		for i := range pricingConfigs {
			pricingConfigs[i].GroupIDs = groupMap[pricingConfigs[i].ID]
			pricingConfigs[i].ModelPricing = pricingMap[pricingConfigs[i].ID]
			pricingConfigs[i].AccountStatsPricingRules = statsRulesMap[pricingConfigs[i].ID]
		}
	}

	pages := 0
	if total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}

	paginationResult := &pagination.PaginationResult{
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	}

	return pricingConfigs, paginationResult, nil
}

func pricingConfigListOrderBy(params pagination.PaginationParams) string {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := strings.ToUpper(params.NormalizedSortOrder(pagination.SortOrderAsc))

	var column string
	switch sortBy {
	case "":
		column = "c.id"
		sortOrder = "ASC"
	case "id":
		column = "c.id"
	case "name":
		column = "c.name"
	case "status":
		column = "c.status"
	case "created_at":
		column = "c.created_at"
	default:
		column = "c.id"
		sortOrder = "ASC"
	}

	return fmt.Sprintf("%s %s, c.id %s", column, sortOrder, sortOrder)
}

func (r *PricingConfigStore) ListAll(ctx context.Context) ([]routing.PricingConfig, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, description, status, billing_model_source, created_at, updated_at FROM pricing_configs ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query all price configurations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pricingConfigs []routing.PricingConfig
	var pricingConfigIDs []int64
	for rows.Next() {
		var ch routing.PricingConfig

		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Status, &ch.BillingModelSource, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan price configuration: %w", err)
		}

		pricingConfigs = append(pricingConfigs, ch)
		pricingConfigIDs = append(pricingConfigIDs, ch.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate price configurations: %w", err)
	}

	if len(pricingConfigIDs) == 0 {
		return pricingConfigs, nil
	}

	// 批量加载分组 ID
	groupMap, err := r.batchLoadGroupIDs(ctx, pricingConfigIDs)
	if err != nil {
		return nil, err
	}

	// 批量加载模型定价
	pricingMap, err := r.batchLoadModelPricing(ctx, pricingConfigIDs)
	if err != nil {
		return nil, err
	}

	// 批量加载账号统计定价规则
	statsRulesMap, err := r.batchLoadAccountStatsPricingRules(ctx, pricingConfigIDs)
	if err != nil {
		return nil, err
	}

	for i := range pricingConfigs {
		pricingConfigs[i].GroupIDs = groupMap[pricingConfigs[i].ID]
		pricingConfigs[i].ModelPricing = pricingMap[pricingConfigs[i].ID]
		pricingConfigs[i].AccountStatsPricingRules = statsRulesMap[pricingConfigs[i].ID]
	}

	return pricingConfigs, nil
}

// batchLoadGroupIDs 批量加载多个价格配置的分组 ID
func (r *PricingConfigStore) batchLoadGroupIDs(ctx context.Context, pricingConfigIDs []int64) (map[int64][]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT pricing_config_id, group_id FROM pricing_config_groups
		 WHERE pricing_config_id = ANY($1) ORDER BY pricing_config_id, group_id`,
		pq.Array(pricingConfigIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load group ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	groupMap := make(map[int64][]int64, len(pricingConfigIDs))
	for rows.Next() {
		var pricingConfigID, groupID int64
		if err := rows.Scan(&pricingConfigID, &groupID); err != nil {
			return nil, fmt.Errorf("scan group id: %w", err)
		}
		groupMap[pricingConfigID] = append(groupMap[pricingConfigID], groupID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group ids: %w", err)
	}
	return groupMap, nil
}

func (r *PricingConfigStore) ExistsByName(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pricing_configs WHERE name = $1)`, name,
	).Scan(&exists)
	return exists, err
}

func (r *PricingConfigStore) ExistsByNameExcluding(ctx context.Context, name string, excludeID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pricing_configs WHERE name = $1 AND id != $2)`, name, excludeID,
	).Scan(&exists)
	return exists, err
}

func (r *PricingConfigStore) GetGroupIDs(ctx context.Context, pricingConfigID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT group_id FROM pricing_config_groups WHERE pricing_config_id = $1 ORDER BY group_id`, pricingConfigID,
	)
	if err != nil {
		return nil, fmt.Errorf("get group ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan group id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group ids: %w", err)
	}
	return ids, nil
}

func (r *PricingConfigStore) SetGroupIDs(ctx context.Context, pricingConfigID int64, groupIDs []int64) error {
	return setGroupIDsTx(ctx, r.db, pricingConfigID, groupIDs)
}

func (r *PricingConfigStore) GetPricingConfigIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	var pricingConfigID int64
	err := r.db.QueryRowContext(ctx,
		`SELECT pricing_config_id FROM pricing_config_groups WHERE group_id = $1`, groupID,
	).Scan(&pricingConfigID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return pricingConfigID, err
}

func (r *PricingConfigStore) GetGroupsInOtherPricingConfigs(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT group_id FROM pricing_config_groups WHERE group_id = ANY($1) AND pricing_config_id != $2`,
		pq.Array(groupIDs), pricingConfigID,
	)
	if err != nil {
		return nil, fmt.Errorf("get groups in other price configurations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var conflicting []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan conflicting group id: %w", err)
		}
		conflicting = append(conflicting, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conflicting group ids: %w", err)
	}
	return conflicting, nil
}

// GetGroupPlatforms 批量查询分组 ID 对应的平台
func (r *PricingConfigStore) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	if len(groupIDs) == 0 {
		return make(map[int64]string), nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, platform FROM groups WHERE id = ANY($1)`,
		pq.Array(groupIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("get group platforms: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	result := make(map[int64]string, len(groupIDs))
	for rows.Next() {
		var id int64
		var platform string
		if err := rows.Scan(&id, &platform); err != nil {
			return nil, fmt.Errorf("scan group platform: %w", err)
		}
		result[id] = platform
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group platforms: %w", err)
	}
	return result, nil
}
