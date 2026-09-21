package app

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
)

// provideAccountRefresh 为所有旧平台消费者绑定同一协调器与账号条件写入实现。
func provideAccountRefresh(store *accountpostgres.AccountStore, cache account.AccessTokenCache, manager *lifecycle.Manager) *account.OAuthRefreshAPI {
	coordinator := account.NewOAuthRefreshAPI(store, cache, account.RefreshOptions{
		Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error,
		Platform: account.AccountRefreshPlatformPolicy(),
	})
	manager.Register(lifecycle.Hook{Name: "AccountRefreshCoordinator", StopOrder: 25, Stop: coordinator.StopContext})
	return coordinator
}
