package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

// provideKeyHTTP 直接绑定 Key 用例与容量展示投影，不创建额外认证缓存。
func provideKeyHTTP(keys *apikey.APIKeyService, capacity *routing.CapacityService) *keyhttp.APIKeyHandler[dto.Group] {
	handler := keyhttp.NewAPIKeyHandler(keys, func(group *routing.Group, summary *accessview.GroupCapacitySummary) *dto.Group {
		result := dto.GroupFromRouting(apikey.RoutingGroup(group))
		if result != nil && summary != nil {
			result.Capacity = dto.GroupCapacityFromSummary(summary)
		}
		return result
	})
	handler.SetGroupCapacityService(capacity)
	return handler
}

// provideKeyAdminHTTP 直接注入 Key 管理用例，管理展示不经过旧实体往返转换。
func provideKeyAdminHTTP(keys *apikey.Admin) *keyhttp.AdminAPIKeyHandler[dto.Group] {
	return keyhttp.NewAdminAPIKeyHandler(keys, func(group *routing.Group) *dto.Group {
		return dto.GroupFromRouting(apikey.RoutingGroup(group))
	})
}
