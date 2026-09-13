// 旧后台构造器只完成参数投影，S15/S16 清理。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/redis/go-redis/v9"

	time "time"
)

type OpsScheduledReportService = ops.OpsScheduledReportService
type opsScheduledReport = ops.CompatOpsScheduledReport

func opsScheduledReportDeliverySourceID(report *opsScheduledReport) string {
	return ops.CompatOpsScheduledReportDeliverySourceID(report)
}

func opsScheduledReportLocalizedEmailVariables(report *opsScheduledReport, now time.Time, locale string) map[string]string {
	return ops.CompatOpsScheduledReportLocalizedEmailVariables(report, now, locale)
}

func opsSummaryReportEmailVariables(report *opsScheduledReport, now time.Time, overview *OpsDashboardOverview, locale string) map[string]string {
	return ops.CompatOpsSummaryReportEmailVariables(report, now, overview, locale)
}
func formatOpsReportInteger(value int64) string { return ops.CompatFormatOpsReportInteger(value) }

func NewOpsScheduledReportService(s *OpsService, user *UserService, email *EmailService, r *redis.Client, cfg *config.Config) *OpsScheduledReportService {
	var u ops.AdminReader
	if user != nil {
		u = legacyOpsAdmin{user}
	}
	return ops.NewOpsScheduledReportService(s, u, LegacyOpsEmail(email), rediscache.NewRuntime(r), LegacyOpsOptions(cfg))
}
