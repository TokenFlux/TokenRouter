//go:build integration

package account_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/stretchr/testify/require"
)

// Extra 与窗口仍分别提交，二者之间的身份或窗口变化不能被旧结果覆盖。
type passiveUsageInterleaveStore struct {
	*accountpostgres.AccountStore
	afterExtra func()
}

func (s passiveUsageInterleaveStore) UpdateUsageExtraIfUnchanged(ctx context.Context, v account.UsageObservationVersion, updates map[string]any) (bool, error) {
	applied, err := s.AccountStore.UpdateUsageExtraIfUnchanged(ctx, v, updates)
	if s.afterExtra != nil {
		s.afterExtra()
	}
	return applied, err
}
func TestS06PassiveUsageIdentityAndIndependentWindow(t *testing.T) {
	for _, scenario := range []string{"success", "extra_write_failed", "changed_before", "changed_between", "window_changed", "window_outbox_failed", "canceled", "missing_conditional_writer"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			now := time.Now().UTC().Truncate(time.Second)
			oldEnd := now.Add(time.Hour)
			newEnd := now.Add(2 * time.Hour)
			row, err := client.Account.Create().SetName("s06-passive").SetPlatform(account.PlatformAnthropic).SetType(account.AccountTypeOAuth).SetStatus(account.StatusActive).SetCredentials(map[string]any{"access_token": "old"}).SetSessionWindowEnd(oldEnd).SetExtra(map[string]any{"unrelated": "keep"}).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
			store := newAccountStoreContract(client, integrationDB, nil)
			observed, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			changeIdentity := func() {
				require.NoError(t, client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"access_token": "administrator"}).Exec(ctx))
			}
			writer := passiveUsageInterleaveStore{AccountStore: store}
			switch scenario {
			case "changed_before":
				changeIdentity()
			case "changed_between":
				writer.afterExtra = changeIdentity
			case "window_changed":
				writer.afterExtra = func() {
					require.NoError(t, client.Account.UpdateOneID(row.ID).SetSessionWindowEnd(newEnd.Add(time.Hour)).Exec(ctx))
				}
			case "window_outbox_failed":
				store.SetEvents(s06FailConfigurationOutbox{})
			case "extra_write_failed":
				constraint := fmt.Sprintf("s06_passive_extra_%d", row.ID)
				_, err := integrationDB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE accounts ADD CONSTRAINT %s CHECK (id<>%d OR NOT (COALESCE(extra,'{}'::jsonb) ? 'session_window_utilization')) NOT VALID", constraint, row.ID))
				require.NoError(t, err)
				t.Cleanup(func() {
					_, err := integrationDB.ExecContext(context.Background(), "ALTER TABLE accounts DROP CONSTRAINT "+constraint)
					require.NoError(t, err)
				})
			}
			var reader account.OAuthUsageReader = writer
			if scenario == "missing_conditional_writer" {
				reader = struct{ account.OAuthUsageReader }{store}
			}
			operation, cancel := context.WithCancel(ctx)
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			var warnings []error
			core := account.NewOAuthUsageService(reader, nil, nil, account.OAuthUsageOptions{Warn: func(_ string, args ...any) {
				for _, arg := range args {
					if err, ok := arg.(error); ok {
						warnings = append(warnings, err)
					}
				}
			}})
			core.SyncActiveToPassive(operation, observed, &account.UsageInfo{FiveHour: &account.UsageProgress{Utilization: 37, ResetsAt: &newEnd}})
			got, err := store.GetByID(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, "keep", got.Extra["unrelated"])
			switch scenario {
			case "success":
				require.Equal(t, .37, got.Extra["session_window_utilization"])
				require.True(t, newEnd.Equal(*got.SessionWindowEnd))
			case "changed_before", "canceled", "missing_conditional_writer":
				require.NotContains(t, got.Extra, "session_window_utilization")
				require.True(t, oldEnd.Equal(*got.SessionWindowEnd))
			case "changed_between":
				require.Equal(t, .37, got.Extra["session_window_utilization"])
				require.True(t, oldEnd.Equal(*got.SessionWindowEnd))
				require.Equal(t, "administrator", got.GetCredential("access_token"))
			case "window_changed":
				require.True(t, newEnd.Add(time.Hour).Equal(*got.SessionWindowEnd))
			case "window_outbox_failed":
				require.Equal(t, .37, got.Extra["session_window_utilization"])
				require.True(t, newEnd.Equal(*got.SessionWindowEnd))
			case "extra_write_failed":
				require.NotContains(t, got.Extra, "session_window_utilization")
				require.True(t, newEnd.Equal(*got.SessionWindowEnd))
				require.NotEmpty(t, warnings)
			}
			if scenario == "canceled" {
				require.True(t, errors.Is(warnings[0], context.Canceled))
			}
		})
	}
}
