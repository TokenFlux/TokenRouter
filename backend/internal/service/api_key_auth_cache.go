// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

type APIKeyAuthSnapshot = apikey.APIKeyAuthSnapshot

type APIKeyAuthCompositeGroupSnapshot = apikey.APIKeyAuthCompositeGroupSnapshot

type APIKeyAuthActorSnapshot = apikey.APIKeyAuthActorSnapshot

type APIKeyAuthTeamSnapshot = apikey.APIKeyAuthTeamSnapshot

type APIKeyAuthUserSnapshot = apikey.APIKeyAuthUserSnapshot

type APIKeyAuthGroupSnapshot = apikey.APIKeyAuthGroupSnapshot

type APIKeyAuthCacheEntry = apikey.APIKeyAuthCacheEntry
