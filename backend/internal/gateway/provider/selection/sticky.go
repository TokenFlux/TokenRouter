package selection

import (
	"context"
	"time"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

func (s *Compatible) openAISessionHashReadOldFallbackEnabled() bool {
	if s == nil {
		return true
	}
	return s.options.ReadLegacySticky
}

func (s *Compatible) openAISessionHashDualWriteOldEnabled() bool {
	if s == nil {
		return true
	}
	return s.options.WriteLegacySticky
}

func (s *Compatible) schedulerSticky() *schedulercore.StickySession {
	var cache schedulercore.StickyCache
	if s != nil {
		cache = s.cache
	}
	return schedulercore.NewStickySession(cache, schedulercore.StickyOptions{Prefix: "openai:", ReadLegacy: s.openAISessionHashReadOldFallbackEnabled(), DualWriteLegacy: s.openAISessionHashDualWriteOldEnabled(), DefaultTTL: openaiStickySessionTTL}, s.stickyStats())
}

func (s *Compatible) getStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	return s.schedulerSticky().Get(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx))
}
func (s *Compatible) setStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) error {
	return s.schedulerSticky().Set(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx), accountID, ttl)
}
func (s *Compatible) refreshStickySessionTTL(ctx context.Context, groupID *int64, sessionHash string, ttl time.Duration) error {
	return s.schedulerSticky().Refresh(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx), ttl)
}
func (s *Compatible) deleteStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) error {
	return s.schedulerSticky().Delete(ctx, derefGroupID(groupID), sessionHash, requeststate.OpenAILegacySessionHashFromContext(ctx))
}
