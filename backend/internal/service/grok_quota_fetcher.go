package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const grokQuotaSnapshotExtraKey = "grok_usage_snapshot"

func stampGrokQuotaSnapshotForPlan(account *Account, snapshot *xai.QuotaSnapshot, model string) {
	accountcore.StampGrokQuotaPlan(AccountRecordView(account), snapshot, model, xai.ResolveGrokTextResponsesModelID, xai.ApplyGrok45ResponsesPlanSignal)
}
