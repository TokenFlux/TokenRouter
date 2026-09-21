package account_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestAccountServiceUpdateStripsOllamaManagedExtra(t *testing.T) {
	account := ollamaUsageAccount(61)
	account.Extra = map[string]any{
		accountcore.OllamaCloudUsageSessionExtraKey:     "local-ciphertext",
		accountcore.OllamaCloudUsageAutoRefreshExtraKey: true,
		accountcore.OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": accountcore.OllamaCloudUsageStatusOK},
	}
	repo := &ollamaManagedExtraUpdateRepo{account: account}
	svc := accountcore.NewBasicAccounts(repo, nil, uuid.NewString)
	requestedExtra := map[string]any{
		"note": "preserved",
		accountcore.OllamaCloudUsageSessionExtraKey:     "forged-ciphertext",
		accountcore.OllamaCloudUsageAutoRefreshExtraKey: nil,
		accountcore.OllamaCloudUsageSnapshotExtraKey:    nil,
	}

	_, err := svc.Update(context.Background(), account.ID, accountcore.UpdateAccountRequest{Extra: &requestedExtra})
	require.NoError(t, err)
	require.Equal(t, "preserved", repo.updated.Extra["note"])
	require.NotContains(t, repo.updated.Extra, accountcore.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, repo.updated.Extra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, repo.updated.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)

	require.Contains(t, requestedExtra, accountcore.OllamaCloudUsageSessionExtraKey)
}

type ollamaManagedExtraUpdateRepo struct {
	accountcore.BasicAccountStore
	account *accountcore.Record
	updated *accountcore.Record
}

func (r *ollamaManagedExtraUpdateRepo) GetByID(_ context.Context, _ int64) (*accountcore.Record, error) {
	return accountcore.CloneRecord(r.account), nil
}

func (r *ollamaManagedExtraUpdateRepo) Update(_ context.Context, account *accountcore.Record) error {
	r.updated = accountcore.CloneRecord(account)
	return nil
}
