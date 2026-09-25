package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// gatewayCompatibilitySnapshot 只投影共享粘性计数，已退役的metadata计数保持零。
func gatewayCompatibilitySnapshot(shared *schedulerSharedState) gatewayhttp.CompatibilityLogSnapshot {
	if shared == nil || shared.Sticky == nil {
		return gatewayhttp.CompatibilityLogSnapshot{}
	}
	total, hit, dual := shared.Sticky.Snapshot()
	rate := float64(0)
	if total > 0 {
		rate = float64(hit) / float64(total)
	}
	return gatewayhttp.CompatibilityLogSnapshot{ReadTotal: total, ReadHit: hit, DualWrite: dual, ReadHitRate: rate}
}

// stopOpenAI429 仅把凭据资格投影给唯一重试预算规则。
func stopOpenAI429(value *provider.ExecutionAccount, status, switches int, state *failover.OAuth429State) bool {
	return failover.StopOAuth429(failover.OAuth429Account{OpenAI: value != nil && value.View().IsOpenAIOAuthLike(), Grok: value != nil && value.Record.Platform == capability.PlatformGrok && value.Record.Type == capability.AccountTypeOAuth}, status, switches, state)
}
