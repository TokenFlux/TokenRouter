package app

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/google/uuid"
)

// provideOllamaUsage 直接绑定唯一账号存储、加密器、动态设置与供应商执行句柄。
func provideOllamaUsage(store *accountpostgres.AccountStore, upstream httpclient.UpstreamTransport, settings *account.RuntimeSettings, cipher identity.SecretEncryptor, cfg *config.Config, leader account.CNMonitorLeader, db *sql.DB) *account.OllamaCloudUsageService {
	var do func(*http.Request, string, int64, int) (*http.Response, error)
	if upstream != nil {
		do = upstream.Do
	}
	var advisory func(context.Context, string) (func(), bool)
	if db != nil {
		advisory = func(ctx context.Context, key string) (func(), bool) {
			return postgresinfra.TryAcquireDBAdvisoryLock(ctx, db, postgresinfra.HashAdvisoryLockID(key))
		}
	}
	options := account.OllamaUsageOptions{EncryptionKeyConfigured: cfg != nil && cfg.Totp.EncryptionKeyConfigured, Now: time.Now, Jitter: rand.Int64N, InstanceID: uuid.NewString(), Fetch: accountprovider.OllamaUsageFetcher(do), Log: func(format string, args ...any) {
		logging.LegacyPrintf("service.ollama_cloud_usage", format, args...)
	}, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return account.AcquireSingletonLease(ctx, leader, advisory, key, owner, ttl)
	}}
	return account.NewOllamaCloudUsageService(store, settings, cipher, options)
}
