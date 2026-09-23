package service

import (
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

const (
	grokTeamRateLimitDefaultTTL = 10 * time.Minute
	grokTeamRateLimitMaxTTL     = time.Hour
	grokTeamRateLimitMinTTL     = 30 * time.Second
)

func markGrokTeamModelRateLimit(account *gatewayprovider.ExecutionAccount, model string, until time.Time) {
	accountcore.MarkGrokTeamModelRateLimit(gatewayprovider.ExecutionRecord(account), model, until)
}

func isGrokTeamModelRateLimited(account *gatewayprovider.ExecutionAccount, model string, now time.Time) bool {
	return accountcore.IsGrokTeamModelRateLimited(gatewayprovider.ExecutionRecord(account), model, now)
}

// filterGrokTeamModelRateLimitedAccounts 移除团队处于模型级冷却的候选；没有 team_id 的账号直接通过。
func filterGrokTeamModelRateLimitedAccounts(accounts []gatewayprovider.ExecutionAccount, model string, now time.Time) []gatewayprovider.ExecutionAccount {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := accounts[:0]
	kept := false
	for i := range accounts {
		upstreamModel := gatewayprovider.ExecutionModelPolicy(&accounts[i]).CanonicalSchedulingModel(model)
		if isGrokTeamModelRateLimited(&accounts[i], upstreamModel, now) {
			continue
		}
		out = append(out, accounts[i])
		kept = true
	}
	if !kept && len(out) == 0 {
		// 全部被过滤时返回空集合，由调用方按无容量处理。
		return nil
	}
	return out
}
