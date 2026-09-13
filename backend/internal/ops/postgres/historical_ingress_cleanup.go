package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/lib/pq"
)

// HistoricalIngressCleanup 只操作分析记录，保留原主键分页和截止过滤。
type HistoricalIngressCleanup struct{ db *sql.DB }

func NewHistoricalIngressCleanup(db *sql.DB) *HistoricalIngressCleanup {
	return &HistoricalIngressCleanup{db}
}
func (s *HistoricalIngressCleanup) ListCandidates(ctx context.Context, cursor int64, before time.Time, batchSize int) ([]ops.HistoricalIngressCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
			SELECT id, COALESCE(status_code, 0), COALESCE(error_message, ''), COALESCE(error_body, '')
			FROM ops_error_logs
			WHERE id > $1
			  AND created_at < $2
			  AND error_phase = 'auth'
			  AND account_id IS NULL
			  AND upstream_status_code IS NULL
			  AND COALESCE(upstream_error_message, '') = ''
			  AND COALESCE(upstream_error_detail, '') = ''
			ORDER BY id ASC
			LIMIT $3`, cursor, before, batchSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	batch := make([]ops.HistoricalIngressCandidate, 0, batchSize)
	for rows.Next() {
		var item ops.HistoricalIngressCandidate
		if err := rows.Scan(&item.ID, &item.StatusCode, &item.Message, &item.Body); err != nil {
			return nil, err
		}
		batch = append(batch, item)
	}
	return batch, rows.Err()
}
func (s *HistoricalIngressCleanup) DeleteCandidates(ctx context.Context, ids []int64, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM ops_error_logs WHERE id = ANY($1) AND created_at < $2`, pq.Array(ids), before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
