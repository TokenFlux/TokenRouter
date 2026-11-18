package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// pricing_config_models.go reads and writes model-price rows, their intervals,
// and pricing group associations.
// A model-price row owns its context intervals, whose foreign key needs the
// price ID returned by insertion. Writers therefore create the parent before
// assigning that ID to interval rows. Whole-list replacements delete prior
// parents first, relying on foreign-key cascades for their intervals and on the
// caller's transaction to roll back a later failure.
//
// The same helpers accept either a connection pool or a transaction.
// Pool-backed calls can leave completed writes when later child insertion
// fails, while transactional aggregate saves commit them together. Read
// decoding treats malformed model-name JSON as an empty match list, rejects
// malformed time-pricing JSON, and maps an absent FastMultiplier to the stored
// legacy fast-mode pointer.

// dbExec supplies database operations on either a pool or caller-owned
// transaction.
type dbExec interface {
	// ExecContext executes a statement and returns its affected-row result on the
	// selected executor. Transaction completion belongs to its owner; a pool
	// executor commits each statement independently.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	// QueryContext opens result rows using the selected database connection or
	// transaction. The reader closes the returned rows, checks iteration errors,
	// and leaves transaction completion to the owner.
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	// QueryRowContext prepares a single-row result for the caller to scan. Query
	// errors are reported by Scan, and using a transaction executor leaves commit
	// or rollback with its owner.
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ListModelPricing reads a configuration's price rows in ID order and attaches
// their intervals in stored sort order. Missing configurations or entries yield
// no rows, while interval-loading failures discard the assembled prices.
func (r *PricingConfigStore) ListModelPricing(ctx context.Context, pricingConfigID int64) ([]routing.ModelPricingEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_config_id, models, billing_mode, price_multiplier, fast_mode_multiplier, fast_multiplier, flex_multiplier, max_reasoning_effort_multiplier, input_price, output_price, cache_write_price, cache_write_1h_price, cache_write_30m_price, cache_write_12h_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, reference_model, created_at, updated_at
		 FROM pricing_config_model_pricing WHERE pricing_config_id = $1 ORDER BY id`, pricingConfigID,
	)
	if err != nil {
		return nil, fmt.Errorf("list model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result, pricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}

	if len(pricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, pricingIDs)
		if err != nil {
			return nil, err
		}
		for i := range result {
			result[i].Intervals = intervalMap[result[i].ID]
		}
	}

	return result, nil
}

// CreateModelPricing inserts a nonnil price and then its intervals through the
// connection pool. It assigns generated IDs and timestamps to the input; a
// child failure can leave the parent and earlier intervals committed.
func (r *PricingConfigStore) CreateModelPricing(ctx context.Context, pricing *routing.ModelPricingEntry) error {
	return createModelPricingExec(ctx, r.db, pricing)
}

// UpdateModelPricing replaces a nonnil entry's model names and price columns,
// defaulting an empty billing mode to token. Existing intervals remain
// unchanged; no affected row returns a missing-entry error, and input
// timestamps are not refreshed.
func (r *PricingConfigStore) UpdateModelPricing(ctx context.Context, pricing *routing.ModelPricingEntry) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = routing.BillingModeToken
	}
	timePricingJSON, err := marshalTimePricingConfig(pricing.TimePricing)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE pricing_config_model_pricing
			 SET models = $1, billing_mode = $2, price_multiplier = $3, fast_mode_multiplier = $4, fast_multiplier = $5, flex_multiplier = $6, max_reasoning_effort_multiplier = $7, input_price = $8, output_price = $9, cache_write_price = $10, cache_write_1h_price = $11, cache_write_30m_price = $12, cache_write_12h_price = $13, cache_read_price = $14, image_input_price = $15, image_output_price = $16, per_request_price = $17, time_pricing = $18, reference_model = $20, updated_at = NOW()
			 WHERE id = $19`,
		modelsJSON, billingMode, pricing.PriceMultiplier, pricing.FastModeMultiplier, pricing.FastMultiplier, pricing.FlexMultiplier, pricing.MaxReasoningEffortMultiplier,
		pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheWrite1hPrice, pricing.CacheWrite30mPrice, pricing.CacheWrite12hPrice, pricing.CacheReadPrice,
		pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice, timePricingJSON, pricing.ID, pricing.ReferenceModel,
	)
	if err != nil {
		return fmt.Errorf("update model pricing: %w", err)
	}
	// Use the affected count to detect a missing price entry. Any RowsAffected
	// error is discarded, so the check reports missing when the supplied count
	// is zero.
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("pricing entry not found: %d", pricing.ID)
	}
	return nil
}

