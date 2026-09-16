package middleware

import (
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
)

type APIKeyBillingContext = billingcore.APIKeyBillingContext

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
