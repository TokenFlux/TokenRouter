package admission

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// KeyLimits 只执行当前 Key 的原有时间及额度判断，不暴露凭据。
type KeyLimits interface {
	IsExpired() bool
	IsQuotaExhausted() bool
}

// SubscriptionValidator 使用同一权益实例维护窗口，保持原查询时点。
type SubscriptionValidator interface {
	ValidateAndCheckLimits(*billing.UserSubscription) (bool, error)
	EnsureWindowMaintenance(context.Context, *billing.UserSubscription) (*billing.UserSubscription, error)
}

type ConsumptionFailureKind string

const (
	KeyQuotaExceeded          ConsumptionFailureKind = "key_quota"
	KeyExpired                ConsumptionFailureKind = "key_expired"
	MaintenanceFailed         ConsumptionFailureKind = "maintenance"
	SubscriptionInvalid       ConsumptionFailureKind = "subscription"
	SubscriptionLimitExceeded ConsumptionFailureKind = "subscription_limit"
	InsufficientBalance       ConsumptionFailureKind = "balance"
)

// ConsumptionFailure 只返回失败阶段；通用与 Google 的文本、状态码仍由 HTTP 决定。
type ConsumptionFailure struct {
	Kind  ConsumptionFailureKind
	Cause error
}

func (e *ConsumptionFailure) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Kind)
}
func (e *ConsumptionFailure) Unwrap() error { return e.Cause }

type ConsumptionInput struct {
	Status       string
	Limits       KeyLimits
	Balance      float64
	Subscription *billing.UserSubscription
}

// CheckConsumption 在资金来源解析之后检查 Key 与权益；不读取请求体或累计 RPM。
func CheckConsumption(ctx context.Context, input ConsumptionInput, subscriptions SubscriptionValidator) (*billing.UserSubscription, *ConsumptionFailure) {
	switch input.Status {
	case apikey.StatusAPIKeyQuotaExhausted:
		return nil, &ConsumptionFailure{Kind: KeyQuotaExceeded}
	case apikey.StatusAPIKeyExpired:
		return nil, &ConsumptionFailure{Kind: KeyExpired}
	}
	if input.Limits.IsExpired() {
		return nil, &ConsumptionFailure{Kind: KeyExpired}
	}
	if input.Limits.IsQuotaExhausted() {
		return nil, &ConsumptionFailure{Kind: KeyQuotaExceeded}
	}
	subscription := input.Subscription
	if subscription != nil {
		needsMaintenance, err := subscriptions.ValidateAndCheckLimits(subscription)
		if needsMaintenance {
			refreshed, maintenanceErr := subscriptions.EnsureWindowMaintenance(ctx, subscription)
			if maintenanceErr != nil {
				return nil, &ConsumptionFailure{Kind: MaintenanceFailed, Cause: maintenanceErr}
			}
			subscription = refreshed
			_, err = subscriptions.ValidateAndCheckLimits(subscription)
		}
		if err != nil {
			kind := SubscriptionInvalid
			if errors.Is(err, billing.ErrDailyLimitExceeded) || errors.Is(err, billing.ErrWeeklyLimitExceeded) || errors.Is(err, billing.ErrMonthlyLimitExceeded) {
				kind = SubscriptionLimitExceeded
			}
			return nil, &ConsumptionFailure{Kind: kind, Cause: err}
		}
		return subscription, nil
	}
	if input.Balance <= 0 {
		return nil, &ConsumptionFailure{Kind: InsufficientBalance}
	}
	return nil, nil
}
