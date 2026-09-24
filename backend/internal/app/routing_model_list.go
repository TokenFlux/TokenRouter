// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	config "github.com/TokenFlux/TokenRouter/internal/config"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	time "time"
)

// provideRoutingModelList 由 app 投影原 15 秒默认 TTL；缓存无构造启动副作用。
func provideRoutingModelList(repo *accountpostgres.AccountStore, cfg *config.Config) *routing.ModelList {
	return routing.NewModelList(catalogueReader(repo), resolveModelsListCacheTTL(cfg))
}

// resolveModelsListCacheTTL 保留默认十五秒及显式正数配置，投影仅属于组合根。
func resolveModelsListCacheTTL(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Gateway.ModelsListCacheTTLSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(cfg.Gateway.ModelsListCacheTTLSeconds) * time.Second
}
