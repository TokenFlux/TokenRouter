//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 在每次旧独立提交前插入管理员修改，使用真实 PostgreSQL 验证条件而非模拟 SQL。
type managedRecoveryInterleaveStore struct {
	*accountpostgres.AccountStore
	before  func(account.ManagedRecoveryStep) error
	applied []account.ManagedRecoveryStep
}

func (s *managedRecoveryInterleaveStore) ApplyManagedRecoveryStep(ctx context.Context, step account.ManagedRecoveryStep, v account.ManagedRecoveryVersion) (bool, error) {
	if err := s.before(step); err != nil {
		return false, err
	}
	applied, err := s.AccountStore.ApplyManagedRecoveryStep(ctx, step, v)
	if applied {
		s.applied = append(s.applied, step)
	}
	return applied, err
}

func TestS06ManagedRecoveryIndependentCommitsAndIdentity(t *testing.T) {
	for step := account.ManagedRecoveryError; step <= account.ManagedRecoveryTemporary; step++ {
		for _, scenario := range []string{"unchanged", "credentials", "error", "owned_state", "write_failure", "outbox_failure", "cancel"} {
			t.Run(fmt.Sprintf("step_%d/%s", step, scenario), func(t *testing.T) {
				ctx := context.Background()
				client := testEntClient(t)
				now := time.Now().UTC().Truncate(time.Second)
				until := now.Add(time.Hour)
				row, err := client.Account.Create().SetName("s06-ag-recovery").SetPlatform(account.PlatformAntigravity).SetType(account.AccountTypeOAuth).SetStatus(account.StatusError).SetErrorMessage("missing_project_id: original").SetCredentials(map[string]any{"access_token": "old", "refresh_token": "old"}).SetRateLimitedAt(now).SetRateLimitResetAt(until).SetOverloadUntil(until).SetTempUnschedulableUntil(until).SetTempUnschedulableReason("old reason").SetExtra(map[string]any{"antigravity_quota_scopes": map[string]any{"old": true}, "model_rate_limits": map[string]any{"old": true}, "unrelated": "keep"}).Save(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
				store := (&accountRepository{client: client, sql: integrationDB}).accountData()
				if scenario == "outbox_failure" {
					store.SetEvents(s06FailConfigurationOutbox{})
				}
				observed, err := store.GetByID(ctx, row.ID)
				require.NoError(t, err)
				var before int
				require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&before))
				operation, cancel := context.WithCancel(ctx)
				defer cancel()
				writer := &managedRecoveryInterleaveStore{AccountStore: store, before: func(current account.ManagedRecoveryStep) error {
					if current != step {
						return nil
					}
					switch scenario {
					case "credentials":
						return client.Account.UpdateOneID(row.ID).SetCredentials(map[string]any{"access_token": "administrator"}).SetStatus(account.StatusDisabled).Exec(ctx)
					case "error":
						return client.Account.UpdateOneID(row.ID).SetStatus(account.StatusError).SetErrorMessage("administrator error").Exec(ctx)
					case "owned_state":
						switch step {
						case account.ManagedRecoveryError:
							return client.Account.UpdateOneID(row.ID).SetErrorMessage("new error").Exec(ctx)
						case account.ManagedRecoveryRateLimit:
							return client.Account.UpdateOneID(row.ID).SetRateLimitResetAt(until.Add(time.Hour)).Exec(ctx)
						case account.ManagedRecoveryQuotaScopes:
							_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET extra=jsonb_set(extra,'{antigravity_quota_scopes}','null'::jsonb) WHERE id=$1", row.ID)
							return err
						case account.ManagedRecoveryModelLimits:
							_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET extra=extra-'model_rate_limits' WHERE id=$1", row.ID)
							return err
						case account.ManagedRecoveryTemporary:
							return client.Account.UpdateOneID(row.ID).SetTempUnschedulableReason("new reason").Exec(ctx)
						}
					case "write_failure":
						return errors.New("fixture step failed")
					case "cancel":
						cancel()
					case "unchanged":
						return client.Account.UpdateOneID(row.ID).SetName("administrator rename").Exec(ctx)
					}
					return nil
				}}
				_, applied, err := account.NewAdmin(writer, account.AdminOptions{}).ClearManagedRefreshError(operation, observed)
				if scenario == "write_failure" || scenario == "cancel" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				success := scenario == "unchanged" || scenario == "outbox_failure"
				require.Equal(t, success, applied)
				current, err := store.GetByID(ctx, row.ID)
				require.NoError(t, err)
				require.Equal(t, "keep", current.Extra["unrelated"])
				var after int
				require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&after))
				expectedEvents := int(step)
				if success {
					expectedEvents = 5
				}
				require.Len(t, writer.applied, expectedEvents)
				// 原 outbox 对同账号合并待处理事件，五次独立发布仍只留一条待消费记录。
				if expectedEvents > 0 {
					expectedEvents = 1
				}
				if scenario == "outbox_failure" {
					expectedEvents = 0
				}
				require.Equal(t, before+expectedEvents, after)
				if success {
					require.Equal(t, account.StatusActive, current.Status)
					require.Empty(t, current.ErrorMessage)
					require.Nil(t, current.RateLimitedAt)
					require.Nil(t, current.RateLimitResetAt)
					require.Nil(t, current.OverloadUntil)
					require.Nil(t, current.TempUnschedulableUntil)
					require.NotContains(t, current.Extra, "antigravity_quota_scopes")
					require.NotContains(t, current.Extra, "model_rate_limits")
				} else if scenario == "credentials" {
					require.Equal(t, account.StatusDisabled, current.Status)
					require.Equal(t, "administrator", current.GetCredential("access_token"))
				} else if scenario == "error" {
					require.Equal(t, "administrator error", current.ErrorMessage)
				} else if scenario == "write_failure" || scenario == "cancel" {
					// 失败前已完成的旧独立写入保留，不将本阶段修复变成更大的事务。
					if step > account.ManagedRecoveryError {
						require.Equal(t, account.StatusActive, current.Status)
					} else {
						require.Equal(t, account.StatusError, current.Status)
					}
					if step > account.ManagedRecoveryRateLimit {
						require.Nil(t, current.RateLimitedAt)
					} else {
						require.NotNil(t, current.RateLimitedAt)
					}
					require.NotNil(t, current.TempUnschedulableUntil)
				}
			})
		}
	}
}
