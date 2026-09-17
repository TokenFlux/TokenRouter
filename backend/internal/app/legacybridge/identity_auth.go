// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// IdentityAuthSettings 保持认证动态设置的即时读取，只转换钉钉策略投影。
type IdentityAuthSettings struct{ *service.SettingService }

func (s IdentityAuthSettings) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	c, e := s.SettingService.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: c.Enabled, BypassRegistration: c.BypassRegistration, CorpRestrictionPolicy: c.CorpRestrictionPolicy}, e
}
