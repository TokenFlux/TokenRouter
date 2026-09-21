package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"entgo.io/ent/dialect"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"
)

func TestUpdateCNUsageMonitorSnapshotCASWritesSnapshotAndOutboxAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	expectedUpdatedAt := time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)WITH updated AS \(.*updated_at = \$5.*INSERT INTO scheduler_outbox`).
		WithArgs("", accountcore.CNUsageMonitorSnapshotExtraKey, sqlmock.AnyArg(), int64(27), expectedUpdatedAt, scheduler.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	written, err := repo.UpdateCNUsageMonitorSnapshotCAS(context.Background(), 27, expectedUpdatedAt, &accountcore.CNUsageMonitorSnapshot{
		Version:       1,
		Adapter:       accountcore.UpstreamUsageAdapterKimiBalance,
		IdentityHash:  "identity",
		LastAttemptAt: expectedUpdatedAt,
	}, "")
	require.NoError(t, err)
	require.True(t, written)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateCNUsageMonitorSnapshotCASRejectsStaleUpdatedAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	expectedUpdatedAt := time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)WITH updated AS \(.*updated_at = \$5.*INSERT INTO scheduler_outbox`).
		WithArgs("", accountcore.CNUsageMonitorSnapshotExtraKey, sqlmock.AnyArg(), int64(27), expectedUpdatedAt, scheduler.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	written, err := repo.UpdateCNUsageMonitorSnapshotCAS(context.Background(), 27, expectedUpdatedAt, &accountcore.CNUsageMonitorSnapshot{
		Version:       1,
		Adapter:       accountcore.UpstreamUsageAdapterKimiBalance,
		IdentityHash:  "stale",
		LastAttemptAt: expectedUpdatedAt,
	}, "")
	require.NoError(t, err)
	require.False(t, written)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockAndMergeAccountManagedExtraProtectsOllamaFields(t *testing.T) {
	tests := []struct {
		name                 string
		groupIdentityMatches bool
		proxyIdentityMatches bool
		wantSession          bool
		wantSnapshot         bool
	}{
		{name: "身份与代理未变时保留全部状态", groupIdentityMatches: true, proxyIdentityMatches: true, wantSession: true, wantSnapshot: true},
		{name: "代理变化时保留会话并清理快照", groupIdentityMatches: true, proxyIdentityMatches: false, wantSession: true, wantSnapshot: false},
		{name: "凭据变化时清理全部状态", groupIdentityMatches: false, proxyIdentityMatches: true, wantSession: false, wantSnapshot: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })

			mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT")+`.*`+regexp.QuoteMeta("FOR NO KEY UPDATE")).
				WithArgs(int64(29), capability.PlatformAnthropic, capability.AccountTypeAPIKey, `{"api_key":"key","base_url":"https://ollama.com"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{"ollama_group_unchanged", "ollama_proxy_unchanged", "ollama_session", "ollama_auto", "ollama_snapshot"}).
					AddRow(tt.groupIdentityMatches, tt.proxyIdentityMatches, []byte(`"local-ciphertext"`), []byte(`true`), []byte(`{"status":"ok"}`)))

			account := &service.Account{
				ID: 29, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "key", "base_url": "https://ollama.com"},
				Extra: map[string]any{
					accountcore.OllamaCloudUsageSessionExtraKey:     "forged-ciphertext",
					accountcore.OllamaCloudUsageAutoRefreshExtraKey: false,
					accountcore.OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": "forged"},
					deprecatedUpstreamBillingProbeExtraKey:          map[string]any{"status": "stale"},
				},
			}
			got, err := lockAndMergeAccountManagedExtra(context.Background(), client, account)
			require.NoError(t, err)
			require.NotContains(t, got, deprecatedUpstreamBillingProbeExtraKey)
			if tt.wantSession {
				require.Equal(t, "local-ciphertext", got[accountcore.OllamaCloudUsageSessionExtraKey])
				require.Equal(t, true, got[accountcore.OllamaCloudUsageAutoRefreshExtraKey])
			} else {
				require.NotContains(t, got, accountcore.OllamaCloudUsageSessionExtraKey)
				require.NotContains(t, got, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
			}
			if tt.wantSnapshot {
				require.Equal(t, map[string]any{"status": "ok"}, got[accountcore.OllamaCloudUsageSnapshotExtraKey])
			} else {
				require.NotContains(t, got, accountcore.OllamaCloudUsageSnapshotExtraKey)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateExtraDiscardsDeprecatedAccountExtraKeys(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .* - 'upstream_billing_probe' - 'upstream_billing_probe_enabled' - 'openai_long_context_billing_enabled'.*`).
		WithArgs(`{"custom":"value"}`, int64(27)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(scheduler.SchedulerOutboxEventAccountChanged, int64(27), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	repo := newAccountRepositoryWithSQL(client, db, nil)

	err = repo.UpdateExtra(context.Background(), 27, map[string]any{
		deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "forged"},
		deprecatedUpstreamBillingProbeEnabledExtraKey: true,
		deprecatedOpenAILongContextBillingExtraKey:    []bool{true},
		"custom": "value",
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .* - 'openai_long_context_billing_enabled'.*`).
		WithArgs([]byte(`{"custom":"value"}`), `{27}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	repo := newAccountRepositoryWithSQL(client, db, nil)
	extra := map[string]any{
		deprecatedOpenAILongContextBillingExtraKey: map[string]any{"malformed": true},
		"custom": "value",
	}

	rows, err := repo.BulkUpdate(context.Background(), []int64{27}, accountcore.AccountBulkUpdate{Extra: extra})

	require.NoError(t, err)
	require.Equal(t, int64(1), rows)
	require.NotContains(t, extra, deprecatedOpenAILongContextBillingExtraKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateExtraRollsBackWhenOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .*`).
		WithArgs(`{"custom":"value"}`, int64(27)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnError(errors.New("outbox failed"))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	err = repo.UpdateExtra(context.Background(), 27, map[string]any{"custom": "value"})

	require.EqualError(t, err, "outbox failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateCredentialsRollsBackWhenOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts.*credentials IS DISTINCT FROM \$1::jsonb.*- 'upstream_billing_probe'.*- 'ollama_cloud_usage_snapshot'`).
		WithArgs(`{"api_key":"sk-new"}`, int64(27)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnError(errors.New("outbox failed"))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	err = repo.UpdateCredentials(context.Background(), 27, map[string]any{"api_key": "sk-new"})

	require.EqualError(t, err, "outbox failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateRollsBackWhenOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	name := "renamed"
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET name = \$1.*WHERE id = ANY\(\$2\)`).
		WithArgs(name, `{27,28}`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnError(errors.New("outbox failed"))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	rows, err := repo.BulkUpdate(context.Background(), []int64{27, 28}, accountcore.AccountBulkUpdate{Name: &name})

	require.EqualError(t, err, "outbox failed")
	require.Zero(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
