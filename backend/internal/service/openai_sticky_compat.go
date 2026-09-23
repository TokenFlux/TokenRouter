package service

import (
	"context"
	"time"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
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

func (s *OpenAIGatewayService) openAISessionHashReadOldFallbackEnabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.OpenAIWS.SessionHashReadOldFallback
}

func (s *OpenAIGatewayService) openAISessionHashDualWriteOldEnabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.OpenAIWS.SessionHashDualWriteOld
}

func (s *OpenAIGatewayService) schedulerSticky() *scheduler.StickySession {
	var cache scheduler.StickyCache
	if s != nil {
		cache = s.cache
	}
	return scheduler.NewStickySession(cache, scheduler.StickyOptions{Prefix: "openai:", ReadLegacy: s.openAISessionHashReadOldFallbackEnabled(), DualWriteLegacy: s.openAISessionHashDualWriteOldEnabled(), DefaultTTL: openaiStickySessionTTL}, s.stickyStats())
}

func (s *OpenAIGatewayService) getStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	return s.schedulerSticky().Get(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx))
}
func (s *OpenAIGatewayService) setStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) error {
	return s.schedulerSticky().Set(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx), accountID, ttl)
}
func (s *OpenAIGatewayService) refreshStickySessionTTL(ctx context.Context, groupID *int64, sessionHash string, ttl time.Duration) error {
	return s.schedulerSticky().Refresh(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx), ttl)
}
func (s *OpenAIGatewayService) deleteStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) error {
	return s.schedulerSticky().Delete(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx))
}
