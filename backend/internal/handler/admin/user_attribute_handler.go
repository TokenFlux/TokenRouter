// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type UserAttributeHandler = identityhttp.UserAttributeHandler

// NewUserAttributeHandler 委托所属模块的唯一实现。
func NewUserAttributeHandler(attrService *service.UserAttributeService) *UserAttributeHandler {
	return identityhttp.NewUserAttributeHandler(attrService)
}

type CreateAttributeDefinitionRequest = identityhttp.CreateAttributeDefinitionRequest

type UpdateAttributeDefinitionRequest = identityhttp.UpdateAttributeDefinitionRequest

type ReorderRequest = identityhttp.ReorderRequest

type UpdateUserAttributesRequest = identityhttp.UpdateUserAttributesRequest

type BatchGetUserAttributesRequest = identityhttp.BatchGetUserAttributesRequest

type BatchUserAttributesResponse = identityhttp.BatchUserAttributesResponse

type AttributeDefinitionResponse = identityhttp.AttributeDefinitionResponse

type AttributeValueResponse = identityhttp.AttributeValueResponse
