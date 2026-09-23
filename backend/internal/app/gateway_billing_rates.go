package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// gatewayBillingRates 保留两条完成链的隔离缓存，由完成器和应用清理任务直接共享。
type gatewayBillingRates struct {
	Forward *billing.GroupRateResolver
	OpenAI  *billing.GroupRateResolver
}

func provideGatewayBillingRates(repo billing.UserGroupRateRepository, cfg *config.Config) *gatewayBillingRates {
	ttl := gatewayGroupRateCacheTTL(cfg)
	return &gatewayBillingRates{
		Forward: billing.NewGroupRateResolver(repo, nil, ttl, nil, "service.gateway", logging.LegacyPrintf),
		OpenAI:  billing.NewGroupRateResolver(repo, nil, ttl, nil, "service.openai_gateway", logging.LegacyPrintf),
	}
}

// gatewayGroupRateCacheTTL 只投影原默认值和显式正数配置。
func gatewayGroupRateCacheTTL(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Gateway.UserGroupRateCacheTTLSeconds <= 0 {
		return billing.DefaultGroupRateCacheTTL
	}
	return time.Duration(cfg.Gateway.UserGroupRateCacheTTLSeconds) * time.Second
}

// Expire 清理同一对完成缓存，沿用应用时间轮的频率和停止等待。
func (r *gatewayBillingRates) Expire() {
	if r != nil {
		r.Forward.DeleteExpired()
		r.OpenAI.DeleteExpired()
	}
}
