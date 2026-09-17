// 推广的存储、资金参与与运行依赖由唯一组合根构造。
package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"

	dbent "github.com/TokenFlux/TokenRouter/ent"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/promotion"

	promotionpostgres "github.com/TokenFlux/TokenRouter/internal/promotion/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func providePromotionAffiliateStore(client *dbent.Client) promotion.AffiliateRepository {
	return promotionpostgres.NewAffiliateRepository(client, func(tx *dbent.Tx) promotionpostgres.TransferBalance { return billingpostgres.BalanceInTx(tx) })
}
func providePromotionAffiliate(repo promotion.AffiliateRepository, settings *service.SettingService, auth service.APIKeyAuthCacheInvalidator, balances *service.BillingCacheService) *promotion.AffiliateService {
	return promotion.NewAffiliateService(repo, settings, auth, balances, promotion.Runtime{Now: time.Now, Warn: func(id int64, err error) {
		logging.LegacyPrintf("service.affiliate", "[Affiliate] Failed to invalidate billing cache for user %d: %v", id, err)
	}})
}

// identityPromotion 将推广档案结果投影为身份用例需要的成功/失败。
type identityPromotion struct{ Service *promotion.AffiliateService }

func (p identityPromotion) EnsureUserAffiliate(ctx context.Context, id int64) error {
	_, err := p.Service.EnsureUserAffiliate(ctx, id)
	return err
}
func (p identityPromotion) BindInviterByCode(ctx context.Context, id int64, code string) error {
	return p.Service.BindInviterByCode(ctx, id, code)
}

func providePromotionPromoStore(client *dbent.Client) promotion.PromoCodeRepository {
	return promotionpostgres.NewPromoCodeRepository(client)
}
func providePromotionPromo(client *dbent.Client, repo promotion.PromoCodeRepository, auth service.APIKeyAuthCacheInvalidator, balances *service.BillingCacheService, tasks *lifecycle.Tasks) *promotion.PromoService {
	mutations := promotionpostgres.NewPromoMutations(client, func(tx *dbent.Tx) promotionpostgres.PromoBalance { return billingpostgres.BalanceInTx(tx) })
	return promotion.NewPromoService(repo, mutations, auth, balances, promotion.Runtime{Now: time.Now, Background: func(name string, fn func()) { tasks.Go(name, fn) }})
}

// 公开预览只投影优惠码验证结果，不增加字段或读取。
func identityPromotionPreview(s *promotion.PromoService) func(context.Context, string) identityhttp.PromotionPreview {
	return func(ctx context.Context, code string) identityhttp.PromotionPreview {
		v := s.PreviewRegistrationPromotion(ctx, code)
		return identityhttp.PromotionPreview{Valid: v.Valid, BonusAmount: v.BonusAmount, ErrorCode: v.ErrorCode}
	}
}
func providePromotionPromoHTTP(s *promotion.PromoService) *promotionhttp.PromoHandler {
	return promotionhttp.NewPromoHandler(s)
}
func providePromotionAffiliateHTTP(s *promotion.AffiliateService, users *identity.UserAdmin) *promotionhttp.AffiliateHandler {
	return promotionhttp.NewAffiliateHandler(s, func(ctx context.Context, keyword string) ([]promotionhttp.AffiliateUserSummary, error) {
		values, _, err := users.ListUsers(ctx, 1, 20, identity.UserListFilters{Search: keyword}, "email", "asc")
		if err != nil {
			return nil, err
		}
		out := make([]promotionhttp.AffiliateUserSummary, len(values))
		for i, u := range values {
			out[i] = promotionhttp.AffiliateUserSummary{ID: u.ID, Email: u.Email, Username: u.Username}
		}
		return out, nil
	})
}
