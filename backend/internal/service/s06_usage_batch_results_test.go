package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

// 缺失账号错误和并发查询错误写入同一结果集，二者必须共享同步边界。
type usageBatchRaceRepo struct{ AccountRepository }

func (usageBatchRaceRepo) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	out := make([]*Account, 0, len(ids)/2)
	for _, id := range ids {
		if id%2 == 0 {
			out = append(out, &Account{ID: id, Type: AccountTypeAPIKey})
		}
	}
	return out, nil
}
func TestUsageBatchMissingAndFailedAccountsShareSafeResultMap(t *testing.T) {
	ids := make([]int64, 601)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	svc := &AccountUsageService{accountRepo: usageBatchRaceRepo{}, cache: NewUsageCache()}
	values, failures, err := svc.GetUsageBatch(context.Background(), ids, false)
	require.NoError(t, err)
	require.Empty(t, values)
	require.Len(t, failures, len(ids))
}
