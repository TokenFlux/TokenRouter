package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// 旧选择入口仅保留原缓存查询，亲缘值已归请求状态。
func (s *OpenAIGatewayService) resolveOpenAIGuardianParentAccountID(ctx context.Context, groupID *int64) int64 {
	if s == nil || s.cache == nil {
		return 0
	}
	affinity, ok := requeststate.GuardianParentAffinityFromContext(ctx)
	if !ok {
		return 0
	}
	lookupCtx := requeststate.WithOpenAILegacySessionHash(ctx, affinity.LegacySessionHash)
	accountID, err := s.getStickySessionAccountID(lookupCtx, groupID, affinity.CurrentSessionHash)
	if err != nil || accountID <= 0 {
		return 0
	}
	return accountID
}
