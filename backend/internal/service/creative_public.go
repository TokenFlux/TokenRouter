package service

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/creative"

	_ "image/jpeg"

	_ "image/png"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

var ErrCreativeContentBlocked = creative.ErrCreativeContentBlocked

// CreativePublicService 是创作台的用户侧服务：模型列表、任务创建、查询与输出获取。
type CreativePublicService struct {
	Core              *creative.Public
	Repo              CreativeRunRepository
	ApiKeyRepo        CreativeManagedKeyRepository
	UserRepo          CreativeUserRepository
	AccountRepo       CreativeAccountRepository
	GroupRepo         CreativeGroupRepository
	UserGroupRateRepo CreativeUserGroupRateRepository
	Queue             CreativeRunQueue
	Outbox            CreativeRunOutboxRepository
	TransientStore    CreativeTransientStore
	BillingRepo       UsageBillingRepository
	UsageLogRepo      UsageLogRepository
	Pricing           *BillingService
	PricingResolver   *ModelPricingResolver
	Moderation        *ContentModerationService
	AuthCache         APIKeyAuthCacheInvalidator
	Settings          CreativeSettingReader
	Config            *config.Config
}

// CreativeSettingReader 是创作台运行时开关的读取接口，由 SettingService 实现。
type CreativeSettingReader interface {
	// IsCreativeEnabled 读取数据库开关 creative_enabled，缺省视为开启。
	IsCreativeEnabled(ctx context.Context) bool
	// GetCreativeModelSettings 读取创作台模型白名单；缺失或异常时返回空列表。
	GetCreativeModelSettings(ctx context.Context) []CreativeModelSetting
}

// 以下窄接口只依赖真正用到的方法，便于单测替身实现；生产环境由现有仓储实现。
type CreativeUserRepository interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type CreativeGroupRepository interface {
	GetByIDLite(ctx context.Context, id int64) (*Group, error)
	ListActive(ctx context.Context) ([]Group, error)
}

type CreativeAccountRepository interface {
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
}

type CreativeUserGroupRateRepository interface {
	GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error)
}

// CreativeManagedKeyRepository 供应创作台隐藏执行 Key（managed_by = 'creative_studio'）。
type CreativeManagedKeyRepository interface {
	GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*APIKey, error)
	CreateManagedKey(ctx context.Context, key *APIKey) error
}

// CreativeManagedKeyAPIKey 是 ApiKeyRepo 生产实现的组合接口（由 apiKeyRepository 实现）。
type CreativeManagedKeyAPIKey interface {
	CreativeManagedKeyRepository
}

func NewCreativePublicService(
	repo CreativeRunRepository,
	apiKeyRepo CreativeManagedKeyRepository,
	userRepo CreativeUserRepository,
	accountRepo CreativeAccountRepository,
	groupRepo CreativeGroupRepository,
	userGroupRateRepo CreativeUserGroupRateRepository,
	queue CreativeRunQueue,
	transientStore CreativeTransientStore,
	billingRepo UsageBillingRepository,
	usageLogRepo UsageLogRepository,
	pricing *BillingService,
	pricingResolver *ModelPricingResolver,
	moderation *ContentModerationService,
	authCache APIKeyAuthCacheInvalidator,
	settings CreativeSettingReader,
	cfg *config.Config,
	outboxes ...CreativeRunOutboxRepository,
) *CreativePublicService {
	svc := &CreativePublicService{
		Repo:              repo,
		ApiKeyRepo:        apiKeyRepo,
		UserRepo:          userRepo,
		AccountRepo:       accountRepo,
		GroupRepo:         groupRepo,
		UserGroupRateRepo: userGroupRateRepo,
		Queue:             queue,
		TransientStore:    transientStore,
		BillingRepo:       billingRepo,
		UsageLogRepo:      usageLogRepo,
		Pricing:           pricing,
		PricingResolver:   pricingResolver,
		Moderation:        moderation,
		AuthCache:         authCache,
		Settings:          settings,
		Config:            cfg,
	}
	if len(outboxes) > 0 {
		svc.Outbox = outboxes[0]
	}
	return svc
}

// SetOutboxRepository 注入可选的创作台后台补偿仓储，保持测试构造器向后兼容。
func (s *CreativePublicService) SetOutboxRepository(outbox CreativeRunOutboxRepository) {
	if s != nil {
		s.Outbox = outbox
	}
}

// enabled 判定创作台是否可用：进程配置 creative.enabled 为前置条件，
// 再叠加数据库运行时开关 creative_enabled（缺省开启）。
func (s *CreativePublicService) enabled(ctx context.Context) bool {
	if s == nil || s.Repo == nil || s.GroupRepo == nil || s.Config == nil || !s.Config.Creative.Enabled {
		return false
	}
	// 设置服务缺失时无法确认白名单，按 fail-closed 处理。
	if s.Settings == nil {
		return false
	}
	return s.Settings.IsCreativeEnabled(ctx)
}

// ---------------------------------------------------------------------------
// 模型列表
// ---------------------------------------------------------------------------

func (s *CreativePublicService) GetCapabilities(ctx context.Context) *CreativeCapabilitiesResponse {
	return s.nativePublic().GetCapabilities(ctx)
}

func (s *CreativePublicService) ListModels(ctx context.Context, userID int64) (*CreativeModelsResponse, error) {
	return s.nativePublic().ListModels(ctx, userID)
}

