//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 真实 PostgreSQL 检验旧交换版本的写权限，普通改名不干扰失败处理，其它身份更改必须拒绝。
func TestS06RefreshFailureVersionCAS(t *testing.T) {
	for _, kind := range []account.RefreshFailureKind{account.RefreshFailurePermanent, account.RefreshFailureCooldown} {
		for _, change := range []string{"none", "name", "credentials", "platform", "type", "status", "schedulable", "proxy"} {
			t.Run(string(rune('0'+kind))+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				client := testEntClient(t)
				row, err := client.Account.Create().SetName("refresh-failure-fixture").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetSchedulable(true).SetCredentials(map[string]any{"access_token": "old-fixture", "refresh_token": "old-refresh"}).Save(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
				repo := &accountRepository{client: client, sql: integrationDB}
				store := repo.accountData()
				snapshot, err := store.GetByID(ctx, row.ID)
				require.NoError(t, err)
				version := account.FailureVersion(snapshot)
				switch change {
				case "name":
					_, err = client.Account.UpdateOneID(row.ID).SetName("current-name").Save(ctx)
				case "credentials":
					_, err = client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"access_token": "admin-new-fixture"}).Save(ctx)
				case "platform":
					_, err = client.Account.UpdateOneID(row.ID).SetPlatform(account.PlatformGemini).Save(ctx)
				case "type":
					_, err = client.Account.UpdateOneID(row.ID).SetType(account.AccountTypeAPIKey).Save(ctx)
				case "status":
					_, err = client.Account.UpdateOneID(row.ID).SetStatus("inactive").Save(ctx)
				case "schedulable":
					_, err = client.Account.UpdateOneID(row.ID).SetSchedulable(false).Save(ctx)
				case "proxy":
					proxy, createErr := client.Proxy.Create().SetName("refresh-failure-proxy").SetProtocol("http").SetHost("127.0.0.1").SetPort(8181).Save(ctx)
					require.NoError(t, createErr)
					t.Cleanup(func() {
						_, clearErr := integrationDB.ExecContext(context.Background(), "UPDATE accounts SET proxy_id=NULL WHERE id=$1", row.ID)
						require.NoError(t, clearErr)
						require.NoError(t, client.Proxy.DeleteOneID(proxy.ID).Exec(context.Background()))
					})
					_, err = client.Account.UpdateOneID(row.ID).SetProxyID(proxy.ID).Save(ctx)
				}
				require.NoError(t, err)
				count := func() int {
					t.Helper()
					var n int
					require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&n))
					return n
				}
				before := count()
				failure := account.RefreshFailure{Kind: kind, Message: "fixture rejected", Until: time.Now().Add(10 * time.Minute).Truncate(time.Microsecond)}
				matched, err := store.ApplyOAuthRefreshFailure(ctx, version, failure)
				require.NoError(t, err)
				current, err := client.Account.Get(ctx, row.ID)
				require.NoError(t, err)
				if change != "none" && change != "name" {
					require.False(t, matched)
					require.Empty(t, current.ErrorMessage)
					require.Nil(t, current.TempUnschedulableUntil)
					require.Equal(t, before, count())
					return
				}
				require.True(t, matched)
				require.Equal(t, before+1, count())
				if kind == account.RefreshFailurePermanent {
					require.Equal(t, account.StatusError, current.Status)
					require.False(t, current.Schedulable)
					require.NotNil(t, current.ErrorMessage)
					require.Equal(t, failure.Message, *current.ErrorMessage)
				} else {
					require.True(t, failure.Until.Equal(*current.TempUnschedulableUntil))
					require.NotNil(t, current.TempUnschedulableReason)
					require.Equal(t, failure.Message, *current.TempUnschedulableReason)
					require.Equal(t, account.StatusActive, current.Status)
				}
				if change == "name" {
					require.Equal(t, "current-name", current.Name)
				}
			})
		}
	}
}

// 更长 cooldown 是同身份的原有无变更结果；outbox 失败仍不回滚已经生效的健康写入。
func TestS06RefreshFailureLongerCooldownAndOutboxFailure(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	until := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	row, err := client.Account.Create().SetName("refresh-failure-semantics").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetSchedulable(true).SetCredentials(map[string]any{}).SetTempUnschedulableUntil(until).SetTempUnschedulableReason("existing-longer").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	store := (&accountRepository{client: client, sql: integrationDB}).accountData()
	snapshot, err := store.GetByID(ctx, row.ID)
	require.NoError(t, err)
	version := account.FailureVersion(snapshot)
	matched, err := store.ApplyOAuthRefreshFailure(ctx, version, account.RefreshFailure{Kind: account.RefreshFailureCooldown, Message: "shorter", Until: until.Add(-time.Minute)})
	require.NoError(t, err)
	require.True(t, matched)
	current, err := client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.NotNil(t, current.TempUnschedulableReason)
	require.Equal(t, "existing-longer", *current.TempUnschedulableReason)
	require.True(t, until.Equal(*current.TempUnschedulableUntil))
	require.True(t, row.UpdatedAt.Equal(current.UpdatedAt))
	store.SetEvents(s06FailConfigurationOutbox{})
	matched, err = store.ApplyOAuthRefreshFailure(ctx, version, account.RefreshFailure{Kind: account.RefreshFailurePermanent, Message: "fixture auth rejected"})
	require.NoError(t, err)
	require.True(t, matched)
	current, err = client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, account.StatusError, current.Status)
	require.False(t, current.Schedulable)
}

// 强制刷新请求仍使用原 Extra/outbox 原子范围，并在后置清理时再次检查交换身份。
func TestS06AntigravityRefreshRequestClearCAS(t *testing.T) {
	for _, mode := range []string{"success", "reauthorized", "outbox_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName("refresh-request-fixture").SetPlatform(account.PlatformAntigravity).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetSchedulable(true).SetCredentials(map[string]any{"access_token": "fixture"}).SetExtra(map[string]any{account.AntigravityForceTokenRefreshExtraKey: true, account.AntigravityForceTokenRefreshReasonExtraKey: "fixture 401", account.AntigravityForceTokenRefreshAtExtraKey: "fixture time", "quota_used": 17.0}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			snapshot, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			version := account.FailureVersion(snapshot).CredentialVersion
			if mode == "reauthorized" {
				_, err = client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"access_token": "new-admin-fixture"}).Save(ctx)
				require.NoError(t, err)
			}
			if mode == "outbox_failure" {
				store.SetEvents(s06FailConfigurationOutbox{})
			}
			matched, err := store.ClearAntigravityRefreshRequest(ctx, version)
			if mode == "outbox_failure" {
				require.Error(t, err)
				require.False(t, matched)
			} else {
				require.NoError(t, err)
				require.Equal(t, mode == "success", matched)
			}
			current, err := client.Account.Get(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, 17.0, current.Extra["quota_used"])
			require.Equal(t, mode != "success", current.Extra[account.AntigravityForceTokenRefreshExtraKey])
			if mode == "success" {
				require.Equal(t, "", current.Extra[account.AntigravityForceTokenRefreshReasonExtraKey])
				require.Equal(t, "", current.Extra[account.AntigravityForceTokenRefreshAtExtraKey])
			}
		})
	}
}
