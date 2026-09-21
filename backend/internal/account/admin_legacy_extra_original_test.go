package account_test

import (
	"context"
	"net/http"
	"testing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestCreateAccountDiscardsDeprecatedBillingProbeExtra(t *testing.T) {
	repo := &accountServiceTestRepo{}
	created, err := newOriginalAccountEditor(repo).CreateAccount(context.Background(), &account.CreateAccountInput{
		Name:                 "upstream",
		Platform:             capability.PlatformOpenAI,
		Type:                 capability.AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "sk-test"},
		SkipDefaultGroupBind: true,
		Extra: map[string]any{
			"upstream_billing_probe_enabled": true,
			"upstream_billing_probe":         map[string]any{"status": "ok"},
			"custom":                         "value",
		},
	})

	require.NoError(t, err)
	require.NotContains(t, created.Extra, "upstream_billing_probe_enabled")
	require.NotContains(t, created.Extra, "upstream_billing_probe")
	require.Equal(t, "value", created.Extra["custom"])
}

func TestUpdateAccountDiscardsDeprecatedBillingProbeExtra(t *testing.T) {
	accountID := int64(110)
	repo := &accountServiceTestRepo{accounts: map[int64]*account.Record{
		accountID: {
			ID:       accountID,
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Status:   billingcore.StatusActive,
			Extra: map[string]any{
				"upstream_billing_probe_enabled": true,
				"upstream_billing_probe":         map[string]any{"status": "ok"},
			},
		},
	}}

	updated, err := newOriginalAccountEditor(repo).UpdateAccount(context.Background(), accountID, &account.UpdateAccountInput{
		Extra: map[string]any{
			"upstream_billing_probe_enabled": false,
			"upstream_billing_probe":         map[string]any{"status": "forged"},
			"custom":                         "value",
		},
	})

	require.NoError(t, err)
	require.NotContains(t, updated.Extra, "upstream_billing_probe_enabled")
	require.NotContains(t, updated.Extra, "upstream_billing_probe")
	require.Equal(t, "value", updated.Extra["custom"])
}

func TestBulkUpdateAccountsDiscardsDeprecatedBillingProbeExtra(t *testing.T) {
	repo := &accountServiceTestRepo{}
	result, err := newOriginalAccountEditor(repo).BulkUpdateAccounts(context.Background(), &account.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			"upstream_billing_probe_enabled": true,
			"upstream_billing_probe":         map[string]any{"status": "ok"},
			"custom":                         "value",
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Len(t, repo.bulkUpdates, 1)
	require.NotContains(t, repo.bulkUpdates[0].Extra, "upstream_billing_probe_enabled")
	require.NotContains(t, repo.bulkUpdates[0].Extra, "upstream_billing_probe")
	require.Equal(t, "value", repo.bulkUpdates[0].Extra["custom"])
}

func TestUpdateAccountPreservesGrokBillingSnapshotForUnrelatedEdit(t *testing.T) {
	accountID := int64(112)
	billing := &xai.BillingSummary{
		StatusCode:       http.StatusForbidden,
		WeeklyStatusCode: http.StatusForbidden,
	}
	repo := &accountServiceTestRepo{accounts: map[int64]*account.Record{
		accountID: {
			ID:       accountID,
			Platform: capability.PlatformGrok,
			Type:     capability.AccountTypeOAuth,
			Status:   billingcore.StatusActive,
			Extra:    map[string]any{account.GrokUsageBillingExtraKey: billing},
		},
	}}

	updated, err := newOriginalAccountEditor(repo).UpdateAccount(context.Background(), accountID, &account.UpdateAccountInput{
		Extra: map[string]any{"custom": "value"},
	})

	require.NoError(t, err)
	require.Equal(t, billing, updated.Extra[account.GrokUsageBillingExtraKey])
	require.Equal(t, "value", updated.Extra["custom"])
	eligible, reason := account.GrokMediaGenerationEligibility(updated, accountprovider.GrokTierRules())
	require.False(t, eligible)
	require.Equal(t, "billing_forbidden", reason)
}
