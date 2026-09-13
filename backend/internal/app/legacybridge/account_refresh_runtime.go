package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// BackgroundRefreshOptions 只转接旧供应商/调度缓存端口；S07/S09 改绑后删除。
func BackgroundRefreshOptions(source *service.TokenRefreshService) account.BackgroundRefreshOptions {
	return source.BackgroundRefreshOptions()
}
