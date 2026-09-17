// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
)

var ErrUsageBillingRequestIDRequired = billing.ErrUsageBillingRequestIDRequired

var ErrUsageBillingRequestConflict = billing.ErrUsageBillingRequestConflict

type UsageBillingCommand = billing.UsageBillingCommand

const UsageBillingMonetaryScale = billing.UsageBillingMonetaryScale

func QuantizeUsageBillingAmount(v float64) float64 { return billing.QuantizeUsageBillingAmount(v) }

func HashUsageRequestPayload(payload []byte) string { return billing.HashUsageRequestPayload(payload) }

type AccountQuotaState = billing.AccountQuotaState

type UsageBillingApplyResult = billing.UsageBillingApplyResult

// BatchImageBalanceHoldCommand describes an idempotent balance hold operation.
type BatchImageBalanceHoldCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string
	UserID             int64
	ActorUserID        int64
	TeamID             *int64
	GroupID            *int64
	// APIKeyBillingMode 与 PreferredSubscriptionID 冻结提交时的资金来源，避免任务执行期间切换 Key 配置改变结算对象。
	APIKeyBillingMode       string
	PreferredSubscriptionID *int64
	BatchID                 string
	HoldAmount              float64
	ActualAmount            float64
	// 第二版价格快照按基础金额分配，避免订阅与余额共担时沿用同一个倍率。
	PricingSnapshotVersion          int
	BaseAmountUSD                   float64
	ActualBaseAmountUSD             float64
	SubscriptionRateMultiplier      float64
	SubscriptionRateMultiplierScale float64
	BalanceRateMultiplier           float64
	SettlementRateScale             float64
	DisablePlanGroupRateMultiplier  bool
	// BalanceHoldAmount 和 SubscriptionHoldAllocations 是提交时持久化的资金预占快照。
	// 两者都为空时按旧任务处理，视为 HoldAmount 全部来自余额冻结。
	BalanceHoldAmount           float64
	SubscriptionHoldAllocations []domain.BillingAllocation
	// AllowanceReserved 区分新任务预记和滚动升级期间的旧任务。
	AllowanceReserved bool
	// CreativeEntity 标记计费实体是创作台任务（creative_runs）而非批量图片作业（batch_image_jobs）。
	// 仅影响任务行上的预记标记与预占快照落表位置，不参与幂等指纹计算。
	CreativeEntity bool
	// ReservedAt 用于只回退仍属于原窗口的预记额度。
	ReservedAt time.Time
}

type BatchImageBalanceHoldResult = billing.TaskFundsResult

type BatchImageBillingCapturePlan = billing.TaskCapturePlan

func EffectiveBatchImageBalanceHoldAmount(cmd *BatchImageBalanceHoldCommand) float64 {
	return billing.EffectiveTaskBalanceHoldAmount(cmd.BillingCommand())
}

func TotalBatchImageHoldAmount(cmd *BatchImageBalanceHoldCommand) float64 {
	return billing.TotalTaskHoldAmount(cmd.BillingCommand())
}

func PlanBatchImageBillingCapture(cmd *BatchImageBalanceHoldCommand) (*BatchImageBillingCapturePlan, error) {
	return billing.PlanTaskCapture(cmd.BillingCommand())
}

type UsageBillingRepository interface {
	Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error)
	ReserveBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	CaptureBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	ReleaseBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
}

// BillingCommand 将旧任务命令投影为通用资金动作，S13 清理旧入口。
func (c *BatchImageBalanceHoldCommand) BillingCommand() *billing.TaskFundsCommand {
	if c == nil {
		return nil
	}
	reference := batchimage.FundingReference(c.BatchID)
	if c.CreativeEntity {
		reference = creative.FundingReference(c.BatchID)
	}
	return &billing.TaskFundsCommand{Task: reference,
		RequestID:                       c.RequestID,
		APIKeyID:                        c.APIKeyID,
		RequestFingerprint:              c.RequestFingerprint,
		RequestPayloadHash:              c.RequestPayloadHash,
		UserID:                          c.UserID,
		ActorUserID:                     c.ActorUserID,
		TeamID:                          c.TeamID,
		GroupID:                         c.GroupID,
		APIKeyBillingMode:               c.APIKeyBillingMode,
		PreferredSubscriptionID:         c.PreferredSubscriptionID,
		HoldAmount:                      c.HoldAmount,
		ActualAmount:                    c.ActualAmount,
		PricingSnapshotVersion:          c.PricingSnapshotVersion,
		BaseAmountUSD:                   c.BaseAmountUSD,
		ActualBaseAmountUSD:             c.ActualBaseAmountUSD,
		SubscriptionRateMultiplier:      c.SubscriptionRateMultiplier,
		SubscriptionRateMultiplierScale: c.SubscriptionRateMultiplierScale,
		BalanceRateMultiplier:           c.BalanceRateMultiplier,
		SettlementRateScale:             c.SettlementRateScale,
		DisablePlanGroupRateMultiplier:  c.DisablePlanGroupRateMultiplier,
		BalanceHoldAmount:               c.BalanceHoldAmount,
		SubscriptionHoldAllocations:     c.SubscriptionHoldAllocations,
		AllowanceReserved:               c.AllowanceReserved,
		ReservedAt:                      c.ReservedAt,
	}
}

