package app

import (
	"context"
	"database/sql"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/config"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/uuid"
	"math/rand/v2"
	"time"
)

// provideOllamaUsage 直接绑定唯一账号存储、加密器、动态设置与供应商执行句柄。
func provideOllamaUsage(store *accountpostgres.AccountStore, upstream service.HTTPUpstream, settings *service.SettingService, cipher service.SecretEncryptor, cfg *config.Config, leader service.LeaderLockCache, db *sql.DB) *service.OllamaCloudUsageService {
	execution := service.NewOllamaUsageExecution(upstream)
	var advisory func(context.Context, string) (func(), bool)
	if db != nil {
		advisory = func(ctx context.Context, key string) (func(), bool) {
			return postgresinfra.TryAcquireDBAdvisoryLock(ctx, db, postgresinfra.HashAdvisoryLockID(key))
		}
	}
	options := account.OllamaUsageOptions{EncryptionKeyConfigured: cfg != nil && cfg.Totp.EncryptionKeyConfigured, Now: time.Now, Jitter: rand.Int64N, InstanceID: uuid.NewString(), Fetch: legacybridge.OllamaUsageFetch{Source: execution}.Fetch, Log: func(format string, args ...any) { logger.LegacyPrintf("service.ollama_cloud_usage", format, args...) }, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return account.AcquireSingletonLease(ctx, leader, advisory, key, owner, ttl)
	}}
	execution.BindCore(account.NewOllamaCloudUsageService(store, settings, cipher, options))
	return execution
}
func provideOllamaUsageCore(source *service.OllamaCloudUsageService) *account.OllamaCloudUsageService {
	return source.Core()
}
