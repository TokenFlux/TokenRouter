package service

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	batchImageHoldRequestPrefix    = "batch_image_hold:"
	batchImageCaptureRequestPrefix = "batch_image_capture:"
	batchImageReleaseRequestPrefix = "batch_image_release:"
)

func BatchImageHoldRequestID(batchID string) string {
	return batchImageHoldRequestPrefix + strings.TrimSpace(batchID)
}

func BatchImageCaptureRequestID(batchID string) string {
	return batchImageCaptureRequestPrefix + strings.TrimSpace(batchID)
}

func BatchImageReleaseRequestID(batchID string) string {
	return batchImageReleaseRequestPrefix + strings.TrimSpace(batchID)
}

// batchImageBillingUserID 返回余额实际归属用户，兼容旧任务未写付款人的情况。
func batchImageBillingUserID(job *BatchImageJob) int64 {
	if job == nil {
		return 0
	}
	if job.BillingUserID > 0 {
		return job.BillingUserID
	}
	return job.UserID
}

func buildBatchImageHoldCommand(job *BatchImageJob, requestID string, actualAmount float64, payloadHash string) (*BatchImageBalanceHoldCommand, error) {
	if job == nil {
		return nil, ErrBatchImageBillingHoldFailed
	}
	if job.APIKeyID == nil || *job.APIKeyID <= 0 {
		return nil, ErrBatchImageSettlementMissingAPIKeyID
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	if holdAmount < 0 {
		holdAmount = 0
	}
	if actualAmount < 0 {
		actualAmount = 0
	}
	billingUserID := batchImageBillingUserID(job)
	command := &BatchImageBalanceHoldCommand{
		RequestID:                   requestID,
		APIKeyID:                    *job.APIKeyID,
		UserID:                      billingUserID,
		ActorUserID:                 job.UserID,
		TeamID:                      job.TeamID,
		BatchID:                     job.BatchID,
		APIKeyBillingMode:           job.BillingMode,
		PreferredSubscriptionID:     batchImageCloneInt64Ptr(job.PreferredSubscriptionID),
		HoldAmount:                  holdAmount,
		ActualAmount:                actualAmount,
		BalanceHoldAmount:           job.BalanceHoldAmount,
		SubscriptionHoldAllocations: cloneBillingAllocations(job.SubscriptionHoldAllocations),
		AllowanceReserved:           job.AllowanceReserved,
		ReservedAt:                  job.CreatedAt,
		RequestPayloadHash:          strings.TrimSpace(payloadHash),
	}
	if job.PricingSnapshotVersion >= 2 {
		scale := math.Max(job.AccountRateMultiplier, 0) * math.Max(job.HoldMultiplier, 0)
		settlementScale := 0.0
		if job.HoldMultiplier > 0 {
			settlementScale = math.Max(job.BatchDiscountMultiplier, 0) / job.HoldMultiplier
		}
		command.PricingSnapshotVersion = job.PricingSnapshotVersion
		command.BaseAmountUSD = math.Max(job.BaseUnitPrice, 0) * float64(max(job.ItemCount, 0))
		command.ActualBaseAmountUSD = math.Max(actualAmount, 0)
		command.ActualAmount = 0
		command.SubscriptionRateMultiplier = math.Max(job.SubscriptionRateMultiplier, 0) * scale
		command.SubscriptionRateMultiplierScale = scale
		command.BalanceRateMultiplier = math.Max(job.BalanceRateMultiplier, 0) * scale
		command.SettlementRateScale = settlementScale
		command.DisablePlanGroupRateMultiplier = !job.PlanGroupRateEnabled
	}
	return command, nil
}

func reserveBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, groupID *int64, payloadHash string) error {
	if repo == nil {
		return ErrBatchImageBillingHoldFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageHoldRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0 {
		return nil
	}
	cmd.GroupID = batchImageCloneInt64Ptr(groupID)
	result, err := repo.ReserveBatchImageBalance(ctx, cmd)
	if err != nil {
		if errors.Is(err, ErrBatchImageInsufficientBalance) {
			return ErrBatchImageInsufficientBalance
		}
		// 指定订阅是严格资金来源，冻结阶段的状态变化需要原样返回给调用方。
		if errors.Is(err, ErrPreferredSubscriptionInvalid) || errors.Is(err, ErrPreferredSubscriptionGroup) || errors.Is(err, ErrPreferredSubscriptionInsufficient) {
			return err
		}
		if errors.Is(err, ErrAPIKeyQuotaExhausted) || errors.Is(err, ErrAPIKeyRateLimit5hExceeded) || errors.Is(err, ErrAPIKeyRateLimit1dExceeded) || errors.Is(err, ErrAPIKeyRateLimit7dExceeded) ||
			errors.Is(err, ErrTeamMemberDailyExceeded) || errors.Is(err, ErrTeamMemberWeeklyExceeded) || errors.Is(err, ErrTeamMemberMonthlyExceeded) {
			return err
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	if result != nil {
		job.BalanceHoldAmount = result.BalanceAmountUSD
		job.SubscriptionHoldAllocations = batchImageSubscriptionAllocations(result.BillingAllocations)
		if job.PricingSnapshotVersion >= 2 {
			holdAmount := result.HoldAmountUSD
			job.HoldAmount = &holdAmount
			job.EstimatedCost = result.EstimatedAmountUSD
		}
	}
	job.AllowanceReserved = true
	return nil
}

func captureBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, actualAmount float64, payloadHash string) (*BatchImageBalanceHoldResult, error) {
	if repo == nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageCaptureRequestID(job.BatchID), actualAmount, payloadHash)
	if err != nil {
		return nil, err
	}
	result, err := repo.CaptureBatchImageBalance(ctx, cmd)
	if err != nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	job.AllowanceReserved = false
	return result, nil
}

func releaseBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, payloadHash string) error {
	if repo == nil || job == nil {
		return nil
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageReleaseRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := repo.ReleaseBatchImageBalance(ctx, cmd); err != nil {
		// 同一 release request id 出现指纹冲突，说明此前已有一次携带不同
		// payloadHash 的释放成功提交（资金已归还）。视为幂等成功，
		// 避免历史指纹不一致的 job 永远卡在释放失败的毒消息循环里。
		if errors.Is(err, ErrUsageBillingRequestConflict) {
			logger.L().Warn("batch_image.release_fingerprint_conflict_treated_as_released",
				zap.String("batch_id", job.BatchID),
			)
			return nil
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	job.AllowanceReserved = false
	return nil
}

func batchImageSubscriptionAllocations(allocations []domain.BillingAllocation) []domain.BillingAllocation {
	result := make([]domain.BillingAllocation, 0, len(allocations))
	for _, allocation := range allocations {
		if allocation.Type != domain.BillingAllocationTypeSubscription || allocation.AmountUSD <= 0 || allocation.SubscriptionID == nil {
			continue
		}
		result = append(result, cloneBatchImageBillingAllocation(allocation, allocation.AmountUSD))
	}
	return result
}

func batchImageCloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
