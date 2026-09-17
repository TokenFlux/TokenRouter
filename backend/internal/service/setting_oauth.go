package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

func CoerceDingTalkCorpPolicyForWrite(policy string) string {
	return identity.SettingsCoerceDingTalkCorpPolicyForWrite(policy)
}

func DefaultWeChatConnectScopesForMode(mode string) string {
	return identity.SettingsDefaultWeChatConnectScopesForMode(mode)
}

func (s *SettingService) OIDCSecurityWriteDefaults(ctx context.Context) (bool, bool, error) {
	return s.OAuthSettings().OIDCSecurityWriteDefaults(ctx)
}

func (s *SettingService) GetEmailOAuthProviderConfig(ctx context.Context, provider string) (config.EmailOAuthProviderConfig, error) {
	return s.OAuthSettings().GetEmailOAuthProviderConfig(ctx, provider)
}

func (s *SettingService) GetGoogleOneTapConfig(ctx context.Context) (config.EmailOAuthProviderConfig, error) {
	return s.OAuthSettings().GetGoogleOneTapConfig(ctx)
}

func (s *SettingService) GetLinuxDoConnectOAuthConfig(ctx context.Context) (config.LinuxDoConnectConfig, error) {
	return s.OAuthSettings().GetLinuxDoConnectOAuthConfig(ctx)
}

func (s *SettingService) GetDingTalkConnectOAuthConfig(ctx context.Context) (config.DingTalkConnectConfig, error) {
	return s.OAuthSettings().GetDingTalkConnectOAuthConfig(ctx)
}

func (s *SettingService) GetWeChatConnectOAuthConfig(ctx context.Context) (WeChatConnectOAuthConfig, error) {
	return s.OAuthSettings().GetWeChatConnectOAuthConfig(ctx)
}

func (s *SettingService) GetOIDCConnectOAuthConfig(ctx context.Context) (config.OIDCConnectConfig, error) {
	return s.OAuthSettings().GetOIDCConnectOAuthConfig(ctx)
}
