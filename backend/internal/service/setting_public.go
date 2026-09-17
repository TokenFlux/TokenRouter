// 站点公开实现已迁移；此处只保留来源投影及历史入口。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/site"
)

// GetPublicSettings 获取公开设置（无需登录）
func (s *SettingService) LoadSitePublicInputs(ctx context.Context) (site.PublicInputs, error) {
	return site.NewInputSource(s.settingRepo, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		value := s.OAuthSettings().PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, s.cfg == nil || s.cfg.Team.Enabled, s.cfg == nil || s.cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: value.LinuxDo, DingTalk: value.DingTalk, OIDC: value.OIDC, OIDCName: value.OIDCName, WeChat: value.WeChat, WeChatOpen: value.WeChatOpen, WeChatMP: value.WeChatMP, WeChatMobile: value.WeChatMobile, GitHub: value.GitHub, Google: value.Google, GoogleOneTap: value.GoogleOneTap, GoogleClientID: value.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: s.cfg != nil && s.cfg.WebAuthn.Enabled, TencentRegion: value.TencentRegion, AliyunRegion: value.AliyunRegion, RegistrationSuffixes: value.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		value := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: value.Limit, Enabled: value.Enabled, SortBy: string(value.SortBy), ShowTotalTokens: value.ShowTotalTokens, ShowRequests: value.ShowRequests, ShowActualCost: value.ShowActualCost}
	}, Version: s.PublicVersion}).LoadSitePublicInputs(ctx)
}

// GetFrontendURL 获取前端基础URL（数据库优先，fallback 到配置文件）
func (s *SettingService) GetFrontendURL(ctx context.Context) string {
	return site.ReadFrontendURL(ctx, s.settingRepo, func() string { return s.cfg.Server.FrontendURL })
}

// IsUserErrorViewAllowed 读取用户侧失败请求展示开关。
// 读取失败时默认关闭，避免公开未确认的错误日志数据。
func (s *SettingService) IsUserErrorViewAllowed(ctx context.Context) bool {
	return s.UsageSettings().IsUserErrorViewAllowed(ctx)
}
func (s *SettingService) PublicVersion() string { return s.settingRuntime().Version() }
func (s *SettingService) sitePublic() *site.PublicService {
	if s.publicSite != nil {
		return s.publicSite
	}
	return site.NewPublicService(s)
}
func (s *SettingService) SetSitePublic(p *site.PublicService) { s.publicSite = p }
func (s *SettingService) GetPublicSettings(ctx context.Context) (*PublicSettings, error) {
	return s.sitePublic().GetPublicSettings(ctx)
}

func defaultLoginAgreementDocuments() []LoginAgreementDocument {
	return site.DefaultLoginAgreementDocuments()
}

func marshalLoginAgreementDocuments(docs []LoginAgreementDocument) (string, error) {
	return site.MarshalLoginAgreementDocuments(docs)
}

func (s *SettingService) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
	return s.sitePublic().GetPublicSettingsForInjection(ctx)
}

func (s *SettingService) GetFrameSrcOrigins(ctx context.Context) ([]string, error) {
	return s.sitePublic().GetFrameSrcOrigins(ctx)
}
