package middleware

import (
	"context"
	"errors"

	"github.com/BrandonVee/TokenRouter/internal/service"
)

const (
	apiKeyBillingSourceSubscription = "subscription"
	apiKeyBillingSourceBalance      = "balance"
)

// APIKeyBillingContext 是一次 API Key 请求已解析的资金来源。
// subscription 模式会保留不可用套餐快照，供 /v1/usage 准确呈现状态而不回退余额。
type APIKeyBillingContext struct {
	Mode         string
	Source       string
	Subscription *service.UserSubscription
	Available    bool
}

// resolveAPIKeyBillingContext 统一解析 API Key 的结算来源。
// auto 保留现有的可用订阅优先策略；subscription 和 balance 则绝不发生隐式回退。
func resolveAPIKeyBillingContext(ctx context.Context, apiKey *service.APIKey, subscriptionService *service.SubscriptionService, enforce bool) (*APIKeyBillingContext, error) {
	mode := service.APIKeyEffectiveBillingMode(apiKey)
	result := &APIKeyBillingContext{Mode: mode, Source: apiKeyBillingSourceBalance, Available: true}
	if apiKey == nil || apiKey.User == nil {
		return result, service.ErrPreferredSubscriptionInvalid
	}

	var groupID int64
	if apiKey.GroupID != nil {
		groupID = *apiKey.GroupID
	}

	switch mode {
	case service.APIKeyBillingModeBalance:
		return result, nil
	case service.APIKeyBillingModeAuto:
		if subscriptionService == nil {
			return result, nil
		}
		subscription, _, err := subscriptionService.GetUsableSubscription(ctx, apiKey.User.ID, groupID)
		if err != nil {
			if errors.Is(err, service.ErrSubscriptionNotFound) {
				return result, nil
			}
			return nil, err
		}
		if subscription != nil {
			result.Source = apiKeyBillingSourceSubscription
			result.Subscription = subscription
		}
		return result, nil
	case service.APIKeyBillingModeSubscription:
		result.Source = apiKeyBillingSourceSubscription
		result.Available = false
		if apiKey.PreferredSubscriptionID == nil || *apiKey.PreferredSubscriptionID <= 0 || subscriptionService == nil {
			if enforce {
				return nil, service.ErrPreferredSubscriptionInvalid
			}
			return result, nil
		}
		subscription, err := subscriptionService.GetSubscriptionForAPIKey(ctx, apiKey.User.ID, *apiKey.PreferredSubscriptionID)
		if err != nil {
			if enforce {
				return nil, service.ErrPreferredSubscriptionInvalid
			}
			return result, nil
		}
		result.Subscription = subscription
		// 受限套餐的普通 Key 必须已经解析出最终分组；复合 Key 的模型列表没有单一分组，
		// 由 handler 逐条过滤映射。实际消费请求会在复合选组后携带 GroupID 并走同一校验。
		requiresGroupCoverage := subscription.Plan != nil && len(subscription.Plan.GroupIDs) > 0 && (!apiKey.IsComposite || groupID > 0)
		if requiresGroupCoverage && !service.SubscriptionAllowsGroup(subscription, groupID) {
			if enforce {
				return nil, service.ErrPreferredSubscriptionGroup
			}
			return result, nil
		}
		if subscription.IsEffective() {
			_, validateErr := subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
			result.Available = validateErr == nil
		}
		return result, nil
	default:
		return nil, service.ErrInvalidAPIKeyBillingMode
	}
}

// GetAPIKeyBillingContext 读取中间件保存的结算来源。
func GetAPIKeyBillingContext(c ContextGetter) (*APIKeyBillingContext, bool) {
	if c == nil {
		return nil, false
	}
	value, exists := c.Get(string(ContextKeyAPIKeyBilling))
	if !exists {
		return nil, false
	}
	billing, ok := value.(*APIKeyBillingContext)
	return billing, ok
}

// ContextGetter 让 Gin 上下文读取方法可被轻量测试替代。
type ContextGetter interface {
	Get(key string) (value any, exists bool)
}
