//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 用真实数据库检查迟到用量恢复的行身份、错误条件和原尽力 outbox 行为。
func TestS06UsageRecoveryDatabaseIdentity(t *testing.T) {
	for _, change := range []string{"none", "name", "credentials", "status", "error", "proxy", "cancelled", "outbox_failure"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName("s06-usage-recovery").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeOAuth).SetStatus(account.StatusError).SetErrorMessage("token refresh failed").SetSchedulable(false).SetCredentials(map[string]any{"refresh_token": "observed"}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			observed, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			switch change {
			case "name":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetName("new name").Exec(ctx))
			case "credentials":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"refresh_token": "administrator"}).Exec(ctx))
			case "status":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus(account.StatusDisabled).Exec(ctx))
			case "error":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetErrorMessage("administrator forbidden").Exec(ctx))
			case "proxy":
				proxy, err := client.Proxy.Create().SetName("s06-usage-recovery-proxy").SetProtocol("http").SetHost("127.0.0.1").SetPort(8182).Save(ctx)
				require.NoError(t, err)
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetProxyID(proxy.ID).Exec(ctx))
				t.Cleanup(func() {
					require.NoError(t, client.Account.UpdateOneID(row.ID).ClearProxyID().Exec(context.Background()))
					require.NoError(t, client.Proxy.DeleteOneID(proxy.ID).Exec(context.Background()))
				})
			case "outbox_failure":
				store.SetEvents(s06FailConfigurationOutbox{})
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, err := client.Account.Get(context.Background(), row.ID)
			require.NoError(t, err)
			var outboxBefore int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&outboxBefore))
			applied, err := account.RecoverUsageAccountError(ctx, observed, store)
			if change == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
			current, readErr := client.Account.Get(context.Background(), row.ID)
			require.NoError(t, readErr)
			want := change == "none" || change == "name" || change == "outbox_failure"
			require.Equal(t, want, applied)
			require.False(t, current.Schedulable, "恢复错误不等同于显式重新启用调度")
			var outboxAfter int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&outboxAfter))
			if want {
				require.Equal(t, account.StatusActive, current.Status)
				require.NotNil(t, current.ErrorMessage)
				require.Empty(t, *current.ErrorMessage)
				require.Equal(t, account.StatusActive, observed.Status)
				if change != "outbox_failure" {
					require.Equal(t, outboxBefore+1, outboxAfter)
				}
			} else {
				require.Equal(t, before.Status, current.Status)
				require.Equal(t, before.ErrorMessage, current.ErrorMessage)
				require.Equal(t, before.Credentials, current.Credentials)
				require.Equal(t, outboxBefore, outboxAfter)
				require.Equal(t, account.StatusError, observed.Status)
			}
		})
	}
}
