// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
)

var ErrPendingAuthSessionNotFound = identity.ErrPendingAuthSessionNotFound

var ErrPendingAuthSessionExpired = identity.ErrPendingAuthSessionExpired

var ErrPendingAuthSessionConsumed = identity.ErrPendingAuthSessionConsumed

var ErrPendingAuthCodeInvalid = identity.ErrPendingAuthCodeInvalid

var ErrPendingAuthCodeExpired = identity.ErrPendingAuthCodeExpired

var ErrPendingAuthCodeConsumed = identity.ErrPendingAuthCodeConsumed

var ErrPendingAuthBrowserMismatch = identity.ErrPendingAuthBrowserMismatch

type PendingAuthIdentityKey = identity.PendingAuthIdentityKey

type CreatePendingAuthSessionInput = identity.CreatePendingAuthSessionInput

type IssuePendingAuthCompletionCodeInput = identity.IssuePendingAuthCompletionCodeInput

type IssuePendingAuthCompletionCodeResult = identity.IssuePendingAuthCompletionCodeResult

type PendingIdentityAdoptionDecisionInput = identity.PendingIdentityAdoptionDecisionInput

type AuthPendingIdentityService = identitypostgres.AuthPendingIdentityService

func NewAuthPendingIdentityService(entClient *dbent.Client) *AuthPendingIdentityService {
	return identitypostgres.NewAuthPendingIdentityService(entClient)
}
