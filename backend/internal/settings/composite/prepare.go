package composite

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// prepare.go normalizes combined administrator settings snapshots and optionally
// collects their persistence values for standalone test fixtures.
// A combined Snapshot contains settings from multiple owning modules whose
// normalizers run in a fixed order before participant preparation or standalone
// batch persistence. NormalizeSnapshot updates Snapshot fields and runs module
// validators without allocating storage maps or reading Ops settings, while
// Prepare additionally validates default subscription plans and collects string
// values for composite test fixtures.
//
// Gateway preparation validates values without copying normalized fields back,
// and initial scheduler normalization is copied back before its error is
// checked. An error can therefore leave the supplied Snapshot partly normalized
// even though neither entry point performs storage writes.

// PrepareOptions supplies validation, current values, and module defaults for batch preparation.
type PrepareOptions struct {
	ValidatePlans func(context.Context, []billing.DefaultSubscriptionSetting) error // default subscription plan validation
	// ReadValues is invoked when the quota presence flag requires an Ops JSON
	// merge. It supplies current strings, and a read failure stops preparation.
	ReadValues func(context.Context) (map[string]string, error)
	Gateway    gateway.AdminSettingsRules // gateway normalization and validation rules
	Scheduler  scheduler.AdminDefaults    // inherited scheduler parameter defaults
}

// NormalizeSnapshot normalizes and validates a nonnil Snapshot in place across
// owning modules without allocating persistence maps or reading stored Ops
// JSON. Module normalizers run in declared order and may mutate earlier fields
// on settings before a later module returns a validation error.
func NormalizeSnapshot(settings *Snapshot, gatewayRules gateway.AdminSettingsRules, schedulerDefaults scheduler.AdminDefaults) error {
	_, err := prepareSnapshot(settings, gatewayRules, schedulerDefaults, false)
	return err
}

// Prepare normalizes a nonnil Snapshot and returns strings for standalone
// combined settings fixtures. ValidatePlans runs when configured before module
// preparation, and each successful module normalization can mutate the Snapshot
// before another module reports an error. The function performs no persistence.
//
// Collected values are merged in the declared module sequence, and a supplied
// OpenAI quota flag triggers the Ops advanced-JSON read/merge step before
// returning the complete string map.
func Prepare(ctx context.Context, settings *Snapshot, options PrepareOptions) (map[string]string, error) {
	if options.ValidatePlans != nil {
		if err := options.ValidatePlans(ctx, settings.DefaultSubscriptions); err != nil {
			return nil, err
		}
	}
	updates, err := prepareSnapshot(settings, options.Gateway, options.Scheduler, true)
	if err != nil {
		return nil, err
	}
	// Merge thresholds only after presence selected a quota update so standalone
	// fixture saves leave wider Ops JSON untouched when quota settings are omitted.
	if settings.OpenAIQuotaAutoPauseSettingsSet {
		opsAdvanced, err := prepareQuotaMerge(ctx, options, settings.OpenAIQuotaAutoPauseSettings)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(opsAdvanced)
		if err != nil {
			return nil, fmt.Errorf("marshal ops advanced settings: %w", err)
		}
		updates["ops_advanced_settings"] = string(raw)
	}
	return updates, nil
}

