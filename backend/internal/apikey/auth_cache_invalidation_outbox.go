// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	KeyAuthInvalidationBatchSize    = 100
	KeyAuthInvalidationPollInterval = 500 * time.Millisecond
	KeyAuthInvalidationLease        = 30 * time.Second
	KeyAuthInvalidationRedisTimeout = 2 * time.Second
	KeyAuthInvalidationSafetyDelay  = 30 * time.Second
	KeyAuthInvalidationConcurrency  = 16
)

type AuthCacheInvalidationEvent struct {
	ID        int64
	CacheKey  string
	Attempts  int
	Stage     int
	CreatedAt time.Time
}

type AuthCacheInvalidationOutboxStats struct {
	Pending         int64
	OldestCreatedAt *time.Time
	MaxAttempts     int
	LastError       string
}

type AuthCacheInvalidationOutboxRepository interface {
	Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]AuthCacheInvalidationEvent, error)
	DeleteClaimed(ctx context.Context, id int64, workerID string) error
	ScheduleSecondPass(ctx context.Context, id int64, workerID string, availableAt time.Time) error
	RetryClaimed(ctx context.Context, id int64, workerID string, availableAt time.Time, lastError string) error
	Stats(ctx context.Context) (AuthCacheInvalidationOutboxStats, error)
}

type AuthCacheInvalidationHealth struct {
	Running    bool          `json:"running"`
	Processed  uint64        `json:"processed"`
	Failures   uint64        `json:"failures"`
	Pending    int64         `json:"pending"`
	OldestLag  time.Duration `json:"oldest_lag"`
	LastError  string        `json:"last_error,omitempty"`
	StatsError string        `json:"stats_error,omitempty"`
	// HealthySLA 包含延迟安全重试；RecoverySLA 是 Redis 恢复后含上限退避在内的最大收敛时间。
	HealthySLA  time.Duration `json:"healthy_sla"`
	RecoverySLA time.Duration `json:"recovery_sla"`
	MaxAttempts int           `json:"max_attempts"`
}

type OpsAuthCacheInvalidationHealth struct {
	Outbox       AuthCacheInvalidationHealth           `json:"outbox"`
	Subscriber   AuthCacheInvalidationSubscriberHealth `json:"subscriber"`
	Lookup       APIKeyAuthLookupMetrics               `json:"lookup"`
	InvalidAbuse InvalidAuthAbuseHealth                `json:"invalid_abuse"`
}

type AuthCacheInvalidationWorker struct {
	repo        AuthCacheInvalidationOutboxRepository
	cache       APIKeyCache
	local       *APIKeyService
	workerID    string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	start       sync.Once
	stop        sync.Once
	lifecycleMu sync.Mutex
	stopping    bool
	stopped     chan struct{}
	running     atomic.Bool
	processed   atomic.Uint64
	failures    atomic.Uint64
	lastError   atomic.Value
}

func NewAuthCacheInvalidationWorker(repo AuthCacheInvalidationOutboxRepository, cache APIKeyCache, local ...*APIKeyService) *AuthCacheInvalidationWorker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &AuthCacheInvalidationWorker{
		repo: repo, cache: cache, workerID: uuid.NewString(), ctx: ctx, cancel: cancel,
	}
	if len(local) > 0 {
		w.local = local[0]
	}
	w.lastError.Store("")
	return w
}

func (w *AuthCacheInvalidationWorker) Start() {
	if w == nil || w.repo == nil || w.cache == nil {
		return
	}
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if w.stopping {
		return
	}
	w.start.Do(func() {
		w.running.Store(true)
		w.wg.Add(1)
		go w.KeyRun()
	})
}

