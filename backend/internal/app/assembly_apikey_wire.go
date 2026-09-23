//go:build wireinject

package app

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"

	keyredis "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"

	"github.com/google/wire"
)

// apikey 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var apikeyAssemblyProviders = wire.NewSet(
	provideKeyAdminHTTP,
	provideKeyHTTP,
	provideKeyAdmin,
	provideKeyStore,
	provideKeyRepository,
	provideKeys,
	provideKeyInvalidator,
	apikey.ProvideAuthCacheInvalidationWorker,
	keyredis.NewAPIKeyCache,
	keypostgres.NewAuthCacheInvalidationOutboxRepository,
	provideAPIKeyAuth,
)
