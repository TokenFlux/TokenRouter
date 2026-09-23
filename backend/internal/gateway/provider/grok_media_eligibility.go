package provider

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// GrokMediaEligibilityProber 只在既有计费资格缺少观测时探测，不执行媒体推理。
type GrokMediaEligibilityProber interface {
	ProbeMediaEligibility(context.Context, int64) (bool, string, error)
}

// CheckGrokMediaEligibility 保留账号规则、原错误与探测短路次序。
func CheckGrokMediaEligibility(ctx context.Context, value *ExecutionAccount, probe GrokMediaEligibilityProber) (bool, string, error) {
	if value == nil {
		return false, "missing_account", errors.New("grok media account is required")
	}
	eligible, reason := account.GrokMediaGenerationEligibility(ExecutionRecord(value), accountprovider.GrokTierRules())
	if eligible || reason != "billing_unobserved" {
		return eligible, reason, nil
	}
	if probe == nil {
		return false, "billing_probe_unavailable", errors.New("grok media eligibility probe is not configured")
	}
	return probe.ProbeMediaEligibility(ctx, value.Record.ID)
}
