// app 持有本阶段的唯一审计与预聚合设置实例，旧入口只投影同一实例。
package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	auditHTTP "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	auditpostgres "github.com/TokenFlux/TokenRouter/internal/audit/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	p "github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

func provideAuditRepository(db *sql.DB) audit.AuditLogRepository {
	return auditpostgres.NewAuditLogRepository(db)
}
func provideAuditService(repo audit.AuditLogRepository, settings *service.SettingService) *audit.AuditLogService {
	redactor := audit.NewRedactor(service.LegacyAuditSensitiveKeys())
	service.BindAuditRedactor(redactor)
	return audit.NewAuditLogService(repo, func(ctx context.Context) int { return settings.GetAuditLogRetentionDays(ctx) })
}
func provideAuditHTTP(s *audit.AuditLogService, totp *identity.TotpService) *auditHTTP.AuditLogHandler {
	return auditHTTP.NewAuditLogHandler(s, totp)
}
func providePreAggregationSettings(repo *settings.Store, cfg *config.Config) *p.PreAggregationSettingsService {
	return p.NewPreAggregationSettingsService(repo, &p.Options{Usage: p.UsageOptions{Enabled: cfg.DashboardAgg.Enabled, IntervalSeconds: cfg.DashboardAgg.IntervalSeconds, BackfillEnabled: cfg.DashboardAgg.BackfillEnabled, BackfillMaxDays: cfg.DashboardAgg.BackfillMaxDays}, OpsEnabled: cfg.Ops.Enabled, OpsAggregationEnabled: cfg.Ops.Aggregation.Enabled})
}

func provideSystemLogSink(repo ops.OpsRepository) *ops.OpsSystemLogSink {
	host, e := os.Hostname()
	return ops.NewOpsSystemLogSink(repo, ops.SystemLogSinkOptions{Host: host, HostError: e, OnWriteFailure: func(err error, batch, failures int, backoff time.Duration) {
		_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"ops system log sink flush failed\" err=%v batch=%d failures=%d backoff=%s\n", time.Now().Format(time.RFC3339Nano), err, batch, failures, backoff)
	}})
}
