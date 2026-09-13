package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log"
	"log/slog"
)

// provideManagedRefresh 将账号用例、隐私、同一个协调器与旧供应商端口组合，构造不执行交换。
func provideManagedRefresh(admin *account.Admin, privacy *account.PrivacyService, coordinator *account.OAuthRefreshAPI, legacyAdmin service.AdminService, claude *service.OAuthService, openai *service.OpenAIOAuthService, gemini *service.GeminiOAuthService, ag *service.AntigravityOAuthService, grok service.GrokOAuthTokenService, invalidator service.TokenCacheInvalidator) *account.ManagedRefreshService {
	exchange := legacybridge.ManagedRefreshExchange(service.ManualCredentialExchangeOptions{Admin: legacyAdmin, Claude: claude, OpenAI: openai, Gemini: gemini, Antigravity: ag, Grok: grok})
	return account.NewManagedRefreshService(account.ManagedRefreshOptions{Store: admin, Privacy: privacy, Coordinate: coordinator.WithManagedRefresh, CacheKey: legacybridge.ManagedRefreshCacheKey, Exchange: exchange, Invalidate: legacybridge.ManagedRefreshInvalidation(invalidator), Log: log.Printf, Warn: slog.Warn, Error: slog.Error})
}
