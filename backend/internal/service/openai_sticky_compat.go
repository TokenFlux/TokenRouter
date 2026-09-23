package service

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// stickyStats 读取当前网关的观测实例，独立构造不会污染其他运行时。
func (s *OpenAIGatewayService) stickyStats() *scheduler.StickyStats {
	if s == nil {
		return &scheduler.StickyStats{}
	}
	if v := s.schedulerStickyStats.Load(); v != nil {
		return v
	}
	candidate := &scheduler.StickyStats{}
	if s.schedulerStickyStats.CompareAndSwap(nil, candidate) {
		return candidate
	}
	return s.schedulerStickyStats.Load()
}

// BindSchedulerStickyStats 绑定 app 唯一观测，旧入口不会保留第二份计数。
func (s *OpenAIGatewayService) BindSchedulerStickyStats(value *scheduler.StickyStats) {
	s.schedulerStickyStats.Store(value)
}

// DeriveSessionHashFromSeed computes the current-format sticky-session hash
// from an arbitrary seed string.
func DeriveSessionHashFromSeed(seed string) string {
	currentHash, _ := scheduler.DeriveSessionHashes(seed)
	return currentHash
}
