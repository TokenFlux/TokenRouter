package admin

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
)

// 维护锁测试替身与生产存储一样比较独立认领代次。
func (r *memoryIdempotencyRepoStub) ClaimOperation(ctx context.Context, c idempotency.OperationClaim) (*idempotency.IdempotencyRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := r.key(c.Scope, c.KeyHash)
	record := r.data[key]
	if record != nil && record.LockedUntil != nil && record.LockedUntil.After(c.Now) {
		copy := *record
		return &copy, false, nil
	}
	id := r.nextID
	if record != nil {
		id = record.ID
	} else {
		r.nextID++
	}
	record = &idempotency.IdempotencyRecord{ID: id, Scope: c.Scope, IdempotencyKeyHash: c.KeyHash, RequestFingerprint: c.OperationID, Status: idempotency.IdempotencyStatusProcessing, ResponseBody: &c.Ownership, LockedUntil: &c.LockedUntil, ExpiresAt: c.ExpiresAt}
	r.data[key] = record
	copy := *record
	return &copy, true, nil
}
func (r *memoryIdempotencyRepoStub) RenewOperation(ctx context.Context, id int64, operation, ownership string, until, expires time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.data {
		if v.ID == id && v.Status == idempotency.IdempotencyStatusProcessing && v.RequestFingerprint == operation && v.ResponseBody != nil && *v.ResponseBody == ownership {
			v.LockedUntil = &until
			v.ExpiresAt = expires
			return true, nil
		}
	}
	return false, nil
}
func (r *memoryIdempotencyRepoStub) FinishOperation(ctx context.Context, id int64, operation, ownership string, success bool, reason string, expires time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.data {
		if v.ID == id && v.Status == idempotency.IdempotencyStatusProcessing && v.RequestFingerprint == operation && v.ResponseBody != nil && *v.ResponseBody == ownership {
			v.Status = idempotency.IdempotencyStatusFailedRetryable
			if success {
				v.Status = idempotency.IdempotencyStatusSucceeded
			}
			v.LockedUntil = nil
			v.ExpiresAt = expires
			return true, nil
		}
	}
	return false, nil
}
