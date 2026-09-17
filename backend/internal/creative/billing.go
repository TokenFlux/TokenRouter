package creative

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// 创作台计费请求 ID 前缀：全部经由 usage_billing_dedup 幂等表去重，
// 同一 runID 的同一操作（hold/capture/release）重试不会产生重复资金动作。
const (
	creativeHoldRequestPrefix       = "creative_hold:"
	creativeCaptureRequestPrefix    = "creative_capture:"
	creativeReleaseRequestPrefix    = "creative_release:"
	creativeSettlementRequestPrefix = "creative_settle:"
)

// creativePricingSnapshotVersion 采用与批量图片第二版一致的按基础金额分配语义。
// 创作台没有批量折扣与账号倍率：scale 固定为 1，hold 与结算同价。
const creativePricingSnapshotVersion = 2

func CreativeHoldRequestID(runID string) string {
	return creativeHoldRequestPrefix + strings.TrimSpace(runID)
}

func CreativeCaptureRequestID(runID string) string {
	return creativeCaptureRequestPrefix + strings.TrimSpace(runID)
}

func CreativeReleaseRequestID(runID string) string {
	return creativeReleaseRequestPrefix + strings.TrimSpace(runID)
}

func CreativeSettlementRequestID(runID string) string {
	return creativeSettlementRequestPrefix + strings.TrimSpace(runID)
}

// BuildHoldCommand 把任务元数据转换为计费预占命令。
// 直接复用 billing.TaskFundsCommand（BatchID 填 runID），不复制计费 SQL。
func BuildHoldCommand(run *CreativeRun, requestID string, actualBaseAmount float64) (*billing.TaskFundsCommand, error) {
	if run == nil {
		return nil, ErrCreativeBillingHoldFailed
	}
	if run.APIKeyID <= 0 {
		return nil, ErrCreativeBillingHoldFailed
	}
	holdAmount := run.EstimatedCost
	if run.HoldAmount != nil {
		holdAmount = *run.HoldAmount
	}
	if holdAmount < 0 {
		holdAmount = 0
	}
	if actualBaseAmount < 0 {
		actualBaseAmount = 0
	}
	groupID := run.GroupID
	cmd := &billing.TaskFundsCommand{
		RequestID:                       requestID,
		APIKeyID:                        run.APIKeyID,
		UserID:                          run.UserID,
		ActorUserID:                     run.UserID,
		GroupID:                         &groupID,
		Task:                            FundingReference(run.RunID),
		APIKeyBillingMode:               billing.APIKeyBillingModeAuto,
		HoldAmount:                      holdAmount,
		ActualAmount:                    0,
		PricingSnapshotVersion:          creativePricingSnapshotVersion,
		BaseAmountUSD:                   math.Max(run.BaseUnitPrice, 0) * float64(max(run.RequestedOutputCount, 0)),
		ActualBaseAmountUSD:             actualBaseAmount,
		SubscriptionRateMultiplier:      math.Max(run.SubscriptionRateMultiplier, 0),
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           math.Max(run.BalanceRateMultiplier, 0),
		SettlementRateScale:             1,
		DisablePlanGroupRateMultiplier:  !run.PlanGroupRateEnabled,
		BalanceHoldAmount:               run.BalanceHoldAmount,
		SubscriptionHoldAllocations:     cloneBillingAllocations(run.SubscriptionHoldAllocations),
		AllowanceReserved:               run.AllowanceReserved,

		ReservedAt:         run.CreatedAt,
		RequestPayloadHash: strings.TrimSpace(run.RequestFingerprint),
	}
	return cmd, nil
}

