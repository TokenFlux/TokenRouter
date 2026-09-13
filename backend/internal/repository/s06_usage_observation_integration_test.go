//go:build integration

package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 条件语句使用真实 PostgreSQL JSONB/NULL 比较，不将模拟存储当作竞争证据。
func TestS06UsageObservationDatabaseIdentity(t *testing.T) {
	for _, action := range []string{"extra", "limit", "clear"} {
		t.Run(action, func(t *testing.T) {
			for _, change := range []string{"none", "name", "credentials", "status", "proxy", "window", "overload", "outbox_failure", "cancelled"} {
				t.Run(change, func(t *testing.T) {
					ctx := context.Background()
					client := testEntClient(t)
					now := time.Now().UTC().Truncate(time.Millisecond)
					until := now.Add(time.Hour)
					row, err := client.Account.Create().SetName("s06-observation").SetPlatform(account.PlatformQoder).SetType(account.AccountTypeCosy).SetStatus(account.StatusActive).SetCredentials(map[string]any{"security_oauth_token": "observed"}).SetRateLimitedAt(now).SetRateLimitResetAt(until).Save(ctx)
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
					store := (&accountRepository{client: client, sql: integrationDB}).accountData()
					observed, err := store.GetByID(ctx, row.ID)
					require.NoError(t, err)
					version := account.ObserveUsageVersion(observed)
					switch change {
					case "name":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetName("renamed").Exec(ctx))
					case "credentials":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"security_oauth_token": "administrator"}).Exec(ctx))
					case "status":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus(account.StatusDisabled).Exec(ctx))
					case "proxy":
						p, err := client.Proxy.Create().SetName("s06-usage-proxy").SetProtocol("http").SetHost("127.0.0.1").SetPort(8182).Save(ctx)
						require.NoError(t, err)
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetProxyID(p.ID).Exec(ctx))
						t.Cleanup(func() {
							require.NoError(t, client.Account.UpdateOneID(row.ID).ClearProxyID().Exec(context.Background()))
							require.NoError(t, client.Proxy.DeleteOneID(p.ID).Exec(context.Background()))
						})
					case "window":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetRateLimitResetAt(until.Add(time.Hour)).Exec(ctx))
					case "overload":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetOverloadUntil(until).Exec(ctx))
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
					var applied bool
					switch action {
					case "extra":
						applied, err = store.UpdateUsageExtraIfUnchanged(ctx, version, map[string]any{account.QoderUsageQuotaSnapshotExtraKey: map[string]any{"used": 50}})
					case "limit":
						applied, err = store.SetUsageRateLimitIfUnchanged(ctx, version, until.Add(2*time.Hour))
					case "clear":
						applied, err = store.ClearUsageRateLimitIfUnchanged(ctx, version)
					}
					if change == "cancelled" {
						require.ErrorIs(t, err, context.Canceled)
					} else {
						require.NoError(t, err)
					}
					want := change == "none" || change == "name" || change == "outbox_failure" || (action != "clear" && (change == "window" || change == "overload"))
					require.Equal(t, want, applied)
					after, err := client.Account.Get(context.Background(), row.ID)
					require.NoError(t, err)
					require.Equal(t, before.Credentials, after.Credentials)
					require.Equal(t, before.Status, after.Status)
					require.Equal(t, before.Name, after.Name)
					var eventsAfter int
					require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&eventsAfter))
					if !want {
						require.Equal(t, before.Extra, after.Extra)
						require.Equal(t, before.RateLimitResetAt, after.RateLimitResetAt)
						require.Equal(t, before.OverloadUntil, after.OverloadUntil)
						require.Equal(t, eventsBefore, eventsAfter)
					} else {
						switch action {
						case "extra":
							require.Contains(t, after.Extra, account.QoderUsageQuotaSnapshotExtraKey)
							require.Equal(t, before.RateLimitResetAt, after.RateLimitResetAt)
							require.Equal(t, eventsBefore, eventsAfter, "观测型 Extra 仍不新增 outbox")
						case "limit":
							require.WithinDuration(t, until.Add(2*time.Hour), *after.RateLimitResetAt, time.Millisecond)
						case "clear":
							require.Nil(t, after.RateLimitResetAt)
							require.Nil(t, after.RateLimitedAt)
							require.Nil(t, after.OverloadUntil)
						}
						if action != "extra" && change != "outbox_failure" {
							require.Equal(t, eventsBefore+1, eventsAfter)
						}
					}
				})
			}
		})
	}
}
