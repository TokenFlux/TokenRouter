package app

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provideGatewayPromptPolicy 绑定原设置表与原诊断输出，保持缓存作用域与更新时机。
func provideGatewayPromptPolicy(store *settings.Store, legacy *service.SettingService) *promptpolicy.Service {
	policy := promptpolicy.New(store, settings.ErrSettingNotFound, slog.Warn)
	legacy.BindUserPromptPolicy(policy)
	return policy
}
