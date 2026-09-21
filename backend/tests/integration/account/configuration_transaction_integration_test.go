//go:build integration

package account_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	dbent "github.com/TokenFlux/TokenRouter/ent"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/stretchr/testify/require"
)

type s06FailConfigurationOutbox struct{ accountEventsFixture }

func (s06FailConfigurationOutbox) Write(ctx context.Context, exec postgresinfra.Executor, _ accountpostgres.AccountEvent, _, _ *int64, _ any) error {
	// 故障在真实事务连接上发生，验证配置 SQL 不会先行提交。
	_, err := exec.ExecContext(ctx, "INSERT INTO s06_missing_outbox_fixture DEFAULT VALUES")
	return err
}

func TestS06ConfigurationTransactionAndOutbox(t *testing.T) {
	for _, mode := range []string{"outer_rollback", "outbox_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			row, err := client.Account.Create().SetName("before-config").SetPlatform(capability.PlatformOpenAI).
				SetType(capability.AccountTypeAPIKey).SetExtra(map[string]any{"quota_used": 2.0}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := accountpostgres.NewAccountStore(client, integrationDB, accountpostgres.AccountStoreOptions{OllamaIdentity: acctcore.IsOllamaCloudUsageAccount, Events: accountEventsFixture{}})
			var tx *dbent.Tx
			if mode == "outer_rollback" {
				tx, err = client.Tx(ctx)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback() }()
				ctx = dbent.NewTxContext(ctx, tx)
			} else {
				store.SetEvents(s06FailConfigurationOutbox{})
			}
			value := &acctcore.Record{ID: row.ID, Name: "pending-config"}
			err = store.UpdateConfiguration(ctx, value, acctcore.ConfigurationChange{Fields: acctcore.ConfigName})
			if mode == "outer_rollback" {
				require.NoError(t, err)
				inside, err := tx.Client().Account.Get(ctx, row.ID)
				require.NoError(t, err)
				require.Equal(t, "pending-config", inside.Name)
				var count int
				rows, err := tx.Client().QueryContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", row.ID)
				require.NoError(t, err)
				require.True(t, rows.Next())
				require.NoError(t, rows.Scan(&count))
				require.NoError(t, rows.Close())
				require.Equal(t, 1, count)
				require.NoError(t, tx.Rollback())
			} else {
				require.ErrorContains(t, err, "s06_missing_outbox_fixture")
			}
			outside, err := client.Account.Get(context.Background(), row.ID)
			require.NoError(t, err)
			require.Equal(t, "before-config", outside.Name)
			require.Equal(t, 2.0, outside.Extra["quota_used"])
			var count int
			require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&count))
			require.Zero(t, count)
		})
	}
}
