// 用户可见支付方式先固定渠道类别，再复用原唯一选择器。
package payment

import (
	"context"
	"fmt"
)

type visibleMethodLoadBalancer struct {
	inner         LoadBalancer
	configService *ConfigService
}

func NewVisibleMethodLoadBalancer(inner LoadBalancer, configService *ConfigService) LoadBalancer {
	if inner == nil || configService == nil || configService.store == nil {
		return inner
	}
	return &visibleMethodLoadBalancer{inner: inner, configService: configService}
}
func (lb *visibleMethodLoadBalancer) GetInstanceConfig(ctx context.Context, instanceID int64) (map[string]string, error) {
	return lb.inner.GetInstanceConfig(ctx, instanceID)
}
func (lb *visibleMethodLoadBalancer) SelectInstance(ctx context.Context, providerKey string, paymentType PaymentType, strategy Strategy, orderAmount float64) (*InstanceSelection, error) {
	visibleMethod := NormalizeVisibleMethod(paymentType)
	if providerKey != "" || (visibleMethod != TypeAlipay && visibleMethod != TypeWxpay) {
		return lb.inner.SelectInstance(ctx, providerKey, paymentType, strategy, orderAmount)
	}

	inst, err := lb.configService.ConfigResolveEnabledVisibleMethodInstance(ctx, visibleMethod)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, fmt.Errorf("visible payment method %s has no enabled provider instance", visibleMethod)
	}
	return lb.inner.SelectInstance(ctx, inst.ProviderKey, paymentType, strategy, orderAmount)
}
