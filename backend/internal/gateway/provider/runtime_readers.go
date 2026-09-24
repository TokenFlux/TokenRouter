package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// RuntimeReaders 只列出执行适配器的已装配读取端口，不拥有规则、缓存或后台任务。
// 各读取器共享应用实例；调用方仍在原请求位置读取动态值。
type RuntimeReaders struct {
	Gateway    *gateway.RuntimeSettings
	Account    *account.RuntimeSettings
	Quota      *account.QuotaSettingsCache
	Routing    *routing.RuntimeSettings
	Moderation *moderation.RuntimeSettings
	Search     *search.ConfigService
	Scheduler  settings.Repository
}
