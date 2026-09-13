//go:build integration

package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 手动刷新沿用原管理校验与配置事务，仅在锁内附加交换身份条件。
func TestS06ManagedCredentialsDatabaseCAS(t *testing.T) {
	for _, change := range []string{"none", "name", "credentials", "status", "proxy", "outbox_failure", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName("s06-managed-refresh").SetPlatform(account.PlatformAnthropic).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetCredentials(map[string]any{"refresh_token": "observed", "access_token": "old"}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			observed, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			expected := account.FailureVersion(observed).CredentialVersion
			switch change {
			case "name":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetName("renamed").Exec(ctx))
			case "credentials":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"refresh_token": "administrator"}).Exec(ctx))
			case "status":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus(account.StatusDisabled).Exec(ctx))
			case "proxy":
				proxy, err := client.Proxy.Create().SetName("s06-managed-proxy").SetProtocol("http").SetHost("127.0.0.1").SetPort(8123).Save(ctx)
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
			var eventsBefore int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&eventsBefore))
			desired := account.CloneRecord(observed)
			desired.Credentials = map[string]any{"refresh_token": "exchanged", "access_token": "new"}
			err = store.UpdateConfiguration(ctx, desired, account.ConfigurationChange{Fields: account.ConfigCredentials, ExpectedCredentials: &expected})
			want := change == "none" || change == "name"
			switch {
			case want:
				require.NoError(t, err)
			case change == "cancelled":
				require.ErrorIs(t, err, context.Canceled)
			case change == "outbox_failure":
				require.Error(t, err)
			default:
				require.ErrorIs(t, err, account.ErrRefreshAccountStateChanged)
			}
			after, readErr := client.Account.Get(context.Background(), row.ID)
			require.NoError(t, readErr)
			require.Equal(t, before.Name, after.Name)
			require.Equal(t, before.Status, after.Status)
			var eventsAfter int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&eventsAfter))
			if want {
				require.Equal(t, "exchanged", after.Credentials["refresh_token"])
				require.Equal(t, eventsBefore+1, eventsAfter)
			} else {
				require.Equal(t, before.Credentials, after.Credentials)
				require.Equal(t, eventsBefore, eventsAfter)
			}
			// 同一 Admin 入口仍执行原凭据校验；明确冲突不会进入校验或写入。
			if change == "credentials" {
				validations := 0
				admin := account.NewAdmin(store, account.AdminOptions{Creation: account.CreationOptions{Now: time.Now}, Credentials: account.CreateCredentialHooks{Site: func(*account.Record) (string, error) { validations++; return "", nil }}})
				_, err := admin.UpdateAccount(context.Background(), row.ID, &account.UpdateAccountInput{Credentials: desired.Credentials, ExpectedCredentials: &expected})
				require.ErrorIs(t, err, account.ErrRefreshAccountStateChanged)
				require.Zero(t, validations)
			}
		})
	}
}
