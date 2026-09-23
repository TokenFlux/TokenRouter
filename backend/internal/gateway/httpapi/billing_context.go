package httpapi

import (
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
)

// GetAPIKeyBillingContext 读取中间件保存的结算来源。
func GetAPIKeyBillingContext(c ContextGetter) (*billingcore.APIKeyBillingContext, bool) {
	if c == nil {
		return nil, false
	}
	value, exists := c.Get(string(ContextKeyAPIKeyBilling))
	if !exists {
		return nil, false
	}
	billing, ok := value.(*billingcore.APIKeyBillingContext)
	return billing, ok
}

// ContextGetter 让 Gin 上下文读取方法可被轻量测试替代。
type ContextGetter interface {
	Get(key string) (value any, exists bool)
}

// 资金来源仅保存既有请求投影，不在此重新授权或查询订阅。
const ContextKeyAPIKeyBilling = "api_key_billing"
const ContextKeySubscription = "subscription"
