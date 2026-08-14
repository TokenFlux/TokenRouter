package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/shopspring/decimal"
)

var ErrUsageBillingRequestIDRequired = errors.New("usage billing request_id is required")
var ErrUsageBillingRequestConflict = errors.New("usage billing request fingerprint conflict")

// UsageBillingCommand describes one billable request that must be applied at most once.
type UsageBillingCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string
	// APIKeyBillingMode 与 PreferredSubscriptionID 固化请求进入网关时的资金来源选择。
	APIKeyBillingMode       string
	PreferredSubscriptionID *int64

	UserID                          int64
	ActorUserID                     int64
	TeamID                          *int64
	AccountID                       int64
	GroupID                         *int64
	BillableAmountUSD               float64
	BaseAmountUSD                   float64
	SubscriptionRateMultiplier      float64
	SubscriptionRateMultiplierScale float64
	BalanceRateMultiplier           float64
	// 批量预占可关闭套餐倍率覆盖，并要求返回后续结算所需的基础金额明细。
	DisablePlanGroupRateMultiplier bool
	IncludeAllocationPricing       bool
	AccountType                    string
	Model                          string
	ServiceTier                    string
	ReasoningEffort                string
	BillingType                    int8
	InputTokens                    int
	OutputTokens                   int
	CacheCreationTokens            int
	CacheReadTokens                int
	ImageCount                     int
	MediaType                      string

	APIKeyQuotaCost     float64
	APIKeyRateLimitCost float64
	AccountQuotaCost    float64
}

func (c *UsageBillingCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	rawMode := strings.TrimSpace(c.APIKeyBillingMode)
	mode, ok := NormalizeAPIKeyBillingMode(c.APIKeyBillingMode)
	if !ok {
		mode = APIKeyBillingModeAuto
	}
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		// 缺省模式代表升级前的命令，保留旧指纹格式；显式模式才写入新字段。
		fingerprintCommand := *c
		if rawMode == "" {
			fingerprintCommand.APIKeyBillingMode = ""
		} else {
			fingerprintCommand.APIKeyBillingMode = mode
		}
		if mode != APIKeyBillingModeSubscription {
			fingerprintCommand.PreferredSubscriptionID = nil
		}
		c.RequestFingerprint = buildUsageBillingFingerprint(&fingerprintCommand)
	}
	c.APIKeyBillingMode = mode
	if mode != APIKeyBillingModeSubscription {
		c.PreferredSubscriptionID = nil
	}
	// 量化必须在指纹计算之后：指纹是请求幂等键，保持由原始金额派生可以避免
	// 升级前后同一 request_id 的重试算出不同指纹而被判为 fingerprint conflict。
	c.quantizeMonetaryFields()
}

// UsageBillingMonetaryScale 是余额与 API Key 金额计数的规范小数位数，
// 对齐 users.balance / api_keys.quota_used 等 NUMERIC(20,8) 列。
const UsageBillingMonetaryScale = 8

// quantizeMonetaryFields 量化命令中直接写入 NUMERIC(20,8) 计数列的金额。
// BillableAmountUSD 和 BaseAmountUSD 仍保留原始精度，供 10 位精度的订阅结算与用量事实使用；
// 最终余额扣减由仓储层在 SQL 边界单独量化。
//
// 不量化时，同一笔 ActualCost 会在两条方向相反的 SQL 上被 PostgreSQL 分别舍入：
//
//	balance    = balance - $1      // 存剩余额度，舍入的是「减法结果」
//	quota_used = quota_used + $1   // 存累计用量，舍入的是「加法结果」
//
// PostgreSQL 对 NUMERIC 采用 half-away-from-zero。当金额在第 9 位出现 half 边界
// （例：10 个输入 token × 0.00000125 + 5 个输出 token × 0.00001000，再乘分组倍率
// 1.25 = 0.000078125）时：
//
//	balance:    10000 - 0.000078125 = 9999.999921875 → 9999.99992188（delta 0.00007812）
//	quota_used:     0 + 0.000078125 =     0.000078125 →     0.00007813（delta 0.00007813）
//
// 两个 delta 相差 1e-8，且方向相反——余额少扣、Key 配额多记，随请求量线性累积，
// 使余额、API Key 配额与用量记录无法精确对账（需要 epsilon 比较才能勉强吻合）。
//
// 在参数进入 SQL 之前量化一次，两条语句就都拿到已经落在 8 位刻度上的同一个金额，
// 存储阶段不再发生任何舍入，delta 精确相等。
func (c *UsageBillingCommand) quantizeMonetaryFields() {
	c.APIKeyQuotaCost = QuantizeUsageBillingAmount(c.APIKeyQuotaCost)
	c.APIKeyRateLimitCost = QuantizeUsageBillingAmount(c.APIKeyRateLimitCost)
	c.AccountQuotaCost = QuantizeUsageBillingAmount(c.AccountQuotaCost)
}

