// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AdminCatalogOptions 仅转换平台目录端口，无缓存或目录规则。
func AdminCatalogOptions() routing.AdminCatalogOptions { return service.AdminCatalogOptions() }
func AccountAdminModelDefaults() account.ModelMappingDefaults {
	return service.AccountAdminModelDefaults()
}
