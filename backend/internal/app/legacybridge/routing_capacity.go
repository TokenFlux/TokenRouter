package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// CapacitySettings 只提供未迁请求上下文/动态设置的读投影，S07/S11 改绑；账号直接读取新存储。
func CapacitySettings(reader service.OpenAIQuotaAutoPauseSettingsReader) func(context.Context) account.QuotaAutoPauseSettings {
	return func(ctx context.Context) account.QuotaAutoPauseSettings {
		return service.AccountCapacitySettings(ctx, reader)
	}
}
