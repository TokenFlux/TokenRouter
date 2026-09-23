//go:build integration

package promotion_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/promocodeusage"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	dto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	promotionpostgres "github.com/TokenFlux/TokenRouter/internal/promotion/postgres"
	"github.com/stretchr/testify/require"
)

// 新核心与真实存储组合，确认资金、累计充值、usage 和次数的原子边界。
func TestPromotionApplyCodeFundsAndUsageAtomicity(t *testing.T) {
	for _, failure := range []string{"", "usage", "count"} {
		t.Run("failure_"+failure, func(t *testing.T) {
			ctx := context.Background()
			client, integrationDB := testStore(t)
			user, err := client.User.Create().SetEmail("s12-promo@example.com").SetPasswordHash("must-not-export").SetUsername("promo").SetBalance(5).Save(ctx)
			require.NoError(t, err)
			repo := promotionpostgres.NewPromoCodeRepository(client)
			code := &promotion.PromoCode{Code: "S12PROMO", BonusAmount: 10, MaxUses: 2, Status: promotion.PromoCodeStatusActive}
			require.NoError(t, repo.Create(ctx, code))
			t.Cleanup(func() {
				_, e := client.PromoCodeUsage.Delete().Where(promocodeusage.PromoCodeIDEQ(code.ID)).Exec(ctx)
				require.NoError(t, e)
				require.NoError(t, repo.Delete(ctx, code.ID))
			})
			mutations := promotionpostgres.NewPromoMutations(client, func(tx *dbent.Tx) promotionpostgres.PromoBalance { return billingpostgres.BalanceInTx(tx) })
			invalidation := &promotionInvalidationProbe{}
			svc := promotion.NewPromoService(repo, mutations, invalidation, invalidation, promotion.Runtime{Now: time.Now})
			if failure != "" {
				table, event, condition := "promo_code_usages", "INSERT", fmt.Sprintf("NEW.promo_code_id=%d", code.ID)
				if failure == "count" {
					table, event, condition = "promo_codes", "UPDATE", fmt.Sprintf("NEW.id=%d AND NEW.used_count > OLD.used_count", code.ID)
				}
				name := fmt.Sprintf("s12_promo_failure_%d", code.ID)
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN IF (SELECT balance FROM users WHERE id=%d)<>15 THEN RAISE EXCEPTION 'credit not in same transaction'; END IF; RAISE EXCEPTION 's12 promo forced failure'; END IF; RETURN NEW; END; $$`, name, condition, user.ID))
				require.NoError(t, err)
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION %s()", name, event, table, name))
				require.NoError(t, err)
				t.Cleanup(func() {
					_, e := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s; DROP FUNCTION IF EXISTS %s()", name, table, name))
					require.NoError(t, e)
				})
			}
			err = svc.ApplyPromoCode(ctx, user.ID, code.Code)
			current, readErr := client.User.Get(ctx, user.ID)
			require.NoError(t, readErr)
			saved, readErr := repo.GetByID(ctx, code.ID)
			require.NoError(t, readErr)
			count, readErr := client.PromoCodeUsage.Query().Where(promocodeusage.PromoCodeIDEQ(code.ID)).Count(ctx)
			require.NoError(t, readErr)
			if failure != "" {
				require.ErrorContains(t, err, "s12 promo forced failure")
				require.Equal(t, 5.0, current.Balance)
				require.Zero(t, current.TotalRecharged)
				require.Zero(t, count)
				require.Zero(t, saved.UsedCount)
				require.Zero(t, invalidation.auth)
				require.Zero(t, invalidation.balance)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 15.0, current.Balance)
			require.Equal(t, 10.0, current.TotalRecharged)
			require.Equal(t, 1, count)
			require.Equal(t, 1, saved.UsedCount)
			require.Equal(t, 1, invalidation.auth)
			require.Equal(t, 1, invalidation.balance)
			require.ErrorIs(t, svc.ApplyPromoCode(ctx, user.ID, code.Code), promotion.ErrPromoCodeAlreadyUsed)
			current, readErr = client.User.Get(ctx, user.ID)
			require.NoError(t, readErr)
			require.Equal(t, 15.0, current.Balance)
			// 关系查询保留原公开浅层 JSON，不输出密码或递归实体。
			usages, _, err := repo.ListUsagesByPromoCode(ctx, code.ID, pagination.PaginationParams{Page: 1, PageSize: 20})
			require.NoError(t, err)
			require.Len(t, usages, 1)
			actual, err := json.Marshal(promotionhttp.PromoCodeUsageFromService(&usages[0]).User)
			require.NoError(t, err)
			expected, err := json.Marshal(dto.UserFromIdentityShallow[json.RawMessage](identity.CopyUser(identitypostgres.UserFromEntity(current))))
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(actual))
			require.NotContains(t, string(actual), "must-not-export")
		})
	}
}

// 后置失效只计数；失败事务不得触达该端口。
type promotionInvalidationProbe struct{ auth, balance int }

func (p *promotionInvalidationProbe) InvalidateAuthCacheByUserID(context.Context, int64) { p.auth++ }
func (p *promotionInvalidationProbe) InvalidateUserBalance(context.Context, int64) error {
	p.balance++
	return nil
}
