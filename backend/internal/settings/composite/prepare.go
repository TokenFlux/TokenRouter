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

// prepare.go normalizes combined administrator settings and collects their
// persistence values.
// A combined Snapshot contains settings from multiple owning modules, whose
// normalizers need to run before any key/value batch reaches storage. Preparation
// visits those modules in a fixed order, updates selected Snapshot fields, and
// collects strings for later persistence by the update session. Gateway
// preparation contributes strings without copying its normalized value back;
// initial scheduler normalization is copied back before its error is checked.
//
// An error can therefore leave the supplied Snapshot partly normalized even
// though this preparation performs no write. OpenAI quota thresholds require a
// read/merge of the wider Ops JSON when their presence flag is set; that step
// carries recognized advanced fields through Ops normalization. Request omission
// filtering and registry ownership replacement happen after this broad preparation
// in the combined HTTP flow.

// PrepareOptions supplies validation, current values, and module defaults for batch preparation.
type PrepareOptions struct {
	ValidatePlans func(context.Context, []billing.DefaultSubscriptionSetting) error // default subscription plan validation
	// ReadValues is invoked when the quota presence flag requires an Ops JSON
	// merge. It supplies current strings, and a read failure stops preparation.
	ReadValues func(context.Context) (map[string]string, error)
	Gateway    gateway.AdminSettingsRules // gateway normalization and validation rules
	Scheduler  scheduler.AdminDefaults    // inherited scheduler parameter defaults
}

