package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/redis/go-redis/v9"
)

// provideGrokAuthorization 保留动态配置读取与 Redis 会话替换顺序，构造不启动任务。
func provideGrokAuthorization(proxies egress.ProxyRepository, client account.GrokAuthorizationClient, cfg *config.Config, redisClient *redis.Client) *account.GrokAuthorization {
	options := provider.GrokAuthorizationOptions(proxies, func() bool {
		return cfg != nil && cfg.Gateway.Grok.PasswordAuthEnabled
	})
	authorization := account.NewGrokAuthorization(client, options)
	if redisClient != nil {
		authorization.Store.Stop()
		authorization.Store = rediscache.NewGrokSessionStore(redisClient)
	}
	return authorization
}
