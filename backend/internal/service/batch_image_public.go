package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

type BatchImageAccountSelectionRepository interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
	ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error)
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
}

type BatchImageGroupPricingRepository interface {
	GetByIDLite(ctx context.Context, id int64) (*Group, error)
}

type BatchImageUserGroupRateRepository = batchimage.BatchImageUserGroupRateRepository

type BatchImageSubmitRequest = batchimage.BatchImageSubmitRequest

type BatchImageSubmitItem = batchimage.BatchImageSubmitItem

type BatchImageReferenceInput = batchimage.BatchImageReferenceInput

type BatchImageOwner = batchimage.BatchImageOwner

type BatchImagePublicService struct {
	Core              *batchimage.Public
	Repo              BatchImageRepository
	AccountRepo       BatchImageAccountSelectionRepository
	ChannelService    *ChannelService
	GroupRepo         BatchImageGroupPricingRepository
	UserGroupRateRepo BatchImageUserGroupRateRepository
	Queue             BatchImageQueue
	ProviderRegistry  *BatchImageProviderRegistry
	Pricing           BatchImagePricingResolver
	BillingRepo       UsageBillingRepository
	AuthCache         APIKeyAuthCacheInvalidator
	Config            *config.Config
}

type BatchImagePricingSnapshot = batchimage.BatchImagePricingSnapshot

type BatchImagePublicBatch = batchimage.BatchImagePublicBatch

type BatchImagePublicItem = batchimage.BatchImagePublicItem

type BatchImagePublicError = batchimage.BatchImagePublicError

type BatchImagePublicItemsResponse = batchimage.BatchImagePublicItemsResponse

type BatchImagePublicListResponse = batchimage.BatchImagePublicListResponse

type BatchImagePublicModel = batchimage.BatchImagePublicModel

type BatchImagePublicModelsResponse = batchimage.BatchImagePublicModelsResponse

type BatchImageJobsQuery = batchimage.BatchImageJobsQuery

type BatchImageItemsQuery = batchimage.BatchImageItemsQuery

func NewBatchImagePublicService(repo BatchImageRepository, accountRepo AccountRepository, channelService *ChannelService, groupRepo GroupRepository, userGroupRateRepo UserGroupRateRepository, queue BatchImageQueue, pricing *BatchImageModelPricingResolver, billingRepo UsageBillingRepository, authCache APIKeyAuthCacheInvalidator, cfg *config.Config, registries ...*BatchImageProviderRegistry) *BatchImagePublicService {
	return &BatchImagePublicService{
		Repo:              repo,
		AccountRepo:       accountRepo,
		ChannelService:    channelService,
		GroupRepo:         groupRepo,
		UserGroupRateRepo: userGroupRateRepo,
		Queue:             queue,
		ProviderRegistry:  batchRegistryFromOptions(cfg, registries),
		Pricing:           pricing,
		BillingRepo:       billingRepo,
		AuthCache:         authCache,
		Config:            cfg,
	}
}

func (s *BatchImagePublicService) Submit(ctx context.Context, owner BatchImageOwner, req BatchImageSubmitRequest, idempotencyKey string) (*BatchImagePublicBatch, error) {
	return s.nativePublic().Submit(ctx, owner, req, idempotencyKey)
}

func (s *BatchImagePublicService) Get(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	return s.nativePublic().Get(ctx, owner, batchID)
}

func (s *BatchImagePublicService) List(ctx context.Context, owner BatchImageOwner, query BatchImageJobsQuery) (*BatchImagePublicListResponse, error) {
	return s.nativePublic().List(ctx, owner, query)
}

func (s *BatchImagePublicService) MarkDownloaded(ctx context.Context, owner BatchImageOwner, batchID string) error {
	return s.nativePublic().MarkDownloaded(ctx, owner, batchID)
}

func (s *BatchImagePublicService) DeleteRecord(ctx context.Context, owner BatchImageOwner, batchID string) error {
	return s.nativePublic().DeleteRecord(ctx, owner, batchID)
}

func (s *BatchImagePublicService) ListModels(ctx context.Context, owner BatchImageOwner) (*BatchImagePublicModelsResponse, error) {
	return s.nativePublic().ListModels(ctx, owner)
}

func (s *BatchImagePublicService) ListItems(ctx context.Context, owner BatchImageOwner, batchID string, query BatchImageItemsQuery) (*BatchImagePublicItemsResponse, error) {
	return s.nativePublic().ListItems(ctx, owner, batchID, query)
}

func (s *BatchImagePublicService) Cancel(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	return s.nativePublic().Cancel(ctx, owner, batchID)
}

func BatchImageJobToPublic(job *BatchImageJob) *BatchImagePublicBatch {
	return batchimage.BatchImageJobToPublic(job)
}

func BatchImageItemToPublic(item *BatchImageItem) BatchImagePublicItem {
	return batchimage.BatchImageItemToPublic(item)
}

func PublicBatchImageStatus(status string) string { return batchimage.PublicBatchImageStatus(status) }

func HashBatchImageSubmitRequest(req BatchImageSubmitRequest) string {
	return batchimage.HashBatchImageSubmitRequest(req)
}

// BindBatchCore 仅在 app 构造阶段调用，之后所有兼容入口委托同一实例。
func (s *BatchImagePublicService) BindBatchCore() *batchimage.Public {
	if s.Core == nil {
		s.Core = s.nativePublic()
	}
	return s.Core
}
