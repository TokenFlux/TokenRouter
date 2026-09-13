package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

const opsCleanupBatchTimeout = 15 * time.Second
const opsCleanupDefaultBatchSize = ops.OpsCleanupDefaultBatchSize
const opsCleanupDefaultBatchPause = ops.OpsCleanupDefaultBatchPause

func opsCleanupRunOne(
	ctx context.Context,
	db *sql.DB,
	truncate bool,
	cutoff time.Time,
	table, timeCol string,
	castDate bool,
	batchSize int,
	batchPause time.Duration,
) (ops.CleanupTargetResult, error) {
	if truncate {
		deleted, err := truncateOpsTable(ctx, db, table)
		result := ops.CleanupTargetResult{Deleted: deleted}
		if deleted > 0 {
			result.Batches = 1
		}
		return result, err
	}
	return deleteOldRowsByCTID(ctx, db, table, timeCol, cutoff, batchSize, batchPause, castDate)
}

// deleteOldRowsByCTID 按时间顺序选取物理行并分批删除，避免为每批旧数据额外按主键排序。
func deleteOldRowsByCTID(
	ctx context.Context,
	db *sql.DB,
	table string,
	timeColumn string,
	cutoff time.Time,
	batchSize int,
	batchPause time.Duration,
	castCutoffToDate bool,
) (ops.CleanupTargetResult, error) {
	result := ops.CleanupTargetResult{}
	if db == nil {
		return result, nil
	}
	if batchSize <= 0 {
		batchSize = opsCleanupDefaultBatchSize
	}
	if batchPause <= 0 {
		batchPause = opsCleanupDefaultBatchPause
	}

	where := fmt.Sprintf("%s < $1", timeColumn)
	if castCutoffToDate {
		where = fmt.Sprintf("%s < $1::date", timeColumn)
	}

	q := fmt.Sprintf(`
WITH batch AS MATERIALIZED (
	  SELECT ctid AS row_ctid FROM %s
	  WHERE %s
	  ORDER BY %s ASC, id ASC
	  LIMIT $2
)
DELETE FROM %s AS target
USING batch
WHERE target.ctid = batch.row_ctid
`, table, where, timeColumn, table)

	for {
		batchCtx, cancel := context.WithTimeout(ctx, opsCleanupBatchTimeout)
		res, err := db.ExecContext(batchCtx, q, cutoff, batchSize)
		cancel()
		if err != nil {
			if isMissingRelationError(err) {
				return result, nil
			}
			return result, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return result, err
		}
		result.Deleted += affected
		if affected == 0 {
			break
		}
		result.Batches++
		if affected < int64(batchSize) {
			break
		}
		pauseStarted := time.Now()
		if err := sleepOpsCleanupWithContext(ctx, batchPause); err != nil {
			return result, err
		}
		result.Throttled += time.Since(pauseStarted)
	}
	return result, nil
}

// sleepOpsCleanupWithContext 在节流等待期间响应任务取消，避免停机时无条件阻塞。
func sleepOpsCleanupWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncateOpsTable 用 TRUNCATE TABLE 清空指定表，并从统计信息读取近似行数用于 heartbeat。
// 这里不能执行 COUNT(*)，否则大表会在清空前再次触发全表扫描和文件页缓存压力。
func truncateOpsTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	if db == nil {
		return 0, nil
	}

	truncateCtx, cancel := context.WithTimeout(ctx, opsCleanupBatchTimeout)
	defer cancel()

	var estimatedRows int64
	const estimateQuery = `
		SELECT COALESCE((
			SELECT GREATEST(reltuples, 0)::bigint
			FROM pg_class
			WHERE oid = to_regclass($1)
		), 0)`
	if err := db.QueryRowContext(truncateCtx, estimateQuery, table).Scan(&estimatedRows); err != nil {
		return 0, fmt.Errorf("estimate %s rows: %w", table, err)
	}
	if _, err := db.ExecContext(truncateCtx, fmt.Sprintf("TRUNCATE TABLE %s", table)); err != nil {
		if isMissingRelationError(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("truncate %s: %w", table, err)
	}
	return estimatedRows, nil
}
func isMissingRelationError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "does not exist") && strings.Contains(s, "relation")
}

// CleanupStore 保留每批事务与节流边界，复用原底层连接池。
type CleanupStore struct {
	*Advisory
	db *sql.DB
}

func NewCleanupStore(db *sql.DB) ops.CleanupBackend {
	if db == nil {
		return nil
	}
	return &CleanupStore{&Advisory{db}, db}
}
func (s *CleanupStore) AcquireMaintenance(ctx context.Context) (func(), bool, error) {
	return infra.TryAcquireDBAdvisoryLockWithError(ctx, s.db, infra.HashAdvisoryLockID("maintenance:database-heavy"))
}
func (s *CleanupStore) RunTarget(ctx context.Context, truncate bool, cutoff time.Time, table, col string, cast bool, batch int, pause time.Duration) (ops.CleanupTargetResult, error) {
	return opsCleanupRunOne(ctx, s.db, truncate, cutoff, table, col, cast, batch, pause)
}