// prepareSnapshot executes ordered module normalization on settings and
// collects encoded storage values only when collectValues is true.
func prepareSnapshot(settings *Snapshot, gatewayRules gateway.AdminSettingsRules, schedulerDefaults scheduler.AdminDefaults, collectValues bool) (map[string]string, error) {
	identityInput := settings.IdentityAdminSettings()
	identityValues, err := identity.PrepareAdminSettings(&identityInput)
	if err != nil {
		return nil, err
	}
	settings.ApplyIdentityAdminSettings(identityInput)

	billingInput := settings.BillingAdminSettings()
	billingValues, err := billing.PrepareAdminSettings(&billingInput)
	if err != nil {
		return nil, err
	}
	settings.ApplyBillingAdminSettings(billingInput)

	routingInput := settings.RoutingAdminSettings()
	routingValues := routing.PrepareAdminSettings(&routingInput)
	settings.ApplyRoutingAdminSettings(routingInput)

	forwardedInput, forwardedValues, err := runtimeconfig.PrepareForwardedSettings(runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: settings.APIKeyACLTrustForwardedIP, ForwardedClientIPHeaders: settings.ForwardedClientIPHeaders})
	if err != nil {
		return nil, err
	}
	settings.ForwardedClientIPHeaders = forwardedInput.ForwardedClientIPHeaders
	visibleInput := payment.VisibleMethodSettings{PaymentVisibleMethodAlipaySource: settings.PaymentVisibleMethodAlipaySource, PaymentVisibleMethodWxpaySource: settings.PaymentVisibleMethodWxpaySource, PaymentVisibleMethodAlipayEnabled: settings.PaymentVisibleMethodAlipayEnabled, PaymentVisibleMethodWxpayEnabled: settings.PaymentVisibleMethodWxpayEnabled}
	visibleValues, err := payment.PrepareVisibleMethodSettings(&visibleInput)
	if err != nil {
		return nil, err
	}
	schedulerInitial := settings.SchedulerAdminSettings()
	schedulerErr := scheduler.NormalizeAdminSettings(&schedulerInitial, schedulerDefaults)
	settings.ApplySchedulerAdminSettings(schedulerInitial)
	if err := schedulerErr; err != nil {
		return nil, err
	}
	settings.PaymentVisibleMethodAlipaySource = visibleInput.PaymentVisibleMethodAlipaySource
	settings.PaymentVisibleMethodWxpaySource = visibleInput.PaymentVisibleMethodWxpaySource

	siteInput := settings.SiteAdminSettings()
	siteValues, err := site.PrepareAdminSettings(&siteInput)
	if err != nil {
		return nil, err
	}
	settings.ApplySiteAdminSettings(siteInput)

	usageRanking, rankingValues := usage.PrepareRankingSettings(usage.UsageRankingSettings{
		Enabled:         settings.UsageRankingEnabled,
		SortBy:          usage.UsageRankingSortBy(settings.UsageRankingSortBy),
		ShowTotalTokens: settings.UsageRankingShowTotalTokens,
		ShowRequests:    settings.UsageRankingShowRequests,
		ShowActualCost:  settings.UsageRankingShowActualCost,
		Limit:           settings.UsageRankingLimit,
	})
	settings.UsageRankingEnabled = usageRanking.Enabled
	settings.UsageRankingSortBy = string(usageRanking.SortBy)
	settings.UsageRankingShowTotalTokens = usageRanking.ShowTotalTokens
	settings.UsageRankingShowRequests = usageRanking.ShowRequests
	settings.UsageRankingShowActualCost = usageRanking.ShowActualCost
	settings.UsageRankingLimit = usageRanking.Limit

	creativeSettings, creativeValues, err := creative.PrepareAdminSettings(creative.AdminSettings{CreativeEnabled: settings.CreativeEnabled, CreativeWorkerCount: settings.CreativeWorkerCount, CreativeModelSettings: settings.CreativeModelSettings})
	if err != nil {
		return nil, err
	}
	settings.CreativeModelSettings = creativeSettings.CreativeModelSettings
	settings.CreativeWorkerCount = creativeSettings.CreativeWorkerCount

	promotionValues, preparedPromotion := promotion.PrepareAdminSettings(promotion.AdminSettings{PromoCodeEnabled: settings.PromoCodeEnabled, InvitationCodeEnabled: settings.InvitationCodeEnabled, AffiliateEnabled: settings.AffiliateEnabled, AffiliateRebateRate: settings.AffiliateRebateRate, AffiliateRebateFreezeHours: settings.AffiliateRebateFreezeHours, AffiliateRebateDurationDays: settings.AffiliateRebateDurationDays, AffiliateRebatePerInviteeCap: settings.AffiliateRebatePerInviteeCap, AdminRechargeRebateEnabled: settings.AdminRechargeRebateEnabled})
	settings.PromoCodeEnabled = promotionValues.PromoCodeEnabled
	settings.InvitationCodeEnabled = promotionValues.InvitationCodeEnabled
	settings.AffiliateEnabled = promotionValues.AffiliateEnabled
	settings.AffiliateRebateRate = promotionValues.AffiliateRebateRate
	settings.AffiliateRebateFreezeHours = promotionValues.AffiliateRebateFreezeHours
	settings.AffiliateRebateDurationDays = promotionValues.AffiliateRebateDurationDays
	settings.AffiliateRebatePerInviteeCap = promotionValues.AffiliateRebatePerInviteeCap
	settings.AdminRechargeRebateEnabled = promotionValues.AdminRechargeRebateEnabled

	gatewayInput := settings.GatewayAdminSettings()
	gatewayValues, err := gateway.PrepareAdminSettings(&gatewayInput, gatewayRules)
	if err != nil {
		return nil, err
	}

	schedulerInput := settings.SchedulerAdminSettings()
	schedulerValues, err := scheduler.PrepareAdminSettings(&schedulerInput, schedulerDefaults)
	if err != nil {
		return nil, err
	}
	settings.ApplySchedulerAdminSettings(schedulerInput)

	providerValues, err := provider.PrepareAdminSettings(provider.AdminSettings{ProviderQuotaNotifyEnabled: settings.ProviderQuotaNotifyEnabled, ProviderQuotaNotifyEmails: settings.ProviderQuotaNotifyEmails, ProviderSchedulingThresholds: settings.ProviderSchedulingThresholds})
	if err != nil {
		return nil, err
	}
	if !collectValues {
		return nil, nil
	}

	updates := make(map[string]string)
	maps.Copy(updates, visibleValues)
	maps.Copy(updates, routingValues)
	maps.Copy(updates, billingValues)
	maps.Copy(updates, identityValues)
	maps.Copy(updates, siteValues)
	updates[audit.SettingKeyAuditLogRetentionDays] = audit.PrepareRetentionDays(settings.AuditLogRetentionDays)
	maps.Copy(updates, notification.PrepareSMTPSettings(notification.AdminSMTPSettings{SMTPHost: settings.SMTPHost, SMTPPort: settings.SMTPPort, SMTPUsername: settings.SMTPUsername, SMTPPassword: settings.SMTPPassword, SMTPFrom: settings.SMTPFrom, SMTPFromName: settings.SMTPFromName, SMTPUseTLS: settings.SMTPUseTLS}))
	maps.Copy(updates, forwardedValues)
	maps.Copy(updates, rankingValues)
	maps.Copy(updates, creativeValues)
	maps.Copy(updates, preparedPromotion)
	maps.Copy(updates, gatewayValues)
	maps.Copy(updates, ops.PrepareMonitoringSettings(ops.CompositeMonitoringSettings{OpsMonitoringEnabled: settings.OpsMonitoringEnabled, OpsRealtimeMonitoringEnabled: settings.OpsRealtimeMonitoringEnabled, OpsMetricsIntervalSeconds: settings.OpsMetricsIntervalSeconds}))
	maps.Copy(updates, schedulerValues)
	// Team and moderation preparation return no validation failure for these
	// supplied value records when assembling a standalone fixture batch.
	_, teamValues, _ := team.PrepareAdminSettings(team.AdminSettings{TeamEnabled: settings.TeamEnabled})
	maps.Copy(updates, teamValues)
	_, moderationValues, _ := moderation.PrepareAdminSettings(moderation.AdminSettings{RiskControlEnabled: settings.RiskControlEnabled, CyberSessionBlockEnabled: settings.CyberSessionBlockEnabled, CyberSessionBlockTTLSeconds: settings.CyberSessionBlockTTLSeconds})
	maps.Copy(updates, moderationValues)
	maps.Copy(updates, providerValues)
	updates[usage.SettingKeyAllowUserViewErrorRequests] = strconv.FormatBool(settings.AllowUserViewErrorRequests)
	maps.Copy(updates, settings.BStatsConfig().Values())
	return updates, nil
}

// prepareQuotaMerge reads current strings when quota thresholds must be prepared
// inside Ops advanced settings. Read failures retain their cause. The shared Ops
// merger decodes recognized saved fields over defaults, replaces the quota object,
// and normalizes that typed result before it is encoded for persistence.
func prepareQuotaMerge(ctx context.Context, options PrepareOptions, quota provider.QuotaAutoPauseSettings) (*ops.OpsAdvancedSettings, error) {
	values, err := options.ReadValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("get settings for ops advanced merge: %w", err)
	}
	return ops.MergeQuotaAutoPauseSettings(values["ops_advanced_settings"], quota)
}
