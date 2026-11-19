//go:build wireinject

package app

import (
	"github.com/google/wire"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	keyredis "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"
)

// assembly_apikey_wire.go registers API key service, storage, authentication,
// and invalidation constructors with Wire.
// Gateway keys depend on both persistent records and authentication caches.
// The application graph supplies their administration and HTTP entry points
// together with cache invalidation resources, so handlers and the invalidation
// worker receive the same assembled dependencies.

// apikeyAssemblyProviders supplies key administration, authentication,
// persistence, and invalidation constructors to the application dependency
// graph.
var apikeyAssemblyProviders = wire.NewSet(
	provideKeyAdminHTTP,
	provideKeyHTTP,
	provideKeyAdmin,
	provideKeyStore,
	provideKeys,
	provideKeyInvalidator,
	apikey.ProvideAuthCacheInvalidationWorker,
	keyredis.NewAPIKeyCache,
	keypostgres.NewAuthCacheInvalidationOutboxRepository,
	provideAPIKeyAuth,
)
