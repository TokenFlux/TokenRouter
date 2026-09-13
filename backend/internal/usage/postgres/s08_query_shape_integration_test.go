//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

// 现有存储端口的计数器用于固定 SQL 查询次数与小型夹具 Explain 基线。
type s08SQLCall struct {
	Query string `json:"query"`
	Args  []any  `json:"-"`
}
type s08CountingSQL struct {
	sqlExecutor
	calls []s08SQLCall
}

func (s *s08CountingSQL) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	s.calls = append(s.calls, s08SQLCall{q, a})
	return s.sqlExecutor.QueryContext(ctx, q, a...)
}
func TestS08QueryShapeMatchesPlanning(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	writer := NewUsageLogRepositoryWithSQL(client, tx)
	account := mustCreateAccount(t, client, &service.Account{Name: "s08-query-shape"})
	ids := []int64{}
	keys := []int64{}
	for i := 0; i < 8; i++ {
		u := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("s08-query-%d@test.local", i)})
		key := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("sk-s08-query-%d", i), Name: "k"})
		ids = append(ids, u.ID)
		keys = append(keys, key.ID)
		for j := 0; j < 4; j++ {
			_, e := writer.Create(ctx, &usage.UsageLog{UserID: u.ID, APIKeyID: key.ID, AccountID: account.ID, Model: "planning", InputTokens: 10, OutputTokens: 5, TotalCost: 0.1, ActualCost: 0.1, CreatedAt: time.Now().Add(-time.Hour)})
			require.NoError(t, e)
		}
	}
	counter := &s08CountingSQL{sqlExecutor: tx}
	repo := NewUsageLogRepositoryWithSQL(client, counter)
	start, end := time.Now().Add(-2*time.Hour), time.Now()
	results := map[string]any{}
	for _, n := range []int{4, 8} {
		counter.calls = nil
		_, e := repo.GetBatchUserUsageStats(ctx, ids[:n], start, end)
		require.NoError(t, e)
		results[fmt.Sprintf("batch_users_%d", n)] = len(counter.calls)
		counter.calls = nil
		_, e = repo.GetBatchAPIKeyUsageStats(ctx, keys[:n], start, end)
		require.NoError(t, e)
		results[fmt.Sprintf("batch_keys_%d", n)] = len(counter.calls)
	}
	counter.calls = nil
	_, e := repo.GetUserSpendingRanking(ctx, start, end, 8)
	require.NoError(t, e)
	results["user_ranking"] = len(counter.calls)
	plans := []any{}
	for _, call := range counter.calls {
		rows, e := tx.QueryContext(ctx, "EXPLAIN (FORMAT JSON) "+call.Query, call.Args...)
		require.NoError(t, e)
		for rows.Next() {
			var raw []byte
			require.NoError(t, rows.Scan(&raw))
			plans = append(plans, json.RawMessage(raw))
		}
		require.NoError(t, rows.Close())
	}
	results["ranking_explain"] = plans
	raw, e := json.MarshalIndent(results, "", "  ")
	require.NoError(t, e)
	for _, key := range []string{"batch_users_4", "batch_users_8", "batch_keys_4", "batch_keys_8", "user_ranking"} {
		require.Equal(t, 1, results[key], key)
	}
	t.Logf("S08_QUERY_SHAPE=%s", raw)
}