// Prepare normalizes a nonnil Snapshot and returns strings for a later combined
// settings write. ValidatePlans runs before module preparation, and each successful
// module normalization can mutate the Snapshot before another module reports an
// error. The function itself performs no persistence.
//
// Collected values are merged in the declared module sequence. The caller later
// drops omitted keys and replaces keys owned by its registered participants, so
// this broad map is an intermediate result. A supplied OpenAI quota flag selects
// the Ops advanced-JSON read/merge step; that failure also returns before the batch
// can be committed by the update session.
func Prepare(ctx context.Context, settings *Snapshot, options PrepareOptions) (map[string]string, error) {
	if err := options.ValidatePlans(ctx, settings.DefaultSubscriptions); err != nil {
		return nil, err
	}
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
	schedulerErr := scheduler.NormalizeAdminSettings(&schedulerInitial, options.Scheduler)
	settings.ApplySchedulerAdminSettings(schedulerInitial)
	if err := schedulerErr; err != nil {
		return nil, err
	}
	settings.PaymentVisibleMethodAlipaySource = visibleInput.PaymentVisibleMethodAlipaySource
	settings.PaymentVisibleMethodWxpaySource = visibleInput.PaymentVisibleMethodWxpaySource
	updates := make(map[string]string)
	maps.Copy(updates, visibleValues)
	maps.Copy(updates, routingValues)
	maps.Copy(updates, billingValues)
	maps.Copy(updates, identityValues)

	siteInput := settings.SiteAdminSettings()
	siteValues, err := site.PrepareAdminSettings(&siteInput)
	if err != nil {
		return nil, err
	}
	settings.ApplySiteAdminSettings(siteInput)
	maps.Copy(updates, siteValues)

	updates[audit.SettingKeyAuditLogRetentionDays] = audit.PrepareRetentionDays(settings.AuditLogRetentionDays)

	maps.Copy(updates, notification.PrepareSMTPSettings(notification.AdminSMTPSettings{SMTPHost: settings.SMTPHost, SMTPPort: settings.SMTPPort, SMTPUsername: settings.SMTPUsername, SMTPPassword: settings.SMTPPassword, SMTPFrom: settings.SMTPFrom, SMTPFromName: settings.SMTPFromName, SMTPUseTLS: settings.SMTPUseTLS}))

	maps.Copy(updates, forwardedValues)

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
	maps.Copy(updates, rankingValues)

	creativeSettings, creativeValues, err := creative.PrepareAdminSettings(creative.AdminSettings{CreativeEnabled: settings.CreativeEnabled, CreativeWorkerCount: settings.CreativeWorkerCount, CreativeModelSettings: settings.CreativeModelSettings})
	if err != nil {
		return nil, err
	}
	settings.CreativeModelSettings = creativeSettings.CreativeModelSettings
	settings.CreativeWorkerCount = creativeSettings.CreativeWorkerCount
	maps.Copy(updates, creativeValues)

	promotionValues, preparedPromotion := promotion.PrepareAdminSettings(promotion.AdminSettings{PromoCodeEnabled: settings.PromoCodeEnabled, InvitationCodeEnabled: settings.InvitationCodeEnabled, AffiliateEnabled: settings.AffiliateEnabled, AffiliateRebateRate: settings.AffiliateRebateRate, AffiliateRebateFreezeHours: settings.AffiliateRebateFreezeHours, AffiliateRebateDurationDays: settings.AffiliateRebateDurationDays, AffiliateRebatePerInviteeCap: settings.AffiliateRebatePerInviteeCap, AdminRechargeRebateEnabled: settings.AdminRechargeRebateEnabled})
	settings.PromoCodeEnabled = promotionValues.PromoCodeEnabled
	settings.InvitationCodeEnabled = promotionValues.InvitationCodeEnabled
	settings.AffiliateEnabled = promotionValues.AffiliateEnabled
	settings.AffiliateRebateRate = promotionValues.AffiliateRebateRate
	settings.AffiliateRebateFreezeHours = promotionValues.AffiliateRebateFreezeHours
	settings.AffiliateRebateDurationDays = promotionValues.AffiliateRebateDurationDays
	settings.AffiliateRebatePerInviteeCap = promotionValues.AffiliateRebatePerInviteeCap
	settings.AdminRechargeRebateEnabled = promotionValues.AdminRechargeRebateEnabled
	maps.Copy(updates, preparedPromotion)

	gatewayInput := settings.GatewayAdminSettings()
	gatewayValues, err := gateway.PrepareAdminSettings(&gatewayInput, options.Gateway)
	if err != nil {
		return nil, err
	}
	maps.Copy(updates, gatewayValues)

	maps.Copy(updates, ops.PrepareMonitoringSettings(ops.CompositeMonitoringSettings{OpsMonitoringEnabled: settings.OpsMonitoringEnabled, OpsRealtimeMonitoringEnabled: settings.OpsRealtimeMonitoringEnabled, OpsMetricsIntervalSeconds: settings.OpsMetricsIntervalSeconds}))

	schedulerInput := settings.SchedulerAdminSettings()
	schedulerValues, err := scheduler.PrepareAdminSettings(&schedulerInput, options.Scheduler)
	if err != nil {
		return nil, err
	}
	settings.ApplySchedulerAdminSettings(schedulerInput)
	maps.Copy(updates, schedulerValues)

	// Merge thresholds only after presence selected a quota update so routine
	// saves can leave this wider Ops JSON untouched until its own participant prepares it.
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

	// Team and moderation preparation currently return no validation failure for
	// these supplied value records. Their discarded errors accompany encoded keys
	// collected for the eventual combined batch.
	_, teamValues, _ := team.PrepareAdminSettings(team.AdminSettings{TeamEnabled: settings.TeamEnabled})
	maps.Copy(updates, teamValues)
	_, moderationValues, _ := moderation.PrepareAdminSettings(moderation.AdminSettings{RiskControlEnabled: settings.RiskControlEnabled, CyberSessionBlockEnabled: settings.CyberSessionBlockEnabled, CyberSessionBlockTTLSeconds: settings.CyberSessionBlockTTLSeconds})
	maps.Copy(updates, moderationValues)

	providerValues, err := provider.PrepareAdminSettings(provider.AdminSettings{ProviderQuotaNotifyEnabled: settings.ProviderQuotaNotifyEnabled, ProviderQuotaNotifyEmails: settings.ProviderQuotaNotifyEmails, ProviderSchedulingThresholds: settings.ProviderSchedulingThresholds})
	if err != nil {
		return nil, err
	}
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
