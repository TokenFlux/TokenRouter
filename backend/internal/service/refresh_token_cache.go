// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

var ErrRefreshTokenNotFound = identity.ErrRefreshTokenNotFound

type RefreshTokenData = identity.RefreshTokenData

type RefreshTokenCache = identity.RefreshTokenCache
