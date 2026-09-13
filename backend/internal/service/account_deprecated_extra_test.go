//go:build unit

package service

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

type deprecatedAccountExtraRepoStub struct {
	accountRepoStub
	account             *Account
	updateExtraCalls    int
	lastExtraUpdates    map[string]any
	bulkUpdateCalls     int
	lastBulkExtraUpdate map[string]any
}

func (r *deprecatedAccountExtraRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *deprecatedAccountExtraRepoStub) Update(_ context.Context, account *Account) error {
	r.account = account
	return nil
}

func (r *deprecatedAccountExtraRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updateExtraCalls++
	r.lastExtraUpdates = maps.Clone(updates)
	return nil
}

func (r *deprecatedAccountExtraRepoStub) BulkUpdate(_ context.Context, _ []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkUpdateCalls++
	r.lastBulkExtraUpdate = maps.Clone(updates.Extra)
	return 1, nil
}

func TestDiscardDeprecatedAccountExtra(t *testing.T) {
	extra := map[string]any{
		deprecatedOpenAILongContextBillingExtraKey:    "malformed",
		deprecatedUpstreamBillingProbeExtraKey:        map[string]any{"status": "ok"},
		deprecatedUpstreamBillingProbeEnabledExtraKey: true,
		"preserved": "value",
	}

	DiscardDeprecatedAccountExtra(extra)

	require.Equal(t, map[string]any{"preserved": "value"}, extra)
}

func TestAdminServiceUpdateAccountDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{account: &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			deprecatedOpenAILongContextBillingExtraKey: false,
			"old":        true,
			"quota_used": float64(5),
		},
	}}
	svc := &adminServiceImpl{accountRepo: repo}

	account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{
		deprecatedOpenAILongContextBillingExtraKey: []bool{true},
		"privacy_mode": "blocked",
		"quota_limit":  float64(25),
	}})

	require.NoError(t, err)
	require.NotContains(t, account.Extra, deprecatedOpenAILongContextBillingExtraKey)
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
			repo := &deprecatedAccountExtraRepoStub{account: &Account{
				ID:       1,
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					deprecatedOpenAILongContextBillingExtraKey: true,
					"privacy_mode":      "limited",
					"quota_limit":       float64(100),
					"quota_daily_limit": float64(20),
					"custom":            "preserved",
				},
			}}
			svc := &adminServiceImpl{accountRepo: repo}

			account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{
				deprecatedOpenAILongContextBillingExtraKey: tt.value,
			}})

			require.NoError(t, err)
			require.NotContains(t, account.Extra, deprecatedOpenAILongContextBillingExtraKey)
			require.Equal(t, "limited", account.Extra["privacy_mode"])
			require.Equal(t, float64(100), account.Extra["quota_limit"])
			require.Equal(t, float64(20), account.Extra["quota_daily_limit"])
			require.Equal(t, "preserved", account.Extra["custom"])
		})
	}
}

func TestAdminServiceUpdateAccountExplicitEmptyExtraStillClearsConfig(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{account: &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"privacy_mode": "limited",
			"quota_limit":  float64(100),
			"quota_used":   float64(7),
		},
	}}
	svc := &adminServiceImpl{accountRepo: repo}

	account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{}})

	require.NoError(t, err)
	require.NotNil(t, account.Extra)
	require.NotContains(t, account.Extra, "privacy_mode")
	require.NotContains(t, account.Extra, "quota_limit")
	require.Equal(t, float64(7), account.Extra["quota_used"])
}

func TestAdminServiceUpdateAccountExtraIgnoresDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{}
	svc := &adminServiceImpl{accountRepo: repo}

	err := svc.UpdateAccountExtra(context.Background(), 1, map[string]any{
		deprecatedOpenAILongContextBillingExtraKey: 1,
	})

	require.NoError(t, err)
	require.Zero(t, repo.updateExtraCalls)

	err = svc.UpdateAccountExtra(context.Background(), 1, map[string]any{
		deprecatedOpenAILongContextBillingExtraKey: "true",
		"preserved": true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updateExtraCalls)
	require.Equal(t, map[string]any{"preserved": true}, repo.lastExtraUpdates)
}

func TestAdminServiceBulkUpdateAccountsIgnoresDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedAccountExtraRepoStub{}
	svc := &adminServiceImpl{accountRepo: repo}

	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			deprecatedOpenAILongContextBillingExtraKey: map[string]any{"invalid": true},
			"preserved": true,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Equal(t, 1, repo.bulkUpdateCalls)
	require.Equal(t, map[string]any{"preserved": true}, repo.lastBulkExtraUpdate)
}
