// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

type UserAttributeService = identity.UserAttributeService

// NewUserAttributeService 委托所属模块的唯一实现。
func NewUserAttributeService(
	defRepo UserAttributeDefinitionRepository,
	valueRepo UserAttributeValueRepository,
) *UserAttributeService {
	return identity.NewUserAttributeService(defRepo, valueRepo)
}
