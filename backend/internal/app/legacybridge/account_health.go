// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountHealthOptions 仅投影原设置、计数器和调度阻断反馈，S07 改绑反馈源。
func AccountHealthOptions(source *service.RateLimitService) account.HealthOptions {
	return source.HealthOptions()
}
