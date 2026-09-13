// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/config"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountRecovery 将原 HTTP、成功测试和窗口恢复绑定到唯一账号健康规则。
func provideAccountRecovery(store *accountpostgres.AccountStore, cache service.TempUnschedCache, source *service.RateLimitService, precheck *account.GeminiPrecheck, cfg *config.Config) *account.RecoveryService {
	source.BindGeminiPrecheck(precheck)
	healthOptions := legacybridge.AccountHealthOptions(source)
	healthOptions.CNIntervalMinutes = cfg.Gateway.CNProviders.IntervalMinutes
	healthOptions.OverloadMinutes = cfg.RateLimit.OverloadCooldownMinutes
	source.BindHealth(account.NewHealthService(store, cache, healthOptions))
	core := account.NewRecoveryService(store, cache, legacybridge.AccountRecoveryOptions(source))
	source.BindRecovery(core)
	return core
}
