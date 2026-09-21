// 旧 WS 池入口只投影技术配置和已选账号；连接、队列和生命周期由原生唯一实现持有。
package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func newOpenAIWSConnPool(cfg *config.Config) *openai.WSConnPool {
	return openai.NewWSConnPool(openAIWSPoolOptions(cfg))
}

func activeCodexFingerprintMode(account *Account) accountcore.CodexFingerprintMode {
	if account == nil || account.GetCodexFingerprintMode() == accountcore.CodexFingerprintOff {
		return accountcore.CodexFingerprintOff
	}
	if _, ok := accountcore.CodexFingerprintSeed(account.Extra); !ok {
		return accountcore.CodexFingerprintOff
	}
	return account.GetCodexFingerprintMode()
}

// 原配置在取值时投影；不把完整配置或账号凭据交给原生池。
func openAIWSPoolOptions(cfg *config.Config) *openai.WSPoolOptions {
	if cfg == nil {
		return nil
	}
	options := cfg.Gateway.OpenAIWS
	return &openai.WSPoolOptions{

		MaxConnsPerAccount: options.MaxConnsPerAccount,

		DynamicMaxConnsByAccountConcurrencyEnabled: options.DynamicMaxConnsByAccountConcurrencyEnabled,

		ModeRouterV2Enabled: options.ModeRouterV2Enabled,

		OAuthMaxConnsFactor:  options.OAuthMaxConnsFactor,
		APIKeyMaxConnsFactor: options.APIKeyMaxConnsFactor,

		MinIdlePerAccount: options.MinIdlePerAccount,
		MaxIdlePerAccount: options.MaxIdlePerAccount,
		QueueLimitPerConn: options.QueueLimitPerConn,

		PoolTargetUtilization: options.PoolTargetUtilization,
		PrewarmCooldownMS:     options.PrewarmCooldownMS,
		DialTimeoutSeconds:    options.DialTimeoutSeconds,
	}
}
func openAIWSPoolAccountView(account *Account) *openai.WSPoolAccount {
	if account == nil {
		return nil
	}
	return &openai.WSPoolAccount{ID: account.ID, Concurrency: account.Concurrency, Type: account.Type, FingerprintMode: string(activeCodexFingerprintMode(account))}
}
