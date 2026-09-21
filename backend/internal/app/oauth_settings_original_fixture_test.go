//go:build unit

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// newOAuthSettingsFixture 直接验证装配的静态配置投影与身份模块动态读取。
func newOAuthSettingsFixture(repo settings.Repository, cfg *config.Config) *identity.OAuthSettings {
	return provideOAuthSettings(settings.New(repo), cfg)
}

// readOAuthAdminSettings 保留原测试未使用的默认并发值，展示规则由身份模块拥有。
func readOAuthAdminSettings(source *identity.OAuthSettings, values map[string]string) *identity.AdminReadSettings {
	return source.ReadAdminSettings(values, func() int { return 0 })
}
