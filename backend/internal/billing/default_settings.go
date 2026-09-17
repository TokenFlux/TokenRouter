package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// DefaultSubscriptionSetting 是默认权益配置的纯值，原 JSON 形状保持不变。
type DefaultSubscriptionSetting struct {
	PlanID int64 `json:"plan_id"`
}

// DefaultPlatformQuotaSetting 是默认权益配置的纯值，原 JSON 形状保持不变。
type DefaultPlatformQuotaSetting struct {
	DailyLimitUSD   *float64 `json:"daily"`
	WeeklyLimitUSD  *float64 `json:"weekly"`
	MonthlyLimitUSD *float64 `json:"monthly"`
}

func ValidateDefaultSubscriptionPlans(ctx context.Context, items []DefaultSubscriptionSetting, lookup func(context.Context, int64) (*SubscriptionPlan, error)) error {
	if len(items) == 0 {
		return nil
	}

	checked := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.PlanID <= 0 {
			continue
		}
		if _, ok := checked[item.PlanID]; ok {
			return ErrDefaultSubPlanDuplicate.WithMetadata(map[string]string{
				"plan_id": strconv.FormatInt(item.PlanID, 10),
			})
		}
		checked[item.PlanID] = struct{}{}
		if lookup == nil {
			continue
		}

		plan, err := lookup(ctx, item.PlanID)
		if err != nil {
			if apperror.IsNotFound(err) {
				return ErrDefaultSubPlanInvalid.WithMetadata(map[string]string{
					"plan_id": strconv.FormatInt(item.PlanID, 10),
				})
			}
			return fmt.Errorf("get default subscription plan %d: %w", item.PlanID, err)
		}
		if plan == nil || plan.ID <= 0 {
			return ErrDefaultSubPlanInvalid.WithMetadata(map[string]string{
				"plan_id": strconv.FormatInt(item.PlanID, 10),
			})
		}
	}

	return nil
}

func ValidateDefaultPlatformQuotaMap(m map[string]*DefaultPlatformQuotaSetting) error {
	for platform, pq := range m {
		if !IsAllowedQuotaPlatform(platform) {
			return apperror.BadRequest("INVALID_DEFAULT_PLATFORM_QUOTA", fmt.Sprintf("unknown platform %q", platform))
		}
		if pq == nil {
			continue
		}
		for _, v := range []*float64{pq.DailyLimitUSD, pq.WeeklyLimitUSD, pq.MonthlyLimitUSD} {
			if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
				return apperror.BadRequest("INVALID_DEFAULT_PLATFORM_QUOTA", "platform quota limit must be a finite non-negative number")
			}
		}
	}
	return nil
}

var ErrDefaultSubPlanInvalid = apperror.BadRequest(
	"DEFAULT_SUBSCRIPTION_PLAN_INVALID",
	"default subscription plan must exist",
)

var ErrDefaultSubPlanDuplicate = apperror.BadRequest(
	"DEFAULT_SUBSCRIPTION_PLAN_DUPLICATE",
	"default subscription plan cannot be duplicated",
)

// ParseDefaultSubscriptions 保持无效载荷与无效套餐 ID 的原过滤语义。
func ParseDefaultSubscriptions(raw string) []DefaultSubscriptionSetting {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var items []DefaultSubscriptionSetting
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}

	normalized := make([]DefaultSubscriptionSetting, 0, len(items))
	for _, item := range items {
		if item.PlanID <= 0 {
			continue
		}
		normalized = append(normalized, item)
	}

	return normalized
}
