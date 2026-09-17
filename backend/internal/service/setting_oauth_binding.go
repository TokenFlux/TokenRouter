package service

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
)

// SetOAuthSettings 仅在 app 装配期间绑定唯一原生配置用例。
func (s *SettingService) SetOAuthSettings(settings *identity.OAuthSettings) {
	s.oauthSettings = settings
}

// OAuthSettings 保留旧构造和测试入口；生产值由 app 投影和注入。
func (s *SettingService) OAuthSettings() *identity.OAuthSettings {
	if s == nil {
		return identity.NewOAuthSettings(nil, nil, identityprovider.ResolveSettingsOIDCMetadata)
	}
	s.oauthSettingsOnce.Do(func() {
		if s.oauthSettings == nil {
			var defaults *identity.OAuthSettingsDefaults
			if s.cfg != nil {
				defaults = &identity.OAuthSettingsDefaults{LinuxDo: s.cfg.LinuxDo, DingTalk: s.cfg.DingTalk, OIDC: s.cfg.OIDC, WeChat: s.cfg.WeChat, GitHubOAuth: s.cfg.GitHubOAuth, GoogleOAuth: s.cfg.GoogleOAuth}
			}
			s.oauthSettings = identity.NewOAuthSettings(s.settingRepo, defaults, identityprovider.ResolveSettingsOIDCMetadata)
		}
	})
	return s.oauthSettings
}
