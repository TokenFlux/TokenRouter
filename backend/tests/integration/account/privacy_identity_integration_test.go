//go:build integration

package account_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 隐私观测沿用原 Extra/outbox 提交，身份改变或事件失败不得写入成功模式。
func TestS06PrivacyObservationIdentityAndOutbox(t *testing.T) {
	for _, platform := range []string{account.PlatformOpenAI, account.PlatformAntigravity} {
		for _, scenario := range []string{"success", "credential_changed", "status_changed", "outbox_failure"} {
			t.Run(platform+"/"+scenario, func(t *testing.T) {
				ctx := context.Background()
				client := testEntClient(t)
				row, err := client.Account.Create().SetName("s06-privacy").SetPlatform(platform).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetCredentials(map[string]any{"access_token": "old", "project_id": "fixture"}).SetExtra(map[string]any{"privacy_mode": "previous"}).Save(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(ctx)) })
				store := newAccountStoreContract(client, integrationDB, nil)
				if scenario == "outbox_failure" {
					store.SetEvents(s06FailConfigurationOutbox{})
				}
				v, err := store.GetByID(ctx, row.ID)
				require.NoError(t, err)
				observe := func() string {
					switch scenario {
					case "credential_changed":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"access_token": "new", "project_id": "new-project"}).Exec(ctx))
					case "status_changed":
						require.NoError(t, client.Account.UpdateOneID(row.ID).SetStatus(account.StatusDisabled).Exec(ctx))
					}
					return "privacy_set"
				}
				core := account.NewPrivacyService(store, nil, account.PrivacyOptions{OpenAI: func(context.Context, string, string) string { return observe() }, Antigravity: func(context.Context, string, string, string) string { return observe() }})
				var before int
				require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&before))
				if platform == account.PlatformOpenAI {
					core.ForceOpenAIPrivacy(ctx, v)
				} else {
					core.ForceAntigravityPrivacy(ctx, v)
				}
				current, err := client.Account.Get(ctx, row.ID)
				require.NoError(t, err)
				var after int
				require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&after))
				if scenario == "success" {
					require.Equal(t, "privacy_set", current.Extra["privacy_mode"])
					require.Equal(t, before+1, after)
				} else {
					require.Equal(t, "previous", current.Extra["privacy_mode"])
					require.Equal(t, before, after)
				}
				require.NoError(t, core.StopContext(ctx))
			})
		}
	}
}
