// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	strings "strings"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// IdentityHTTPSettings 只投影原运行时设置，各入口保持自己的读取时机。
type IdentityHTTPSettings struct{ Service *service.SettingService }

func (s IdentityHTTPSettings) BackendMode(ctx context.Context) bool {
	v, e := s.Service.GetPublicSettings(ctx)
	if e == nil && v != nil {
		return v.BackendModeEnabled
	}
	return s.Service.IsBackendModeEnabled(ctx)
}
func (s IdentityHTTPSettings) ForceEmail(ctx context.Context) bool {
	v, e := s.Service.GetAuthSourceDefaultSettings(ctx)
	return e == nil && v != nil && v.ForceEmailOnThirdPartySignup
}
func (s IdentityHTTPSettings) LinuxDo(ctx context.Context) (identity.LinuxDoOAuthOptions, error) {
	v, e := s.Service.GetLinuxDoConnectOAuthConfig(ctx)
	return identity.LinuxDoOAuthOptions(v), e
}
func (s IdentityHTTPSettings) OIDC(ctx context.Context) (identity.OIDCOAuthOptions, error) {
	v, e := s.Service.GetOIDCConnectOAuthConfig(ctx)
	return identity.OIDCOAuthOptions(v), e
}
func (s IdentityHTTPSettings) Email(ctx context.Context, p string) (identity.EmailOAuthOptions, error) {
	v, e := s.Service.GetEmailOAuthProviderConfig(ctx, p)
	return identity.EmailOAuthOptions(v), e
}
func (s IdentityHTTPSettings) DingTalk(ctx context.Context) (identity.DingTalkOAuthOptions, error) {
	v, e := s.Service.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkOAuthOptions(v), e
}
func (s IdentityHTTPSettings) GoogleOneTap(ctx context.Context) (identityhttp.GoogleOneTapOptions, error) {
	v, e := s.Service.GetGoogleOneTapConfig(ctx)
	return identityhttp.GoogleOneTapOptions{ClientID: v.ClientID, FrontendRedirectURL: v.FrontendRedirectURL}, e
}
func (s IdentityHTTPSettings) APIBaseURL(ctx context.Context) string {
	v, e := s.Service.GetAllSettings(ctx)
	if e == nil && v != nil {
		return strings.TrimSpace(v.APIBaseURL)
	}
	return ""
}
func (s IdentityHTTPSettings) WeChat(ctx context.Context, mode string) (identity.WeChatOAuthOptions, error) {
	base := s.APIBaseURL(ctx)
	v, e := s.Service.GetWeChatConnectOAuthConfig(ctx)
	if e != nil {
		return identity.WeChatOAuthOptions{}, e
	}
	return identity.WeChatOAuthOptions{Mode: mode, AppID: v.AppIDForMode(mode), AppSecret: v.AppSecretForMode(mode), Scope: v.ScopeForMode(mode), RedirectURI: v.RedirectURL, FrontendCallback: v.FrontendRedirectURL, APIBaseURL: base, OpenEnabled: v.OpenEnabled, MPEnabled: v.MPEnabled}, nil
}
func (s IdentityHTTPSettings) WeChatFrontend(ctx context.Context) string {
	v, e := s.Service.GetWeChatConnectOAuthConfig(ctx)
	if e == nil && strings.TrimSpace(v.FrontendRedirectURL) != "" {
		return strings.TrimSpace(v.FrontendRedirectURL)
	}
	return identityhttp.WechatOAuthDefaultFrontendCB
}
