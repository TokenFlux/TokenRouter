package maintenance

import (
	"context"
	"strconv"
	"sync"
	"time"

	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	systemOperationLockScope = "admin.system.operations.global_lock"
	systemOperationLockKey   = "global-system-operation-lock"
)

var (
	ErrSystemOperationBusy = infraerrors.Conflict("SYSTEM_OPERATION_BUSY", "another system operation is in progress")
)

type SystemOperationLock struct {
	recordID    int64
	ownership   string
	cancel      context.CancelFunc
	ctx         context.Context
	done        chan struct{}
	releaseOnce sync.Once
	releaseErr  error
	operationID string

	stopOnce sync.Once
	stopCh   chan struct{}
}

func (l *SystemOperationLock) OperationID() string {
	if l == nil {
		return ""
	}
	return l.operationID
}

type SystemOperationLockService struct {
	log func(string, string, ...any)

	repo idempotency.OperationLeaseStore

	lease         time.Duration
	renewInterval time.Duration
	ttl           time.Duration
}

func NewSystemOperationLockService(repo idempotency.OperationLeaseStore, cfg Options) *SystemOperationLockService {
	lease := cfg.ProcessingTimeout
	if lease <= 0 {
		lease = 30 * time.Second
	}
	renewInterval := lease / 3
	if renewInterval < time.Second {
		renewInterval = time.Second
	}
	ttl := cfg.SystemOperationTTL
	if ttl <= 0 {
		ttl = time.Hour
	}

	if cfg.Log == nil {
		cfg.Log = func(string, string, ...any) {}
	}
	return &SystemOperationLockService{log: cfg.Log,
		repo:          repo,
		lease:         lease,
		renewInterval: renewInterval,
		ttl:           ttl,
	}
}

func (s *SystemOperationLockService) Acquire(ctx context.Context, operationID string) (*SystemOperationLock, error) {
	if s == nil || s.repo == nil {
		return nil, ErrIdempotencyStoreUnavail
	}
	if operationID == "" {
		return nil, infraerrors.BadRequest("SYSTEM_OPERATION_ID_REQUIRED", "operation id is required")
	}

	now := time.Now()
	expiresAt := now.Add(s.ttl)
	lockedUntil := now.Add(s.lease)
	keyHash := idempotency.HashIdempotencyKey(systemOperationLockKey)

	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]string{"owner_token": hex.EncodeToString(token)})
	ownership := string(metadata)
	record, owner, err := s.repo.ClaimOperation(ctx, idempotency.OperationClaim{Scope: systemOperationLockScope, KeyHash: keyHash, OperationID: operationID, Ownership: ownership, Now: now, LockedUntil: lockedUntil, ExpiresAt: expiresAt})
	if err != nil {
		return nil, ErrIdempotencyStoreUnavail.WithCause(err)
	}
	if !owner {
		if record == nil {
			return nil, ErrIdempotencyStoreUnavail
		}
		return nil, s.busyError(record.RequestFingerprint, record.LockedUntil, now)
	}

	if record.ID == 0 {
		return nil, ErrIdempotencyStoreUnavail
	}

	operationCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	lock := &SystemOperationLock{
		ownership: ownership, ctx: operationCtx, cancel: cancel, done: make(chan struct{}),
		recordID:    record.ID,
		operationID: operationID,
		stopCh:      make(chan struct{}),
	}
	go s.renewLoop(lock)

	return lock, nil
}

func (s *SystemOperationLockService) Release(ctx context.Context, lock *SystemOperationLock, succeeded bool, failureReason string) error {
	if s == nil || s.repo == nil || lock == nil {
		return nil
	}

	lock.releaseOnce.Do(func() {
		lock.stopOnce.Do(func() { close(lock.stopCh); lock.cancel() })
		<-lock.done
		if ctx == nil {
			ctx = context.Background()
		}
		if failureReason == "" {
			failureReason = "SYSTEM_OPERATION_FAILED"
		}
		ok, err := s.repo.FinishOperation(ctx, lock.recordID, lock.operationID, lock.ownership, succeeded, failureReason, time.Now().Add(s.ttl))
		if err != nil {
			lock.releaseErr = err
		} else if !ok {
			lock.releaseErr = ErrOperationOwnershipLost
		}
	})
	return lock.releaseErr
}

func (s *SystemOperationLockService) renewLoop(lock *SystemOperationLock) {
	defer close(lock.done)
	ticker := time.NewTicker(s.renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			ctx, cancel := context.WithTimeout(lock.ctx, 2*time.Second)
			ok, err := s.repo.RenewOperation(
				ctx,
				lock.recordID,
				lock.operationID,
				lock.ownership,
				now.Add(s.lease),
				now.Add(s.ttl),
			)
			cancel()
			if err != nil {
				s.log("service.system_operation_lock", "[SystemOperationLock] renew failed operation_id=%s err=%v", lock.operationID, err)
				// 瞬时故障不应导致续租协程退出，下一轮继续尝试续租。
				continue
			}
			if !ok {
				lock.cancel()
				s.log("service.system_operation_lock", "[SystemOperationLock] renew stopped operation_id=%s reason=ownership_lost", lock.operationID)
				return
			}
		case <-lock.stopCh:
			return
		}
	}
}

func (s *SystemOperationLockService) busyError(operationID string, lockedUntil *time.Time, now time.Time) error {
	metadata := make(map[string]string)
	if operationID != "" {
		metadata["operation_id"] = operationID
	}
	if lockedUntil != nil {
		sec := int(lockedUntil.Sub(now).Seconds())
		if sec <= 0 {
			sec = 1
		}
		metadata["retry_after"] = strconv.Itoa(sec)
	}
	if len(metadata) == 0 {
		return ErrSystemOperationBusy
	}
	return ErrSystemOperationBusy.WithMetadata(metadata)
}

// Options 由装配投影原系统维护锁的时限。
type Options struct {
	Log                                   func(string, string, ...any)
	ProcessingTimeout, SystemOperationTTL time.Duration
}

var ErrIdempotencyStoreUnavail = idempotency.ErrIdempotencyStoreUnavail
var ErrOperationOwnershipLost = infraerrors.Conflict("SYSTEM_OPERATION_OWNERSHIP_LOST", "system operation ownership lost")

// Context 在确认丢失所有权时取消后续阶段，浏览器断开不取消已接受操作。
func (l *SystemOperationLock) Context() context.Context { return l.ctx }
