package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func provideS13BatchRegistry(cfg *config.Config) *service.BatchImageProviderRegistry {
	return service.NewBatchImageProviderRegistryFromConfig(cfg)
}
func provideS13CreativeHTTP(s *service.CreativePublicService, activity *taskRequestActivity) *handler.CreativeHandler {
	h := creativehttp.NewCreativeHandler(s.Core)
	h.BindActivity(activity.Enter)
	return h
}
func provideS13BatchHTTP(s *service.BatchImagePublicService, d *service.BatchImageDownloadService, c *service.BatchImageCleanupService, activity *taskRequestActivity) *handler.BatchImageHandler {
	h := batchhttp.NewBatchImageHandler(s.Core, d.Core, c.Core, handler.BatchImageAccessPorts())
	h.BindActivity(activity.Enter)
	return h
}

// provideS13CreativePublic 在 app 构造并固定该任务能力的唯一运行实例。
func provideS13CreativePublic(
	repo service.CreativeRunRepository,
	apiKeyRepo service.CreativeManagedKeyRepository,
	userRepo service.CreativeUserRepository,
	accountRepo service.CreativeAccountRepository,
	groupRepo service.CreativeGroupRepository,
	userGroupRateRepo service.CreativeUserGroupRateRepository,
	queue service.CreativeRunQueue,
	transientStore service.CreativeTransientStore,
	billingRepo service.UsageBillingRepository,
	usageLogRepo service.UsageLogRepository,
	pricing *service.BillingService,
	pricingResolver *service.ModelPricingResolver,
	moderation *service.ContentModerationService,
	authCache service.APIKeyAuthCacheInvalidator,
	settings service.CreativeSettingReader,
	cfg *config.Config,
	outboxes ...service.CreativeRunOutboxRepository,
) *service.CreativePublicService {
	value := service.NewCreativePublicService(repo, apiKeyRepo, userRepo, accountRepo, groupRepo, userGroupRateRepo, queue, transientStore, billingRepo, usageLogRepo, pricing, pricingResolver, moderation, authCache, settings, cfg, outboxes...)
	core := value.BindCreativeCore()
	core.Now = time.Now
	core.Results.Now = time.Now
	return value
}

// provideS13BatchPublic 在 app 构造并固定该任务能力的唯一运行实例。
func provideS13BatchPublic(repo service.BatchImageRepository, accountRepo service.AccountRepository, channelService *service.ChannelService, groupRepo service.GroupRepository, userGroupRateRepo service.UserGroupRateRepository, queue service.BatchImageQueue, pricing *service.BatchImageModelPricingResolver, billingRepo service.UsageBillingRepository, authCache service.APIKeyAuthCacheInvalidator, cfg *config.Config, registry *service.BatchImageProviderRegistry) *service.BatchImagePublicService {
	value := service.NewBatchImagePublicService(repo, accountRepo, channelService, groupRepo, userGroupRateRepo, queue, pricing, billingRepo, authCache, cfg, registry)
	value.BindBatchCore().Now = time.Now
	return value
}

// provideS13BatchDownload 在 app 构造并固定该任务能力的唯一运行实例。
func provideS13BatchDownload(repo service.BatchImageRepository, accountRepo service.AccountRepository, limiter service.BatchImageDownloadLimiter, cfg *config.Config, registry *service.BatchImageProviderRegistry) *service.BatchImageDownloadService {
	value := service.NewBatchImageDownloadService(repo, accountRepo, limiter, cfg, registry)
	value.BindDownloadCore()
	return value
}

// provideS13BatchCleanup 在 app 构造并固定该任务能力的唯一运行实例。
func provideS13BatchCleanup(repo service.BatchImageRepository, accountRepo service.AccountRepository, cfg *config.Config, registry *service.BatchImageProviderRegistry) *service.BatchImageCleanupService {
	value := service.NewBatchImageCleanupService(repo, accountRepo, cfg, registry)
	value.BindCleanupCore().Now = time.Now
	return value
}

// provideS13BatchRuntime 在 app 构造并固定该任务能力的唯一运行实例。
func provideS13BatchRuntime(
	repo service.BatchImageRepository,
	accountRepo service.AccountRepository,
	queue service.BatchImageQueue,
	billingRepo service.UsageBillingRepository,
	usageLogRepo service.UsageLogRepository,
	pricing *service.BatchImageModelPricingResolver,
	authCache service.APIKeyAuthCacheInvalidator,
	cfg *config.Config,
	registry *service.BatchImageProviderRegistry,
) *service.BatchImageWorkerRuntime {
	value := service.ProvideBatchImageWorkerRuntime(repo, accountRepo, queue, billingRepo, usageLogRepo, pricing, authCache, cfg, registry)
	return value
}

// taskRequestActivity 等待提交、下载及管理请求结束，再停止 task worker 和共享存储。
type taskRequestActivity struct{ *lifecycle.Operations }

func provideS13TaskActivity(manager *lifecycle.Manager) *taskRequestActivity {
	activity := &taskRequestActivity{lifecycle.NewOperations("TaskRequestsAndDownloads")}
	manager.Register(lifecycle.Hook{Name: "TaskRequestsAndDownloads", StopOrder: 16, Stop: activity.StopContext})
	return activity
}
