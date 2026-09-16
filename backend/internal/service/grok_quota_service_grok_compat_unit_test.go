//go:build unit

// 迁移后的私有测试转接不进入生产构建。
package service

import (
	"log/slog"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func preferBillingObservationStatus(weeklyStatus, monthlyStatus int) int {
	return (&accountcore.GrokQuotaService{Options: accountcore.GrokQuotaOptions{MapStatus: mapUpstreamStatusCode, Warn: slog.Warn}}).PreferBillingObservationStatus(weeklyStatus, monthlyStatus)
}
func isRetryableGrokBillingStatus(statusCode int) bool {
	return xai.IsRetryableBillingStatus(statusCode)
}
