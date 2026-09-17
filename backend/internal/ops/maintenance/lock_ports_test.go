package maintenance

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type IdempotencyRecord = idempotency.IdempotencyRecord

const IdempotencyStatusProcessing = idempotency.IdempotencyStatusProcessing
const IdempotencyStatusSucceeded = idempotency.IdempotencyStatusSucceeded
const IdempotencyStatusFailedRetryable = idempotency.IdempotencyStatusFailedRetryable

func HashIdempotencyKey(s string) string { return idempotency.HashIdempotencyKey(s) }
func ptrTime(t time.Time) *time.Time     { return &t }
func errorCode(err error) int            { return int(apperror.FromError(err).Code) }
func errorReason(err error) string       { return apperror.FromError(err).Reason }
func closedTestChannel() chan struct{}   { ch := make(chan struct{}); close(ch); return ch }

// 维护锁测试替身与生产存储一样比较独立认领代次。
func (r *inMemoryIdempotencyRepo) ClaimOperation(ctx context.Context, c idempotency.OperationClaim) (*idempotency.IdempotencyRecord, bool, error) {
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
func (r *inMemoryIdempotencyRepo) RenewOperation(ctx context.Context, id int64, operation, ownership string, until, expires time.Time) (bool, error) {
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
func (r *inMemoryIdempotencyRepo) FinishOperation(ctx context.Context, id int64, operation, ownership string, success bool, reason string, expires time.Time) (bool, error) {
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

func (s *systemLockRepoStub) ClaimOperation(ctx context.Context, c idempotency.OperationClaim) (*idempotency.IdempotencyRecord, bool, error) {
	if s.createErr != nil {
		return nil, false, s.createErr
	}
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	if s.reclaimErr != nil {
		return nil, false, s.reclaimErr
	}
	if s.createOwner {
		return &idempotency.IdempotencyRecord{}, true, nil
	}
	return cloneRecord(s.existing), s.reclaimOK, nil
}
func (s *systemLockRepoStub) RenewOperation(context.Context, int64, string, string, time.Time, time.Time) (bool, error) {
	return true, nil
}
func (s *systemLockRepoStub) FinishOperation(ctx context.Context, id int64, op, owner string, success bool, reason string, expires time.Time) (bool, error) {
	if success {
		return true, s.markSuccErr
	}
	return true, s.markFailErr
}
