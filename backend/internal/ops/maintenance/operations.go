package maintenance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type RestartRequester interface{ RequestRestart() error }
type UpdateAPI interface {
	CheckUpdate(context.Context, bool) (*ops.UpdateInfo, error)
	PerformUpdate(context.Context) error
	Rollback() error
	ListRollbackVersions(context.Context) ([]ops.RollbackVersion, error)
	RollbackToVersion(context.Context, string) error
}

// Operations 拥有已接受维护操作的锁、取消和关闭等待。
type Operations struct {
	update    UpdateAPI
	lock      *SystemOperationLockService
	restarter RestartRequester
	mu        sync.Mutex
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stopOnce  sync.Once
	done      chan struct{}
	stopErr   error
}

// @project-doc docs/operations/deployment_and_migrations.md#maintenance_execution
func NewOperations(update UpdateAPI, lock *SystemOperationLockService, restart RestartRequester) *Operations {
	ctx, cancel := context.WithCancel(context.Background())
	return &Operations{update: update, lock: lock, restarter: restart, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
func (s *Operations) BeginStop() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock() }
func (s *Operations) StopContext(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.BeginStop()
		go func() {
			finished := make(chan struct{})
			go func() { s.wg.Wait(); close(finished) }()
			select {
			case <-finished:
			case <-ctx.Done():
				s.stopErr = fmt.Errorf("maintenance operations unfinished: %w", ctx.Err())
			}
			close(s.done)
		}()
	})
	<-s.done
	return s.stopErr
}
func (s *Operations) workContext(ctx context.Context) (context.Context, context.CancelFunc) {
	work, cancel := context.WithTimeout(ctx, 15*time.Minute)
	stop := context.AfterFunc(s.ctx, cancel)
	return work, func() { stop(); cancel() }
}
func (s *Operations) acquire(ctx context.Context, id string) (*SystemOperationLock, func(string, bool), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, apperror.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}
	s.wg.Add(1)
	s.mu.Unlock()
	if s.lock == nil {
		s.wg.Done()
		return nil, nil, ErrIdempotencyStoreUnavail
	}
	acquisition, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	lock, err := s.lock.Acquire(acquisition, id)
	stop()
	cancel()
	if err != nil {
		s.wg.Done()
		return nil, nil, err
	}
	stopWork := context.AfterFunc(s.ctx, lock.cancel)
	var once sync.Once
	return lock, func(reason string, success bool) {
		once.Do(func() {
			defer s.wg.Done()
			stopWork()
			release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.lock.Release(release, lock, success, reason)
		})
	}, nil
}
func (s *Operations) Update(ctx context.Context, operationID string) (any, error) {
	lock, release, err := s.acquire(ctx, operationID)
	if err != nil {
		return nil, err
	}
	var releaseReason string
	succeeded := false
	defer func() {
		release(releaseReason, succeeded)
	}()

	updateCtx, cancel := s.workContext(lock.Context())
	defer cancel()

	if err := s.update.PerformUpdate(updateCtx); err != nil {
		if errors.Is(err, ErrNoUpdateAvailable) {
			info, checkErr := s.update.CheckUpdate(updateCtx, false)
			if checkErr != nil {
				releaseReason = "SYSTEM_UPDATE_FAILED"
				return nil, checkErr
			}
			succeeded = true
			return map[string]any{
				"message":            "Already up to date",
				"already_up_to_date": true,
				"current_version":    info.CurrentVersion,
				"latest_version":     info.LatestVersion,
				"operation_id":       lock.OperationID(),
			}, nil
		}
		releaseReason = "SYSTEM_UPDATE_FAILED"
		return nil, err
	}
	succeeded = true

	return map[string]any{
		"message":      "Update completed. Please restart the service.",
		"need_restart": true,
		"operation_id": lock.OperationID(),
	}, nil
}
func (s *Operations) Rollback(ctx context.Context, operationID, targetVersion string) (any, error) {
	lock, release, err := s.acquire(ctx, operationID)
	if err != nil {
		return nil, err
	}
	var releaseReason string
	succeeded := false
	defer func() {
		release(releaseReason, succeeded)
	}()

	if err := lock.Context().Err(); err != nil {
		return nil, err
	}
	if targetVersion != "" {
		// 指定版本回退同样要下载完整二进制，与更新一样和请求生命周期解耦。
		rollbackCtx, cancel := s.workContext(lock.Context())
		defer cancel()
		err = s.update.RollbackToVersion(rollbackCtx, targetVersion)
	} else {
		err = s.update.Rollback()
	}
	if err != nil {
		releaseReason = "SYSTEM_ROLLBACK_FAILED"
		return nil, err
	}
	succeeded = true

	return map[string]any{
		"message":      "Rollback completed. Please restart the service.",
		"need_restart": true,
		"version":      targetVersion,
		"operation_id": lock.OperationID(),
	}, nil
}
func (s *Operations) Restart(ctx context.Context, operationID string) (any, error) {
	lock, release, err := s.acquire(ctx, operationID)
	if err != nil {
		return nil, err
	}
	succeeded := false
	defer func() {
		release("", succeeded)
	}()

	if err := lock.Context().Err(); err != nil {
		return nil, err
	}
	if s.restarter != nil {
		if err := s.restarter.RequestRestart(); err != nil {
			return nil, err
		}
	}

	succeeded = true
	return map[string]any{
		"message":      "Service restart initiated",
		"operation_id": lock.OperationID(),
	}, nil
}
