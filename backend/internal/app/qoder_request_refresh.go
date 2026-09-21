package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// provideQoderRequestRefresh 为所有 Qoder 入站链绑定同一协调器、原生存储和会话缓存。
func provideQoderRequestRefresh(store *accountpostgres.AccountStore, tokens *accountprovider.QoderTokenProvider, coordinator *account.OAuthRefreshAPI, transport accountprovider.QoderTransport, profiles *egressprovider.TLSProfiles) *accountprovider.QoderRequestRefresh {
	return &accountprovider.QoderRequestRefresh{Store: store, Tokens: tokens, Coordinator: coordinator, Transport: transport, Profiles: profiles}
}

// provideQoderRuntime 绑定同一令牌源、传输池与账号存储，会话及执行器由原生运行时独占。
func provideQoderRuntime(tokens *accountprovider.QoderTokenProvider, transport accountprovider.QoderTransport, profiles *egressprovider.TLSProfiles, store *accountpostgres.AccountStore) *gatewayprovider.QoderRuntime {
	return gatewayprovider.NewQoderRuntime(gatewayprovider.QoderRuntimeOptions{Tokens: tokens, Transport: transport, Profiles: profiles, Health: store})
}