// QuantizeUsageBillingAmount 把金额舍入到 UsageBillingMonetaryScale 位小数，
// 采用与 PostgreSQL NUMERIC 一致的 half-away-from-zero 规则。
//
// 走 decimal 而不是 math.Round(v*1e8)/1e8：后者在乘除过程中会引入额外的二进制
// 误差，边界值可能被推到错误的一侧。decimal.NewFromFloat 取 float64 的最短十进制
// 表示，正是 PostgreSQL 把 float8 参数转成 numeric 时所用的表示。
func QuantizeUsageBillingAmount(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	quantized, _ := decimal.NewFromFloat(v).Round(UsageBillingMonetaryScale).Float64()
	return quantized
}

func buildUsageBillingFingerprint(c *UsageBillingCommand) string {
	if c == nil {
		return ""
	}
	teamID := int64(0)
	if c.TeamID != nil {
		teamID = *c.TeamID
	}
	preferredSubscriptionID := int64(0)
	if c.PreferredSubscriptionID != nil {
		preferredSubscriptionID = *c.PreferredSubscriptionID
	}
	raw := fmt.Sprintf(
		"%d|%d|%d|%d|%d|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%s|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f",
		c.UserID,
		c.ActorUserID,
		teamID,
		c.AccountID,
		c.APIKeyID,
		strings.TrimSpace(c.AccountType),
		strings.TrimSpace(c.Model),
		strings.TrimSpace(c.ServiceTier),
		strings.TrimSpace(c.ReasoningEffort),
		c.BillingType,
		c.InputTokens,
		c.OutputTokens,
		c.CacheCreationTokens,
		c.CacheReadTokens,
		c.ImageCount,
		strings.TrimSpace(c.MediaType),
		c.BillableAmountUSD,
		c.BaseAmountUSD,
		c.SubscriptionRateMultiplier,
		c.SubscriptionRateMultiplierScale,
		c.BalanceRateMultiplier,
		c.APIKeyQuotaCost,
		c.APIKeyRateLimitCost,
		c.AccountQuotaCost,
	)
	raw += fmt.Sprintf("|%s|%d", c.APIKeyBillingMode, preferredSubscriptionID)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func HashUsageRequestPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// AccountQuotaState holds the post-increment quota state returned by the DB transaction.
// All values are post-update (i.e., already include the increment).
type AccountQuotaState struct {
	TotalUsed   float64
	TotalLimit  float64
	DailyUsed   float64
	DailyLimit  float64
	WeeklyUsed  float64
	WeeklyLimit float64
}

type UsageBillingApplyResult struct {
	Applied                 bool
	APIKeyQuotaExhausted    bool
	NewBalance              *float64           // post-deduction balance (nil = no balance deduction)
	QuotaState              *AccountQuotaState // post-increment quota state (nil = no quota increment)
	SubscriptionAmountUSD   float64
	BalanceAmountUSD        float64
	BillingAllocations      []domain.BillingAllocation
	EffectiveRateMultiplier *float64
}

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
	// ReservedAt 用于只回退仍属于原窗口的预记额度。
	ReservedAt time.Time
}

func (c *BatchImageBalanceHoldCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	c.BatchID = strings.TrimSpace(c.BatchID)
	rawMode := strings.TrimSpace(c.APIKeyBillingMode)
	mode, ok := NormalizeAPIKeyBillingMode(c.APIKeyBillingMode)
	if !ok {
		mode = APIKeyBillingModeAuto
	}
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		// 缺省模式代表升级前的命令，保留旧指纹格式；显式模式才写入新字段。
		fingerprintCommand := *c
		if rawMode == "" {
			fingerprintCommand.APIKeyBillingMode = ""
		} else {
			fingerprintCommand.APIKeyBillingMode = mode
		}
		if mode != APIKeyBillingModeSubscription {
			fingerprintCommand.PreferredSubscriptionID = nil
		}
		c.RequestFingerprint = buildBatchImageBalanceHoldFingerprint(&fingerprintCommand)
	}
	c.APIKeyBillingMode = mode
	if mode != APIKeyBillingModeSubscription {
		c.PreferredSubscriptionID = nil
	}
}

