package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/site/filesystem"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// siteBillingSubscriptions 将 billing 的有效订阅转换为公告资格判断需要的数据。
type siteBillingSubscriptions struct {
	repo billing.UserSubscriptionRepository
}

func (a siteBillingSubscriptions) ListActiveByUserID(ctx context.Context, id int64) ([]site.SubscriptionSnapshot, error) {
	subs, err := a.repo.ListActiveByUserID(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]site.SubscriptionSnapshot, len(subs))
	for i, s := range subs {
		out[i] = site.SubscriptionSnapshot{PlanID: s.PlanID}
	}
	return out, nil
}

func provideAnnouncementSubscriptions(repo billing.UserSubscriptionRepository) site.SubscriptionReader {
	return siteBillingSubscriptions{repo: repo}
}

// provideAnnouncementExpiry 创建每分钟检查公告过期状态的服务。
func provideAnnouncementExpiry(repo site.AnnouncementRepository) *site.AnnouncementExpiryService {
	return site.NewAnnouncementExpiryService(repo, time.Minute)
}

func provideSitePages(cfg *config.Config, settings *site.DisplaySettings) *sitehttp.PageHandler {
	return sitehttp.NewPageHandler(site.NewPages(filesystem.New(cfg.Pricing.DataDir), settings))
}

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

// provideSiteDisplay 按需读取前端回退地址，站点名称通过单键查询取得。
func provideSiteDisplay(store *settings.Store, cfg *config.Config) *site.DisplaySettings {
	return site.NewDisplaySettings(store, func() string { return cfg.Server.FrontendURL })
}

func provideSitePublicHTTP(s *site.PublicService, info BuildInfo) *sitehttp.PublicHandler {
	return sitehttp.NewPublicHandler(s, info.Version)
}
