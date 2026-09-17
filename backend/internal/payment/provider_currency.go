package payment

import "strings"

// ProviderConfigCurrency 保持原渠道币种解析和 CNY 回退。
func ProviderConfigCurrency(providerKey string, cfg map[string]string) string {
	switch strings.TrimSpace(providerKey) {
	case TypeStripe, TypeAirwallex:
		currency, err := NormalizePaymentCurrency(cfg["currency"])
		if err == nil {
			return currency
		}
	}
	return DefaultPaymentCurrency
}
