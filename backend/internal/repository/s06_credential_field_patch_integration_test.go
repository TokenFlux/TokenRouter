//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 管理校验后、配置行锁前发生真实凭据轮换；字段补丁必须保留新 token 和未选配置。
func TestS06CredentialFieldPatchPreservesLockTimeState(t *testing.T) {
	cases := []struct {
		name, field   string
		value         any
		outboxFailure bool
	}{{"org", "org_uuid", "patched", false}, {"clear_org", "org_uuid", nil, false}, {"account", "account_uuid", "patched", false}, {"warmup_on", "intercept_warmup_requests", true, false}, {"warmup_off", "intercept_warmup_requests", false, false}, {"rollback", "org_uuid", "patched", true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			initial := map[string]any{"access_token": "old", "refresh_token": "old-refresh", "base_url": "https://old.invalid", "org_uuid": "old-org", "account_uuid": "old-account", "intercept_warmup_requests": true}
			row, err := client.Account.Create().SetName("s06-credential-field").SetPlatform(account.PlatformAnthropic).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetCredentials(initial).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := (&accountRepository{client: client, sql: integrationDB}).accountData()
			if tc.outboxFailure {
				store.SetEvents(s06FailConfigurationOutbox{})
			}
			rotated := account.CloneValues(initial)
			rotated["access_token"] = "rotated"
			rotated["refresh_token"] = "rotated-refresh"
			rotated["base_url"] = "https://new.invalid"
			validations := 0
			options := account.AdminOptions{Creation: account.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: func() string { return "00000000-0000-4000-8000-000000000001" }}, Credentials: account.CreateCredentialHooks{Site: func(*account.Record) (string, error) { return "", nil }, ValidateEdit: func(context.Context, *account.Record, bool) error {
				validations++
				return client.Account.UpdateOneID(row.ID).SetCredentials(rotated).Exec(ctx)
			}}}
			admin := account.NewAdmin(store, options)
			var before int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&before))
			_, err = admin.UpdateAccount(ctx, row.ID, &account.UpdateAccountInput{Credentials: map[string]any{tc.field: tc.value}, PatchCredentials: true})
			if tc.outboxFailure {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, validations)
			current, err := client.Account.Get(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, "rotated", current.Credentials["access_token"])
			require.Equal(t, "rotated-refresh", current.Credentials["refresh_token"])
			require.Equal(t, "https://new.invalid", current.Credentials["base_url"])
			require.Contains(t, current.Credentials, tc.field)
			if tc.outboxFailure {
				require.Equal(t, rotated[tc.field], current.Credentials[tc.field])
			} else {
				require.Equal(t, tc.value, current.Credentials[tc.field])
			}
			var after int
			require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&after))
			if tc.outboxFailure {
				require.Equal(t, before, after)
			} else {
				require.Equal(t, before+1, after)
			}
		})
	}
}
