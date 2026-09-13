// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountTierOptions 只转交原 Drive 客户端观测，供应商解析 S09 改绑。
func AccountTierOptions(source *service.GeminiOAuthService) account.TierManagementOptions {
	return service.AccountTierManagementOptions(source)
}
