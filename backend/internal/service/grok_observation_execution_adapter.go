package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// grokObservationAdapter 绑定现有账号健康命令，不复制领域规则或快照节流器。
type grokObservationAdapter struct {
	s       *OpenAIGatewayService
	account *Account
}

func (a grokObservationAdapter) Stamp(snapshot *grok.QuotaSnapshot, model string) {
	stampGrokQuotaSnapshotForPlan(a.account, snapshot, model)
}
func (a grokObservationAdapter) Store(ctx context.Context, snapshot *grok.QuotaSnapshot) {
	a.s.updateGrokUsageSnapshot(ctx, a.account, snapshot)
}
func (a grokObservationAdapter) Recovery(snapshot *grok.QuotaSnapshot) bool {
	return isSuccessfulGrokRateLimitRecovery(a.account, snapshot)
}
func (a grokObservationAdapter) ClearRecovered(ctx context.Context) {
	clearGrokRateLimitAfterRecovery(ctx, a.s.accountRepo, a.account)
}
