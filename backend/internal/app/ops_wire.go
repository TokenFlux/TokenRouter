//go:build wireinject

package app

import (
	opsredis "github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/google/wire"
)

var opsProviders = wire.NewSet(provideOpsOptions, provideOpsRepository, provideOpsService, provideOpsCollector, provideOpsAggregation, provideOpsEvaluator, provideOpsCleanup, provideOpsReports, provideOpsIngress, provideReleaseClient, opsredis.NewUpdateCache, provideReleaseQuery, provideUpdateMaintenance)
