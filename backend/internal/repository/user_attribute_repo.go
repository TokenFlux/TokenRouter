// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	postgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func NewUserAttributeDefinitionRepository(client *dbent.Client) service.UserAttributeDefinitionRepository {
	return postgres.NewUserAttributeDefinitionRepository(client)
}

func NewUserAttributeValueRepository(client *dbent.Client) service.UserAttributeValueRepository {
	return postgres.NewUserAttributeValueRepository(client)
}
