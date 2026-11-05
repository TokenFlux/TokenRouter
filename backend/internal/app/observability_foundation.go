package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	auditHTTP "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	auditpostgres "github.com/TokenFlux/TokenRouter/internal/audit/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	p "github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// observability_foundation.go assembles auditing, pre-aggregation settings, and
// system-log persistence diagnostics. Audit and pre-aggregation use shared
// settings with their own storage and retention rules. Audit redaction combines
// provider credential keys and payment secret fields, while system-log flush
// failures are reported to stderr so the failing persistence path has an
// independent diagnostic sink.

// providePreAggregationHTTP binds usage and Ops aggregation to the
// administration handler.
func providePreAggregationHTTP(settings *p.PreAggregationSettingsService, usage *usage.DashboardAggregationService, ops *ops.OpsAggregationService) *settingshttp.PreAggregationHandler {
	return settingshttp.NewPreAggregationHandler(settings, usage, ops)
}

// provideAuditRepository constructs audit persistence using the shared SQL
// connection pool.
func provideAuditRepository(db *sql.DB) audit.AuditLogRepository {
	return auditpostgres.NewAuditLogRepository(db)
}

// provideAuditService constructs auditing with a callback for current retention
// days. The redactor argument orders Wire construction, while the service reads
// retention settings later using the operation context.
func provideAuditService(repo audit.AuditLogRepository, settings *audit.RetentionSettings, _ *audit.Redactor) *audit.AuditLogService {
	return audit.NewAuditLogService(repo, func(ctx context.Context) int { return settings.GetAuditLogRetentionDays(ctx) })
}

// provideAuditHTTP combines stored audit queries and TOTP authorization for
// administration.
func provideAuditHTTP(s *audit.AuditLogService, totp *identity.TotpService) *auditHTTP.AuditLogHandler {
	return auditHTTP.NewAuditLogHandler(s, totp)
}

// providePreAggregationSettings copies required usage and Ops deployment
// options into shared settings.
func providePreAggregationSettings(repo *settings.Store, cfg *config.Config) *p.PreAggregationSettingsService {
	return p.NewPreAggregationSettingsService(repo, &p.Options{Usage: p.UsageOptions{Enabled: cfg.Usage.Aggregation.Enabled, IntervalSeconds: cfg.Usage.Aggregation.IntervalSeconds, BackfillEnabled: cfg.Usage.Aggregation.BackfillEnabled, BackfillMaxDays: cfg.Usage.Aggregation.BackfillMaxDays}, OpsEnabled: cfg.Ops.Enabled, OpsAggregationEnabled: cfg.Ops.Aggregation.Enabled})
}

// provideSystemLogSink constructs persisted system logs with host lookup
// diagnostics. Hostname errors are passed into the sink, and write failures
// report batch count, failure count, and backoff directly to stderr.
func provideSystemLogSink(repo ops.OpsRepository) *ops.OpsSystemLogSink {
	host, e := os.Hostname()
	return ops.NewOpsSystemLogSink(repo, ops.SystemLogSinkOptions{Host: host, HostError: e, OnWriteFailure: func(err error, batch, failures int, backoff time.Duration) {
		// Persistence failure is already being reported by this callback. Write
		// its diagnostic to stderr and discard output errors because the sink has no reporting result channel here.
		_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"ops system log sink flush failed\" err=%v batch=%d failures=%d backoff=%s\n", time.Now().Format(time.RFC3339Nano), err, batch, failures, backoff)
	}})
}

// provideAuditRedactor combines provider credential and payment configuration
// secret keys for auditing.
func provideAuditRedactor() *audit.Redactor {
	keys := append([]string(nil), provider.SensitiveCredentialKeys...)
	for _, fields := range payment.ConfigProviderSensitiveConfigFields {
		for key := range fields {
			keys = append(keys, key)
		}
	}
	return audit.NewRedactor(keys)
}
