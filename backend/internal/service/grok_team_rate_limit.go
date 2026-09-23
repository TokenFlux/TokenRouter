package service

import (
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
