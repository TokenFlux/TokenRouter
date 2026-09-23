package service

import (
	"context"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// BindFreeQuotaGate 在开始接收请求前绑定应用持有的普通选择缓存。
func (s *GatewayService) BindFreeQuotaGate(gate *account.FreeQuotaGate) { s.freeQuotaGate = gate }

// BindFreeQuotaGates 保留普通选择共享缓存与每个高级调度器的独立缓存。
func (s *OpenAIGatewayService) BindFreeQuotaGates(gate *account.FreeQuotaGate, factory func() *account.FreeQuotaGate) {
	s.freeQuotaGate = gate
	s.newFreeQuotaGate = factory
}

func (s *defaultOpenAIAccountScheduler) filterGrokFreeQuotaAccounts(_ context.Context, accounts []gatewayprovider.ExecutionAccount) []gatewayprovider.ExecutionAccount {
	if s == nil || s.service == nil || s.service.newFreeQuotaGate == nil {
		return accounts
	}
	gate := loadFreeQuotaGate(&s.freeQuotaGate, s.service.newFreeQuotaGate)
	return filterFreeQuotaProjection(gate, accounts)
}

// loadFreeQuotaGate 只登记一个实例，构造未启动工作，因此竞争中未发布对象不需要清理。
func loadFreeQuotaGate(slot *atomic.Pointer[account.FreeQuotaGate], factory func() *account.FreeQuotaGate) *account.FreeQuotaGate {
	if gate := slot.Load(); gate != nil {
		return gate
	}
	gate := factory()
	if slot.CompareAndSwap(nil, gate) {
		return gate
	}
	return slot.Load()
}

func (s *GatewayService) filterGrokFreeQuotaAccountsForGateway(_ context.Context, accounts []gatewayprovider.ExecutionAccount) []gatewayprovider.ExecutionAccount {
	if s == nil {
		return accounts
	}
	return filterFreeQuotaProjection(s.freeQuotaGate, accounts)
}

// 旧执行形状仅投影资格与结果，不拥有缓存或裁决算法。
func filterFreeQuotaProjection(gate *account.FreeQuotaGate, accounts []gatewayprovider.ExecutionAccount) []gatewayprovider.ExecutionAccount {
	if gate == nil {
		return accounts
	}
	candidates := make([]account.FreeQuotaCandidate, len(accounts))
	for i := range accounts {
		candidates[i] = account.FreeQuotaCandidate{ID: accounts[i].Record.ID, Eligible: account.IsExplicitGrokFreeOAuthAccount(gatewayprovider.ExecutionProtocolRecord(&accounts[i]))}
	}
	blocked := gate.Blocked(candidates)
	if blocked == nil {
		return accounts
	}
	filtered := make([]gatewayprovider.ExecutionAccount, 0, len(accounts))
	for i, candidate := range candidates {
		if !candidate.Eligible || !blocked[candidate.ID] {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}
