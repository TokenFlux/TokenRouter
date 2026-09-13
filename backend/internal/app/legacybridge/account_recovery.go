// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountRecoveryOptions 只转交尚未迁移的计数、令牌与调度通知端口。
func AccountRecoveryOptions(source *service.RateLimitService) account.RecoveryOptions {
	return source.RecoveryOptions()
}
