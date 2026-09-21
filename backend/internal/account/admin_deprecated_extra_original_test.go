//go:build unit

package account_test

import (
	"context"
	"maps"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type deprecatedAccountExtraRepoStub struct {
	accountcore.AdminStore
	account             *accountcore.Record
	updateExtraCalls    int
	lastExtraUpdates    map[string]any
	bulkUpdateCalls     int
	lastBulkExtraUpdate map[string]any
}

func (r *deprecatedAccountExtraRepoStub) GetByID(_ context.Context, _ int64) (*accountcore.Record, error) {
	return accountcore.CloneRecord(r.account), nil
}

func (r *deprecatedAccountExtraRepoStub) Update(_ context.Context, account *accountcore.Record) error {
	r.account = accountcore.CloneRecord(account)
	return nil
}

func (r *deprecatedAccountExtraRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updateExtraCalls++
	r.lastExtraUpdates = maps.Clone(updates)
	return nil
}

func (r *deprecatedAccountExtraRepoStub) BulkUpdate(_ context.Context, _ []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	r.bulkUpdateCalls++
	r.lastBulkExtraUpdate = maps.Clone(updates.Extra)
	return 1, nil
}

func TestDiscardDeprecatedAccountExtra(t *testing.T) {
	extra := map[string]any{
		"openai_long_context_billing_enabled": "malformed",
		"upstream_billing_probe":              map[string]any{"status": "ok"},
		"upstream_billing_probe_enabled":      true,
		"preserved":                           "value",
	}

	accountcore.DiscardDeprecatedAccountExtra(extra)

	require.Equal(t, map[string]any{"preserved": "value"}, extra)
}

func TestAdminServiceUpdateAccountDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{account: &accountcore.Record{
		ID:       1,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Extra: map[string]any{
			"openai_long_context_billing_enabled": false,
			"old":                                 true,
			"quota_used":                          float64(5),
		},
	}}
	svc := newOriginalAccountEditor(repo)

	account, err := svc.UpdateAccount(context.Background(), 1, &accountcore.UpdateAccountInput{Extra: map[string]any{
		"openai_long_context_billing_enabled": []bool{true},
		"privacy_mode":                        "blocked",
		"quota_limit":                         float64(25),
	}})

	require.NoError(t, err)
	require.NotContains(t, account.Extra, "openai_long_context_billing_enabled")
	require.Equal(t, "blocked", account.Extra["privacy_mode"])
	require.Equal(t, float64(25), account.Extra["quota_limit"])
	require.Equal(t, float64(5), account.Extra["quota_used"])
	require.NotContains(t, account.Extra, "old")
}

func TestAdminServiceUpdateAccountDeprecatedOnlyPreservesExistingExtra(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "布尔旧值", value: false},
		{name: "非法类型", value: map[string]any{"malformed": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &deprecatedAccountExtraRepoStub{account: &accountcore.Record{
				ID:       1,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeAPIKey,
				Extra: map[string]any{
					"openai_long_context_billing_enabled": true,
					"privacy_mode":                        "limited",
					"quota_limit":                         float64(100),
					"quota_daily_limit":                   float64(20),
					"custom":                              "preserved",
				},
			}}
			svc := newOriginalAccountEditor(repo)

			account, err := svc.UpdateAccount(context.Background(), 1, &accountcore.UpdateAccountInput{Extra: map[string]any{
				"openai_long_context_billing_enabled": tt.value,
			}})

			require.NoError(t, err)
			require.NotContains(t, account.Extra, "openai_long_context_billing_enabled")
			require.Equal(t, "limited", account.Extra["privacy_mode"])
			require.Equal(t, float64(100), account.Extra["quota_limit"])
			require.Equal(t, float64(20), account.Extra["quota_daily_limit"])
			require.Equal(t, "preserved", account.Extra["custom"])
		})
	}
}

func TestAdminServiceUpdateAccountExplicitEmptyExtraStillClearsConfig(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{account: &accountcore.Record{
		ID:       1,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Extra: map[string]any{
			"privacy_mode": "limited",
			"quota_limit":  float64(100),
			"quota_used":   float64(7),
		},
	}}
	svc := newOriginalAccountEditor(repo)

	account, err := svc.UpdateAccount(context.Background(), 1, &accountcore.UpdateAccountInput{Extra: map[string]any{}})

	require.NoError(t, err)
	require.NotNil(t, account.Extra)
	require.NotContains(t, account.Extra, "privacy_mode")
	require.NotContains(t, account.Extra, "quota_limit")
	require.Equal(t, float64(7), account.Extra["quota_used"])
}

func TestAdminServiceUpdateAccountExtraIgnoresDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{}
	svc := newOriginalAccountEditor(repo)

	err := svc.UpdateAccountExtra(context.Background(), 1, map[string]any{
		"openai_long_context_billing_enabled": 1,
	})

	require.NoError(t, err)
	require.Zero(t, repo.updateExtraCalls)

	err = svc.UpdateAccountExtra(context.Background(), 1, map[string]any{
		"openai_long_context_billing_enabled": "true",
		"preserved":                           true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updateExtraCalls)
	require.Equal(t, map[string]any{"preserved": true}, repo.lastExtraUpdates)
}

func TestAdminServiceBulkUpdateAccountsIgnoresDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			"openai_long_context_billing_enabled": map[string]any{"invalid": true},
			"preserved":                           true,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Equal(t, 1, repo.bulkUpdateCalls)
	require.Equal(t, map[string]any{"preserved": true}, repo.lastBulkExtraUpdate)
}
