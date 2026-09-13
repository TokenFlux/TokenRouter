package repository

import (
	"database/sql"
	"errors"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/wire"
)

// ProvideConcurrencyCache 创建并发控制缓存，从配置读取 TTL 参数
// 性能优化：TTL 可配置，支持长时间运行的 LLM 请求场景

// ProvideGitHubReleaseClient 创建 GitHub Release 客户端
// 从配置中读取代理设置，支持国内服务器通过代理访问 GitHub
func ProvideGitHubReleaseClient(cfg *config.Config) service.GitHubReleaseClient {
	return NewGitHubReleaseClient(cfg.Update.ProxyURL, cfg.Security.ProxyFallback.AllowDirectOnError)
}

// ProvidePricingRemoteClient 创建定价数据远程客户端
// 从配置中读取代理设置，支持国内服务器通过代理访问 GitHub 上的定价数据
func ProvidePricingRemoteClient(cfg *config.Config) service.PricingRemoteClient {
	return NewPricingRemoteClient(cfg.Update.ProxyURL, cfg.Security.ProxyFallback.AllowDirectOnError)
}

// ProvideSessionLimitCache 创建会话限制缓存
// 用于 Anthropic OAuth/SetupToken 账号的并发会话数量控制

// ProvideSchedulerCache 创建调度快照缓存，并注入快照分块参数。

// ProvideCreativeManagedKeyRepository 把 API Key 仓储以创作台托管 Key 窄接口注入。
func ProvideCreativeManagedKeyRepository(client *ent.Client, sqlDB *sql.DB) service.CreativeManagedKeyRepository {
	return newAPIKeyRepositoryWithSQL(client, sqlDB)
}

// ProviderSet is the Wire provider set for all repositories
var ProviderSet = wire.NewSet(
	NewGroupAvailabilityProbeRepository,
	NewRedeemCodeRepository,
	NewPromoCodeRepository,
	NewBatchImageRepository,
	NewCreativeRunRepository,
	NewCreativeRunOutboxRepository,
	NewCreativeTransientStore,
	NewCreativeQueue,
	ProvideCreativeManagedKeyRepository,
	NewIdempotencyRepository,
	NewSettingRepository,

	NewUserSubscriptionRepository,
	NewUserGroupRateRepository,
	NewErrorPassthroughRepository,
	NewContentModerationRepository,
	NewAffiliateRepository,
	NewUserPlatformQuotaRepository, // T14: user × platform quota

	// Cache implementations
	NewGatewayCache,
	NewBillingCache,
	NewInternal500CounterCache,

	NewEmailCache,
	NewIdentityCache,
	NewRedeemCache,

	NewGeminiTokenCache,
	NewBatchImageQueue,
	NewBatchImageDownloadLimiter,
	NewLeaderLockCache,
	NewErrorPassthroughCache,
	NewContentModerationHashCache,

	// Encryptors

	// Backup infrastructure
	NewPgDumper,
	NewS3BackupStoreFactory,

	// HTTP service ports (DI Strategy A: return interface directly)
	ProvidePricingRemoteClient,

	NewClaudeUsageFetcher,
	NewClaudeOAuthClient,
	NewHTTPUpstream,
	NewOpenAIOAuthClient,
	NewGrokOAuthClient,
	NewGeminiOAuthClient,
	NewGeminiCliCodeAssistClient,
	NewGeminiDriveClient,

	ProvideSQLDB,
)

// ProvideSQLDB 从 Ent 客户端提取底层的 *sql.DB 连接。
//
// 某些 Repository 需要直接执行原生 SQL（如复杂的批量更新、聚合查询），
// 此时需要访问底层的 sql.DB 而不是通过 Ent ORM。
//
// 设计说明：
//   - Ent 底层使用 sql.DB，通过 Driver 接口可以访问
//   - 这种设计允许在同一事务中混用 Ent 和原生 SQL
//
// 依赖：*ent.Client
// 提供：*sql.DB
func ProvideSQLDB(client *ent.Client) (*sql.DB, error) {
	if client == nil {
		return nil, errors.New("nil ent client")
	}
	// 从 Ent 客户端获取底层驱动
	drv, ok := client.Driver().(*entsql.Driver)
	if !ok {
		return nil, errors.New("ent driver does not expose *sql.DB")
	}
	// 返回驱动持有的 sql.DB 实例
	return drv.DB(), nil
}
