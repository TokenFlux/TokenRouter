package composite

import (
	"github.com/TokenFlux/TokenRouter/internal/team"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/gateway"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// ReadOptions 只提供所属模块值和当前运行快照，不接收完整配置。
type ReadOptions struct {
	OAuth              *identity.OAuthSettings
	Gateway            gateway.AdminSettingsRules
	Scheduler          scheduler.AdminDefaults
	DefaultBalance     func() float64
	DefaultConcurrency func() int
	Forwarded          func() runtimeconfig.ForwardedInput
	PublishModel       func(string, bool)
}

// Parse 将各领域的只读投影组合为管理快照，不增加存储查询。
func Parse(settings map[string]string, options ReadOptions) *Snapshot {

	prior := options.Forwarded()
	forwarded := runtimeconfig.ReadForwardedSettings(settings, prior)
	result := &Snapshot{
		APIKeyACLTrustForwardedIP: forwarded.APIKeyACLTrustForwardedIP,
		ForwardedClientIPHeaders:  forwarded.ForwardedClientIPHeaders,
		TeamEnabled:               settings[team.SettingKeyTeamEnabled] != "false",
	}
	result.ApplySearchAdminReadSettings(search.ReadAdminSettings(settings))
	result.ApplyOpsAdminReadSettings(ops.ReadAdminSettings(settings))
	result.ApplyPaymentAdminReadSettings(payment.ReadAdminSettings(settings))
	result.ApplyAccountAdminReadSettings(account.ReadAdminSettings(settings))
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

	// 系统层默认 platform quota（修复 Bug B：parseSettings 不填充导致回显恒为 nil）

	// 保留旧读取时发布动态默认模型的时点，具体平台由装配投影。
	if options.PublishModel != nil {
		options.PublishModel(result.GrokDefaultTextModel, result.GrokCrossClientModelMapEnabled)
	}

	return result
}
