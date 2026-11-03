package composite

import (
	"encoding/json"
	"maps"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/bstats"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	settingvalues "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// read.go interprets stored module settings into a combined administrator
// snapshot.
// One raw settings batch supplies the combined administrator Snapshot, while
// each owning module interprets its keys with the appropriate startup defaults.
// The batch map is copied for later revision checks, and module results provide
// the typed display and update fields. Reading can also publish the resolved
// Grok default model when assembly installs that callback.
//
// Responses WebSocket settings retain their override JSON separately from the
// effective parameters. A failed override resolution selects deployment defaults
// or the current runtime Snapshot, leaving valid JSON available for administrator
// inspection; invalid JSON is replaced with an empty object. Other module
// collections are transferred through their read-result values and keep the
// sharing rules of the Snapshot and its extraction helpers.

// ReadOptions supplies module interpreters, deployment fallback callbacks, and
// optional read publication.
type ReadOptions struct {
	// ResponsesWS supplies deployment defaults and a current effective value when
	// override resolution fails. Nil selects the built-in WebSocket defaults for both paths.
	ResponsesWS *ws.Runtime                // optional Responses WebSocket runtime reader
	OAuth       *identity.OAuthSettings    // identity OAuth settings interpreter
	Gateway     gateway.AdminSettingsRules // gateway administrator read rules
	Scheduler   scheduler.AdminDefaults    // scheduler deployment parameter defaults
	// DefaultBalance and DefaultConcurrency provide startup numeric defaults when
	// the owning billing/identity readers cannot use stored values. Callers install
	// these callbacks before Parse can select their fallback paths.
	DefaultBalance     func() float64 // registration balance fallback reader
	DefaultConcurrency func() int     // registration concurrency fallback reader
	// Forwarded is called before module parsing to supply the current forwarded-IP
	// defaults. It is required for every Parse call and stays valid during that read.
	Forwarded func() runtimeconfig.ForwardedInput // current forwarded-IP deployment reader
	// PublishModel is called synchronously after all fields are parsed. A nil
	// callback skips publication; installed callbacks can update runtime model selection
	// even when Parse was invoked for an administrator GET.
	PublishModel func(string) // optional Grok model publication callback
}

// Parse combines a raw settings batch and module readers into a new Snapshot.
// It calls Forwarded for current defaults, clones the stored map for later
// conditional updates, and transfers parsed module values with their retained
// collection references. Assembly supplies the required readers and numeric callbacks.
//
// Responses overrides are kept beside their effective parameters. Invalid
// resolution selects built-in defaults or the current WebSocket runtime value;
// valid override JSON stays visible even when its contents cannot resolve, while
// invalid JSON becomes {}. The optional PublishModel callback runs after parsing
// so this read can publish its resolved Grok model before the Snapshot is returned.
func Parse(settings map[string]string, options ReadOptions) *Snapshot {
	metrics := bstats.ReadSettings(settings)
	prior := options.Forwarded()
	forwarded := runtimeconfig.ReadForwardedSettings(settings, prior)
	result := &Snapshot{
		BStatsEnabled:               metrics.Enabled,
		BStatsLogErrors:             metrics.LogErrors,
		BStatsLogSentData:           metrics.LogSentData,
		BStatsLogResponseStatusText: metrics.LogResponseStatusText,
		StoredValues:                maps.Clone(settings),
		LocalizedSettings:           settingvalues.ReadLocalizedTexts(settings),
		APIKeyACLTrustForwardedIP:   forwarded.APIKeyACLTrustForwardedIP,
		ForwardedClientIPHeaders:    forwarded.ForwardedClientIPHeaders,
		TeamEnabled:                 settings[team.SettingKeyTeamEnabled] != "false",
	}
	defaults := ws.DefaultParameters()
	if options.ResponsesWS != nil {
		defaults = options.ResponsesWS.Defaults()
	}
	raw := settings[ws.SettingKey]
	if raw == "" {
		raw = "{}"
	}
	result.ResponsesWS = json.RawMessage(raw)
	effective, err := ws.ResolveParameters(defaults, raw)
	if err != nil {
		// Failed overrides leave effective runtime parameters available for display,
		// while valid raw JSON remains visible so an administrator can inspect and correct it.
		effective = defaults
		if options.ResponsesWS != nil {
			effective = options.ResponsesWS.Snapshot()
		}
		if !json.Valid([]byte(raw)) {
			// Invalid JSON cannot be embedded in the HTTP response, so show an empty
			// override object while retaining the selected effective parameters alongside it.
			result.ResponsesWS = json.RawMessage("{}")
		}
	}
	result.ResponsesWSEffective = effective
	result.ApplySearchAdminReadSettings(search.ReadAdminSettings(settings))
	result.ApplyOpsAdminReadSettings(ops.ReadAdminSettings(settings))
	result.ApplyPaymentAdminReadSettings(payment.ReadAdminSettings(settings))
	result.ApplyProviderAdminReadSettings(provider.ReadAdminSettings(settings))
	result.ApplyGatewayAdminReadSettings(gateway.ReadAdminSettings(settings, options.Gateway))
	result.ApplyBillingAdminReadSettings(billing.ReadAdminSettings(settings, options.DefaultBalance))
	result.ApplyUsageAdminReadSettings(usage.ReadAdminSettings(settings))
	result.ApplyAuditAdminReadSettings(audit.ReadAdminSettings(settings))
	result.ApplyCreativeAdminReadSettings(creative.ReadAdminSettings(settings))
	result.ApplyModerationAdminReadSettings(moderation.ReadAdminSettings(settings))
	result.ApplyRoutingAdminReadSettings(routing.ReadAdminSettings(settings))
	result.ApplyPromotionAdminReadSettings(promotion.ReadAdminSettings(settings))
	result.ApplyNotificationAdminReadSettings(notification.ReadAdminSettings(settings))
	result.ApplySiteAdminReadSettings(site.ReadAdminSettings(settings))
	result.ApplyIdentityAdminReadSettings(options.OAuth.ReadAdminSettings(settings, options.DefaultConcurrency))

	result.ApplySchedulerAdminReadSettings(scheduler.ReadAdminSettings(settings, options.Scheduler))

	// Publish the resolved model after module interpretation so later requests
	// that omit a model can use this value, including when this parsing began as an admin read.
	if options.PublishModel != nil {
		options.PublishModel(result.GrokDefaultTextModel)
	}

	return result
}

// ApplyAuditAdminReadSettings assigns the parsed audit retention value for administrator display.
func (s *Snapshot) ApplyAuditAdminReadSettings(value *audit.AdminReadSettings) {
	s.AuditLogRetentionDays = value.AuditLogRetentionDays
}

// ApplyCreativeAdminReadSettings transfers Creative access, worker count, and
// model selections from a nonnil read result. The model slice and its nested
// operation lists remain the same references used by that result until callers
// choose a separate copy.
func (s *Snapshot) ApplyCreativeAdminReadSettings(value *creative.AdminReadSettings) {
	s.CreativeEnabled = value.CreativeEnabled
	s.CreativeModelSettings = value.CreativeModelSettings
	s.CreativeWorkerCount = value.CreativeWorkerCount
}

// ApplyModerationAdminReadSettings assigns parsed risk-center and session-blocking
// controls to the Snapshot.
func (s *Snapshot) ApplyModerationAdminReadSettings(value *moderation.AdminReadSettings) {
	s.CyberSessionBlockEnabled = value.CyberSessionBlockEnabled
	s.CyberSessionBlockTTLSeconds = value.CyberSessionBlockTTLSeconds
	s.RiskControlEnabled = value.RiskControlEnabled
}

// ApplyNotificationAdminReadSettings transfers SMTP connection and sender values
// from a nonnil read result. Its raw password remains internal alongside the
// configured flag; the response mapper exposes that flag while settings preparation
// can use the credential.
func (s *Snapshot) ApplyNotificationAdminReadSettings(value *notification.AdminReadSettings) {
	s.SMTPFrom = value.SMTPFrom
	s.SMTPFromName = value.SMTPFromName
	s.SMTPHost = value.SMTPHost
	s.SMTPPassword = value.SMTPPassword
	s.SMTPPasswordConfigured = value.SMTPPasswordConfigured
	s.SMTPPort = value.SMTPPort
	s.SMTPUseTLS = value.SMTPUseTLS
	s.SMTPUsername = value.SMTPUsername
}

// ApplyOpsAdminReadSettings assigns quota thresholds and monitoring cadence from
// a nonnil Ops read result.
func (s *Snapshot) ApplyOpsAdminReadSettings(value *ops.AdminReadSettings) {
	s.OpenAIQuotaAutoPauseSettings = value.OpenAIQuotaAutoPauseSettings
	s.OpsMetricsIntervalSeconds = value.OpsMetricsIntervalSeconds
	s.OpsMonitoringEnabled = value.OpsMonitoringEnabled
	s.OpsRealtimeMonitoringEnabled = value.OpsRealtimeMonitoringEnabled
}

// ApplyPaymentAdminReadSettings assigns parsed Alipay/WeChat visibility switches
// and source choices to the Snapshot.
func (s *Snapshot) ApplyPaymentAdminReadSettings(value *payment.AdminReadSettings) {
	s.PaymentVisibleMethodAlipayEnabled = value.PaymentVisibleMethodAlipayEnabled
	s.PaymentVisibleMethodAlipaySource = value.PaymentVisibleMethodAlipaySource
	s.PaymentVisibleMethodWxpayEnabled = value.PaymentVisibleMethodWxpayEnabled
	s.PaymentVisibleMethodWxpaySource = value.PaymentVisibleMethodWxpaySource
}

// ApplyPromotionAdminReadSettings assigns invitation, promo, and rebate controls
// from the promotion read result.
func (s *Snapshot) ApplyPromotionAdminReadSettings(value *promotion.AdminReadSettings) {
	s.AdminRechargeRebateEnabled = value.AdminRechargeRebateEnabled
	s.AffiliateEnabled = value.AffiliateEnabled
	s.AffiliateRebateDurationDays = value.AffiliateRebateDurationDays
	s.AffiliateRebateFreezeHours = value.AffiliateRebateFreezeHours
	s.AffiliateRebatePerInviteeCap = value.AffiliateRebatePerInviteeCap
	s.AffiliateRebateRate = value.AffiliateRebateRate
	s.InvitationCodeEnabled = value.InvitationCodeEnabled
	s.PromoCodeEnabled = value.PromoCodeEnabled
}

// ApplyProviderAdminReadSettings transfers provider notification recipients and
// auto-pause thresholds from a nonnil read result. The recipient slice and
// threshold map remain shared with that result; later response and preparation
// helpers choose their own conversions.
func (s *Snapshot) ApplyProviderAdminReadSettings(value *provider.AdminReadSettings) {
	s.ProviderQuotaNotifyEmails = value.ProviderQuotaNotifyEmails
	s.ProviderQuotaNotifyEnabled = value.ProviderQuotaNotifyEnabled
	s.ProviderSchedulingThresholds = value.ProviderSchedulingThresholds
}

// ApplySearchAdminReadSettings assigns the parsed web-search emulation switch to the Snapshot.
func (s *Snapshot) ApplySearchAdminReadSettings(value *search.AdminReadSettings) {
	s.WebSearchEmulationEnabled = value.WebSearchEmulationEnabled
}

// ApplyUsageAdminReadSettings assigns parsed ranking choices and failed-request
// visibility for administrator display.
func (s *Snapshot) ApplyUsageAdminReadSettings(value *usage.AdminReadSettings) {
	s.AllowUserViewErrorRequests = value.AllowUserViewErrorRequests
	s.UsageRankingEnabled = value.UsageRankingEnabled
	s.UsageRankingLimit = value.UsageRankingLimit
	s.UsageRankingShowActualCost = value.UsageRankingShowActualCost
	s.UsageRankingShowRequests = value.UsageRankingShowRequests
	s.UsageRankingShowTotalTokens = value.UsageRankingShowTotalTokens
	s.UsageRankingSortBy = value.UsageRankingSortBy
}

// BStatsConfig extracts reporting controls without exposing the stored instance identifier.
func (s *Snapshot) BStatsConfig() bstats.Config {
	return bstats.Config{
		Enabled:               s.BStatsEnabled,
		LogErrors:             s.BStatsLogErrors,
		LogSentData:           s.BStatsLogSentData,
		LogResponseStatusText: s.BStatsLogResponseStatusText,
	}
}
