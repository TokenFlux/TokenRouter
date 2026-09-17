package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	pg "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// ClaimOperation 将认领与代次替换放在同一原子语句中，兼容没有所有者元数据的旧记录。
func (r *idempotencyRepository) ClaimOperation(ctx context.Context, c idempotency.OperationClaim) (*idempotency.IdempotencyRecord, bool, error) {
	var id int64
	err := pg.ScanSingleRow(ctx, r.sql, `INSERT INTO idempotency_records(scope,idempotency_key_hash,request_fingerprint,status,response_body,locked_until,expires_at)
 VALUES($1,$2,$3,'processing',$4,$5,$6)
 ON CONFLICT(scope,idempotency_key_hash) DO UPDATE SET request_fingerprint=EXCLUDED.request_fingerprint,status='processing',response_body=EXCLUDED.response_body,response_status=NULL,error_reason=NULL,locked_until=EXCLUDED.locked_until,expires_at=EXCLUDED.expires_at,updated_at=NOW()
 WHERE idempotency_records.locked_until IS NULL OR idempotency_records.locked_until <= $7 RETURNING id`, []any{c.Scope, c.KeyHash, c.OperationID, c.Ownership, c.LockedUntil, c.ExpiresAt, c.Now}, &id)
	if errors.Is(err, sql.ErrNoRows) {
		record, e := r.GetByScopeAndKeyHash(ctx, c.Scope, c.KeyHash)
		return record, false, e
	}
	if err != nil {
		return nil, false, err
	}
	return &idempotency.IdempotencyRecord{
		ID:                 id,
		Scope:              c.Scope,
		IdempotencyKeyHash: c.KeyHash,
		RequestFingerprint: c.OperationID,
		Status:             idempotency.IdempotencyStatusProcessing,
		ResponseBody:       &c.Ownership,
		LockedUntil:        &c.LockedUntil,
		ExpiresAt:          c.ExpiresAt,
	}, true, nil
}
func (r *idempotencyRepository) RenewOperation(ctx context.Context, id int64, operationID, ownership string, until, expires time.Time) (bool, error) {
	result, err := r.sql.ExecContext(ctx, `UPDATE idempotency_records SET locked_until=$4,expires_at=$5,updated_at=NOW() WHERE id=$1 AND request_fingerprint=$2 AND response_body=$3 AND status='processing'`, id, operationID, ownership, until, expires)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (r *idempotencyRepository) FinishOperation(ctx context.Context, id int64, operationID, ownership string, success bool, reason string, expires time.Time) (bool, error) {
	status := idempotency.IdempotencyStatusFailedRetryable
	var code any
	var body any
	var locked any = time.Now()
	if success {
		status = idempotency.IdempotencyStatusSucceeded
		code = 200
		encoded, _ := json.Marshal(map[string]any{"operation_id": operationID, "released": true})
		body = string(encoded)
		locked = nil
		reason = ""
	}
	result, err := r.sql.ExecContext(ctx, `UPDATE idempotency_records SET status=$4,response_status=$5,response_body=$6,error_reason=NULLIF($7,''),locked_until=$8,expires_at=$9,updated_at=NOW() WHERE id=$1 AND request_fingerprint=$2 AND response_body=$3 AND status='processing'`, id, operationID, ownership, status, code, body, reason, locked, expires)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// NewOperationLeaseStore 仅暴露维护专用的所有者比较能力。
func NewOperationLeaseStore(executor Executor) idempotency.OperationLeaseStore {
	return &idempotencyRepository{sql: executor}
}
