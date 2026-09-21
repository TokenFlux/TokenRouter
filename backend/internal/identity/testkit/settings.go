package testkit

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// SettingsReaders 只组合所属模块的设置解释器，保留同一替身与读取时点。
type SettingsReaders struct {
	*admission.BackendMode
	*identity.RuntimeSettings
	*identity.GrantSettings
	*identity.OAuthSettings
	*site.DisplaySettings
	promotion *promotion.RuntimeSettings
}

func Settings(repo settings.Repository, cfg *config.Config) *SettingsReaders {
	var defaults *identity.OAuthSettingsDefaults
	grants := identity.GrantSettingsOptions{}
	if cfg != nil {
		defaults = &identity.OAuthSettingsDefaults{LinuxDo: cfg.LinuxDo, DingTalk: cfg.DingTalk, OIDC: cfg.OIDC, WeChat: cfg.WeChat, GitHubOAuth: cfg.GitHubOAuth, GoogleOAuth: cfg.GoogleOAuth}
		grants.DefaultBalance = cfg.Default.UserBalance
		grants.DefaultConcurrency = cfg.Default.UserConcurrency
	}
	return &SettingsReaders{
		BackendMode:     admission.NewBackendMode(repo, nil),
		RuntimeSettings: identity.NewRuntimeSettings(repo, settings.ErrSettingNotFound),
		GrantSettings:   identity.NewGrantSettings(repo, grants),
		OAuthSettings:   identity.NewOAuthSettings(repo, defaults, identityprovider.ResolveSettingsOIDCMetadata),
		DisplaySettings: site.NewDisplaySettings(repo, func() string {
			if cfg == nil {
				return ""
			}
			return cfg.Server.FrontendURL
		}),
		promotion: promotion.NewRuntimeSettings(repo),
	}
}

func (s *SettingsReaders) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	value, err := s.OAuthSettings.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: value.Enabled, BypassRegistration: value.BypassRegistration, CorpRestrictionPolicy: value.CorpRestrictionPolicy}, err
}

func (s *SettingsReaders) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsInvitationCodeEnabled(ctx)
}

func (s *SettingsReaders) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsPromoCodeEnabled(ctx)
}
