// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
)

// RuntimePresenter 只执行管理 DTO 与母账号展示投影；查询和阈值由账号用例处理。
type RuntimePresenter struct {
	status  *account.RuntimeStatusReader
	parents interface {
		GetAccountsByIDs(context.Context, []int64) ([]*account.Record, error)
	}
	ollama *account.OllamaCloudUsageService
}

func NewRuntimePresenter(status *account.RuntimeStatusReader, parents interface {
	GetAccountsByIDs(context.Context, []int64) ([]*account.Record, error)
}, ollama *account.OllamaCloudUsageService) *RuntimePresenter {
	return &RuntimePresenter{status, parents, ollama}
}
func (p *RuntimePresenter) Present(ctx context.Context, v *account.Record) AccountWithConcurrency {
	state := p.status.Read(ctx, v)
	item := p.Project(state)
	p.EnrichShadowParents(ctx, []AccountWithConcurrency{item})
	return item
}

// EnrichShadowParentInfo 把母账号的展示信息回填到影子行的 parent_* 字段。
// 纯函数：仅依赖传入的母账号 map，便于单测；非影子或母账号缺失时优雅留空。
func EnrichShadowParentInfo(items []AccountWithConcurrency, parents map[int64]*account.Record) {
	for i := range items {
		a := items[i].Account
		if a == nil || a.ParentAccountID == nil {
			continue
		}
		p := parents[*a.ParentAccountID]
		if p == nil {
			continue
		}
		a.ParentEmail = p.GetCredential("email")
		a.ParentPlanType = p.GetCredential("plan_type")
		a.ParentSubscriptionExpiresAt = p.GetCredential("subscription_expires_at")
		a.ParentChatGPTAccountID = p.GetCredential("chatgpt_account_id")
		a.ParentPrivacyMode = p.GetExtraString("privacy_mode")
	}
}

// enrichShadowParents 收集本批影子行的母账号 ID、一次批量解析（避免 N+1），再回填。
// 解析失败时不报错（parent_* 留空，降级）。
func (p *RuntimePresenter) EnrichShadowParents(ctx context.Context, items []AccountWithConcurrency) {
	seen := make(map[int64]struct{})
	for i := range items {
		a := items[i].Account
		if a == nil || a.ParentAccountID == nil {
			continue
		}
		seen[*a.ParentAccountID] = struct{}{}
	}
	if len(seen) == 0 {
		return
	}
	parentIDs := make([]int64, 0, len(seen))
	for pid := range seen {
		parentIDs = append(parentIDs, pid)
	}
	parents, err := p.parents.GetAccountsByIDs(ctx, parentIDs)
	if err != nil {
		return
	}
	pmap := make(map[int64]*account.Record, len(parents))
	for _, p := range parents {
		pmap[p.ID] = p
	}
	EnrichShadowParentInfo(items, pmap)
}

// Project 只转换已取得的运行观察，不增加查询或改变列表批量语义。
func (p *RuntimePresenter) Project(state account.RuntimeStatus) AccountWithConcurrency {
	item := AccountWithConcurrency{Account: dto.AccountFromRecord(state.Record), CurrentConcurrency: state.CurrentConcurrency, CurrentWindowCost: state.CurrentWindowCost, ActiveSessions: state.ActiveSessions, CurrentRPM: state.CurrentRPM, SchedulerScore: state.SchedulerScore, SchedulerScores: state.SchedulerScores}
	if item.Account == nil {
		return item
	}
	if p.ollama != nil {
		p.ollama.EnrichState(item.OllamaCloudUsage)
	}
	return item
}
