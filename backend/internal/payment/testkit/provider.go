package testkit

import "github.com/TokenFlux/TokenRouter/internal/payment"

// StaticProvider 只声明渠道身份和支付方式；测试未配置的外部调用会直接失败。
type StaticProvider struct {
	payment.Provider
	Key   string
	Types []payment.PaymentType
}

func (p StaticProvider) Name() string                          { return p.Key }
func (p StaticProvider) ProviderKey() string                   { return p.Key }
func (p StaticProvider) SupportedTypes() []payment.PaymentType { return p.Types }