func (s *CreativePublicService) ListCreativeModelCandidates(ctx context.Context) ([]CreativeModelCandidate, error) {
	return s.nativePublic().ListCreativeModelCandidates(ctx)
}

// creativeResolvedImageUnitPrice 与批量图片共享按张价解析，token 价卡回退内置单张价。
func (s *CreativePublicService) creativeResolvedImageUnitPrice(ctx context.Context, group *Group, model, imageSize string) (float64, bool) {
	if s == nil || group == nil {
		return 0, false
	}
	resolver := s.PricingResolver
	if resolver == nil && s.Pricing != nil {
		resolver = NewModelPricingResolver(nil, s.Pricing)
	}
	price, err := resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group}, imageSize)
	return price, err == nil
}

// ---------------------------------------------------------------------------
// 任务创建
// ---------------------------------------------------------------------------

func (s *CreativePublicService) CreateRun(ctx context.Context, scope CreativeRunScope, params CreateCreativeRunParamsPublic, idempotencyKey string) (*CreativeRunPublic, error) {
	return s.nativePublic().CreateRun(ctx, scope, params, idempotencyKey)
}

type CreativePricingSnapshot = creative.CreativePricingSnapshot

func (s *CreativePublicService) ensureCreativeManagedKey(ctx context.Context, userID, groupID int64) (*APIKey, error) {
	if s.ApiKeyRepo == nil {
		return nil, errors.New("creative managed key repository is not configured")
	}
	prefix := ""
	if s.Config != nil {
		prefix = s.Config.Default.APIKeyPrefix
	}
	key, err := (apikey.ManagedKeys{Store: creativeManagedKeys{s.ApiKeyRepo}, Prefix: prefix, ManagedBy: CreativeManagedBy, NamePrefix: "creative-studio"}).Ensure(ctx, userID, groupID)
	return APIKeyFromView(key), err
}

// ---------------------------------------------------------------------------
// 查询 / 输出
// ---------------------------------------------------------------------------

func (s *CreativePublicService) GetRun(ctx context.Context, scope CreativeRunScope, runID string) (*CreativeRunPublic, error) {
	return s.nativeQueries().GetRun(ctx, scope, runID)
}

func (s *CreativePublicService) ListRuns(ctx context.Context, scope CreativeRunScope, filter CreativeRunFilter) (*CreativeListRunsResponse, error) {
	return s.nativeQueries().ListRuns(ctx, scope, filter)
}

type CreativeOutputContent = creative.CreativeOutputContent

func (s *CreativePublicService) GetOutputContent(ctx context.Context, scope CreativeRunScope, runID string, outputIndex int) (*CreativeOutputContent, error) {
	return s.nativeQueries().GetOutputContent(ctx, scope, runID, outputIndex)
}

func (s *CreativePublicService) AckOutput(ctx context.Context, scope CreativeRunScope, runID string, outputIndex int) error {
	return s.nativeQueries().AckOutput(ctx, scope, runID, outputIndex)
}

// ---------------------------------------------------------------------------
// worker 面向的结算方法（第二阶段 worker runtime 调用，本阶段实现并保证幂等）
// ---------------------------------------------------------------------------

func (s *CreativePublicService) MarkRunning(ctx context.Context, runID string, accountID int64) error {
	return s.nativeResults().MarkRunning(ctx, runID, accountID)
}

// CreativeOutputResult 是 worker 上报的单个输出结果。
type CreativeOutputResult = creative.ProviderOutput

func (s *CreativePublicService) SucceedRun(ctx context.Context, runID string, accountID int64, results []CreativeOutputResult) (*CreativeRunPublic, error) {
	return s.nativeResults().SucceedRun(ctx, runID, accountID, results)
}

func (s *CreativePublicService) SettleRun(ctx context.Context, runID string) error {
	return s.nativeResults().SettleRun(ctx, runID)
}

func (s *CreativePublicService) ReleaseRun(ctx context.Context, runID string) error {
	return s.nativeResults().ReleaseRun(ctx, runID)
}

func (s *CreativePublicService) FailRun(ctx context.Context, runID, errorCode, errorMessage string) error {
	return s.nativeResults().FailRun(ctx, runID, errorCode, errorMessage)
}

func (s *CreativePublicService) CancelRunByWorker(ctx context.Context, runID string) error {
	return s.nativeResults().CancelRunByWorker(ctx, runID)
}

func (s *CreativePublicService) MarkResultLost(ctx context.Context, runID string, providerSucceeded bool) error {
	return s.nativeResults().MarkResultLost(ctx, runID, providerSucceeded)
}

// ---------------------------------------------------------------------------
// 配置访问 helper
// ---------------------------------------------------------------------------

func (s *CreativePublicService) MaxAssetBytes() int64 { return s.nativePublic().MaxAssetBytes() }

func (s *CreativePublicService) MaxTotalInputBytes() int64 {
	return s.nativePublic().MaxTotalInputBytes()
}

func (s *CreativePublicService) transientTTL() time.Duration {
	if s != nil && s.Config != nil && s.Config.Creative.TransientTTLSeconds > 0 {
		return time.Duration(s.Config.Creative.TransientTTLSeconds) * time.Second
	}
	return 30 * time.Minute
}

// BindCreativeCore 仅在 app 构造阶段调用，之后所有兼容入口委托同一实例。
func (s *CreativePublicService) BindCreativeCore() *creative.Public {
	if s.Core == nil {
		s.Core = s.nativePublic()
	}
	return s.Core
}
