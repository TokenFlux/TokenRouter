//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

const (
	// defaultMaxPendingOrders and defaultOrderTimeoutMin are defined in
	// payment_config_service.go alongside other payment configuration defaults.
	defaultPageSize    = 20
	maxPageSize        = 100
	topUsersLimit      = 10
	amountToleranceCNY = payment.ProviderAmountTolerance
)
