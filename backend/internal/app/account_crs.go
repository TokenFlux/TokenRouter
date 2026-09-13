package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log/slog"
	"time"
)

// provideCRSSync 使用唯一账号与代理存储，旧服务仅提供供应商交换。
func provideCRSSync(accounts *accountpostgres.AccountStore, proxies *egresspostgres.ProxyStore, oauth *service.OAuthService, openai *service.OpenAIOAuthService, gemini *service.GeminiOAuthService, cfg *config.Config, coordinator *account.OAuthRefreshAPI) *service.CRSSyncService {
	exchange := service.NewCRSPlatformExchange(oauth, openai, gemini)
	v := cfg.Security.URLAllowlist
	client := accountprovider.NewCRSClient(accountprovider.CRSClientOptions{Configured: true, AllowlistEnabled: v.Enabled, AllowInsecureHTTP: v.AllowInsecureHTTP, AllowPrivateHosts: v.AllowPrivateHosts, Hosts: v.CRSHosts})
	core := account.NewCRSSync(accounts, proxies, client, account.CRSOptions{Now: time.Now, Warn: slog.Warn, Refresh: exchange.CoordinatedCRSRefresh(coordinator)})
	exchange.BindCore(core)
	return exchange
}
func provideCRSHTTP(source *service.CRSSyncService) *accounthttp.CRSHandler {
	return accounthttp.NewCRSHandler(source.Core())
}
