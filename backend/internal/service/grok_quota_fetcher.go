package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const grokQuotaSnapshotExtraKey = "grok_usage_snapshot"

type GrokQuotaFetcher struct{}

func NewGrokQuotaFetcher() *GrokQuotaFetcher {
	return &GrokQuotaFetcher{}
}

func (f *GrokQuotaFetcher) BuildUsageInfo(account *Account) *UsageInfo {
	return grokQuotaView().BuildUsageInfo(AccountRecordView(account))
}

func grokBillingSnapshotFromExtra(extra map[string]any) (*xai.BillingSummary, error) {
	return accountcore.ParseGrokBillingSnapshot(extra)
}

func stampGrokQuotaSnapshotForPlan(account *Account, snapshot *xai.QuotaSnapshot, model string) {
	accountcore.StampGrokQuotaPlan(AccountRecordView(account), snapshot, model, xai.ResolveGrokTextResponsesModelID, xai.ApplyGrok45ResponsesPlanSignal)
}

func grokQuotaSnapshotFromExtra(extra map[string]any) (*xai.QuotaSnapshot, error) {
	return accountcore.GrokQuotaSnapshotFromExtra(extra)
}

// 档位解释仍使用唯一原生函数，账号展示不持有供应商客户端。
func grokQuotaView() accountcore.GrokQuotaView {
	return accountcore.GrokQuotaView{
		FreeTokenLimit:      xai.GrokFreeRolling24hTokenLimit,
		NeedsReauth:         func(v *accountcore.Record) bool { return accountGrokNeedsReauth(AccountFromRecord(v)) },
		JWTSubscriptionTier: xai.SubscriptionTierFromJWT,
		CanonicalPlan:       xai.CanonicalGrokPlan,
		ParseTime:           parseTime,
	}
}
