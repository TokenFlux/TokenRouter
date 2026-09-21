package provider

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// PrivacyOptions 组合供应商隐私请求，生命周期和条件写入由账号用例拥有。
func PrivacyOptions(factory openai.PrivacyClientFactory, endpoints openai.PrivacyEndpoints) account.PrivacyOptions {
	antigravity := account.AntigravityAuthorization{Options: AntigravityAuthorizationOptions(nil)}
	options := account.PrivacyOptions{
		Antigravity: antigravity.SetPrivacy,
		Warn:        slog.Warn,
		Info:        slog.Info,
		AdminObserve: func(format string, args ...any) {
			logging.LegacyPrintf("service.admin", format, args...)
		},
	}
	if factory != nil {
		authorization := OpenAIAuthorizationOptions(&OpenAIAuthorizationDependencies{
			PrivacyFactory: factory, PrivacyEndpoints: endpoints,
		})
		options.OpenAI = authorization.DisableTraining
	}
	return options
}
