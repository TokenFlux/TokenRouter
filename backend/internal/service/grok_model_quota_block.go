package service

import (
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func markGrokModelQuotaBlock(accountID int64, model string, until time.Time) {
	accountcore.MarkGrokModelQuotaBlock(accountID, model, until)
}

func markGrokModelTransientBlock(accountID int64, model string, until time.Time) {
	accountcore.MarkGrokModelTransientBlock(accountID, model, until)
}

func isGrokModelQuotaBlocked(accountID int64, model string, now time.Time) bool {
	return accountcore.IsGrokModelQuotaBlocked(accountID, model, now)
}

func filterGrokModelQuotaBlockedAccounts(accounts []Account, model string, now time.Time) []Account {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := make([]Account, 0, len(accounts))
	for i := range accounts {
		upstreamModel := canonicalOpenAIAccountSchedulingModel(&accounts[i], model)
		if isGrokModelQuotaBlocked(accounts[i].ID, upstreamModel, now) {
			continue
		}
		out = append(out, accounts[i])
	}
	return out
}

func isGrokModelSpecificFreeUsage(low, model string) bool {
	return accountcore.IsGrokModelSpecificFreeUsage(low, model)
}