// Normalize 复用资金核心规范化，并把兼容字段写回原命令。
func (c *BatchImageBalanceHoldCommand) Normalize() {
	if c == nil {
		return
	}
	cmd := c.BillingCommand()
	cmd.Normalize()
	c.BatchID = cmd.Task.ID
	c.RequestID = cmd.RequestID
	c.APIKeyID = cmd.APIKeyID
	c.RequestFingerprint = cmd.RequestFingerprint
	c.RequestPayloadHash = cmd.RequestPayloadHash
	c.UserID = cmd.UserID
	c.ActorUserID = cmd.ActorUserID
	c.TeamID = cmd.TeamID
	c.GroupID = cmd.GroupID
	c.APIKeyBillingMode = cmd.APIKeyBillingMode
	c.PreferredSubscriptionID = cmd.PreferredSubscriptionID
	c.HoldAmount = cmd.HoldAmount
	c.ActualAmount = cmd.ActualAmount
	c.PricingSnapshotVersion = cmd.PricingSnapshotVersion
	c.BaseAmountUSD = cmd.BaseAmountUSD
	c.ActualBaseAmountUSD = cmd.ActualBaseAmountUSD
	c.SubscriptionRateMultiplier = cmd.SubscriptionRateMultiplier
	c.SubscriptionRateMultiplierScale = cmd.SubscriptionRateMultiplierScale
	c.BalanceRateMultiplier = cmd.BalanceRateMultiplier
	c.SettlementRateScale = cmd.SettlementRateScale
	c.DisablePlanGroupRateMultiplier = cmd.DisablePlanGroupRateMultiplier
	c.BalanceHoldAmount = cmd.BalanceHoldAmount
	c.SubscriptionHoldAllocations = cmd.SubscriptionHoldAllocations
	c.AllowanceReserved = cmd.AllowanceReserved
	c.ReservedAt = cmd.ReservedAt
}

// UpdateBillingCommand 回写唯一资金实现产生的预占快照，保留旧任务恢复语义。
func (c *BatchImageBalanceHoldCommand) UpdateBillingCommand(cmd *billing.TaskFundsCommand) {
	if c == nil {
		return
	}
	c.BatchID = cmd.Task.ID
	c.RequestID = cmd.RequestID
	c.APIKeyID = cmd.APIKeyID
	c.RequestFingerprint = cmd.RequestFingerprint
	c.RequestPayloadHash = cmd.RequestPayloadHash
	c.UserID = cmd.UserID
	c.ActorUserID = cmd.ActorUserID
	c.TeamID = cmd.TeamID
	c.GroupID = cmd.GroupID
	c.APIKeyBillingMode = cmd.APIKeyBillingMode
	c.PreferredSubscriptionID = cmd.PreferredSubscriptionID
	c.HoldAmount = cmd.HoldAmount
	c.ActualAmount = cmd.ActualAmount
	c.PricingSnapshotVersion = cmd.PricingSnapshotVersion
	c.BaseAmountUSD = cmd.BaseAmountUSD
	c.ActualBaseAmountUSD = cmd.ActualBaseAmountUSD
	c.SubscriptionRateMultiplier = cmd.SubscriptionRateMultiplier
	c.SubscriptionRateMultiplierScale = cmd.SubscriptionRateMultiplierScale
	c.BalanceRateMultiplier = cmd.BalanceRateMultiplier
	c.SettlementRateScale = cmd.SettlementRateScale
	c.DisablePlanGroupRateMultiplier = cmd.DisablePlanGroupRateMultiplier
	c.BalanceHoldAmount = cmd.BalanceHoldAmount
	c.SubscriptionHoldAllocations = cmd.SubscriptionHoldAllocations
	c.AllowanceReserved = cmd.AllowanceReserved
	c.ReservedAt = cmd.ReservedAt
}