func buildBatchImageBalanceHoldFingerprint(c *BatchImageBalanceHoldCommand) string {
	if c == nil {
		return ""
	}
	teamID := int64(0)
	if c.TeamID != nil {
		teamID = *c.TeamID
	}
	var raw string
	if c.PricingSnapshotVersion >= 2 {
		raw = fmt.Sprintf(
			"%d|%d|%d|%d|%s|%d|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%t|%s",
			c.UserID,
			c.ActorUserID,
			teamID,
			c.APIKeyID,
			strings.TrimSpace(c.BatchID),
			c.PricingSnapshotVersion,
			c.BaseAmountUSD,
			c.ActualBaseAmountUSD,
			c.SubscriptionRateMultiplier,
			c.SubscriptionRateMultiplierScale,
			c.BalanceRateMultiplier,
			c.SettlementRateScale,
			c.DisablePlanGroupRateMultiplier,
			c.ReservedAt.UTC().Format(time.RFC3339Nano),
		)
	} else {
		raw = fmt.Sprintf(
			"%d|%d|%d|%d|%s|%0.10f|%0.10f|%s",
			c.UserID,
			c.ActorUserID,
			teamID,
			c.APIKeyID,
			strings.TrimSpace(c.BatchID),
			c.HoldAmount,
			c.ActualAmount,
			c.ReservedAt.UTC().Format(time.RFC3339Nano),
		)
	}
	if c.PricingSnapshotVersion >= 3 {
		preferredSubscriptionID := int64(0)
		if c.PreferredSubscriptionID != nil {
			preferredSubscriptionID = *c.PreferredSubscriptionID
		}
		raw += fmt.Sprintf("|%s|%d", c.APIKeyBillingMode, preferredSubscriptionID)
	}
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type BatchImageBalanceHoldResult struct {
	Applied               bool
	NewBalance            *float64
	FrozenBalance         *float64
	HoldAmountUSD         float64
	EstimatedAmountUSD    float64
	ActualAmountUSD       float64
	SubscriptionAmountUSD float64
	BalanceAmountUSD      float64
	BillingAllocations    []domain.BillingAllocation
}

// BatchImageBillingCapturePlan 描述批量任务从预占金额收敛到实际金额时的资金拆分。
type BatchImageBillingCapturePlan struct {
	BalanceHoldAmount     float64
	ActualAmountUSD       float64
	SubscriptionAmountUSD float64
	BalanceAmountUSD      float64
	BillingAllocations    []domain.BillingAllocation
	SubscriptionReleases  []domain.BillingAllocation
}

// EffectiveBatchImageBalanceHoldAmount 返回实际冻结余额，并兼容迁移前全额冻结余额的任务。
func EffectiveBatchImageBalanceHoldAmount(cmd *BatchImageBalanceHoldCommand) float64 {
	if cmd == nil {
		return 0
	}
	if cmd.BalanceHoldAmount > 0 || len(cmd.SubscriptionHoldAllocations) > 0 {
		return math.Max(cmd.BalanceHoldAmount, 0)
	}
	return math.Max(cmd.HoldAmount, 0)
}

// TotalBatchImageHoldAmount 返回订阅预占与余额冻结的总和。
func TotalBatchImageHoldAmount(cmd *BatchImageBalanceHoldCommand) float64 {
	if cmd == nil {
		return 0
	}
	total := EffectiveBatchImageBalanceHoldAmount(cmd)
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type == domain.BillingAllocationTypeSubscription && allocation.AmountUSD > 0 {
			total += allocation.AmountUSD
		}
	}
	return total
}

