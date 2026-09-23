//go:build integration

package account_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 普通名称编辑不能把读取之后发生的使用时间和限流窗口写回旧值。
func TestS06AccountConfigurationPreservesConcurrentRuntime(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account, err := client.Account.Create().SetName(fmt.Sprintf("s06-runtime-%d", time.Now().UnixNano())).SetPlatform(capability.PlatformOpenAI).SetType(capability.AccountTypeOAuth).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(account.ID).Exec(context.Background())) })
	repo := newAccountStoreContract(client, integrationDB, nil)
	stale, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	usedAt := time.Now().UTC().Truncate(time.Microsecond)
	resetAt := usedAt.Add(time.Hour)
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET last_used_at=$1,rate_limited_at=$1,rate_limit_reset_at=$2 WHERE id=$3", usedAt, resetAt, account.ID)
	require.NoError(t, err)
	stale.Name = "name-edited"
	require.NoError(t, repo.Update(ctx, stale))
	current, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "name-edited", current.Name)
	require.NotNil(t, current.LastUsedAt)
	require.WithinDuration(t, usedAt, *current.LastUsedAt, time.Microsecond)
	require.NotNil(t, current.RateLimitResetAt)
	require.WithinDuration(t, resetAt, *current.RateLimitResetAt, time.Microsecond)
}