// DeleteModelPricing deletes the price row and lets its foreign key cascade
// interval removal. An absent ID succeeds because the affected-row count is
// ignored; SQL execution failures are returned.
func (r *PricingConfigStore) DeleteModelPricing(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM pricing_config_model_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete model pricing: %w", err)
	}
	return nil
}

// ReplaceModelPricing replaces all prices for a configuration inside a new
// transaction. Empty input clears the list; helpers assign new parent and
// interval IDs into the input slice before commit, and failures before commit
// return through rollback.
func (r *PricingConfigStore) ReplaceModelPricing(ctx context.Context, pricingConfigID int64, pricingList []routing.ModelPricingEntry) error {
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		return replaceModelPricingTx(ctx, tx, pricingConfigID, pricingList)
	})
}

// batchLoadModelPricing reads prices for the requested configurations and
// attaches their intervals. Entries retain configuration and ID order,
// configurations without prices have no map entry, and any read failure
// discards the map.
func (r *PricingConfigStore) batchLoadModelPricing(ctx context.Context, pricingConfigIDs []int64) (map[int64][]routing.ModelPricingEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_config_id, models, billing_mode, price_multiplier, fast_mode_multiplier, fast_multiplier, flex_multiplier, max_reasoning_effort_multiplier, input_price, output_price, cache_write_price, cache_write_1h_price, cache_write_30m_price, cache_write_12h_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, reference_model, created_at, updated_at
		 FROM pricing_config_model_pricing WHERE pricing_config_id = ANY($1) ORDER BY pricing_config_id, id`,
		pq.Array(pricingConfigIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	allPricing, allPricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}

	pricingMap := make(map[int64][]routing.ModelPricingEntry, len(pricingConfigIDs))
	for _, p := range allPricing {
		pricingMap[p.PricingConfigID] = append(pricingMap[p.PricingConfigID], p)
	}

	if len(allPricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, allPricingIDs)
		if err != nil {
			return nil, err
		}
		for chID := range pricingMap {
			for i := range pricingMap[chID] {
				pricingMap[chID][i].Intervals = intervalMap[pricingMap[chID][i].ID]
			}
		}
	}

	return pricingMap, nil
}

// batchLoadIntervals groups context intervals by price ID in sort-order and ID
// order. Prices without intervals have no map entry, and query, scan, or
// iteration errors return no usable interval map.
func (r *PricingConfigStore) batchLoadIntervals(ctx context.Context, pricingIDs []int64) (map[int64][]routing.PricingInterval, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_id, min_tokens, max_tokens, tier_label,
		        input_price, output_price, cache_write_price, cache_write_1h_price, cache_write_30m_price, cache_write_12h_price, cache_read_price,
		        input_multiplier, output_multiplier, cache_write_multiplier, cache_read_multiplier,
		        per_request_price, sort_order, created_at, updated_at
		 FROM pricing_config_pricing_intervals
		 WHERE pricing_id = ANY($1) ORDER BY pricing_id, sort_order, id`,
		pq.Array(pricingIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load intervals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	intervalMap := make(map[int64][]routing.PricingInterval, len(pricingIDs))
	for rows.Next() {
		var iv routing.PricingInterval
		if err := rows.Scan(
			&iv.ID, &iv.PricingID, &iv.MinTokens, &iv.MaxTokens, &iv.TierLabel,
			&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheWrite1hPrice, &iv.CacheWrite30mPrice, &iv.CacheWrite12hPrice, &iv.CacheReadPrice,
			&iv.InputMultiplier, &iv.OutputMultiplier, &iv.CacheWriteMultiplier, &iv.CacheReadMultiplier,
			&iv.PerRequestPrice, &iv.SortOrder, &iv.CreatedAt, &iv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan interval: %w", err)
		}
		intervalMap[iv.PricingID] = append(intervalMap[iv.PricingID], iv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate intervals: %w", err)
	}
	return intervalMap, nil
}

// scanModelPricingRows decodes price rows and returns their IDs for interval
// loading in the same row order. The caller owns rows and closes them. Scan and
// iteration failures discard all accumulated entries.
//
// Malformed model-name JSON becomes an empty match list so that row cannot add
// invalid names to pricing lookup. Malformed time-pricing JSON fails the read
// because its daily multiplier would otherwise be lost. An absent
// FastMultiplier takes the legacy FastModeMultiplier pointer, preserving
// optional price presence while sharing that pointer inside the returned entry.
func scanModelPricingRows(rows *sql.Rows) ([]routing.ModelPricingEntry, []int64, error) {
	var result []routing.ModelPricingEntry
	var pricingIDs []int64
	for rows.Next() {
		var p routing.ModelPricingEntry
		var modelsJSON []byte
		var timePricingJSON []byte
		if err := rows.Scan(
			&p.ID, &p.PricingConfigID, &modelsJSON, &p.BillingMode, &p.PriceMultiplier, &p.FastModeMultiplier, &p.FastMultiplier, &p.FlexMultiplier, &p.MaxReasoningEffortMultiplier,
			&p.InputPrice, &p.OutputPrice, &p.CacheWritePrice, &p.CacheWrite1hPrice, &p.CacheWrite30mPrice, &p.CacheWrite12hPrice, &p.CacheReadPrice,
			&p.ImageInputPrice, &p.ImageOutputPrice, &p.PerRequestPrice, &timePricingJSON, &p.ReferenceModel, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan model pricing: %w", err)
		}
		// A corrupt name list cannot define model matches; keep the price row with no
		// names so other stored columns remain readable.
		if err := json.Unmarshal(modelsJSON, &p.Models); err != nil {
			p.Models = []string{}
		}
		timePricing, err := unmarshalTimePricingConfig(timePricingJSON)
		if err != nil {
			return nil, nil, err
		}
		p.TimePricing = timePricing
		// Use the legacy fast-mode amount when the newer field is absent, preserving
		// existing Fast pricing and the distinction between nil and supplied values.
		if p.FastMultiplier == nil {
			p.FastMultiplier = p.FastModeMultiplier
		}
		pricingIDs = append(pricingIDs, p.ID)
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate model pricing: %w", err)
	}
	return result, pricingIDs, nil
}

// setGroupIDsTx replaces a configuration's complete group association set on
// exec. Empty input clears the set. The caller supplies a transaction when
// deletion and reinsertion need to commit together; a pool executes them as
// separate writes.
//
// Deletion precedes bulk insertion so groups previously owned by this
// configuration can be assigned again under the unique group constraint. Insert
// failures return after deletion has executed. A transaction owner rolls back
// that earlier deletion, whereas a pool-backed caller can be left with no
// associations.
func setGroupIDsTx(ctx context.Context, exec dbExec, pricingConfigID int64, groupIDs []int64) error {
	// Remove the previous association set before inserting replacements on the
	// same executor; atomic recovery depends on that executor being a transaction.
	if _, err := exec.ExecContext(ctx, `DELETE FROM pricing_config_groups WHERE pricing_config_id = $1`, pricingConfigID); err != nil {
		return fmt.Errorf("delete old group associations: %w", err)
	}
	if len(groupIDs) == 0 {
		return nil
	}
	_, err := exec.ExecContext(ctx,
		`INSERT INTO pricing_config_groups (pricing_config_id, group_id)
		 SELECT $1, unnest($2::bigint[])`,
		pricingConfigID, pq.Array(groupIDs),
	)
	if err != nil {
		return fmt.Errorf("insert group associations: %w", err)
	}
	return nil
}

// createModelPricingExec inserts a nonnil model-price entry, then its intervals
// using the generated price ID. Empty billing mode stores token, and nil or
// period-free time pricing stores SQL null. IDs and timestamps are assigned
// directly to the input.
//
// The parent insert runs before child insertion because every interval
// references its returned ID. A child failure stops further writes; the
// transaction owner can roll back all preceding writes, while a pool executor
// leaves successful statements committed. In-memory assignments also remain
// after an error, so callers reload persistent state before retrying those
// values.
func createModelPricingExec(ctx context.Context, exec dbExec, pricing *routing.ModelPricingEntry) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = routing.BillingModeToken
	}
	timePricingJSON, err := marshalTimePricingConfig(pricing.TimePricing)
	if err != nil {
		return err
	}
	err = exec.QueryRowContext(ctx,
		`INSERT INTO pricing_config_model_pricing (pricing_config_id, models, billing_mode, price_multiplier, fast_mode_multiplier, fast_multiplier, flex_multiplier, max_reasoning_effort_multiplier, input_price, output_price, cache_write_price, cache_write_1h_price, cache_write_30m_price, cache_write_12h_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, reference_model)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20) RETURNING id, created_at, updated_at`,
		pricing.PricingConfigID, modelsJSON, billingMode,
		pricing.PriceMultiplier, pricing.FastModeMultiplier, pricing.FastMultiplier, pricing.FlexMultiplier, pricing.MaxReasoningEffortMultiplier,
		pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheWrite1hPrice, pricing.CacheWrite30mPrice, pricing.CacheWrite12hPrice, pricing.CacheReadPrice,
		pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice, timePricingJSON, pricing.ReferenceModel,
	).Scan(&pricing.ID, &pricing.CreatedAt, &pricing.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert model pricing: %w", err)
	}

	// Use the newly generated parent ID for every interval; reusing an earlier
	// entry ID would associate its tier prices with the wrong row.
	for i := range pricing.Intervals {
		pricing.Intervals[i].PricingID = pricing.ID
		if err := createIntervalExec(ctx, exec, &pricing.Intervals[i]); err != nil {
			return err
		}
	}

	return nil
}

