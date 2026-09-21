package testkit

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// RuntimeReaders 只装配原生设置读取器，保留 HTTP 夹具的原动态设置数据。
func RuntimeReaders(repo settings.Repository) *gatewayprovider.RuntimeReaders {
	runtime := gateway.NewRuntimeSettings(repo, settings.ErrSettingNotFound, func() *gateway.BetaPolicySettings {
		return gatewayprovider.GatewayBetaPolicy(anthropic.DefaultBetaPolicySettings())
	}, gateway.ClientSettingsOptions{NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, DefaultUserAgentVersion: antigravity.GetDefaultUserAgentVersion})
	return &gatewayprovider.RuntimeReaders{Gateway: runtime, Account: account.NewRuntimeSettings(repo, settings.ErrSettingNotFound), Quota: account.NewQuotaSettingsCache(repo, settings.ErrSettingNotFound, ops.ParseRuntimeQuotaAutoPauseSettings), Routing: routing.NewRuntimeSettings(repo), Moderation: moderation.NewRuntimeSettings(repo, settings.ErrSettingNotFound), Prompts: promptpolicy.New(repo, settings.ErrSettingNotFound, slog.Warn), Search: search.NewConfigService(repo, nil, nil, search.NewRegistry()), Scheduler: repo}
}
