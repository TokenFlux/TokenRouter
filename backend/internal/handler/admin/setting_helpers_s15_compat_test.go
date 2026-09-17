package admin

import (
	gatewaydto "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
) // 旧测试只保留 helper 名称转接，所有断言仍针对唯一 HTTP 实现。
func diffSettings(before *composite.Snapshot, after *composite.Snapshot, beforeAuthSourceDefaults *identity.AuthSourceDefaultSettings, afterAuthSourceDefaults *identity.AuthSourceDefaultSettings, req UpdateSettingsRequest) []string {
	return settingshttp.DiffSettings(before, after, beforeAuthSourceDefaults, afterAuthSourceDefaults, req)
}

func openaiFastPolicySettingsFromDTO(s *gatewaydto.OpenAIFastPolicySettings) *tierpolicy.OpenAIFastPolicySettings {
	return settingshttp.OpenaiFastPolicySettingsFromDTO(s)
}
