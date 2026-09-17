//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func isSensitiveProviderConfigField(providerKey, fieldName string) bool {
	return payment.ConfigIsSensitiveProviderConfigField(providerKey, fieldName)
}

func validateProviderRequest(providerKey, name, supportedTypes string) error {
	return payment.ConfigValidateProviderRequest(providerKey, name, supportedTypes)
}

func validateEasyPayCustomMethods(config map[string]string, supportedTypes string) error {
	return payment.ConfigValidateEasyPayCustomMethods(config, supportedTypes)
}

func (s *PaymentConfigService) decryptConfig(stored string) (map[string]string, error) {
	return s.paymentCoreConfig().ConfigDecryptConfig(stored)
}
