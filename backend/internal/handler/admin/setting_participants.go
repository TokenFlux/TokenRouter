package admin

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/ops"

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
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// SetSettingsParticipants 仅由 app 在路由开放前注入静态参与者，不支持运行时插件注册。
func (h *SettingHandler) SetSettingsParticipants(registry *settings.Registry) {
	h.settingsParticipants = registry
}

// preparedParticipants 兼容旧测试直接构造 Handler；生产始终使用 app 注入的不可变注册表。
func (h *SettingHandler) compositeParticipants() (*settings.Registry, error) {
	registry := h.settingsParticipants
	if registry == nil {
		participants := []settings.Participant{identity.SettingsParticipant(h.settingService.GrantSettings()), team.SettingsParticipant(), moderation.SettingsParticipant(), creative.SettingsParticipant(), runtimeconfig.SettingsParticipant(), billing.SettingsParticipant(), scheduler.SettingsParticipant(h.settingService.SchedulerAdminDefaults()), routing.SettingsParticipant(), site.SettingsParticipant(), account.SettingsParticipant(), ops.SettingsParticipant(), payment.VisibleSettingsParticipant(), notification.SMTPSettingsParticipant(), promotion.SettingsParticipant(), usage.SettingsParticipant(), audit.SettingsParticipant(), gateway.FastSettingsParticipant(), gateway.AdminSettingsParticipant(h.settingService.GatewayAdminRules())}
		if h.paymentConfigService != nil {
			var refresh func(context.Context) error
			if h.paymentService != nil {
				refresh = h.paymentService.RefreshProvidersChecked
			}
			participants = append(participants, payment.SettingsParticipant(refresh))
		}
		var err error
		registry, err = settings.NewRegistry(participants...)
		if err != nil {
			return nil, err
		}
	}
	return registry, nil
}
