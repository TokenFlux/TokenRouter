package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// wsExecutionOptionsForTest 仅投影WS执行参数，保留nil配置和显式零值。
func wsExecutionOptionsForTest(cfg *config.Config) *gatewayhttp.OpenAIWSOptions {
	if cfg == nil {
		return nil
	}
	v := cfg.Gateway.OpenAIWS
	return &gatewayhttp.OpenAIWSOptions{
		Enabled:                                v.Enabled,
		OAuthEnabled:                           v.OAuthEnabled,
		APIKeyEnabled:                          v.APIKeyEnabled,
		ForceHTTP:                              v.ForceHTTP,
		ResponsesWebsockets:                    v.ResponsesWebsockets,
		ResponsesWebsocketsV2:                  v.ResponsesWebsocketsV2,
		ModeRouterV2Enabled:                    v.ModeRouterV2Enabled,
		IngressModeDefault:                     v.IngressModeDefault,
		ClientFirstMessageTimeoutSeconds:       v.ClientFirstMessageTimeoutSeconds,
		IngressInterTurnIdleTimeoutSeconds:     v.IngressInterTurnIdleTimeoutSeconds,
		ClientReadLimitBytes:                   v.ClientReadLimitBytes,
		HTTPBridgeThresholdBytes:               v.HTTPBridgeThresholdBytes,
		HTTPBridgeEnabled:                      v.HTTPBridgeEnabled,
		AllowStoreRecovery:                     v.AllowStoreRecovery,
		IngressPreviousResponseRecoveryEnabled: v.IngressPreviousResponseRecoveryEnabled,
		StoreDisabledConnMode:                  v.StoreDisabledConnMode,
		StoreDisabledForceNewConn:              v.StoreDisabledForceNewConn,
		PrewarmGenerateEnabled:                 v.PrewarmGenerateEnabled,
		DialTimeoutSeconds:                     v.DialTimeoutSeconds,
		ReadTimeoutSeconds:                     v.ReadTimeoutSeconds,
		WriteTimeoutSeconds:                    v.WriteTimeoutSeconds,
		EventFlushBatchSize:                    v.EventFlushBatchSize,
		EventFlushIntervalMS:                   v.EventFlushIntervalMS,
		PrewarmCooldownMS:                      v.PrewarmCooldownMS,
		FallbackCooldownSeconds:                v.FallbackCooldownSeconds,
		RetryBackoffInitialMS:                  v.RetryBackoffInitialMS,
		RetryBackoffMaxMS:                      v.RetryBackoffMaxMS,
		RetryTotalBudgetMS:                     v.RetryTotalBudgetMS,
		RetryJitterRatio:                       v.RetryJitterRatio,
		PayloadLogSampleRate:                   v.PayloadLogSampleRate,
		StickyResponseIDTTLSeconds:             v.StickyResponseIDTTLSeconds,
	}
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
