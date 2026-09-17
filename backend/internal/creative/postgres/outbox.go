package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
)

// creativeRunOutboxRepository 使用 PostgreSQL 原生 SQL 实现可恢复的创作台后台动作。
type creativeRunOutboxRepository struct {
	db *sql.DB
}

// NewCreativeRunOutboxRepository 创建创作台 outbox 仓储。
func NewCreativeRunOutboxRepository(db *sql.DB) service.CreativeRunOutboxRepository {
	return &creativeRunOutboxRepository{db: db}
}

func (r *creativeRunOutboxRepository) Ensure(ctx context.Context, runID string, operation service.CreativeRunOutboxOperation, availableAt time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("creative run outbox database is not configured")
	}
	if strings.TrimSpace(runID) == "" || operation == "" {
		return errors.New("creative run outbox run and operation are required")
	}
	if availableAt.IsZero() {
		availableAt = time.Now()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO creative_run_outbox (run_id, operation, status, available_at)
		VALUES ($1, $2, 'pending', $3)
		ON CONFLICT (run_id, operation) DO UPDATE
		SET status = CASE WHEN creative_run_outbox.status IN ('done', 'cancelled', 'leased') THEN creative_run_outbox.status ELSE 'pending' END,
			available_at = CASE WHEN creative_run_outbox.status IN ('done', 'cancelled', 'leased') THEN creative_run_outbox.available_at ELSE EXCLUDED.available_at END,
			lease_token = CASE WHEN creative_run_outbox.status = 'leased' THEN creative_run_outbox.lease_token ELSE NULL END,
			lease_until = CASE WHEN creative_run_outbox.status = 'leased' THEN creative_run_outbox.lease_until ELSE NULL END,
			updated_at = NOW()
	`, runID, string(operation), availableAt)
	return err
}

func (r *creativeRunOutboxRepository) Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]service.CreativeRunOutbox, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("creative run outbox database is not configured")
	}
	if limit <= 0 {
		limit = 50
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	token, err := newCreativeOutboxLeaseToken(workerID)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id
			FROM creative_run_outbox
			WHERE available_at <= NOW()
			  AND (status = 'pending' OR (status = 'leased' AND lease_until < NOW()))
			ORDER BY id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE creative_run_outbox AS o
		SET status = 'leased',
			lease_token = $1,
			lease_until = NOW() + ($3 * INTERVAL '1 second'),
			attempt_count = o.attempt_count + 1,
			updated_at = NOW()
		FROM candidates AS c
		WHERE o.id = c.id
		RETURNING o.id, o.run_id, o.operation, o.status, o.available_at,
			o.lease_token, o.lease_until, o.attempt_count, COALESCE(o.last_error, ''),
			o.created_at, o.updated_at
	`, token, limit, leaseSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.CreativeRunOutbox, 0, limit)
	for rows.Next() {
		var event service.CreativeRunOutbox
		var operation, status string
		if err := rows.Scan(
			&event.ID, &event.RunID, &operation, &status, &event.AvailableAt,
			&event.LeaseToken, &event.LeaseUntil, &event.AttemptCount, &event.LastError,
			&event.CreatedAt, &event.UpdatedAt,
		); err != nil {
			return nil, err
		}
		event.Operation = service.CreativeRunOutboxOperation(operation)
		event.Status = service.CreativeRunOutboxStatus(status)
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *creativeRunOutboxRepository) Complete(ctx context.Context, id int64, leaseToken string) error {
	return r.updateClaimed(ctx, id, leaseToken, `
		SET status = 'done', lease_token = NULL, lease_until = NULL, last_error = NULL, updated_at = NOW()
	`)
}

func (r *creativeRunOutboxRepository) Retry(ctx context.Context, id int64, leaseToken string, availableAt time.Time, lastError string) error {
	if availableAt.IsZero() {
		availableAt = time.Now().Add(time.Minute)
	}
	return r.updateClaimed(ctx, id, leaseToken, `
		SET status = 'pending', available_at = $3, lease_token = NULL, lease_until = NULL,
			last_error = $4, updated_at = NOW()
	`, availableAt, strings.TrimSpace(lastError))
}

func (r *creativeRunOutboxRepository) Cancel(ctx context.Context, id int64, leaseToken string) error {
	return r.updateClaimed(ctx, id, leaseToken, `
		SET status = 'cancelled', lease_token = NULL, lease_until = NULL, updated_at = NOW()
	`)
}

func (r *creativeRunOutboxRepository) updateClaimed(ctx context.Context, id int64, leaseToken, setClause string, extra ...any) error {
	if r == nil || r.db == nil {
		return errors.New("creative run outbox database is not configured")
	}
	if id <= 0 || strings.TrimSpace(leaseToken) == "" {
		return errors.New("creative run outbox claim is invalid")
	}
	args := []any{id, leaseToken}
	args = append(args, extra...)
	query := "UPDATE creative_run_outbox " + setClause + " WHERE id = $1 AND lease_token = $2 AND status = 'leased'"
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("creative run outbox claim %d is no longer owned", id)
	}
	return nil
}

func newCreativeOutboxLeaseToken(workerID string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		workerID = "creative"
	}
	return workerID + ":" + hex.EncodeToString(raw[:]), nil
}

var _ service.CreativeRunOutboxRepository = (*creativeRunOutboxRepository)(nil)
