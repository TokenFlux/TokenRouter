package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/lib/pq"
)

type groupAvailabilityProbeRepository struct {
	db *sql.DB
}

func NewGroupAvailabilityProbeRepository(db *sql.DB) service.GroupAvailabilityProbeRepository {
	return &groupAvailabilityProbeRepository{db: db}
}

func (r *groupAvailabilityProbeRepository) ClaimDue(ctx context.Context, now time.Time, lockUntil time.Time, lockedBy string, limit int) ([]service.GroupAvailabilityProbeDueGroup, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 先将启用探测的分组补进状态表；分组被禁用时再清掉状态，避免长期扫描无效记录。
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_availability_probe_states (group_id, next_run_at, created_at, updated_at)
		SELECT id, $1, NOW(), NOW()
		FROM groups
		WHERE deleted_at IS NULL
		  AND status = 'active'
		  AND availability_probe_config @> '{"enabled": true}'::jsonb
		ON CONFLICT (group_id) DO NOTHING
	`, now); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM group_availability_probe_states s
		WHERE NOT EXISTS (
			SELECT 1
			FROM groups g
			WHERE g.id = s.group_id
			  AND g.deleted_at IS NULL
			  AND g.status = 'active'
			  AND g.availability_probe_config @> '{"enabled": true}'::jsonb
		)
	`); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		WITH due AS (
			SELECT s.group_id
			FROM group_availability_probe_states s
			JOIN groups g ON g.id = s.group_id
			WHERE g.deleted_at IS NULL
			  AND g.status = 'active'
			  AND g.availability_probe_config @> '{"enabled": true}'::jsonb
			  AND (s.next_run_at IS NULL OR s.next_run_at <= $1)
			  AND (s.locked_until IS NULL OR s.locked_until <= $1)
			ORDER BY COALESCE(s.next_run_at, 'epoch'::timestamptz), s.group_id
			LIMIT $4
			FOR UPDATE OF s SKIP LOCKED
		),
		claimed AS (
			UPDATE group_availability_probe_states s
			SET locked_until = $2,
				locked_by = $3,
				updated_at = NOW()
			FROM due
			WHERE s.group_id = due.group_id
			RETURNING s.group_id
		)
		SELECT g.id, g.name, g.platform, g.availability_probe_config
		FROM claimed c
		JOIN groups g ON g.id = c.group_id
		ORDER BY g.id
	`, now, lockUntil, lockedBy, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	dueGroups := make([]service.GroupAvailabilityProbeDueGroup, 0)
	for rows.Next() {
		var item service.GroupAvailabilityProbeDueGroup
		var rawConfig []byte
		if err := rows.Scan(&item.GroupID, &item.Name, &item.Platform, &rawConfig); err != nil {
			return nil, err
		}
		if len(rawConfig) > 0 {
			if err := json.Unmarshal(rawConfig, &item.Config); err != nil {
				return nil, fmt.Errorf("decode availability probe config: %w", err)
			}
		}
		dueGroups = append(dueGroups, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return dueGroups, nil
}

func (r *groupAvailabilityProbeRepository) SaveResultAndScheduleNext(ctx context.Context, result *service.GroupAvailabilityProbeResult, nextRunAt time.Time) error {
	if r == nil || r.db == nil || result == nil {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_availability_probe_results (
			group_id, account_id, model_id, status, success, latency_ms,
			error_message, started_at, finished_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
	`, result.GroupID, result.AccountID, result.ModelID, result.Status, result.Success, result.LatencyMs, nullableProbeString(result.ErrorMessage), result.StartedAt, result.FinishedAt); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_availability_probe_states (
			group_id, next_run_at, locked_until, locked_by, last_status,
			last_success, last_latency_ms, last_error, last_checked_at, created_at, updated_at
		)
		VALUES ($1, $2, NULL, NULL, $3, $4, $5, $6, $7, NOW(), NOW())
		ON CONFLICT (group_id) DO UPDATE SET
			next_run_at = EXCLUDED.next_run_at,
			locked_until = NULL,
			locked_by = NULL,
			last_status = EXCLUDED.last_status,
			last_success = EXCLUDED.last_success,
			last_latency_ms = EXCLUDED.last_latency_ms,
			last_error = EXCLUDED.last_error,
			last_checked_at = EXCLUDED.last_checked_at,
			updated_at = NOW()
	`, result.GroupID, nextRunAt, result.Status, result.Success, result.LatencyMs, nullableProbeString(result.ErrorMessage), result.FinishedAt); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *groupAvailabilityProbeRepository) GetSummaryByGroupIDs(ctx context.Context, groupIDs []int64, days int, bucketMinutes int, timezoneName string, now time.Time) (map[int64]*service.GroupAvailabilitySummary, error) {
	out := make(map[int64]*service.GroupAvailabilitySummary, len(groupIDs))
	if r == nil || r.db == nil || len(groupIDs) == 0 {
		return out, nil
	}
	days, bucketMinutes = service.NormalizeMarketplaceAvailabilityWindow(days, bucketMinutes)

	loc := time.UTC
	if timezoneName != "" {
		if parsed, err := time.LoadLocation(timezoneName); err == nil && parsed != nil {
			loc = parsed
		}
	}
	localNow := now.In(loc)
	bucketDuration := time.Duration(bucketMinutes) * time.Minute
	endLocal := nextLocalBucketBoundary(localNow, bucketDuration)
	totalMinutes := days * 24 * 60
	bucketCount := (totalMinutes + bucketMinutes - 1) / bucketMinutes
	if bucketCount <= 0 {
		bucketCount = 1
	}
	startLocal := endLocal.Add(-time.Duration(bucketCount) * bucketDuration)
	startUTC := startLocal.UTC()
	endUTC := endLocal.UTC()

	for _, groupID := range groupIDs {
		summary := &service.GroupAvailabilitySummary{
			WindowDays:    days,
			BucketMinutes: bucketMinutes,
			Days:          make([]service.GroupAvailabilityBucket, 0, bucketCount),
		}
		for i := 0; i < bucketCount; i++ {
			bucketStart := startLocal.Add(time.Duration(i) * bucketDuration)
			summary.Days = append(summary.Days, service.GroupAvailabilityBucket{
				Date: bucketStart.Format(time.RFC3339),
			})
		}
		out[groupID] = summary
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			group_id,
			FLOOR((EXTRACT(EPOCH FROM started_at) - EXTRACT(EPOCH FROM $2::timestamptz)) / ($4::double precision * 60))::int AS bucket_index,
			COUNT(*) FILTER (WHERE success = true) AS success_count,
			COUNT(*) AS total_count
		FROM group_availability_probe_results
		WHERE group_id = ANY($1)
		  AND started_at >= $2
		  AND started_at < $3
		GROUP BY group_id, bucket_index
	`, pq.Array(groupIDs), startUTC, endUTC, bucketMinutes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var groupID int64
		var bucketIndex int
		var successCount int64
		var totalCount int64
		if err := rows.Scan(&groupID, &bucketIndex, &successCount, &totalCount); err != nil {
			return nil, err
		}
		summary, ok := out[groupID]
		if !ok {
			continue
		}
		if bucketIndex < 0 || bucketIndex >= len(summary.Days) {
			continue
		}
		rate := availabilityRate(successCount, totalCount)
		summary.Days[bucketIndex].SuccessCount = successCount
		summary.Days[bucketIndex].TotalCount = totalCount
		summary.Days[bucketIndex].AvailabilityRate = rate
		summary.SuccessCount += successCount
		summary.TotalCount += totalCount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, summary := range out {
		summary.AvailabilityRate = availabilityRate(summary.SuccessCount, summary.TotalCount)
	}

	lastRows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (group_id)
			group_id, status, finished_at
		FROM group_availability_probe_results
		WHERE group_id = ANY($1)
		ORDER BY group_id, started_at DESC, id DESC
	`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = lastRows.Close() }()

	for lastRows.Next() {
		var groupID int64
		var status string
		var checkedAt time.Time
		if err := lastRows.Scan(&groupID, &status, &checkedAt); err != nil {
			return nil, err
		}
		if summary, ok := out[groupID]; ok {
			summary.LastStatus = status
			t := checkedAt
			summary.LastCheckedAt = &t
		}
	}
	if err := lastRows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func nextLocalBucketBoundary(t time.Time, bucketDuration time.Duration) time.Time {
	if bucketDuration <= 0 {
		bucketDuration = 24 * time.Hour
	}
	// 以本地自然日零点对齐，避免非 UTC 时区下使用 time.Truncate 产生偏移桶。
	dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	bucketStart := dayStart.Add(t.Sub(dayStart) / bucketDuration * bucketDuration)
	if bucketStart.Before(t) {
		return bucketStart.Add(bucketDuration)
	}
	return bucketStart
}

func (r *groupAvailabilityProbeRepository) CleanupOldResults(ctx context.Context, before time.Time) error {
	if r == nil || r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM group_availability_probe_results
		WHERE created_at < $1
	`, before)
	return err
}

func availabilityRate(successCount int64, totalCount int64) *float64 {
	if totalCount <= 0 {
		return nil
	}
	value := float64(successCount) / float64(totalCount)
	return &value
}

func nullableProbeString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
