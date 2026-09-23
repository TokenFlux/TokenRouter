package account

import (
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeBlockFallback 和 RuntimeRetryWindow 保留原内存停调及同账号恢复预算。
const RuntimeBlockFallback = 2 * time.Minute
const RuntimeRetryWindow = 2 * time.Minute

// RuntimeBlockState 统一持有停调、刷新失败与恢复代次，所有权不依附 HTTP 执行器。
type RuntimeBlockState struct {
	until                         sync.Map
	locks                         sync.Map
	generation                    sync.Map
	sequence                      atomic.Uint64
	retryStarted                  sync.Map
	refreshFailureBlocks          RefreshFailureBlocks
	refreshFailureClearGeneration sync.Map
	now                           func() time.Time
}

// NewRuntimeBlockState 只创建运行状态；时钟由装配显式传入，不启动后台任务。
func NewRuntimeBlockState(now func() time.Time) *RuntimeBlockState {
	if now == nil {
		now = time.Now
	}
	return &RuntimeBlockState{now: now}
}

// Block 只延长已有停调，重复声明仍推进代次，保护后续回滚所有权。
func (s *RuntimeBlockState) Block(id int64, until time.Time, reason string) {
	if s == nil {
		return
	}
	mu := s.lock(id)
	mu.Lock()
	defer mu.Unlock()
	_, _ = s.blockLocked(id, until, reason)
}

// BlockAccountScheduling 保留平台适用边界，其他平台不会新增本地停调状态。
func (s *RuntimeBlockState) BlockAccountScheduling(value *Record, until time.Time, reason string) {
	if value == nil || (value.Platform != PlatformOpenAI && value.Platform != PlatformGrok) {
		return
	}
	s.Block(value.ID, until, reason)
}

// ResetRetry 在原成功回报位置清除同账号恢复窗口。
func (s *RuntimeBlockState) ResetRetry(id int64) { s.retryStarted.Delete(id) }

// RetryWindowActive 按原首次观测时点创建同账号恢复窗口。
func (s *RuntimeBlockState) RetryWindowActive(accountID int64) bool {
	if s == nil {
		return false
	}
	now := s.now()
	value, _ := s.retryStarted.LoadOrStore(accountID, now)
	startedAt, ok := value.(time.Time)
	if !ok {
		s.retryStarted.Store(accountID, now)
		startedAt = now
	}
	return now.Before(startedAt.Add(RuntimeRetryWindow))
}

// RetryDeadline 只读取已建立窗口，不在查询截止时间时创建状态。
func (s *RuntimeBlockState) RetryDeadline(accountID int64) time.Time {
	if s == nil {
		return time.Time{}
	}
	value, ok := s.retryStarted.Load(accountID)
	if !ok {
		return time.Time{}
	}
	startedAt, ok := value.(time.Time)
	if !ok {
		return time.Time{}
	}
	return startedAt.Add(RuntimeRetryWindow)
}

func (s *RuntimeBlockState) lock(accountID int64) *sync.Mutex {
	actual, _ := s.locks.LoadOrStore(accountID, &sync.Mutex{})
	mu, ok := actual.(*sync.Mutex)
	if !ok {
		mu = &sync.Mutex{}
		s.locks.Store(accountID, mu)
	}
	return mu
}

func (s *RuntimeBlockState) blockLocked(accountID int64, until time.Time, _ string) (uint64, bool) {
	generation := s.sequence.Add(1)
	s.generation.Store(accountID, generation)
	now := s.now()
	blockUntil := until
	if blockUntil.IsZero() || !blockUntil.After(now) {
		blockUntil = now.Add(RuntimeBlockFallback)
	}

	for {
		current, loaded := s.until.Load(accountID)
		if !loaded {
			actual, stored := s.until.LoadOrStore(accountID, blockUntil)
			if !stored {
				return generation, true
			}
			current = actual
		}

		currentUntil, ok := current.(time.Time)
		if !ok || currentUntil.IsZero() {
			if s.until.CompareAndSwap(accountID, current, blockUntil) {
				return generation, true
			}
			continue
		}
		if !blockUntil.After(currentUntil) {
			return generation, false
		}
		if s.until.CompareAndSwap(accountID, current, blockUntil) {
			return generation, true
		}
	}
}

// ClearAccountSchedulingBlock 清除当前运行状态及恢复预算，同时推进显式清理代次。
func (s *RuntimeBlockState) ClearAccountSchedulingBlock(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	mu := s.lock(accountID)
	mu.Lock()
	defer mu.Unlock()
	s.clearLocked(accountID)
}

// ManagedRecoveryFence 取得管理员恢复使用的当前代次。
func (s *RuntimeBlockState) ManagedRecoveryFence(accountID int64) uint64 {
	if s == nil || accountID <= 0 {
		return 0
	}
	mu := s.lock(accountID)
	mu.Lock()
	defer mu.Unlock()
	value, _ := s.generation.Load(accountID)
	generation, _ := value.(uint64)
	return generation
}

// ClearAccountSchedulingBlockIfFence 仅清理仍属于本次恢复观察的状态。
func (s *RuntimeBlockState) ClearAccountSchedulingBlockIfFence(accountID int64, expected uint64) bool {
	if s == nil || accountID <= 0 {
		return false
	}
	mu := s.lock(accountID)
	mu.Lock()
	defer mu.Unlock()
	value, _ := s.generation.Load(accountID)
	generation, _ := value.(uint64)
	if generation != expected {
		return false
	}
	s.clearLocked(accountID)
	return true
}

func (s *RuntimeBlockState) clearLocked(accountID int64) {
	s.refreshFailureBlocks.Clear(accountID)
	s.until.Delete(accountID)
	s.retryStarted.Delete(accountID)
	generation := s.sequence.Add(1)
	s.generation.Store(accountID, generation)
	s.refreshFailureClearGeneration.Store(accountID, generation)
}

// Blocked 先核对刷新凭据身份，再判断尚未到期的本地停调。
func (s *RuntimeBlockState) Blocked(accountID int64, identity func() string) bool {
	if s == nil {
		return false
	}
	mu := s.lock(accountID)
	mu.Lock()
	defer mu.Unlock()
	if s.refreshFailureBlocks.Blocked(accountID, s.now(), identity) {
		return true
	}
	value, ok := s.until.Load(accountID)
	if !ok {
		return false
	}
	cooldownUntil, ok := value.(time.Time)
	if !ok || cooldownUntil.IsZero() {
		s.until.Delete(accountID)
		s.generation.Store(accountID, s.sequence.Add(1))
		return false
	}
	if s.now().Before(cooldownUntil) {
		return true
	}
	s.until.Delete(accountID)
	s.generation.Store(accountID, s.sequence.Add(1))
	return false
}

// BlockRollback 返回只撤销本次暂定状态的回滚，不覆盖继任者。
func (s *RuntimeBlockState) BlockRollback(accountID int64, until time.Time, reason string) func() {
	if s == nil {
		return func() {}
	}
	mu := s.lock(accountID)
	mu.Lock()
	before, hadBefore := s.until.Load(accountID)
	installedGeneration, changed := s.blockLocked(accountID, until, reason)
	installed, installedOK := s.until.Load(accountID)
	installedUntil, isTime := installed.(time.Time)
	mu.Unlock()
	if !changed || !installedOK || !isTime {
		return func() {}
	}
	if hadBefore {
		if beforeUntil, ok := before.(time.Time); ok && beforeUntil.Equal(installedUntil) {
			return func() {}
		}
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		generation, ok := s.generation.Load(accountID)
		if !ok || generation != installedGeneration {
			return
		}
		current, ok := s.until.Load(accountID)
		currentUntil, isTime := current.(time.Time)
		if !ok || !isTime || !currentUntil.Equal(installedUntil) {
			return
		}
		if hadBefore {
			s.until.Store(accountID, before)
			s.generation.Store(accountID, s.sequence.Add(1))
			return
		}
		s.until.Delete(accountID)
		s.generation.Store(accountID, s.sequence.Add(1))
	}
}

// PrepareRefreshFailure 在存储前记录清理代次，阻止迟到通知恢复已清除状态。
func (s *RuntimeBlockState) PrepareRefreshFailure(id int64) func(RefreshFailureNotice) {
	if s == nil {
		return func(RefreshFailureNotice) {}
	}
	mu := s.lock(id)
	mu.Lock()
	generation, _ := s.refreshFailureClearGeneration.Load(id)
	mu.Unlock()
	return func(notice RefreshFailureNotice) {
		if notice.AccountID != id || notice.Identity == "" || (notice.Platform != PlatformOpenAI && notice.Platform != PlatformGrok) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		current, _ := s.refreshFailureClearGeneration.Load(id)
		if current != generation {
			return
		}
		now := s.now()
		until := notice.Until
		if !until.After(now) {
			until = now.Add(RuntimeBlockFallback)
		}
		s.refreshFailureBlocks.Block(id, notice.Identity, until, now)
	}
}
