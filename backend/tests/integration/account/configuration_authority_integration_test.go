//go:build integration

package account_test

import (
	"context"
	"testing"
	"time"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 读取后注入真实数据库变化，固定重现配置写入与刷新、消费、健康维护的交错。
type s06ConfigurationInterleave struct {
	*accountpostgres.AccountStore
	afterRead func()
}

func (r *s06ConfigurationInterleave) GetByID(ctx context.Context, id int64) (*account.Record, error) {
	v, err := r.AccountStore.GetByID(ctx, id)
	if err == nil && r.afterRead != nil {
		f := r.afterRead
		r.afterRead = nil
		f()
	}
	return v, err
}

func TestS06ConfigurationWriteAuthority(t *testing.T) {
	for _, mode := range []string{"name", "extra", "status", "admin_name", "admin_extra", "admin_credentials"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName("s06-config-authority").
				SetPlatform(capability.PlatformOpenAI).SetType(capability.AccountTypeAPIKey).
				SetCredentials(map[string]any{"api_key": "old-test-key", "upstream_protocols": []string{"openai_responses"}}).
				SetExtra(map[string]any{"quota_used": 1.0, "quota_limit": 100.0}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			repo := &s06ConfigurationInterleave{AccountStore: newAccountStoreContract(client, integrationDB, nil)}
			repo.afterRead = func() {
				_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET
				 credentials=jsonb_set(credentials,'{api_key}','"new-test-key"'),
				 extra=extra || '{"quota_used":7,"quota_daily_used":5,"grok_billing_snapshot":{"new":true}}'::jsonb,
				 status='error', error_message='new-health-error', schedulable=false WHERE id=$1`, row.ID)
				require.NoError(t, err)
			}
			svc := account.NewBasicAccounts(repo, nil, uuid.NewString)
			admin := account.NewAdmin(repo, account.AdminOptions{Creation: account.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString}, Credentials: accountprovider.CreateCredentialHooks(nil, nil)})
			switch mode {
			case "admin_name":
				_, err = admin.UpdateAccount(ctx, row.ID, &account.UpdateAccountInput{Name: "edited-name"})
			case "admin_extra":
				_, err = admin.UpdateAccount(ctx, row.ID, &account.UpdateAccountInput{Extra: map[string]any{"quota_limit": 200.0}})
			case "admin_credentials":
				_, err = admin.UpdateAccount(ctx, row.ID, &account.UpdateAccountInput{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}})
			case "name":
				name := "edited-name"
				_, err = svc.Update(ctx, row.ID, account.UpdateAccountRequest{Name: &name})
			case "extra":
				extra := map[string]any{"quota_limit": 200.0}
				_, err = svc.Update(ctx, row.ID, account.UpdateAccountRequest{Extra: &extra})
			case "status":
				err = svc.UpdateStatus(ctx, row.ID, billing.StatusActive, "explicit-recovery")
			}
			require.NoError(t, err)
			current, err := repo.AccountStore.GetByID(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, "new-test-key", current.GetCredential("api_key"))
			require.Equal(t, float64(7), current.Extra["quota_used"])
			require.Equal(t, float64(5), current.Extra["quota_daily_used"])
			require.Equal(t, map[string]any{"new": true}, current.Extra["grok_billing_snapshot"])
			require.False(t, current.Schedulable)
			if mode == "status" {
				require.Equal(t, billing.StatusActive, current.Status)
				require.Equal(t, "explicit-recovery", current.ErrorMessage)
			} else {
				require.Equal(t, account.StatusError, current.Status)
				require.Equal(t, "new-health-error", current.ErrorMessage)
			}
			if mode == "extra" || mode == "admin_extra" {
				require.Equal(t, float64(200), current.Extra["quota_limit"])
			}
		})
	}
}
