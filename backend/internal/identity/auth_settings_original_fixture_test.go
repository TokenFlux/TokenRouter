//go:build unit

package identity_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// authSettingsFixture 只组合所属模块的设置解释器，保留同一替身与读取时点。
type authSettingsFixture struct {
	*identity.RuntimeSettings
	*identity.GrantSettings
	*identity.OAuthSettings
	*site.DisplaySettings
	promotion *promotion.RuntimeSettings
}

func newAuthSettingsFixture(repo settings.Repository, cfg *config.Config) *authSettingsFixture {
	var defaults *identity.OAuthSettingsDefaults
	grants := identity.GrantSettingsOptions{}
	if cfg != nil {
		defaults = &identity.OAuthSettingsDefaults{LinuxDo: cfg.LinuxDo, DingTalk: cfg.DingTalk, OIDC: cfg.OIDC, WeChat: cfg.WeChat, GitHubOAuth: cfg.GitHubOAuth, GoogleOAuth: cfg.GoogleOAuth}
		grants.DefaultBalance = cfg.Default.UserBalance
		grants.DefaultConcurrency = cfg.Default.UserConcurrency
	}
	return &authSettingsFixture{
		RuntimeSettings: identity.NewRuntimeSettings(repo, settings.ErrSettingNotFound),
		GrantSettings:   identity.NewGrantSettings(repo, grants),
		OAuthSettings:   identity.NewOAuthSettings(repo, defaults, identityprovider.ResolveSettingsOIDCMetadata),
		DisplaySettings: site.NewDisplaySettings(repo, nil),
		promotion:       promotion.NewRuntimeSettings(repo),
	}
}

func (s *authSettingsFixture) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	value, err := s.OAuthSettings.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: value.Enabled, BypassRegistration: value.BypassRegistration, CorpRestrictionPolicy: value.CorpRestrictionPolicy}, err
}

func (s *authSettingsFixture) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsInvitationCodeEnabled(ctx)
}

func (s *authSettingsFixture) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsPromoCodeEnabled(ctx)
}

// authSettingsPort 保留旧可选具体指针的 nil 语义。
func authSettingsPort(value *authSettingsFixture) identity.AuthSettings {
	if value == nil {
		return nil
	}
	return value
}

// rebuildOriginalSession 将测试显式修改的会话选项及缓存绑定到原生实现。
func rebuildOriginalSession(service *identity.AuthService) {
	service.SessionService = identity.NewSessionService(service.Options.JWT, service.Users, service.RefreshTokens, service.Settings, service.Observer.Log)
}
