package service

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/gin-gonic/gin"
)

type openAILegacySessionHashContextKey struct{}

var openAILegacySessionHashKey = openAILegacySessionHashContextKey{}

var schedulerStickyStats atomic.Pointer[scheduler.StickyStats]

func SchedulerStickyStats() *scheduler.StickyStats {
	if v := schedulerStickyStats.Load(); v != nil {
		return v
	}
	candidate := &scheduler.StickyStats{}
	if schedulerStickyStats.CompareAndSwap(nil, candidate) {
		return candidate
	}
	return schedulerStickyStats.Load()
}

// BindSchedulerStickyStats 绑定 app 唯一观测，旧入口不会保留第二份计数。
func BindSchedulerStickyStats(value *scheduler.StickyStats) { schedulerStickyStats.Store(value) }

func openAIStickyCompatStats() (int64, int64, int64) { return SchedulerStickyStats().Snapshot() }

// DeriveSessionHashFromSeed computes the current-format sticky-session hash
// from an arbitrary seed string.
func DeriveSessionHashFromSeed(seed string) string {
	currentHash, _ := deriveOpenAISessionHashes(seed)
	return currentHash
}

func deriveOpenAISessionHashes(sessionID string) (string, string) {
	return scheduler.DeriveSessionHashes(sessionID)
}

func withOpenAILegacySessionHash(ctx context.Context, legacyHash string) context.Context {
	if ctx == nil {
		return nil
	}
	trimmed := strings.TrimSpace(legacyHash)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, openAILegacySessionHashKey, trimmed)
}

func openAILegacySessionHashFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(openAILegacySessionHashKey).(string)
	return strings.TrimSpace(value)
}

func attachOpenAILegacySessionHashToGin(c *gin.Context, legacyHash string) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request = c.Request.WithContext(withOpenAILegacySessionHash(c.Request.Context(), legacyHash))
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
	return scheduler.NewStickySession(cache, scheduler.StickyOptions{Prefix: "openai:", ReadLegacy: s.openAISessionHashReadOldFallbackEnabled(), DualWriteLegacy: s.openAISessionHashDualWriteOldEnabled(), DefaultTTL: openaiStickySessionTTL}, SchedulerStickyStats())
}
func (s *OpenAIGatewayService) openAISessionCacheKey(hash string) string {
	return s.schedulerSticky().SessionKey(hash)
}

func (s *OpenAIGatewayService) getStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	return s.schedulerSticky().Get(ctx, derefGroupID(groupID), sessionHash, openAILegacySessionHashFromContext(ctx))
}
func (s *OpenAIGatewayService) setStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) error {
	return s.schedulerSticky().Set(ctx, derefGroupID(groupID), sessionHash, openAILegacySessionHashFromContext(ctx), accountID, ttl)
}
func (s *OpenAIGatewayService) refreshStickySessionTTL(ctx context.Context, groupID *int64, sessionHash string, ttl time.Duration) error {
	return s.schedulerSticky().Refresh(ctx, derefGroupID(groupID), sessionHash, openAILegacySessionHashFromContext(ctx), ttl)
}
func (s *OpenAIGatewayService) deleteStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) error {
	return s.schedulerSticky().Delete(ctx, derefGroupID(groupID), sessionHash, openAILegacySessionHashFromContext(ctx))
}
