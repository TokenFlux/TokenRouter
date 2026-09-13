package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountPrivacyOptions 只转接旧平台交换函数和观察接口，S09 改绑。
func AccountPrivacyOptions(factory service.PrivacyClientFactory) account.PrivacyOptions {
	return service.LegacyPrivacyOptions(factory)
}
