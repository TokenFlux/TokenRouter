package identity

import "strings"

// PublicAuthSettings 只包含浏览器可见的身份能力，不包含 secret 或内部认证选项。
type PublicAuthSettings struct {
	LinuxDo, DingTalk, OIDC, WeChat, WeChatOpen, WeChatMP, WeChatMobile, GitHub, Google, GoogleOneTap bool
	OIDCName, GoogleClientID, TencentRegion, AliyunRegion                                             string
	RegistrationSuffixes                                                                              []string
}

// PublicSettingsFromValues 使用已批量取得的设置，不额外查询或执行 provider 发现。
func (s *OAuthSettings) PublicSettingsFromValues(settings map[string]string) PublicAuthSettings {
	linuxDoEnabled := false
	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		linuxDoEnabled = raw == "true"
	} else {
		linuxDoEnabled = s.defaults != nil && s.defaults.LinuxDo.Enabled
	}
	dingTalkEnabled := false
	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		dingTalkEnabled = raw == "true"
	} else {
		dingTalkEnabled = s.defaults != nil && s.defaults.DingTalk.Enabled
	}
	oidcEnabled := false
	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		oidcEnabled = raw == "true"
	} else {
		oidcEnabled = s.defaults != nil && s.defaults.OIDC.Enabled
	}
	oidcProviderName := strings.TrimSpace(settings[SettingKeyOIDCConnectProviderName])
	if oidcProviderName == "" && s.defaults != nil {
		oidcProviderName = strings.TrimSpace(s.defaults.OIDC.ProviderName)
	}
	if oidcProviderName == "" {
		oidcProviderName = "OIDC"
	}
	weChatEnabled, weChatOpenEnabled, weChatMPEnabled, weChatMobileEnabled := s.WeChatOAuthCapabilitiesFromSettings(settings)
	gitHubOAuthEnabled := s.EmailOAuthPublicEnabled(settings, "github")
	googleOAuthEnabled := s.EmailOAuthPublicEnabled(settings, "google")
	googleOAuthConfig := s.EffectiveEmailOAuthConfig(settings, "google")
	googleOneTapEnabled := settings[SettingKeyGoogleOneTapEnabled] == "true" && googleOAuthEnabled
	googleOAuthClientID := ""
	if googleOneTapEnabled {
		googleOAuthClientID = strings.TrimSpace(googleOAuthConfig.ClientID)
	}

	return PublicAuthSettings{
		LinuxDo: linuxDoEnabled, DingTalk: dingTalkEnabled, OIDC: oidcEnabled, OIDCName: oidcProviderName,
		WeChat: weChatEnabled, WeChatOpen: weChatOpenEnabled, WeChatMP: weChatMPEnabled, WeChatMobile: weChatMobileEnabled,
		GitHub: gitHubOAuthEnabled, Google: googleOAuthEnabled, GoogleOneTap: googleOneTapEnabled, GoogleClientID: googleOAuthClientID,
		RegistrationSuffixes: ParseRegistrationEmailSuffixWhitelist(settings[SettingKeyRegistrationEmailSuffixWhitelist]), TencentRegion: NormalizeTencentCaptchaRegion(settings[SettingKeyTencentCaptchaRegion]), AliyunRegion: NormalizeAliyunCaptchaRegion(settings[SettingKeyAliyunCaptchaRegion]),
	}
}
