package scheduler

import (
	"context"
	"sync"
)

// SessionAttempts 保存当前请求的会话完成状态，允许资源按原取消/接受时机先释放，
// 最终保留与注销仍以执行层观测到的成功或可结算部分结果为准。
type SessionAttempts struct {
	mu          sync.Mutex
	closed      bool
	entries     map[int64]*AttemptLease
	cache       SessionLimitCache
	diagnostics Diagnostics
}

func NewSessionAttempts(cache SessionLimitCache, diagnostics Diagnostics) *SessionAttempts {
	return &SessionAttempts{cache: cache, diagnostics: diagnostics, entries: map[int64]*AttemptLease{}}
}

// Track 接管原入口已处理的会话绑定；同账号再次选择保留原“最新投影替换”语义。
func (s *SessionAttempts) Track(binding SessionBinding) {
	if binding.SessionID == "" {
		return
	}
	attempt := NewAttemptLease(nil, func(outcome AttemptOutcome) {
		FinishSession(context.Background(), s.cache, binding, outcome, s.diagnostics)
	})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		attempt.Release()
		return
	}
	if previous := s.entries[binding.AccountID]; previous != nil {
		attempt.resources.Own(previous.resources.Release)
	}
	s.entries[binding.AccountID] = attempt
	s.mu.Unlock()
}

// Own 将本次账号或串行资源交给对应尝试；提前释放与最终 Finish 共用同一个释放函数。
func (s *SessionAttempts) Own(id int64, release func()) {
	if release == nil {
		return
	}
	s.mu.Lock()
	attempt, closed := s.entries[id], s.closed
	s.mu.Unlock()
	if attempt != nil {
		attempt.resources.Own(release)
	} else if closed {
		release()
	}
}

// Abandon 保留切号时立即注销原账号会话的时机。
func (s *SessionAttempts) Abandon(id int64) {
	s.mu.Lock()
	attempt := s.entries[id]
	delete(s.entries, id)
	s.mu.Unlock()
	if attempt != nil {
		attempt.Finish(AttemptOutcome{})
	}
}

// Reset 在分组回退前完成本轮尝试，下一轮仍可登记。
func (s *SessionAttempts) Reset() {
	s.mu.Lock()
	entries := s.entries
	s.entries = map[int64]*AttemptLease{}
	s.mu.Unlock()
	for _, attempt := range entries {
		attempt.Finish(AttemptOutcome{})
	}
}

// Finish 与原请求最终状态一致；重复调用不把成功结果改成失败。
func (s *SessionAttempts) Finish(outcome AttemptOutcome) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	entries := s.entries
	s.entries = nil
	s.mu.Unlock()
	for _, attempt := range entries {
		attempt.Finish(outcome)
	}
}
