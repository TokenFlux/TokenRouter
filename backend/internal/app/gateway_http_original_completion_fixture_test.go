package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// newHTTPCompletionFixture 显式绑定原HTTP测试的完成依赖，不再从旧网关回取隐式装配。
func newHTTPCompletionFixture(cfg *config.Config, logs usage.UsageLogRepository, calculator *billing.Calculator, eligibility *billing.Eligibility, activity completion.AccountActivity, modelConfigs *routing.PricingConfigService, health completion.HealthObserver, openAI bool) *completion.Recorder {
	f := gatewaytestkit.NewRecording(logs, nil, nil, false)
	f.Dependencies.Calculator = calculator
	f.Dependencies.Health = health
	f.GroupPolicies = modelConfigs
	f.Effects.Funds.Cache = eligibility
	f.Effects.Activity = activity
	f.Options.DefaultMultiplier = 1
	if cfg != nil {
		f.Options.DefaultMultiplier = cfg.Default.RateMultiplier
		f.Options.Simple = cfg.RunMode == config.RunModeSimple
		f.Effects.Funds.FlusherEnabled = cfg.Database.UserPlatformQuotaFlusherEnabled
	}
	return f.Core(nil, openAI)
}
