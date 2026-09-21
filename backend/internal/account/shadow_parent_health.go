package account

// ParentHealthyForShadow 报告 spark 影子账号的母账号凭据是否可用(影子据此可被调度)。
//
// 非影子账号直接返回 true（不受此检查约束）。
// lookup 将母账号 ID 解析为当前 Account（来自调度快照 map 或 repo）。
//
// 关键语义(F1 决策 A + 外审 D):母账号须仍是 OpenAI OAuth(fail-closed——否则透传凭据解析必失败,
// 影子不应进调度候选),且凭据「可用」。IsCredentialUsableForShadow 检查:账号 active、OAuth token
// 未过期、且**未处于 TempUnschedulableUntil 冷却期**——对 OpenAI 账号该字段由 401/token 刷新耗尽/
// transport·proxy 故障写入,代表共享凭据或传输坏死,故**连坐**影子。
//
// **刻意排除** global 维度的 RateLimitResetAt/OverloadUntil 与母账号手动 Schedulable 开关:
// 母账号 global 429 不得连坐 spark 影子,否则会重新耦合影子架构本应解耦的两条 429 道。
// 母账号未找到(nil)、非 OpenAI OAuth、或凭据不可用时影子被挡。
func ParentHealthyForShadow(account *Record, lookup func(int64) *Record) bool {
	if account == nil || !account.IsShadow() {
		return true
	}
	parent := lookup(*account.ParentAccountID)
	if parent == nil {
		return false
	}
	return parent.IsOpenAIOAuth() && parent.IsCredentialUsableForShadow()
}
