package admission

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

// PlatformFromAPIKey 从 APIKey 关联的 Group 推导 platform 名称。
// apiKey 为 nil 或 Group 信息缺失时返回空串（调用方据此 short-circuit quota 累加）。
// 导出供 handler 层调用。
func PlatformFromAPIKey(apiKey *apikey.APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}

// QuotaPlatform 返回 user×platform 配额计量使用的平台标识。
// 强制平台路由（如 /antigravity）优先按 ctx 中的 ForcePlatform 计量，否则回退到
// APIKey 关联 Group 的平台。
//
// 注意：必须用带 ForcePlatform 的请求 context 调用（如 handler 的 c.Request.Context()）。
// 后扣运行在 worker 池的 background ctx 上没有 ForcePlatform，因此后扣平台由 handler
// 预先算定、经 RecordUsageInput.QuotaPlatform 传入，不要在后扣链路用 worker ctx 调用本函数。
func QuotaPlatform(ctx context.Context, apiKey *apikey.APIKey) string {
	if fp, ok := apikey.ForcePlatformFromContext(ctx); ok && fp != "" {
		return fp
	}
	return PlatformFromAPIKey(apiKey)
}
