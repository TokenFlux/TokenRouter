package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// OAuthUsagePlatformOptions 只转交已装配的旧平台执行端口，不安装缓存或后台任务。
func OAuthUsagePlatformOptions(source *service.AccountUsageService) account.OAuthUsageOptions {
	return source.OAuthUsageOptions()
}
