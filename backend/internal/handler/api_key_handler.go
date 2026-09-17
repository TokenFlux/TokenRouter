// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	dto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// APIKeyHandler 是新 HTTP handler 的兼容名称，路由规则与数据均委托新模块。
type APIKeyHandler = keyhttp.APIKeyHandler[dto.Group]

func NewAPIKeyHandler(keys *service.APIKeyService) *APIKeyHandler {
	return keyhttp.NewAPIKeyHandler(keys.APIKeyService, func(g *apikey.Group, capacity *accessview.GroupCapacitySummary) *dto.Group {
		out := dto.GroupFromRouting(apikey.RoutingGroup(g))
		if out != nil && capacity != nil {
			out.Capacity = dto.GroupCapacityFromSummary(capacity)
		}
		return out
	})
}

type CreateAPIKeyRequest = keyhttp.CreateAPIKeyRequest
type UpdateAPIKeyRequest = keyhttp.UpdateAPIKeyRequest

type APIKeyBillingSubscriptionOptionResponse = keyhttp.APIKeyBillingSubscriptionOptionResponse
