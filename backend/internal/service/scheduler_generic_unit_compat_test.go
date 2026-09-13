//go:build unit

package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 旧白盒契约仅在测试中投影到唯一 scheduler 实现，生产入口不再保留这些私有函数。
// filterByMinPriority 过滤出优先级最小的账号集合
func filterByMinPriority(accounts []accountWithLoad) []accountWithLoad {
	if len(accounts) == 0 {
		return accounts
	}
	values, restore := basicLoadProjections(accounts)
	filtered := scheduler.FilterByMinPriority(values)
	out := make([]accountWithLoad, len(filtered))
	for i, v := range filtered {
		out[i] = restore[v.Account]
	}
	return out
}

// filterByMinLoadRate 过滤出负载率最低的账号集合
func filterByMinLoadRate(accounts []accountWithLoad) []accountWithLoad {
	if len(accounts) == 0 {
		return accounts
	}
	values, restore := basicLoadProjections(accounts)
	filtered := scheduler.FilterByMinLoadRate(values)
	out := make([]accountWithLoad, len(filtered))
	for i, v := range filtered {
		out[i] = restore[v.Account]
	}
	return out
}

// filterBySoonestReset 过滤出「会话窗口最早重置」的账号集合（use-it-or-lose-it）。
// 仅保留拥有未来重置时间（SessionWindowEnd 在当前时间之后）且最早的账号；
// 窗口为空或已过期的账号视为无活跃窗口、优先级最低。
// 当所有账号都没有活跃窗口时，返回原集合（不改变后续 LRU 选择）。
func filterBySoonestReset(accounts []accountWithLoad) []accountWithLoad {
	if len(accounts) == 0 {
		return accounts
	}
	values, restore := basicLoadProjections(accounts)
	filtered := scheduler.FilterBySoonestReset(values, time.Now)
	out := make([]accountWithLoad, len(filtered))
	for i, v := range filtered {
		out[i] = restore[v.Account]
	}
	return out
}

// selectByLRU 从集合中选择最久未用的账号
// 如果有多个账号具有相同的最小 LastUsedAt，则随机选择一个
func selectByLRU(accounts []accountWithLoad, preferOAuth bool) *accountWithLoad {
	values, _ := basicLoadProjections(accounts)
	selected := scheduler.SelectByLRU(values, preferOAuth)
	if selected == nil {
		return nil
	}
	for i := range accounts {
		if values[i].Account == selected.Account {
			return &accounts[i]
		}
	}
	return nil
}

func sortAccountsByPriorityAndLastUsed(accounts []*Account, preferOAuth bool) {
	values := make([]*scheduler.BasicAccount, len(accounts))
	restore := make(map[*scheduler.BasicAccount]*Account, len(accounts))
	for i, a := range accounts {
		values[i] = basicAccountProjection(a)
		restore[values[i]] = a
	}
	scheduler.SortAccountsByPriorityAndLastUsed(values, preferOAuth)
	for i, v := range values {
		accounts[i] = restore[v]
	}
}

// selectAccountForModelWithPlatform 选择单平台账户（完全隔离）
func (s *GatewayService) selectAccountForModelWithPlatform(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, platform string) (*Account, error) {
	core, scope := s.genericSelector()
	selected, err := core.SelectPlatform(ctx, groupID, sessionHash, requestedModel, excludedIDs, platform)
	return scope.oldAccount(selected), err
}

// selectAccountWithMixedScheduling 选择账户（支持混合调度）
// 查询原生平台账户 + 启用 mixed_scheduling 的 antigravity 账户
func (s *GatewayService) selectAccountWithMixedScheduling(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, nativePlatform string) (*Account, error) {
	core, scope := s.genericSelector()
	selected, err := core.SelectMixed(ctx, groupID, sessionHash, requestedModel, excludedIDs, nativePlatform)
	return scope.oldAccount(selected), err
}

// 基础选择旧入口只投影排序字段，指针对应表保留原对象及同 ID 候选差异。
func basicAccountProjection(a *Account) *scheduler.BasicAccount {
	if a == nil {
		return nil
	}
	return &scheduler.BasicAccount{ID: a.ID, Type: a.Type, Priority: a.Priority, LastUsedAt: a.LastUsedAt, SessionWindowEnd: a.SessionWindowEnd}
}

func basicLoadProjections(accounts []accountWithLoad) ([]scheduler.BasicCandidate, map[*scheduler.BasicAccount]accountWithLoad) {
	values := make([]scheduler.BasicCandidate, len(accounts))
	restore := make(map[*scheduler.BasicAccount]accountWithLoad, len(accounts))
	for i, a := range accounts {
		v := basicAccountProjection(a.account)
		values[i] = scheduler.BasicCandidate{Account: v, Load: a.loadInfo}
		restore[v] = a
	}
	return values, restore
}
