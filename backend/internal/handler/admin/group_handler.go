// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
)

type GroupHandler = routinghttp.GroupHandler
type CreateGroupRequest = routinghttp.CreateGroupRequest
type UpdateGroupRequest = routinghttp.UpdateGroupRequest
type BatchSetGroupRateMultipliersRequest = routinghttp.BatchSetGroupRateMultipliersRequest
type BatchSetGroupRPMOverridesRequest = routinghttp.BatchSetGroupRPMOverridesRequest
type UpdateSortOrderRequest = routinghttp.UpdateSortOrderRequest
