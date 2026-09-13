package postgres

import (
	"context"
	"time"

	dbaccount "github.com/TokenFlux/TokenRouter/ent/account"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// ListSchedulableAccountLoads 只加载 Ops 队列深度采样所需的账号 ID 与并发字段。
func (r *AccountStore) ListSchedulableAccountLoads(ctx context.Context) ([]accountcore.LoadObservation, error) {
	accounts, err := r.SchedulableAccountsQuery(time.Now()).
		Select(
			dbaccount.FieldID,
			dbaccount.FieldConcurrency,
			dbaccount.FieldLoadFactor,
		).
		All(ctx)
	if err != nil {
		return nil, err
	}

	loads := make([]accountcore.LoadObservation, 0, len(accounts))
	for _, account := range accounts {
		projection := accountcore.Record{
			ID:          account.ID,
			Concurrency: account.Concurrency,
			LoadFactor:  account.LoadFactor,
		}
		loads = append(loads, accountcore.LoadObservation{
			ID:             account.ID,
			MaxConcurrency: projection.EffectiveLoadFactor(),
		})
	}
	return loads, nil
}
