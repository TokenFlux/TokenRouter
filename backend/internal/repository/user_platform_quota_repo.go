// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
)

type UserPlatformQuotaRecord = billing.UserPlatformQuotaRecord

var ErrUserPlatformQuotaNotFound = billing.ErrUserPlatformQuotaNotFound

var ErrUserPlatformQuotaFKViolation = billing.ErrUserPlatformQuotaFKViolation

type UserPlatformQuotaSnapshot = billing.UserPlatformQuotaSnapshot

type UserPlatformQuotaRepository = billing.UserPlatformQuotaRepository

func NewUserPlatformQuotaRepository(client *dbent.Client) UserPlatformQuotaRepository {
	return billingpostgres.NewUserPlatformQuotaRepository(client)
}