// marshalTimePricingConfig encodes a daily multiplier schedule as JSON text for
// PostgreSQL. Nil schedules and schedules with no periods become SQL null,
// regardless of any supplied timezone.
func marshalTimePricingConfig(config *routing.TimePricingConfig) (any, error) {
	if config == nil || len(config.Periods) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal time pricing: %w", err)
	}
	return string(data), nil
}

// unmarshalTimePricingConfig decodes a stored daily multiplier schedule,
// returning nil for absent bytes. Malformed JSON returns a wrapped error;
// present JSON null or an empty object produces a nonnil empty schedule.
func unmarshalTimePricingConfig(data []byte) (*routing.TimePricingConfig, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var config routing.TimePricingConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal time pricing: %w", err)
	}
	return &config, nil
}

// createIntervalExec inserts a nonnil interval using its existing PricingID and
// fills generated identity and timestamps. The caller creates its parent first
// and owns transaction completion or cleanup after insertion failure.
func createIntervalExec(ctx context.Context, exec dbExec, iv *routing.PricingInterval) error {
	return exec.QueryRowContext(ctx,
		`INSERT INTO pricing_config_pricing_intervals
		 (pricing_id, min_tokens, max_tokens, tier_label, input_price, output_price, cache_write_price, cache_write_1h_price, cache_write_30m_price, cache_write_12h_price, cache_read_price, input_multiplier, output_multiplier, cache_write_multiplier, cache_read_multiplier, per_request_price, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id, created_at, updated_at`,
		iv.PricingID, iv.MinTokens, iv.MaxTokens, iv.TierLabel,
		iv.InputPrice, iv.OutputPrice, iv.CacheWritePrice, iv.CacheWrite1hPrice, iv.CacheWrite30mPrice, iv.CacheWrite12hPrice, iv.CacheReadPrice,
		iv.InputMultiplier, iv.OutputMultiplier, iv.CacheWriteMultiplier, iv.CacheReadMultiplier,
		iv.PerRequestPrice, iv.SortOrder,
	).Scan(&iv.ID, &iv.CreatedAt, &iv.UpdatedAt)
}