// StopContext 停止新认领并等待当前批次退出；未来重试和延迟事件保留在持久化 outbox。
func (w *AuthCacheInvalidationWorker) StopContext(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.lifecycleMu.Lock()
	w.stop.Do(func() {
		w.stopping = true
		w.stopped = make(chan struct{})
		if w.cancel != nil {
			w.cancel()
		}
		go func() { w.wg.Wait(); w.running.Store(false); close(w.stopped) }()
	})
	done := w.stopped
	w.lifecycleMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop 兼容旧同步调用，app 使用有预算的停止入口。
func (w *AuthCacheInvalidationWorker) Stop() { _ = w.StopContext(context.Background()) }

func (w *AuthCacheInvalidationWorker) KeyRun() {
	defer w.wg.Done()
	defer w.running.Store(false)
	ticker := time.NewTicker(KeyAuthInvalidationPollInterval)
	defer ticker.Stop()
	for {
		if err := w.KeyProcessBatch(w.ctx); err != nil && w.ctx.Err() == nil {
			w.KeyRecordFailure(err)
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *AuthCacheInvalidationWorker) KeyProcessBatch(ctx context.Context) error {
	events, err := w.repo.Claim(ctx, w.workerID, KeyAuthInvalidationBatchSize, KeyAuthInvalidationLease)
	if err != nil {
		return fmt.Errorf("claim auth cache invalidations: %w", err)
	}
	semaphore := make(chan struct{}, KeyAuthInvalidationConcurrency)
	var wg sync.WaitGroup
	for i := range events {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case semaphore <- struct{}{}:
		}
		wg.Add(1)
		go func(event AuthCacheInvalidationEvent) {
			defer wg.Done()
			defer func() { <-semaphore }()
			w.KeyProcessEvent(ctx, event)
		}(events[i])
	}
	wg.Wait()
	return nil
}

func (w *AuthCacheInvalidationWorker) KeyProcessEvent(parent context.Context, event AuthCacheInvalidationEvent) {
	if w.local != nil {
		w.local.KeyInvalidateLocalAuthCache(event.CacheKey)
	}
	ctx, cancel := context.WithTimeout(parent, KeyAuthInvalidationRedisTimeout)
	err := w.cache.DeleteAuthCache(ctx, event.CacheKey)
	if err == nil {
		err = w.cache.PublishAuthCacheInvalidation(ctx, event.CacheKey)
	}
	cancel()
	if err != nil {
		w.KeyRecordFailure(err)
		retryAt := w.now().UTC().Add(KeyAuthInvalidationRetryDelay(event.Attempts + 1))
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
		retryErr := w.repo.RetryClaimed(retryCtx, event.ID, w.workerID, retryAt, KeyBoundedAuthInvalidationError(err))
		retryCancel()
		if retryErr != nil {
			w.KeyRecordFailure(fmt.Errorf("release failed auth invalidation %d: %w", event.ID, retryErr))
		}
		return
	}
	if event.Stage == 0 {
		nextCtx, nextCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = w.repo.ScheduleSecondPass(nextCtx, event.ID, w.workerID, w.now().UTC().Add(KeyAuthInvalidationSafetyDelay))
		nextCancel()
		if err != nil {
			w.KeyRecordFailure(fmt.Errorf("schedule second auth invalidation pass %d: %w", event.ID, err))
			return
		}
		w.processed.Add(1)
		w.lastError.Store("")
		return
	}

	ackCtx, ackCancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = w.repo.DeleteClaimed(ackCtx, event.ID, w.workerID)
	ackCancel()
	if err != nil {
		w.KeyRecordFailure(fmt.Errorf("ack auth invalidation %d: %w", event.ID, err))
		return
	}
	w.processed.Add(1)
	w.lastError.Store("")
}

func KeyAuthInvalidationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	base := time.Second * time.Duration(1<<(attempt-1))
	return time.Duration(float64(base) * (0.8 + rand.Float64()*0.4))
}

func KeyBoundedAuthInvalidationError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}

func (w *AuthCacheInvalidationWorker) KeyRecordFailure(err error) {
	if err == nil {
		return
	}
	w.failures.Add(1)
	w.lastError.Store(KeyBoundedAuthInvalidationError(err))
	slog.Warn("auth cache invalidation outbox processing failed", "error", err)
}

func (w *AuthCacheInvalidationWorker) Health(ctx context.Context) AuthCacheInvalidationHealth {
	KeyHealth := AuthCacheInvalidationHealth{
		HealthySLA:  KeyAuthInvalidationSafetyDelay + 5*time.Second,
		RecoverySLA: 6 * time.Minute,
	}
	if w == nil {
		return KeyHealth
	}
	KeyHealth.Running = w.running.Load()
	KeyHealth.Processed = w.processed.Load()
	KeyHealth.Failures = w.failures.Load()
	if value := w.lastError.Load(); value != nil {
		KeyHealth.LastError, _ = value.(string)
	}
	if w.repo == nil {
		return KeyHealth
	}
	stats, err := w.repo.Stats(ctx)
	if err != nil {
		KeyHealth.StatsError = KeyBoundedAuthInvalidationError(err)
		return KeyHealth
	}
	KeyHealth.Pending = stats.Pending
	KeyHealth.MaxAttempts = stats.MaxAttempts
	if KeyHealth.LastError == "" {
		KeyHealth.LastError = stats.LastError
	}
	if stats.OldestCreatedAt != nil {
		KeyHealth.OldestLag = w.now().Sub(*stats.OldestCreatedAt)
		if KeyHealth.OldestLag < 0 {
			KeyHealth.OldestLag = 0
		}
	}
	return KeyHealth
}

func ProvideAuthCacheInvalidationWorker(repo AuthCacheInvalidationOutboxRepository, cache APIKeyCache, apiKeyService *APIKeyService) *AuthCacheInvalidationWorker {
	worker := NewAuthCacheInvalidationWorker(repo, cache, apiKeyService)

	return worker
}
