// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type NotifyEmailEntry = identitydto.NotifyEmailEntry

func NotifyEmailEntriesFromService(entries []service.NotifyEmailEntry) []NotifyEmailEntry {
	return identitydto.NotifyEmailEntriesFromIdentity(entries)
}

func NotifyEmailEntriesToService(entries []NotifyEmailEntry) []service.NotifyEmailEntry {
	return identitydto.NotifyEmailEntriesToIdentity(entries)
}