// replaceModelPricingTx deletes all stored model-price parents for a
// configuration and inserts the supplied replacements. Empty input clears the
// list. The supplied configuration ID and generated price and interval IDs
// are written into the input slice.
//
// Foreign-key cascades remove the deleted parents' intervals before any
// replacement is inserted. This ordering lets one caller-owned transaction
// replace the entire price tree or roll it back after a later failure. The
// helper leaves commit to its caller; using a pool instead permits partial
// replacement, and rollback does not reverse in-memory ID assignments.
func replaceModelPricingTx(ctx context.Context, exec dbExec, pricingConfigID int64, pricingList []routing.ModelPricingEntry) error {
	// Deleting parents also removes their intervals through the foreign key, so
	// the replacement transaction rebuilds one complete price tree.
	if _, err := exec.ExecContext(ctx, `DELETE FROM pricing_config_model_pricing WHERE pricing_config_id = $1`, pricingConfigID); err != nil {
		return fmt.Errorf("delete old model pricing: %w", err)
	}
	for i := range pricingList {
		pricingList[i].PricingConfigID = pricingConfigID
		if err := createModelPricingExec(ctx, exec, &pricingList[i]); err != nil {
			return fmt.Errorf("insert model pricing: %w", err)
		}
	}
	return nil
}

// isUniqueViolation reports whether a direct or wrapped PostgreSQL error has
// code 23505. Nil, typed nil PostgreSQL pointers, unrelated errors, and other
// PostgreSQL codes return false.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr != nil {
		return pqErr.Code == "23505"
	}
	return false
}

// escapeLike quotes literal text for PostgreSQL LIKE or ILIKE patterns.
// Callers add intentional wildcards after conversion; List surrounds escaped
// search text with percent signs to select substring matches.
//
// Backslashes are doubled before percent signs and underscores are escaped.
// Reversing that order would also double the newly inserted escapes, changing
// which characters the pattern treats literally. Ordinary text and empty input
// pass through unchanged.
func escapeLike(s string) string {
	// For input 50%, List builds %50\%%: the surrounding percents select
	// substrings, and the escaped middle percent is literal. Escape existing
	// backslashes first so this inserted escape stays intact.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
