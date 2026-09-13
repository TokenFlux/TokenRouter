//go:build integration

package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 真实配置事务验证 Drive 观测只写自身字段，并在取锁后重新比较凭据身份。
func TestS06TierObservationUsesCurrentIdentityAndFieldPatch(t *testing.T) {
	for _, scenario := range []string{"success", "changed_during_observation", "changed_before_lock", "outbox_failure", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := testEntClient(t)
			initial := map[string]any{"oauth_type": "google_one", "access_token": "original", "tier_id": "old"}
			row, err := client.Account.Create().SetName("s06-tier-observation").SetPlatform(account.PlatformGemini).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetCredentials(initial).SetExtra(map[string]any{"admin_setting": "old"}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			if scenario == "outbox_failure" {
				store.SetEvents(s06FailConfigurationOutbox{})
			}
			rotated := account.CloneValues(initial)
			rotated["access_token"] = "admin-new"
			options := account.AdminOptions{Creation: account.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: func() string { return "00000000-0000-4000-8000-000000000001" }}, Credentials: account.CreateCredentialHooks{Site: func(*account.Record) (string, error) { return "", nil }, ValidateEdit: func(context.Context, *account.Record, bool) error {
				if scenario == "changed_before_lock" {
					return client.Account.UpdateOneID(row.ID).SetCredentials(rotated).Exec(ctx)
				}
				return nil
			}}}
			admin := account.NewAdmin(store, options)
			observed, err := admin.GetAccount(ctx, row.ID)
			require.NoError(t, err)
			tier := account.NewTierManagement(admin, account.TierManagementOptions{Observe: func(context.Context, *account.Record) (account.GoogleOneTierObservation, error) {
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetExtra(map[string]any{"admin_setting": "new"}).Exec(ctx))
				if scenario == "changed_during_observation" {
					require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(rotated).Exec(ctx))
				}
				if scenario == "canceled" {
					cancel()
				}
				return account.GoogleOneTierObservation{TierID: "google_ai_pro", Storage: &account.GoogleOneStorage{Limit: 2000, Usage: 12}, ObservedAt: time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)}, nil
			}})
			var before int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&before))
			_, err = tier.Refresh(ctx, observed)
			if scenario == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			current, readErr := client.Account.Get(context.Background(), row.ID)
			require.NoError(t, readErr)
			require.Equal(t, "new", current.Extra["admin_setting"])
			if scenario == "changed_during_observation" || scenario == "changed_before_lock" {
				require.Equal(t, "admin-new", current.Credentials["access_token"])
			} else {
				require.Equal(t, "original", current.Credentials["access_token"])
			}
			var after int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&after))
			if scenario == "success" {
				require.Equal(t, "google_ai_pro", current.Credentials["tier_id"])
				require.EqualValues(t, 2000, current.Extra["drive_storage_limit"])
				require.EqualValues(t, 12, current.Extra["drive_storage_usage"])
				require.Equal(t, before+1, after)
			} else {
				require.Equal(t, "old", current.Credentials["tier_id"])
				require.NotContains(t, current.Extra, "drive_storage_limit")
				require.Equal(t, before, after)
			}
			require.NoError(t, tier.StopContext(context.Background()))
		})
	}
}
