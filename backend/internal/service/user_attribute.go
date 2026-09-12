// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

var ErrAttributeDefinitionNotFound = identity.ErrAttributeDefinitionNotFound

var ErrAttributeKeyExists = identity.ErrAttributeKeyExists

var ErrInvalidAttributeType = identity.ErrInvalidAttributeType

var ErrAttributeValidationFailed = identity.ErrAttributeValidationFailed

type UserAttributeType = identity.UserAttributeType

const AttributeTypeText = identity.AttributeTypeText

const AttributeTypeTextarea = identity.AttributeTypeTextarea

const AttributeTypeNumber = identity.AttributeTypeNumber

const AttributeTypeEmail = identity.AttributeTypeEmail

const AttributeTypeURL = identity.AttributeTypeURL

const AttributeTypeDate = identity.AttributeTypeDate

const AttributeTypeSelect = identity.AttributeTypeSelect

const AttributeTypeMultiSelect = identity.AttributeTypeMultiSelect

type UserAttributeOption = identity.UserAttributeOption

type UserAttributeValidation = identity.UserAttributeValidation

type UserAttributeDefinition = identity.UserAttributeDefinition

type UserAttributeValue = identity.UserAttributeValue

type CreateAttributeDefinitionInput = identity.CreateAttributeDefinitionInput

type UpdateAttributeDefinitionInput = identity.UpdateAttributeDefinitionInput

type UpdateUserAttributeInput = identity.UpdateUserAttributeInput

type UserAttributeDefinitionRepository = identity.UserAttributeDefinitionRepository

type UserAttributeValueRepository = identity.UserAttributeValueRepository
