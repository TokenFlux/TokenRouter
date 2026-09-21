package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
)

// provideAccountPrivacy 统一管理与后台刷新使用的隐私用例，平台交换保留原技术客户端。
func provideAccountPrivacy(store *accountpostgres.AccountStore, proxies *egresspostgres.ProxyStore, factory openai.PrivacyClientFactory, manager *lifecycle.Manager) *account.PrivacyService {
	core := account.NewPrivacyService(store, proxies, provider.PrivacyOptions(factory, openai.PrivacyEndpoints{}))
	manager.Register(lifecycle.Hook{Name: "AccountPrivacy", StopOrder: 26, Stop: core.StopContext})
	return core
}
