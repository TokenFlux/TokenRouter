// 公开 API、HTML 注入与 CSP 使用同一站点实例。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func provideSitePublic(store *settings.Store, oauth *identity.OAuthSettings, cfg *config.Config, calendar timezone.Calendar) *site.PublicService {
	p := site.NewPublicService(site.NewInputSource(store, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		value := oauth.PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, cfg.Team.Enabled, cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: value.LinuxDo, DingTalk: value.DingTalk, OIDC: value.OIDC, OIDCName: value.OIDCName, WeChat: value.WeChat, WeChatOpen: value.WeChatOpen, WeChatMP: value.WeChatMP, WeChatMobile: value.WeChatMobile, GitHub: value.GitHub, Google: value.Google, GoogleOneTap: value.GoogleOneTap, GoogleClientID: value.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: cfg.WebAuthn.Enabled, TencentRegion: value.TencentRegion, AliyunRegion: value.AliyunRegion, RegistrationSuffixes: value.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		value := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: value.Limit, Enabled: value.Enabled, SortBy: string(value.SortBy), ShowTotalTokens: value.ShowTotalTokens, ShowRequests: value.ShowRequests, ShowActualCost: value.ShowActualCost}
	}, Version: store.Version}), calendar, cfg.Timezone)
	return p
}

// provideSiteDisplay 保留前端地址回退的按需读取，名称只查询原单键。
func provideSiteDisplay(store *settings.Store, cfg *config.Config) *site.DisplaySettings {
	return site.NewDisplaySettings(store, func() string { return cfg.Server.FrontendURL })
}

func provideSitePublicHTTP(s *site.PublicService, info BuildInfo) *sitehttp.PublicHandler {
	return sitehttp.NewPublicHandler(s, info.Version)
}
