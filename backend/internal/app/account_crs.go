package app

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
)

// provideCRSSync 使用唯一账号、代理存储与刷新协调器，构造不启动后台任务。
func provideCRSSync(accounts *accountpostgres.AccountStore, proxies *egresspostgres.ProxyStore, oauth *account.ClaudeAuthorization, openai *account.OpenAIAuthorization, gemini *account.GeminiAuthorization, cfg *config.Config, coordinator *account.OAuthRefreshAPI) *account.CRSSync {
	exchange := &account.CRSAuthorization{Claude: oauth, OpenAI: openai, Gemini: gemini}
	v := cfg.Security.URLAllowlist
	client := accountprovider.NewCRSClient(accountprovider.CRSClientOptions{Configured: true, AllowlistEnabled: v.Enabled, AllowInsecureHTTP: v.AllowInsecureHTTP, AllowPrivateHosts: v.AllowPrivateHosts, Hosts: v.CRSHosts})
	return account.NewCRSSync(accounts, proxies, client, account.CRSOptions{Now: time.Now, Warn: slog.Warn, Refresh: exchange.Coordinated(coordinator, accountprovider.GeminiTokenCacheKey)})
}
func provideCRSHTTP(source *account.CRSSync) *accounthttp.CRSHandler {
	return accounthttp.NewCRSHandler(source)
}
