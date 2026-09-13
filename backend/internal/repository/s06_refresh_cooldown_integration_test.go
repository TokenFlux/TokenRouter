//go:build integration

package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// PostgreSQL 条件清理保留命名/余额等无关修改，拒绝身份与新 cooldown，并保留尽力 outbox。
func TestS06RefreshCooldownDatabaseIdentity(t *testing.T) {
	for _, change := range []string{"none", "name", "credentials", "status", "window", "reason", "outbox_failure", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			until := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
			row, err := client.Account.Create().SetName("s06-refresh-cooldown").SetPlatform(account.PlatformGemini).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetCredentials(map[string]any{"refresh_token": "observed"}).SetTempUnschedulableUntil(until).SetTempUnschedulableReason("old").Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			observed, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			switch change {
			case "name":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetName("renamed").Exec(ctx))
			case "credentials":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"refresh_token": "new"}).Exec(ctx))
			case "status":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus(account.StatusDisabled).Exec(ctx))
			case "window":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetTempUnschedulableUntil(until.Add(time.Hour)).Exec(ctx))
			case "reason":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetTempUnschedulableReason("new").Exec(ctx))
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
			applied, err := store.ClearRefreshCooldownIfUnchanged(ctx, account.ObserveRefreshCooldown(observed))
			if change == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
			want := change == "none" || change == "name" || change == "outbox_failure"
			require.Equal(t, want, applied)
			after, err := client.Account.Get(context.Background(), row.ID)
			require.NoError(t, err)
			require.Equal(t, before.Credentials, after.Credentials)
			require.Equal(t, before.Name, after.Name)
			require.Equal(t, before.Status, after.Status)
			var eventsAfter int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&eventsAfter))
			if want {
				require.Nil(t, after.TempUnschedulableUntil)
				require.Nil(t, after.TempUnschedulableReason)
				if change != "outbox_failure" {
					require.Equal(t, eventsBefore+1, eventsAfter)
				}
			} else {
				require.Equal(t, before.TempUnschedulableUntil, after.TempUnschedulableUntil)
				require.Equal(t, before.TempUnschedulableReason, after.TempUnschedulableReason)
				require.Equal(t, eventsBefore, eventsAfter)
			}
		})
	}
}