// Reserve 冻结任务预计费用；返回后 run 上回填混合预占快照。
func (funding Funding) Reserve(ctx context.Context, run *CreativeRun) error {
	if funding.Store == nil {
		return ErrCreativeBillingHoldFailed.WithCause(errors.New("creative billing repository is not configured"))
	}
	if run.EstimatedCost <= 0 {
		// 免费分组无需冻结，直接视为已预占（无资金动作）。
		run.BalanceHoldAmount = 0
		run.SubscriptionHoldAllocations = nil
		return nil
	}
	cmd, err := BuildHoldCommand(run, CreativeHoldRequestID(run.RunID), 0)
	if err != nil {
		return err
	}
	// 预占阶段按新任务预记语义统计 API Key/成员额度，并在任务行上落预记标记；
	// 捕获/释放阶段则使用任务行上持久化的 run.AllowanceReserved。
	cmd.AllowanceReserved = true
	result, err := funding.Store.Reserve(ctx, cmd)
	if err != nil {
		if errors.Is(err, billing.ErrTaskInsufficientBalance) {
			return ErrCreativeInsufficientBalance
		}
		if errors.Is(err, billing.ErrAPIKeyQuotaExhausted) || errors.Is(err, billing.ErrAPIKeyRateLimit5hExceeded) || errors.Is(err, billing.ErrAPIKeyRateLimit1dExceeded) ||
			errors.Is(err, billing.ErrAPIKeyRateLimit7dExceeded) || errors.Is(err, billing.ErrTeamMemberDailyExceeded) || errors.Is(err, billing.ErrTeamMemberWeeklyExceeded) ||
			errors.Is(err, billing.ErrTeamMemberMonthlyExceeded) {
			return err
		}
		return ErrCreativeBillingHoldFailed.WithCause(err)
	}
	if result != nil {
		run.BalanceHoldAmount = result.BalanceAmountUSD
		run.SubscriptionHoldAllocations = batchImageSubscriptionAllocations(result.BillingAllocations)
		holdAmount := result.HoldAmountUSD
		run.HoldAmount = &holdAmount
		run.EstimatedCost = result.EstimatedAmountUSD
	}
	// 预占成功后同步更新内存快照，后续创建失败回滚必须携带真实 allowance 状态。
	run.AllowanceReserved = true
	return nil
}

// Capture 按成功输出数捕获实际费用（幂等，request_id 去重）。
func (funding Funding) Capture(ctx context.Context, run *CreativeRun, successCount int) (*billing.TaskFundsResult, error) {
	if funding.Store == nil {
		return nil, ErrCreativeSettlementBillingFail.WithCause(errors.New("creative billing repository is not configured"))
	}
	actualBase := math.Max(run.BaseUnitPrice, 0) * float64(max(successCount, 0))
	cmd, err := BuildHoldCommand(run, CreativeCaptureRequestID(run.RunID), actualBase)
	if err != nil {
		return nil, err
	}
	result, err := funding.Store.Capture(ctx, cmd)
	if err != nil {
		return nil, ErrCreativeSettlementBillingFail.WithCause(err)
	}
	if result != nil {
		run.ActualCost = &result.ActualAmountUSD
	}
	return result, nil
}

// Release 释放未消耗的冻结（失败/取消/结果丢失路径，幂等）。
// 与批量图片一致：同 request id 的指纹冲突视为已释放，避免毒消息循环。
func (funding Funding) Release(ctx context.Context, run *CreativeRun) error {
	if funding.Store == nil || run == nil {
		return nil
	}
	holdAmount := run.EstimatedCost
	if run.HoldAmount != nil {
		holdAmount = *run.HoldAmount
	}
	if holdAmount <= 0 {
		return nil
	}
	cmd, err := BuildHoldCommand(run, CreativeReleaseRequestID(run.RunID), 0)
	if err != nil {
		return err
	}
	if _, err := funding.Store.Release(ctx, cmd); err != nil {
		if errors.Is(err, billing.ErrUsageBillingRequestConflict) {
			if funding.Observe != nil {
				funding.Observe("creative.release_fingerprint_conflict_treated_as_released", "run_id", run.RunID)
			}
			return nil
		}
		return ErrCreativeBillingHoldFailed.WithCause(err)
	}
	return nil
}

// FundingStore 是任务资金动作端口，生产由唯一 billing.Funds 实现。
type FundingStore interface {
	Reserve(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	Capture(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	Release(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
}
type Funding struct {
	Store   FundingStore
	Observe func(string, ...any)
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
func batchImageSubscriptionAllocations(values []billing.BillingAllocation) []billing.BillingAllocation {
	out := make([]billing.BillingAllocation, 0, len(values))
	for _, v := range values {
		if v.Type == billing.BillingAllocationTypeSubscription && v.AmountUSD > 0 && v.SubscriptionID != nil {
			out = append(out, billing.CloneBillingAllocation(v, v.AmountUSD))
		}
	}
	return out
}
