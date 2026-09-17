package composite

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/site"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/team"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/promotion"

	"github.com/TokenFlux/TokenRouter/internal/notification"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// PrepareOptions 固定原有校验顺序所需的只读能力，准备阶段不接受写入或通知端口。
type PrepareOptions struct {
	ValidatePlans func(context.Context, []billing.DefaultSubscriptionSetting) error
	ReadValues    func(context.Context) (map[string]string, error)
	Gateway       gateway.AdminSettingsRules
	Scheduler     scheduler.AdminDefaults
}

// Prepare 组合所属模块准备结果，保留原完整文档入口的错误优先级。
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
	for key, value := range visibleValues {
		updates[key] = value
	}
	for key, value := range routingValues {
		updates[key] = value
	}
	for key, value := range billingValues {
		updates[key] = value
	}
	for key, value := range identityValues {
		updates[key] = value
	}

	siteInput := settings.SiteAdminSettings()
	siteValues, err := site.PrepareAdminSettings(&siteInput)
	if err != nil {
		return nil, err
	}
	settings.ApplySiteAdminSettings(siteInput)
	for key, value := range siteValues {
		updates[key] = value
	}

	updates[audit.SettingKeyAuditLogRetentionDays] = audit.PrepareRetentionDays(settings.AuditLogRetentionDays)

	for key, value := range notification.PrepareSMTPSettings(notification.AdminSMTPSettings{SMTPHost: settings.SMTPHost, SMTPPort: settings.SMTPPort, SMTPUsername: settings.SMTPUsername, SMTPPassword: settings.SMTPPassword, SMTPFrom: settings.SMTPFrom, SMTPFromName: settings.SMTPFromName, SMTPUseTLS: settings.SMTPUseTLS}) {
		updates[key] = value
	}

	for key, value := range forwardedValues {
		updates[key] = value
	}

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
	for key, value := range rankingValues {
		updates[key] = value
	}

	creativeSettings, creativeValues, err := creative.PrepareAdminSettings(creative.AdminSettings{CreativeEnabled: settings.CreativeEnabled, CreativeWorkerCount: settings.CreativeWorkerCount, CreativeModelSettings: settings.CreativeModelSettings})
	if err != nil {
		return nil, err
	}
	settings.CreativeModelSettings = creativeSettings.CreativeModelSettings
	settings.CreativeWorkerCount = creativeSettings.CreativeWorkerCount
	for key, value := range creativeValues {
		updates[key] = value
	}

	promotionValues, preparedPromotion := promotion.PrepareAdminSettings(promotion.AdminSettings{PromoCodeEnabled: settings.PromoCodeEnabled, InvitationCodeEnabled: settings.InvitationCodeEnabled, AffiliateEnabled: settings.AffiliateEnabled, AffiliateRebateRate: settings.AffiliateRebateRate, AffiliateRebateFreezeHours: settings.AffiliateRebateFreezeHours, AffiliateRebateDurationDays: settings.AffiliateRebateDurationDays, AffiliateRebatePerInviteeCap: settings.AffiliateRebatePerInviteeCap, AdminRechargeRebateEnabled: settings.AdminRechargeRebateEnabled})
	settings.PromoCodeEnabled = promotionValues.PromoCodeEnabled
	settings.InvitationCodeEnabled = promotionValues.InvitationCodeEnabled
	settings.AffiliateEnabled = promotionValues.AffiliateEnabled
	settings.AffiliateRebateRate = promotionValues.AffiliateRebateRate
	settings.AffiliateRebateFreezeHours = promotionValues.AffiliateRebateFreezeHours
	settings.AffiliateRebateDurationDays = promotionValues.AffiliateRebateDurationDays
	settings.AffiliateRebatePerInviteeCap = promotionValues.AffiliateRebatePerInviteeCap
	settings.AdminRechargeRebateEnabled = promotionValues.AdminRechargeRebateEnabled
	for key, value := range preparedPromotion {
		updates[key] = value
	}

	gatewayInput := settings.GatewayAdminSettings()
	gatewayValues, err := gateway.PrepareAdminSettings(&gatewayInput, options.Gateway)
	if err != nil {
		return nil, err
	}
	for key, value := range gatewayValues {
		updates[key] = value
	}

	for key, value := range ops.PrepareMonitoringSettings(ops.CompositeMonitoringSettings{OpsMonitoringEnabled: settings.OpsMonitoringEnabled, OpsRealtimeMonitoringEnabled: settings.OpsRealtimeMonitoringEnabled, OpsMetricsIntervalSeconds: settings.OpsMetricsIntervalSeconds}) {
		updates[key] = value
	}

	schedulerInput := settings.SchedulerAdminSettings()
	schedulerValues, err := scheduler.PrepareAdminSettings(&schedulerInput, options.Scheduler)
	if err != nil {
		return nil, err
	}
	settings.ApplySchedulerAdminSettings(schedulerInput)
	for key, value := range schedulerValues {
		updates[key] = value
	}

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

	_, teamValues, _ := team.PrepareAdminSettings(team.AdminSettings{TeamEnabled: settings.TeamEnabled})
	for key, value := range teamValues {
		updates[key] = value
	}
	_, moderationValues, _ := moderation.PrepareAdminSettings(moderation.AdminSettings{RiskControlEnabled: settings.RiskControlEnabled, CyberSessionBlockEnabled: settings.CyberSessionBlockEnabled, CyberSessionBlockTTLSeconds: settings.CyberSessionBlockTTLSeconds})
	for key, value := range moderationValues {
		updates[key] = value
	}

	quotaValues, err := billing.PrepareDefaultQuotaSettings(settings.DefaultPlatformQuotas)
	if err != nil {
		return nil, err
	}
	for key, value := range quotaValues {
		updates[key] = value
	}

	accountValues, err := account.PrepareAdminSettings(account.AdminSettings{AccountQuotaNotifyEnabled: settings.AccountQuotaNotifyEnabled, AccountQuotaNotifyEmails: settings.AccountQuotaNotifyEmails, AccountSchedulingThresholds: settings.AccountSchedulingThresholds})
	if err != nil {
		return nil, err
	}
	for key, value := range accountValues {
		updates[key] = value
	}

	updates[usage.SettingKeyAllowUserViewErrorRequests] = strconv.FormatBool(settings.AllowUserViewErrorRequests)

	return updates, nil
}

// prepareQuotaMerge 保留额外 GetAll 的原时点，Ops 独占共享 JSON 合并。
func prepareQuotaMerge(ctx context.Context, options PrepareOptions, quota account.QuotaAutoPauseSettings) (*ops.OpsAdvancedSettings, error) {
	values, err := options.ReadValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("get settings for ops advanced merge: %w", err)
	}
	return ops.MergeQuotaAutoPauseSettings(values["ops_advanced_settings"], quota)
}
