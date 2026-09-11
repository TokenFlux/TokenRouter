package postgres

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// BalanceInTx 显式复用调用方事务，所有方法均不取得或结束事务，也不发布副作用。
func BalanceInTx(tx *dbent.Tx) *BalanceStore {
	return NewBalanceStore(tx.Client())
}

// SubscriptionsInTx 让初始读取、计划读取及后续写入全部使用同一事务连接。
func SubscriptionsInTx(tx *dbent.Tx, groups billing.SubscriptionGroupReader, clock billing.DateRuntime) *billing.SubscriptionService {
	return billing.NewSubscriptionService(groups, NewUserSubscriptionRepository(tx.Client()), SubscriptionMutationsInTx(tx), clock)
}

// RedeemInTx 只提供兑换资金/并发数增量参与能力，兑换闭合用例仍由 RedeemService 持有。
// 用于旧外层事务的精确适配，不允许通过本入口独立提交权益。
func RedeemInTx(tx *dbent.Tx) *RedeemParticipant {
	return &RedeemParticipant{tx: tx, mutations: NewRedeemMutations(tx.Client(), BalanceInTx(tx))}
}

type RedeemParticipant struct {
	tx        *dbent.Tx
	mutations *RedeemMutations
}

func (p *RedeemParticipant) ApplyBalance(ctx context.Context, id int64, amount float64) error {
	return p.mutations.ApplyBalance(dbent.NewTxContext(ctx, p.tx), id, amount)
}
func (p *RedeemParticipant) ApplyConcurrency(ctx context.Context, id int64, delta int) error {
	return p.mutations.ApplyConcurrency(dbent.NewTxContext(ctx, p.tx), id, delta)
}
