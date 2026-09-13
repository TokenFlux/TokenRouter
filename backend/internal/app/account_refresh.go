package app

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountRefresh 为所有旧平台消费者绑定同一协调器与账号条件写入实现。
func provideAccountRefresh(store *accountpostgres.AccountStore, cache service.GeminiTokenCache, manager *lifecycle.Manager) *account.OAuthRefreshAPI {
	coordinator := account.NewOAuthRefreshAPI(store, cache, account.RefreshOptions{
		Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error,
		Platform: legacybridge.AccountRefreshPlatformPolicy(),
	})
	manager.Register(lifecycle.Hook{Name: "AccountRefreshCoordinator", StopOrder: 25, Stop: coordinator.StopContext})
	return coordinator
}
func provideLegacyAccountRefresh(core *account.OAuthRefreshAPI) *service.OAuthRefreshAPI {
	return service.WrapOAuthRefreshAPI(core)
}
