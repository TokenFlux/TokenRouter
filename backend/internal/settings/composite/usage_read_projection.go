package composite

import "github.com/TokenFlux/TokenRouter/internal/usage"

// ApplyUsageAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyUsageAdminReadSettings(value *usage.AdminReadSettings) {
	s.AllowUserViewErrorRequests = value.AllowUserViewErrorRequests
	s.UsageRankingEnabled = value.UsageRankingEnabled
	s.UsageRankingLimit = value.UsageRankingLimit
	s.UsageRankingShowActualCost = value.UsageRankingShowActualCost
	s.UsageRankingShowRequests = value.UsageRankingShowRequests
	s.UsageRankingShowTotalTokens = value.UsageRankingShowTotalTokens
	s.UsageRankingSortBy = value.UsageRankingSortBy
}
