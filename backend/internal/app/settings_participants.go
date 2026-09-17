package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
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
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// provideSettingsParticipants 静态登记已提取的准备器，构造失败会阻止应用启动。
func provideSettingsParticipants(runtime *payment.Runtime, grants *identity.GrantSettings, schedulerDefaults *scheduler.AdminDefaults, gatewayRules *gateway.AdminSettingsRules) (*settings.Registry, error) {
	return settings.NewRegistry(staticSettingsParticipants(runtime, grants, schedulerDefaults, gatewayRules)...)
}

// staticSettingsParticipants 固定字段与键的所有权，供装配契约逐项核对。
func staticSettingsParticipants(runtime *payment.Runtime, grants *identity.GrantSettings, schedulerDefaults *scheduler.AdminDefaults, gatewayRules *gateway.AdminSettingsRules) []settings.Participant {
	return []settings.Participant{identity.SettingsParticipant(grants), team.SettingsParticipant(), moderation.SettingsParticipant(), creative.SettingsParticipant(), runtimeconfig.SettingsParticipant(), billing.SettingsParticipant(), scheduler.SettingsParticipant(*schedulerDefaults), routing.SettingsParticipant(), site.SettingsParticipant(), account.SettingsParticipant(), ops.SettingsParticipant(), payment.VisibleSettingsParticipant(), notification.SMTPSettingsParticipant(), promotion.SettingsParticipant(), usage.SettingsParticipant(), audit.SettingsParticipant(), gateway.FastSettingsParticipant(), gateway.AdminSettingsParticipant(*gatewayRules), payment.SettingsParticipant(runtime.RefreshProvidersChecked)}
}