// PlanBatchImageBillingCapture 按订阅优先顺序计算实际结算和应释放的订阅额度。
func PlanBatchImageBillingCapture(cmd *BatchImageBalanceHoldCommand) (*BatchImageBillingCapturePlan, error) {
	plan := &BatchImageBillingCapturePlan{}
	if cmd == nil {
		return plan, nil
	}
	if cmd.PricingSnapshotVersion >= 2 && cmd.BaseAmountUSD > 0 {
		return planBatchImageBaseAmountCapture(cmd)
	}
	actualAmount := math.Max(cmd.ActualAmount, 0)
	remaining := actualAmount
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type != domain.BillingAllocationTypeSubscription || allocation.AmountUSD <= 0 || allocation.SubscriptionID == nil {
			continue
		}
		kept := math.Min(allocation.AmountUSD, remaining)
		if kept > 0 {
			keptAllocation := cloneBatchImageBillingAllocation(allocation, kept)
			plan.BillingAllocations = append(plan.BillingAllocations, keptAllocation)
			plan.SubscriptionAmountUSD += kept
			remaining -= kept
		}
		if released := allocation.AmountUSD - kept; released > batchImageCostEpsilon {
			plan.SubscriptionReleases = append(plan.SubscriptionReleases, cloneBatchImageBillingAllocation(allocation, released))
		}
	}

	plan.BalanceHoldAmount = EffectiveBatchImageBalanceHoldAmount(cmd)
	if remaining-plan.BalanceHoldAmount > batchImageCostEpsilon {
		return nil, ErrBatchImageSettlementCostExceedsHold
	}
	plan.BalanceAmountUSD = math.Min(remaining, plan.BalanceHoldAmount)
	if plan.BalanceAmountUSD > 0 {
		plan.BillingAllocations = append(plan.BillingAllocations, domain.BillingAllocation{
			Type:      domain.BillingAllocationTypeBalance,
			AmountUSD: plan.BalanceAmountUSD,
		})
	}
	plan.ActualAmountUSD = plan.SubscriptionAmountUSD + plan.BalanceAmountUSD
	return plan, nil
}

// planBatchImageBaseAmountCapture 按预占时记录的各来源倍率覆盖实际基础金额。
func planBatchImageBaseAmountCapture(cmd *BatchImageBalanceHoldCommand) (*BatchImageBillingCapturePlan, error) {
	plan := &BatchImageBillingCapturePlan{BalanceHoldAmount: EffectiveBatchImageBalanceHoldAmount(cmd)}
	remainingBase := math.Max(cmd.ActualBaseAmountUSD, 0)
	settlementScale := math.Max(cmd.SettlementRateScale, 0)
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type != domain.BillingAllocationTypeSubscription || allocation.AmountUSD <= 0 || allocation.SubscriptionID == nil {
			continue
		}
		rate := allocation.RateMultiplier * settlementScale
		if rate <= 0 {
			continue
		}
		kept := math.Min(allocation.AmountUSD, remainingBase*rate)
		if kept > 0 {
			keptAllocation := cloneBatchImageBillingAllocation(allocation, kept)
			keptAllocation.BaseAmountUSD = kept / rate
			keptAllocation.RateMultiplier = rate
			plan.BillingAllocations = append(plan.BillingAllocations, keptAllocation)
			plan.SubscriptionAmountUSD += kept
			remainingBase -= kept / rate
		}
		if released := allocation.AmountUSD - kept; released > batchImageCostEpsilon {
			plan.SubscriptionReleases = append(plan.SubscriptionReleases, cloneBatchImageBillingAllocation(allocation, released))
		}
	}

	balanceRate := math.Max(cmd.BalanceRateMultiplier, 0) * settlementScale
	balanceAmount := remainingBase * balanceRate
	if balanceAmount-plan.BalanceHoldAmount > batchImageCostEpsilon {
		return nil, ErrBatchImageSettlementCostExceedsHold
	}
	plan.BalanceAmountUSD = math.Min(balanceAmount, plan.BalanceHoldAmount)
	if plan.BalanceAmountUSD > 0 {
		baseAmount := 0.0
		if balanceRate > 0 {
			baseAmount = plan.BalanceAmountUSD / balanceRate
		}
		plan.BillingAllocations = append(plan.BillingAllocations, domain.BillingAllocation{
			Type:           domain.BillingAllocationTypeBalance,
			AmountUSD:      plan.BalanceAmountUSD,
			BaseAmountUSD:  baseAmount,
			RateMultiplier: balanceRate,
		})
		remainingBase -= baseAmount
	}
	if remainingBase > batchImageCostEpsilon && balanceRate > 0 {
		return nil, ErrBatchImageSettlementCostExceedsHold
	}
	plan.ActualAmountUSD = plan.SubscriptionAmountUSD + plan.BalanceAmountUSD
	return plan, nil
}

func cloneBatchImageBillingAllocation(allocation domain.BillingAllocation, amount float64) domain.BillingAllocation {
	cloned := allocation
	cloned.AmountUSD = amount
	if allocation.SubscriptionID != nil {
		value := *allocation.SubscriptionID
		cloned.SubscriptionID = &value
	}
	if allocation.PlanID != nil {
		value := *allocation.PlanID
		cloned.PlanID = &value
	}
	return cloned
}

type UsageBillingRepository interface {
	Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error)
	ReserveBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	CaptureBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	ReleaseBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
}
