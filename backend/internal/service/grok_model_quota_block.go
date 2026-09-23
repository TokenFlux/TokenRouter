package service

import (
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

func filterGrokModelQuotaBlockedAccounts(accounts []gatewayprovider.ExecutionAccount, model string, now time.Time) []gatewayprovider.ExecutionAccount {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := make([]gatewayprovider.ExecutionAccount, 0, len(accounts))
	for i := range accounts {
		upstreamModel := gatewayprovider.ExecutionModelPolicy(&accounts[i]).CanonicalSchedulingModel(model)
		if accountcore.IsGrokModelQuotaBlocked(accounts[i].Record.ID, upstreamModel, now) {
			continue
		}
		out = append(out, accounts[i])
	}
	return out
}
