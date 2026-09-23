package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const grokQuotaSnapshotExtraKey = "grok_usage_snapshot"

func stampGrokQuotaSnapshotForPlan(account *gatewayprovider.ExecutionAccount, snapshot *xai.QuotaSnapshot, model string) {
	accountcore.StampGrokQuotaPlan(gatewayprovider.ExecutionRecord(account), snapshot, model, xai.ResolveGrokTextResponsesModelID, xai.ApplyGrok45ResponsesPlanSignal)
}
