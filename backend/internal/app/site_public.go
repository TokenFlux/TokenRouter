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

// site_public.go assembles public site settings, page storage, and announcement
// subscription eligibility. Public site settings expose selected authentication
// and usage choices from one raw settings snapshot. OAuth, team, and ranking
// parsers interpret their own values before the site service receives its
// display shape, while announcement eligibility adapts active subscription plan
// IDs and page storage uses the configured data directory.

// siteBillingSubscriptions narrows active subscriptions to announcement plan
// eligibility data.
type siteBillingSubscriptions struct {
	repo billing.UserSubscriptionRepository // active subscription reads
}

// ListActiveByUserID returns plan identities from the payer's active billing
// subscriptions.
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

// provideAnnouncementSubscriptions binds active billing subscription reads for
// announcement eligibility.
func provideAnnouncementSubscriptions(repo billing.UserSubscriptionRepository) site.SubscriptionReader {
	return siteBillingSubscriptions{repo: repo}
}

// provideAnnouncementExpiry constructs one-minute announcement expiration
// checks for lifecycle startup.
func provideAnnouncementExpiry(repo site.AnnouncementRepository) *site.AnnouncementExpiryService {
	return site.NewAnnouncementExpiryService(repo, time.Minute)
}

// provideSitePages binds page storage and display settings using the configured
// pricing data directory.
func provideSitePages(cfg *config.Config, settings *site.DisplaySettings) *sitehttp.PageHandler {
	return sitehttp.NewPageHandler(site.NewPages(filesystem.New(cfg.Storage.Dir), settings))
}

// provideSitePublic binds public authentication, team, and ranking
// interpretation to shared settings. cfg, oauth, and store are required;
// version comes from the store, while timezone display uses the supplied
// calendar and configured timezone text.
func provideSitePublic(store *settings.Store, oauth *identity.OAuthSettings, cfg *config.Config, calendar timezone.Calendar) *site.PublicService {
	p := site.NewPublicService(site.NewInputSource(store, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		value := oauth.PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, cfg.Team.Enabled, cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: value.LinuxDo, DingTalk: value.DingTalk, OIDC: value.OIDC, OIDCName: value.OIDCName, WeChat: value.WeChat, WeChatOpen: value.WeChatOpen, WeChatMP: value.WeChatMP, WeChatMobile: value.WeChatMobile, GitHub: value.GitHub, Google: value.Google, GoogleOneTap: value.GoogleOneTap, GoogleClientID: value.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: cfg.Auth.WebAuthn.Enabled, TencentRegion: value.TencentRegion, AliyunRegion: value.AliyunRegion, RegistrationSuffixes: value.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		value := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: value.Limit, Enabled: value.Enabled, SortBy: string(value.SortBy), ShowTotalTokens: value.ShowTotalTokens, ShowRequests: value.ShowRequests, ShowActualCost: value.ShowActualCost}
	}, Version: store.Version}), calendar, cfg.Timezone)
	return p
}

// provideSiteDisplay binds site names and a callback for the current frontend
// URL. cfg is retained by that callback, which the display reader invokes when
// its own fallback rule needs it.
func provideSiteDisplay(store *settings.Store, cfg *config.Config) *site.DisplaySettings {
	return site.NewDisplaySettings(store, func() string { return cfg.Site.FrontendURL })
}

// provideSitePublicHTTP supplies public site data and running version to HTTP.
func provideSitePublicHTTP(s *site.PublicService, info BuildInfo) *sitehttp.PublicHandler {
	return sitehttp.NewPublicHandler(s, info.Version)
}
