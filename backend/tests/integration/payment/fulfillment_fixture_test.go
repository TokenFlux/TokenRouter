//go:build unit

package payment_test

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// 替身只记录兑换调用；已使用及已完成订单应在到达资金写入前结束。
type redeemCodeRepoStub struct {
	codesByCode map[string]*billing.RedeemCode
	useCalls    []string
}

type recordedRedeemer struct {
	payment.FulfillmentRedeemer
	repo *redeemCodeRepoStub
}

func fulfillmentRedeemer(repo *redeemCodeRepoStub) payment.FulfillmentRedeemer {
	return &recordedRedeemer{repo: repo}
}

func (r *recordedRedeemer) GetByCode(_ context.Context, code string) (*billing.RedeemCode, error) {
	return r.repo.codesByCode[code], nil
}

func (r *recordedRedeemer) Redeem(_ context.Context, _ int64, code string) (*billing.RedeemCode, error) {
	r.repo.useCalls = append(r.repo.useCalls, code)
	return r.repo.codesByCode[code], nil
}

// 对账夹具只暴露兑换所需余额读写，未配置的并发写入立即失败。
type fulfillmentBalance struct {
	billingpostgres.RedeemUserWriter
	getByIDUser     *billing.UserSummary
	updateBalanceFn func(context.Context, int64, float64) error
}

func (r *fulfillmentBalance) GetByID(context.Context, int64) (*billing.UserSummary, error) {
	user := *r.getByIDUser
	return &user, nil
}

func (r *fulfillmentBalance) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	if r.updateBalanceFn != nil {
		return r.updateBalanceFn(ctx, id, amount)
	}
	return nil
}

func newFulfillmentRedeemService(repo billing.RedeemCodeRepository, users *fulfillmentBalance, client *dbent.Client) *billing.RedeemService {
	return billing.NewRedeemService(repo, users, nil, nil, nil, billingpostgres.NewRedeemMutations(client, users), nil, nil, billing.RedeemRuntime{})
}
