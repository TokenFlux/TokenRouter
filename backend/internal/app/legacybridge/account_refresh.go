package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountRefreshPlatformPolicy 只绑定旧平台资格与故障类型；具体交换和分类在 S09 改绑。
func AccountRefreshPlatformPolicy() account.RefreshPlatformPolicy {
	return service.LegacyRefreshPlatformPolicy()
}
