package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestCreateAccountDiscardsDeprecatedBillingProbeExtra(t *testing.T) {
	repo := &accountServiceTestRepo{}
	created, err := (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "upstream",
		Platform:             PlatformOpenAI,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "sk-test"},
		SkipDefaultGroupBind: true,
		Extra: map[string]any{
			deprecatedUpstreamBillingProbeEnabledExtraKey: true,
			deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "ok"},
			"custom":                                      "value",
		},
	})

	require.NoError(t, err)
	require.NotContains(t, created.Extra, deprecatedUpstreamBillingProbeEnabledExtraKey)
	require.NotContains(t, created.Extra, deprecatedUpstreamBillingProbeExtraKey)
	require.Equal(t, "value", created.Extra["custom"])
}

func TestUpdateAccountDiscardsDeprecatedBillingProbeExtra(t *testing.T) {
	accountID := int64(110)
	repo := &accountServiceTestRepo{accounts: map[int64]*Account{
		accountID: {
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Status:   StatusActive,
			Extra: map[string]any{
				deprecatedUpstreamBillingProbeEnabledExtraKey: true,
				deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "ok"},
			},
		},
	}}

	updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Extra: map[string]any{
			deprecatedUpstreamBillingProbeEnabledExtraKey: false,
			deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "forged"},
			"custom":                                      "value",
		},
	})

	require.NoError(t, err)
	require.NotContains(t, updated.Extra, deprecatedUpstreamBillingProbeEnabledExtraKey)
	require.NotContains(t, updated.Extra, deprecatedUpstreamBillingProbeExtraKey)
	require.Equal(t, "value", updated.Extra["custom"])
}

func TestBulkUpdateAccountsDiscardsDeprecatedBillingProbeExtra(t *testing.T) {
	repo := &accountServiceTestRepo{}
	result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			deprecatedUpstreamBillingProbeEnabledExtraKey: true,
			deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "ok"},
			"custom":                                      "value",
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Len(t, repo.bulkUpdates, 1)
	require.NotContains(t, repo.bulkUpdates[0].Extra, deprecatedUpstreamBillingProbeEnabledExtraKey)
	require.NotContains(t, repo.bulkUpdates[0].Extra, deprecatedUpstreamBillingProbeExtraKey)
	require.Equal(t, "value", repo.bulkUpdates[0].Extra["custom"])
}

func TestUpdateAccountPreservesGrokBillingSnapshotForUnrelatedEdit(t *testing.T) {
	accountID := int64(112)
	billing := &xai.BillingSummary{
		StatusCode:       http.StatusForbidden,
		WeeklyStatusCode: http.StatusForbidden,
	}
	repo := &accountServiceTestRepo{accounts: map[int64]*Account{
		accountID: {
			ID:       accountID,
			Platform: PlatformGrok,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Extra:    map[string]any{grokBillingExtraKey: billing},
		},
	}}

	updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Extra: map[string]any{"custom": "value"},
	})

	require.NoError(t, err)
	require.Equal(t, billing, updated.Extra[grokBillingExtraKey])
	require.Equal(t, "value", updated.Extra["custom"])
	eligible, reason := updated.GrokMediaGenerationEligibility()
	require.False(t, eligible)
	require.Equal(t, "billing_forbidden", reason)
}
