// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	time "time"
)

var ErrRedeemCodeNotFound = billing.ErrRedeemCodeNotFound

var ErrRedeemCodeExists = billing.ErrRedeemCodeExists

var ErrRedeemCodeUsed = billing.ErrRedeemCodeUsed

var ErrRedeemCodeExpired = billing.ErrRedeemCodeExpired

var ErrRedeemCodeMaxUsed = billing.ErrRedeemCodeMaxUsed

var ErrRedeemCodeAlreadyUsed = billing.ErrRedeemCodeAlreadyUsed

var ErrInsufficientBalance = billing.ErrInsufficientBalance

var ErrRedeemRateLimited = billing.ErrRedeemRateLimited

var ErrRedeemCodeLocked = billing.ErrRedeemCodeLocked

type RedeemCache = billing.RedeemCache

type RedeemCodeRepository = billing.RedeemCodeRepository

type GenerateCodesRequest = billing.GenerateCodesRequest

type RedeemCodeResponse = billing.RedeemCodeResponse

type NullableTimeUpdate = billing.NullableTimeUpdate

type RedeemCodeBatchUpdateFields = billing.RedeemCodeBatchUpdateFields

type RedeemCodeBatchUpdateInput = billing.RedeemCodeBatchUpdateInput

type RedeemCodeBatchUpdateResult = billing.RedeemCodeBatchUpdateResult

type RedeemService = billing.RedeemService

// ContextSkipRedeemAffiliate 委托同一个上下文键，支付履约保留原去重边界。

func ContextSkipRedeemAffiliate(ctx context.Context) context.Context {
	return billing.ContextSkipRedeemAffiliate(ctx)
}

// NewRedeemService 保留旧调用构造；生产由 app 注入真实存储、投影与生命周期。

func NewRedeemService(repo RedeemCodeRepository, users UserRepository, subs *SubscriptionService, cache RedeemCache, eligibility *BillingCacheService, client *dbent.Client, auth APIKeyAuthCacheInvalidator, affiliates *AffiliateService) *RedeemService {

	var core *billing.Eligibility
	if eligibility != nil {
		core = eligibility.Eligibility
	}

	var affiliate billing.RedeemAffiliate
	if affiliates != nil {
		affiliate = affiliates
	}

	return billing.NewRedeemService(repo, billingBalanceProjection{users}, subs, cache, core, billingpostgres.NewRedeemMutations(client, users), auth, affiliate, billing.RedeemRuntime{Now: time.Now, Observe: logger.LegacyPrintf, Background: func(name string, fn func()) { RunBackgroundTask(name, BackgroundCall0(fn)) }})

}
