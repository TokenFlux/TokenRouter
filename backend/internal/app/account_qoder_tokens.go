package app

import (
	"log/slog"

	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// provideQoderTokens 构造唯一账号会话缓存，站点交换由供应商构建器负责。
func provideQoderTokens(transport accountprovider.QoderTransport, profiles *egressprovider.TLSProfiles) *accountprovider.QoderTokenProvider {
	source := accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{})
	source.SetHTTPUpstream(transport, profiles)
	return source
}

// provideTokenCacheInvalidator 复用已登记会话和令牌缓存，不创建第二份状态。
func provideTokenCacheInvalidator(cache account.AccessTokenCache, sessions *accountprovider.QoderTokenProvider) account.TokenCacheInvalidator {
	return account.NewCompositeTokenCacheInvalidator(cache, sessions, slog.Warn)
}
