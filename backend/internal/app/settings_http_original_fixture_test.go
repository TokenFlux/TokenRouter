package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
)

// newCompositeSettingsHTTPFixture 复用原生读取、准备和参与者，保留旧 HTTP 夹具的无套餐后端边界。
// 只绑定这些断言需要的可信代理发布与更新通知，不创建业务 worker 或旧聚合服务。
func newCompositeSettingsHTTPFixture(repo settings.Repository, cfg *config.Config) (settingshttp.HandlerOptions, *settings.Store) {
	store := settings.New(repo)
	oauth := provideOAuthSettings(store, cfg)
	grants := identity.NewGrantSettings(store, identity.GrantSettingsOptions{DefaultBalance: cfg.Default.UserBalance, DefaultConcurrency: cfg.Default.UserConcurrency})
	gatewayRuntime := provideGatewaySettings(store)
	rules := provideGatewayAdminRules()
	defaults := provideSchedulerAdminDefaults(cfg)
	forwarded := provideForwardedSettings(store, cfg)
	read := composite.ReadOptions{
		OAuth: oauth, Gateway: *rules, Scheduler: *defaults,
		DefaultBalance:     func() float64 { return cfg.Default.UserBalance },
		DefaultConcurrency: func() int { return cfg.Default.UserConcurrency },
		Forwarded: func() runtimeconfig.ForwardedInput {
			value := cfg.ForwardedClientIPSettings()
			return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.TrustForwardedIP, ForwardedClientIPHeaders: value.Headers}
		},
	}
	prepare := composite.PrepareOptions{
		ReadValues: store.GetAll, Gateway: *rules, Scheduler: *defaults,
		ValidatePlans: func(context.Context, []billing.DefaultSubscriptionSetting) error { return nil },
	}
	applications := []composite.Application{
		{Module: "server", Apply: func(_ context.Context, value *composite.Snapshot) error {
			forwarded.Apply(runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.APIKeyACLTrustForwardedIP, ForwardedClientIPHeaders: value.ForwardedClientIPHeaders})
			return nil
		}},
		{Module: "site", Apply: func(context.Context, *composite.Snapshot) error { store.NotifyUpdated(); return nil }},
	}
	source := composite.NewRuntime(store, read, prepare, grants, gatewayRuntime, cfg.Totp.EncryptionKeyConfigured, applications)
	participants := staticSettingsParticipants(&payment.Runtime{}, grants, defaults, rules)
	// 原夹具不启动支付渠道，准备仍使用真实支付参与者，提交后不刷新外部渠道。
	participants[len(participants)-1] = payment.SettingsParticipant(nil)
	registry, err := settings.NewRegistry(participants...)
	return settingshttp.HandlerOptions{Settings: source, Participants: registry, ParticipantError: err}, store
}
