package service

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// BatchImagePriceInput 明确传递计费模型、分组和尺寸；不依赖供应商模型名猜价格。
type BatchImagePriceInput struct {
	Model     string
	GroupID   *int64
	Group     *Group
	ImageSize string
}

type BatchImagePricingResolver interface {
	BatchImageUnitPrice(ctx context.Context, input BatchImagePriceInput) (float64, error)
}

type BatchImageModelPricingResolver struct {
	Resolver  *ModelPricingResolver
	GroupRepo BatchImageGroupPricingRepository
}

func (r *BatchImageModelPricingResolver) BatchImageUnitPrice(ctx context.Context, input BatchImagePriceInput) (float64, error) {
	if r == nil || r.Resolver == nil {
		return 0, ErrBatchImageSettlementPricingMissing
	}
	group := input.Group
	if group == nil && input.GroupID != nil && r.GroupRepo != nil {
		var err error
		group, err = r.GroupRepo.GetByIDLite(ctx, *input.GroupID)
		if err != nil {
			return 0, err
		}
	}
	price, err := r.Resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: input.Model, GroupID: input.GroupID, Group: group}, input.ImageSize)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBatchImageSettlementPricingMissing, err)
	}
	return price, nil
}

type BatchImageSettlementService struct {
	Repo         BatchImageRepository
	BillingRepo  UsageBillingRepository
	UsageLogRepo UsageLogRepository
	Pricing      BatchImagePricingResolver
	AuthCache    APIKeyAuthCacheInvalidator
	Config       *config.Config
}

type BatchImageSettlementResult = batchimage.BatchImageSettlementResult

func (s *BatchImageSettlementService) Settle(ctx context.Context, batchID string) (*BatchImageSettlementResult, error) {
	return s.nativeSettlement().Settle(ctx, batchID)
}

func BatchImageSettlementRequestID(batchID string) string {
	return batchimage.BatchImageSettlementRequestID(batchID)
}

func BuildBatchImageSettlementManifestHash(job *BatchImageJob) string {
	return batchimage.BuildBatchImageSettlementManifestHash(job)
}

type BatchImagePipelineProcessor struct {
	ProviderProcessor *BatchImageProviderProcessor
	SettlementService *BatchImageSettlementService
	RetryDelay        time.Duration
}

func (p *BatchImagePipelineProcessor) Process(ctx context.Context, batchID string) (BatchImageProcessResult, error) {
	if p == nil {
		return (&batchimage.PipelineProcessor{}).Process(ctx, batchID)
	}
	return (&batchimage.PipelineProcessor{ProviderProcessor: p.ProviderProcessor.nativeProcessor(), SettlementService: p.SettlementService.nativeSettlement(), RetryDelay: p.RetryDelay}).Process(ctx, batchID)
}
