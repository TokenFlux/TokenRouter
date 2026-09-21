package account_test

import (
	"time"

	"github.com/google/uuid"

	"fmt"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOllamaCloudUsageManagedExtraCannotBeImported(t *testing.T) {
	remoteExtra := map[string]any{
		accountcore.OllamaCloudUsageSessionExtraKey:     "remote-ciphertext",
		accountcore.OllamaCloudUsageAutoRefreshExtraKey: true,
		accountcore.OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": "forged"},
	}
	created, err := accountcore.BuildAccountForCreate(&accountcore.CreateAccountInput{
		Name: "ollama", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": "key"},
		Concurrency: 1,
	}, accountcore.CRSMergeMap(nil, remoteExtra), accountcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString})
	require.NoError(t, err)
	require.NotContains(t, created.Extra, accountcore.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, created.Extra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, created.Extra, accountcore.OllamaCloudUsageSnapshotExtraKey)

	existing := ollamaUsageAccount(6)
	existing.Extra = map[string]any{
		accountcore.OllamaCloudUsageSessionExtraKey:     "local-ciphertext",
		accountcore.OllamaCloudUsageAutoRefreshExtraKey: false,
		accountcore.OllamaCloudUsageSnapshotExtraKey:    map[string]any{"status": accountcore.OllamaCloudUsageStatusOK},
	}
	targetExtra := accountcore.CRSMergeMap(existing.Extra, remoteExtra)
	accountcore.ReconcileCRSOllamaCloudUsageExtra(existing, existing.Platform, existing.Type, accountcore.CRSMergeMap(existing.Credentials, nil), targetExtra)
	require.Equal(t, "local-ciphertext", targetExtra[accountcore.OllamaCloudUsageSessionExtraKey])
	require.Equal(t, false, targetExtra[accountcore.OllamaCloudUsageAutoRefreshExtraKey])
	require.Equal(t, map[string]any{"status": accountcore.OllamaCloudUsageStatusOK}, targetExtra[accountcore.OllamaCloudUsageSnapshotExtraKey])

	changedCredentials := accountcore.CRSMergeMap(existing.Credentials, map[string]any{"api_key": "rotated"})
	targetExtra = accountcore.CRSMergeMap(existing.Extra, remoteExtra)
	accountcore.ReconcileCRSOllamaCloudUsageExtra(existing, existing.Platform, existing.Type, changedCredentials, targetExtra)
	require.NotContains(t, targetExtra, accountcore.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, targetExtra, accountcore.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, targetExtra, accountcore.OllamaCloudUsageSnapshotExtraKey)
}

func ollamaUsageAccount(id int64) *accountcore.Record {
	return &accountcore.Record{
		ID: id, Name: fmt.Sprintf("ollama-%d", id), Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": fmt.Sprintf("key-%d", id)},
		Extra:       map[string]any{}, Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
	}
}
