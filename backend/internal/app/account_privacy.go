package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountPrivacy 统一管理与后台刷新使用的隐私用例，平台交换保留原技术客户端。
func provideAccountPrivacy(store *accountpostgres.AccountStore, proxies *egresspostgres.ProxyStore, factory service.PrivacyClientFactory, manager *lifecycle.Manager) *account.PrivacyService {
	core := account.NewPrivacyService(store, proxies, legacybridge.AccountPrivacyOptions(factory))
	manager.Register(lifecycle.Hook{Name: "AccountPrivacy", StopOrder: 26, Stop: core.StopContext})
	return core
}
