package usage

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	AllowUserViewErrorRequests  bool
	UsageRankingEnabled         bool
	UsageRankingLimit           int
	UsageRankingShowActualCost  bool
	UsageRankingShowRequests    bool
	UsageRankingShowTotalTokens bool
	UsageRankingSortBy          string
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {
	usageRanking := ParseRankingSettings(settings)
	result := &AdminReadSettings{}

	result.UsageRankingLimit = usageRanking.Limit
	result.UsageRankingEnabled = usageRanking.Enabled
	result.UsageRankingSortBy = string(usageRanking.SortBy)
	result.UsageRankingShowTotalTokens = usageRanking.ShowTotalTokens
	result.UsageRankingShowRequests = usageRanking.ShowRequests
	result.UsageRankingShowActualCost = usageRanking.ShowActualCost
	result.AllowUserViewErrorRequests = settings[SettingKeyAllowUserViewErrorRequests] == "true"
	return result
}
