package idempotency

import (
	"context"
	"time"
)

// OperationClaim 是系统维护专用认领，不改变请求幂等协调器的语义。
type OperationClaim struct {
	Scope, KeyHash, OperationID, Ownership string
	Now, LockedUntil, ExpiresAt            time.Time
}

// OperationLeaseStore 的变更均比较认领身份，旧持有者不能改写新认领。
type OperationLeaseStore interface {
	ClaimOperation(context.Context, OperationClaim) (*IdempotencyRecord, bool, error)
	RenewOperation(context.Context, int64, string, string, time.Time, time.Time) (bool, error)
	FinishOperation(context.Context, int64, string, string, bool, string, time.Time) (bool, error)
}
