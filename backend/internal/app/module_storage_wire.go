//go:build wireinject

package app

import (
	batchpostgres "github.com/TokenFlux/TokenRouter/internal/batchimage/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	creativepostgres "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/google/wire"
)

// moduleStorageProviders 按所有者构造存储，原事务参与者继续使用同一连接。
var moduleStorageProviders = wire.NewSet(
	provideCreativeQueue,
	provideCreativeTransientStore,
	provideBatchQueue,
	provideBatchDownloadLimiter,
	billingpostgres.NewUserSubscriptionRepository,
	wire.Bind(new(billing.UserSubscriptionRepository), new(*billingpostgres.SubscriptionStore)),
	billingpostgres.NewUserGroupRateRepository,
	wire.Bind(new(billing.UserGroupRateRepository), new(*billingpostgres.GroupRateStore)),
	billingpostgres.NewRedeemCodeRepository,
	wire.Bind(new(billing.RedeemCodeRepository), new(*billingpostgres.RedeemStore)),
	billingredis.NewRedeemCache,
	wire.Bind(new(billing.RedeemCache), new(*billingredis.RedeemCache)),
	batchpostgres.NewBatchImageRepository,
	creativepostgres.NewCreativeRunRepository,
	creativepostgres.NewCreativeRunOutboxRepository,
	routingpostgres.NewGroupAvailabilityProbeRepository,
)
