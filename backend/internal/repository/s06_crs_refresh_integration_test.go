//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// PostgreSQL 真正提交管理员修改，与导入交换结果交错，确认不会被后续 CAS 覆盖。
func TestS06CRSRefreshDatabaseInterleaving(t *testing.T) {
	for _, mode := range []string{"success", "credentials", "disabled", "cancelled", "outbox_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName("s06-crs-refresh").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeOAuth).SetCredentials(map[string]any{"refresh_token": "source", "_token_version": 123}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			original, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			if mode == "outbox_failure" {
				store.SetEvents(s06FailConfigurationOutbox{})
			}
			api := account.NewOAuthRefreshAPI(store, nil, account.RefreshOptions{})
			started, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			calls := 0
			go func() {
				done <- api.RefreshImported(ctx, original, fmt.Sprintf("s06-crs:%d", row.ID), func(context.Context, *account.Record) map[string]any {
					calls++
					close(started)
					<-release
					return map[string]any{"refresh_token": "exchanged", "_token_version": 123}
				})
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("导入交换未开始")
			}
			switch mode {
			case "credentials":
				require.NoError(t, store.UpdateCredentials(ctx, row.ID, map[string]any{"refresh_token": "administrator"}))
			case "disabled":
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus("inactive").Exec(ctx))
			case "cancelled":
				cancel()
			}
			close(release)
			var result error
			select {
			case result = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("导入交换未结束")
			}
			current, err := store.GetByID(context.Background(), row.ID)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			switch mode {
			case "success":
				require.NoError(t, result)
				require.Equal(t, "exchanged", current.Credentials["refresh_token"])
				require.Equal(t, float64(123), current.Credentials["_token_version"])
			case "credentials":
				require.NoError(t, result)
				require.Equal(t, "administrator", current.Credentials["refresh_token"])
			case "disabled":
				require.NoError(t, result)
				require.Equal(t, "inactive", current.Status)
				require.Equal(t, "source", current.Credentials["refresh_token"])
			case "cancelled":
				require.ErrorIs(t, result, context.Canceled)
				require.Equal(t, "source", current.Credentials["refresh_token"])
			case "outbox_failure":
				require.Error(t, result)
				require.Equal(t, "source", current.Credentials["refresh_token"])
			}
		})
	}
}
