package batchimage

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
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

// BillingUserID 返回余额实际归属用户，兼容旧任务未写付款人的情况。
func BillingUserID(job *BatchImageJob) int64 {
	if job == nil {
		return 0
	}
	if job.BillingUserID > 0 {
		return job.BillingUserID
	}
	return job.UserID
}

func BuildHoldCommand(job *BatchImageJob, requestID string, actualAmount float64, payloadHash string) (*billing.TaskFundsCommand, error) {
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
	billingUserID := BillingUserID(job)
	command := &billing.TaskFundsCommand{
		RequestID:                   requestID,
		APIKeyID:                    *job.APIKeyID,
		UserID:                      billingUserID,
		ActorUserID:                 job.UserID,
		TeamID:                      job.TeamID,
		Task:                        FundingReference(job.BatchID),
		APIKeyBillingMode:           job.BillingMode,
		PreferredSubscriptionID:     CloneInt64Ptr(job.PreferredSubscriptionID),
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

func (funding Funding) Reserve(ctx context.Context, job *BatchImageJob, groupID *int64, payloadHash string) error {
	if funding.Store == nil {
		return ErrBatchImageBillingHoldFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := BuildHoldCommand(job, BatchImageHoldRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0 {
		return nil
	}
	cmd.GroupID = CloneInt64Ptr(groupID)
	result, err := funding.Store.Reserve(ctx, cmd)
	if err != nil {
		if errors.Is(err, ErrBatchImageInsufficientBalance) {
			return ErrBatchImageInsufficientBalance
		}
		// 指定订阅是严格资金来源，冻结阶段的状态变化需要原样返回给调用方。
		if errors.Is(err, billing.ErrPreferredSubscriptionInvalid) || errors.Is(err, billing.ErrPreferredSubscriptionGroup) || errors.Is(err, billing.ErrPreferredSubscriptionInsufficient) {
			return err
		}
		if errors.Is(err, billing.ErrAPIKeyQuotaExhausted) || errors.Is(err, billing.ErrAPIKeyRateLimit5hExceeded) || errors.Is(err, billing.ErrAPIKeyRateLimit1dExceeded) || errors.Is(err, billing.ErrAPIKeyRateLimit7dExceeded) ||
			errors.Is(err, billing.ErrTeamMemberDailyExceeded) || errors.Is(err, billing.ErrTeamMemberWeeklyExceeded) || errors.Is(err, billing.ErrTeamMemberMonthlyExceeded) {
			return err
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	if result != nil {
		job.BalanceHoldAmount = result.BalanceAmountUSD
		job.SubscriptionHoldAllocations = SubscriptionAllocations(result.BillingAllocations)
		if job.PricingSnapshotVersion >= 2 {
			holdAmount := result.HoldAmountUSD
			job.HoldAmount = &holdAmount
			job.EstimatedCost = result.EstimatedAmountUSD
		}
	}
	job.AllowanceReserved = true
	return nil
}

func (funding Funding) Capture(ctx context.Context, job *BatchImageJob, actualAmount float64, payloadHash string) (*billing.TaskFundsResult, error) {
	if funding.Store == nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := BuildHoldCommand(job, BatchImageCaptureRequestID(job.BatchID), actualAmount, payloadHash)
	if err != nil {
		return nil, err
	}
	result, err := funding.Store.Capture(ctx, cmd)
	if err != nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	job.AllowanceReserved = false
	return result, nil
}

func (funding Funding) Release(ctx context.Context, job *BatchImageJob, payloadHash string) error {
	if funding.Store == nil || job == nil {
		return nil
	}
	cmd, err := BuildHoldCommand(job, BatchImageReleaseRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := funding.Store.Release(ctx, cmd); err != nil {
		// 同一 release request id 出现指纹冲突，说明此前已有一次携带不同
		// payloadHash 的释放成功提交（资金已归还）。视为幂等成功，
		// 避免历史指纹不一致的 job 永远卡在释放失败的毒消息循环里。
		if errors.Is(err, billing.ErrUsageBillingRequestConflict) {
			funding.warn("batch_image.release_fingerprint_conflict_treated_as_released",
				"batch_id", job.BatchID,
			)
			return nil
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	job.AllowanceReserved = false
	return nil
}

func SubscriptionAllocations(allocations []billing.BillingAllocation) []billing.BillingAllocation {
	result := make([]billing.BillingAllocation, 0, len(allocations))
	for _, allocation := range allocations {
		if allocation.Type != billing.BillingAllocationTypeSubscription || allocation.AmountUSD <= 0 || allocation.SubscriptionID == nil {
			continue
		}
		result = append(result, billing.CloneBillingAllocation(allocation, allocation.AmountUSD))
	}
	return result
}

func CloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// FundingStore 由唯一 billing.Funds 提供，同一任务的三个动作复用原幂等标识。
type FundingStore interface {
	Reserve(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	Capture(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	Release(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
}
type Funding struct {
	Store   FundingStore
	Observe func(string, ...any)
}

func (f Funding) warn(event string, values ...any) {
	if f.Observe != nil {
		f.Observe(event, values...)
	}
}

func cloneBillingAllocations(values []billing.BillingAllocation) []billing.BillingAllocation {
	if values == nil {
		return nil
	}
	out := make([]billing.BillingAllocation, len(values))
	for i, v := range values {
		out[i] = billing.CloneBillingAllocation(v, v.AmountUSD)
	}
	return out
}
