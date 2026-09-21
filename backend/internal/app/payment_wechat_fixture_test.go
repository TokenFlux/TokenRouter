package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// 凭据测试复用实际应用的读取与投影，不在夹具复制微信资格规则。
type paymentOAuthSettingsFixture map[string]string

func (s paymentOAuthSettingsFixture) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func newWeChatPaymentCheckout(values map[string]string, key []byte) *payment.Checkout {
	settings := identity.NewOAuthSettings(paymentOAuthSettingsFixture(values), nil, nil)
	runtime := providePaymentRuntime(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, payment.EncryptionKey(key), settings, nil, timezone.NewCalendar(time.UTC))
	return runtime.Checkout
}
