// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/lib/pq"
)

// batchLoadAccountStatsPricingRules 批量加载多个共享价格配置的账号统计定价规则（含模型定价）
func (r *PricingConfigStore) batchLoadAccountStatsPricingRules(ctx context.Context, pricingConfigIDs []int64) (map[int64][]routing.AccountStatsPricingRule, error) {
	// 1. 查询规则
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_config_id, name, group_ids, account_ids, sort_order, created_at, updated_at
		 FROM pricing_config_account_stats_pricing_rules WHERE pricing_config_id = ANY($1) ORDER BY pricing_config_id, sort_order, id`,
		pq.Array(pricingConfigIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load account stats pricing rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var allRules []routing.AccountStatsPricingRule
	var ruleIDs []int64
	for rows.Next() {
		var rule routing.AccountStatsPricingRule
		if err := rows.Scan(
			&rule.ID, &rule.PricingConfigID, &rule.Name,
			pq.Array(&rule.GroupIDs), pq.Array(&rule.AccountIDs),
			&rule.SortOrder, &rule.CreatedAt, &rule.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan account stats pricing rule: %w", err)
		}
		ruleIDs = append(ruleIDs, rule.ID)
		allRules = append(allRules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account stats pricing rules: %w", err)
	}

	// 2. 批量加载规则的模型定价
	pricingMap, err := r.batchLoadAccountStatsModelPricing(ctx, ruleIDs)
	if err != nil {
		return nil, err
	}

	// 3. 按 pricingConfigID 分组并关联定价
	result := make(map[int64][]routing.AccountStatsPricingRule, len(pricingConfigIDs))
	for i := range allRules {
		allRules[i].Pricing = pricingMap[allRules[i].ID]
		result[allRules[i].PricingConfigID] = append(result[allRules[i].PricingConfigID], allRules[i])
	}

	return result, nil
}

// batchLoadAccountStatsModelPricing 批量加载规则的模型定价
func (r *PricingConfigStore) batchLoadAccountStatsModelPricing(ctx context.Context, ruleIDs []int64) (map[int64][]routing.ModelPricingEntry, error) {
	if len(ruleIDs) == 0 {
		return make(map[int64][]routing.ModelPricingEntry), nil
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, rule_id, platform, models, billing_mode, price_multiplier, input_price, output_price,
		        cache_write_price, cache_write_1h_price, cache_read_price, image_output_price, per_request_price, created_at, updated_at
		 FROM pricing_config_account_stats_model_pricing WHERE rule_id = ANY($1) ORDER BY rule_id, id`,
		pq.Array(ruleIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load account stats model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	pricingMap := make(map[int64][]routing.ModelPricingEntry, len(ruleIDs))
	for rows.Next() {
		var p routing.ModelPricingEntry
		var ruleID int64
		var modelsJSON []byte
		if err := rows.Scan(
			&p.ID, &ruleID, &p.Platform, &modelsJSON, &p.BillingMode, &p.PriceMultiplier,
			&p.InputPrice, &p.OutputPrice, &p.CacheWritePrice, &p.CacheWrite1hPrice, &p.CacheReadPrice,
			&p.ImageOutputPrice, &p.PerRequestPrice, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan account stats model pricing: %w", err)
		}
		if err := json.Unmarshal(modelsJSON, &p.Models); err != nil {
			p.Models = []string{}
		}
		pricingMap[ruleID] = append(pricingMap[ruleID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account stats model pricing: %w", err)
	}

	// Load intervals for all pricing entries.
	var allPricingIDs []int64
	for _, pricings := range pricingMap {
		for _, p := range pricings {
			allPricingIDs = append(allPricingIDs, p.ID)
		}
	}
	if len(allPricingIDs) > 0 {
		intervalsMap, err := r.batchLoadAccountStatsIntervals(ctx, allPricingIDs)
		if err != nil {
			return nil, err
		}
		for ruleID, pricings := range pricingMap {
			for i := range pricings {
				pricings[i].Intervals = intervalsMap[pricings[i].ID]
			}
			pricingMap[ruleID] = pricings
		}
	}

	return pricingMap, nil
}

// loadAccountStatsPricingRules 加载单个共享价格配置的账号统计定价规则（供 GetByID 使用）
func (r *PricingConfigStore) loadAccountStatsPricingRules(ctx context.Context, pricingConfigID int64) ([]routing.AccountStatsPricingRule, error) {
	result, err := r.batchLoadAccountStatsPricingRules(ctx, []int64{pricingConfigID})
	if err != nil {
		return nil, err
	}
	return result[pricingConfigID], nil
}

// replaceAccountStatsPricingRulesTx 在事务中替换共享价格配置的账号统计定价规则（删除旧的 + 插入新的）
func replaceAccountStatsPricingRulesTx(ctx context.Context, tx *sql.Tx, pricingConfigID int64, rules []routing.AccountStatsPricingRule) error {
	// CASCADE 会自动删除关联的 model_pricing
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pricing_config_account_stats_pricing_rules WHERE pricing_config_id = $1`, pricingConfigID,
	); err != nil {
		return fmt.Errorf("delete old account stats pricing rules: %w", err)
	}

	for i := range rules {
		rules[i].PricingConfigID = pricingConfigID
		if err := createAccountStatsPricingRuleTx(ctx, tx, &rules[i]); err != nil {
			return fmt.Errorf("insert account stats pricing rule: %w", err)
		}
	}
	return nil
}

// createAccountStatsPricingRuleTx 在事务中创建单条账号统计定价规则及其模型定价
func createAccountStatsPricingRuleTx(ctx context.Context, tx *sql.Tx, rule *routing.AccountStatsPricingRule) error {
	err := tx.QueryRowContext(ctx,
		`INSERT INTO pricing_config_account_stats_pricing_rules (pricing_config_id, name, group_ids, account_ids, sort_order)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at, updated_at`,
		rule.PricingConfigID, rule.Name, pq.Array(rule.GroupIDs), pq.Array(rule.AccountIDs), rule.SortOrder,
	).Scan(&rule.ID, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert account stats pricing rule: %w", err)
	}

	for j := range rule.Pricing {
		if err := createAccountStatsModelPricingTx(ctx, tx, rule.ID, &rule.Pricing[j]); err != nil {
			return err
		}
	}
	return nil
}

// createAccountStatsModelPricingTx 在事务中创建单条账号统计模型定价
func createAccountStatsModelPricingTx(ctx context.Context, tx *sql.Tx, ruleID int64, pricing *routing.ModelPricingEntry) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = routing.BillingModeToken
	}
	platform := pricing.Platform
	err = tx.QueryRowContext(ctx,
		`INSERT INTO pricing_config_account_stats_model_pricing (rule_id, platform, models, billing_mode, price_multiplier, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, image_output_price, per_request_price)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id, created_at, updated_at`,
		ruleID, platform, modelsJSON, billingMode,
		pricing.PriceMultiplier, pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheWrite1hPrice, pricing.CacheReadPrice,
		pricing.ImageOutputPrice, pricing.PerRequestPrice,
	).Scan(&pricing.ID, &pricing.CreatedAt, &pricing.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert account stats model pricing: %w", err)
	}
	// Persist intervals (mirrors pricing_config_pricing_intervals logic).
	for i := range pricing.Intervals {
		iv := &pricing.Intervals[i]
		iv.PricingID = pricing.ID
		if err := createAccountStatsIntervalTx(ctx, tx, iv); err != nil {
			return err
		}
	}
	return nil
}

// createAccountStatsIntervalTx inserts a single interval for an account stats pricing entry.
func createAccountStatsIntervalTx(ctx context.Context, tx *sql.Tx, iv *routing.PricingInterval) error {
	return tx.QueryRowContext(ctx,
		`INSERT INTO pricing_config_account_stats_pricing_intervals
		 (pricing_id, min_tokens, max_tokens, tier_label, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, per_request_price, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id, created_at, updated_at`,
		iv.PricingID, iv.MinTokens, iv.MaxTokens, iv.TierLabel,
		iv.InputPrice, iv.OutputPrice, iv.CacheWritePrice, iv.CacheWrite1hPrice, iv.CacheReadPrice,
		iv.PerRequestPrice, iv.SortOrder,
	).Scan(&iv.ID, &iv.CreatedAt, &iv.UpdatedAt)
}

// batchLoadAccountStatsIntervals loads intervals for account stats pricing entries.
func (r *PricingConfigStore) batchLoadAccountStatsIntervals(ctx context.Context, pricingIDs []int64) (map[int64][]routing.PricingInterval, error) {
	if len(pricingIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_id, min_tokens, max_tokens, tier_label,
		        input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price,
		        per_request_price, sort_order, created_at, updated_at
		 FROM pricing_config_account_stats_pricing_intervals
		 WHERE pricing_id = ANY($1) ORDER BY pricing_id, sort_order, id`,
		pq.Array(pricingIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load account stats pricing intervals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]routing.PricingInterval)
	for rows.Next() {
		var iv routing.PricingInterval
		if err := rows.Scan(
			&iv.ID, &iv.PricingID, &iv.MinTokens, &iv.MaxTokens, &iv.TierLabel,
			&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheWrite1hPrice, &iv.CacheReadPrice,
			&iv.PerRequestPrice, &iv.SortOrder, &iv.CreatedAt, &iv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan account stats pricing interval: %w", err)
		}
		result[iv.PricingID] = append(result[iv.PricingID], iv)
	}
	return result, rows.Err()
}
