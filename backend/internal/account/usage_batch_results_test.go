package account

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 缺失账号错误和并发查询错误写入同一结果集，二者必须共享同步边界。
type usageBatchRaceRepo struct{ OAuthUsageReader }

func (usageBatchRaceRepo) GetByIDs(_ context.Context, ids []int64) ([]*Record, error) {
	out := make([]*Record, 0, len(ids)/2)
	for _, id := range ids {
		if id%2 == 0 {
			out = append(out, &Record{ID: id, Type: capability.AccountTypeAPIKey})
		}
	}
	return out, nil
}
func TestUsageBatchMissingAndFailedAccountsShareSafeResultMap(t *testing.T) {
	ids := make([]int64, 601)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	svc := NewOAuthUsageService(usageBatchRaceRepo{}, NewOAuthUsageCache(), nil, OAuthUsageOptions{})
	values, failures, err := svc.GetUsageBatch(context.Background(), ids, false)
	require.NoError(t, err)
	require.Empty(t, values)
	require.Len(t, failures, len(ids))
}
