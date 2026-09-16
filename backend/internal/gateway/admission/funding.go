// Package admission 拥有网关准入的阶段顺序和只读资金来源投影。
package admission

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

const FundingSourceSubscription = "subscription"
const FundingSourceBalance = "balance"

// FundingInput 不包含身份实体、Key 凭据或可修改的共享缓存。
type FundingInput struct {
	Mode                    string
	HasPayer                bool
	PayerID, GroupID        int64
	PreferredSubscriptionID *int64
	IsComposite             bool
}

// SubscriptionReader 保持原查询、校验及 nil 服务降级边界。
type SubscriptionReader interface {
	GetUsableSubscription(context.Context, int64, ...int64) (*billing.UserSubscription, bool, error)
	GetSubscriptionForAPIKey(context.Context, int64, int64) (*billing.UserSubscription, error)
	ValidateAndCheckLimits(*billing.UserSubscription) (bool, error)
}

// resolveAPIKeyBillingContext 统一解析 API Key 的结算来源。
// auto 保留现有的可用订阅优先策略；subscription 和 balance 则绝不发生隐式回退。
func ResolveFunding(ctx context.Context, input FundingInput, subscriptionService SubscriptionReader, enforce bool) (*billing.APIKeyBillingContext, error) {
	mode := input.Mode
	result := &billing.APIKeyBillingContext{Mode: mode, Source: FundingSourceBalance, Available: true}
	if !input.HasPayer {
		return result, billing.ErrPreferredSubscriptionInvalid
	}

	groupID := input.GroupID

	switch mode {
	case billing.APIKeyBillingModeBalance:
		return result, nil
	case billing.APIKeyBillingModeAuto:
		if subscriptionService == nil {
			return result, nil
		}
		subscription, _, err := subscriptionService.GetUsableSubscription(ctx, input.PayerID, groupID)
		if err != nil {
			if errors.Is(err, billing.ErrSubscriptionNotFound) {
				return result, nil
			}
			return nil, err
		}
		if subscription != nil {
			result.Source = FundingSourceSubscription
			result.Subscription = subscription
		}
		return result, nil
	case billing.APIKeyBillingModeSubscription:
		result.Source = FundingSourceSubscription
		result.Available = false
		if input.PreferredSubscriptionID == nil || *input.PreferredSubscriptionID <= 0 || subscriptionService == nil {
			if enforce {
				return nil, billing.ErrPreferredSubscriptionInvalid
			}
			return result, nil
		}
		subscription, err := subscriptionService.GetSubscriptionForAPIKey(ctx, input.PayerID, *input.PreferredSubscriptionID)
		if err != nil {
			if enforce {
				return nil, billing.ErrPreferredSubscriptionInvalid
			}
			return result, nil
		}
		result.Subscription = subscription
		// 受限套餐的普通 Key 必须已经解析出最终分组；复合 Key 的模型列表没有单一分组，
		// 由 handler 逐条过滤映射。实际消费请求会在复合选组后携带 GroupID 并走同一校验。
		requiresGroupCoverage := subscription.Plan != nil && len(subscription.Plan.GroupIDs) > 0 && (!input.IsComposite || groupID > 0)
		if requiresGroupCoverage && !billing.SubscriptionAllowsGroup(subscription, groupID) {
			if enforce {
				return nil, billing.ErrPreferredSubscriptionGroup
			}
			return result, nil
		}
		if subscription.IsEffective() {
			_, validateErr := subscriptionService.ValidateAndCheckLimits(subscription)
			result.Available = validateErr == nil
		}
		return result, nil
	default:
		return nil, apikey.ErrInvalidAPIKeyBillingMode
	}
}

// ResolveFundingFromKey 只投影已认证的请求 Key，不读取凭据或额外存储。
func ResolveFundingFromKey(ctx context.Context, key *apikey.APIKey, reader SubscriptionReader, enforce bool) (*billing.APIKeyBillingContext, error) {
	input := FundingInput{Mode: apikey.APIKeyEffectiveBillingMode(key)}
	if key != nil {
		input.IsComposite = key.IsComposite
		input.PreferredSubscriptionID = key.PreferredSubscriptionID
		if key.GroupID != nil {
			input.GroupID = *key.GroupID
		}
		if key.User != nil {
			input.HasPayer = true
			input.PayerID = key.User.ID
		}
	}
	return ResolveFunding(ctx, input, reader, enforce)
}
