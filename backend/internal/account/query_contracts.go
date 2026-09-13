// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

const AccountListGroupUngrouped int64 = -1

const AccountPrivacyModeUnsetFilter = "__unset__"

// OAuthRefreshPageOptions 描述一次有界且游标稳定的 OAuth 账号扫描。
// 候选平台由 TokenRefreshService 注册表提供，避免仓储资格与已注册刷新器发生漂移。
type OAuthRefreshPageOptions struct {
	Platforms            []string
	AfterID              int64
	Limit                int
	ActiveOnly           bool
	IncludeSetupToken    bool
	RequireRefreshToken  bool
	ExcludeRetryCooldown bool
}

// OAuthRefreshCandidatePage 保留原始 SQL ID 页面的游标元数据。
// 即使详情加载时记录被并发删除，调用方仍可越过原始页面继续扫描，避免截断或重复。
type OAuthRefreshCandidatePage struct {
	Accounts    []Record
	NextAfterID int64
	HasMore     bool
}
