//go:build unit

package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
)

// 仅保留已有 unit 断言需要的旧入口，退出 S16。
func appendAuthSourceDefaultChanges(changed []string, before *identity.AuthSourceDefaultSettings, after *identity.AuthSourceDefaultSettings) []string {
	return settingshttp.AppendAuthSourceDefaultChanges(changed, before, after)
}

func equalNullableFloat(a, b *float64) bool { return settingshttp.EqualNullableFloat(a, b) }

func equalPlatformQuotaSettings(before, after map[string]*billing.DefaultPlatformQuotaSetting) bool {
	return settingshttp.EqualPlatformQuotaSettings(before, after)
}

func settingsAuditRequest(req UpdateSettingsRequest) UpdateSettingsRequest {
	return settingshttp.SettingsAuditRequest(req)
}
