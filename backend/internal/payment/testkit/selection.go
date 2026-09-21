package testkit

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// CaptureLoadBalancer 记录测试请求，不执行渠道选择或读取配置。
type CaptureLoadBalancer struct {
	LastProviderKey string
	LastPaymentType string
}

func (c *CaptureLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	return map[string]string{}, nil
}

func (c *CaptureLoadBalancer) SelectInstance(_ context.Context, providerKey string, paymentType payment.PaymentType, _ payment.Strategy, _ float64) (*payment.InstanceSelection, error) {
	c.LastProviderKey = providerKey
	c.LastPaymentType = paymentType
	return &payment.InstanceSelection{ProviderKey: providerKey, SupportedTypes: paymentType}, nil
}
