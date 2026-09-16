package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 提交前转换必须切断旧用户、账号、Key 与订阅的可变引用。
func TestCompletionCyberInputFreezesLegacyProjection(t *testing.T) {
	group := int64(7)
	rate := 1.25
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1, Balance: 12}, GroupID: &group}
	account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &rate}
	sub := &UserSubscription{ID: 4, Plan: &SubscriptionPlan{GroupIDs: []int64{7}, GroupRateMultipliers: map[int64]float64{7: 1.5}}}
	input := CompletionCyberInput(context.Background(), CyberPolicyUsageInput{APIKey: key, Account: account, Subscription: sub, Model: " model ", RequestID: "request", InputTokens: 5, NativeCompactionV2: true})
	require.NotNil(t, input)
	key.User.Balance = 99
	key.ID = 22
	group = 70
	rate = 9
	account.ID = 33
	sub.Plan.GroupIDs[0] = 70
	sub.Plan.GroupRateMultipliers[7] = 8
	require.Equal(t, int64(2), input.APIKey.ID)
	require.Equal(t, int64(7), *input.APIKey.GroupID)
	require.Equal(t, 12.0, input.User.Balance)
	require.Equal(t, int64(3), input.Account.ID)
	require.Equal(t, 1.25, input.Account.RateMultiplier)
	require.Equal(t, []int64{7}, input.Subscription.Plan.GroupIDs)
	require.Equal(t, 1.5, input.Subscription.Plan.GroupRateMultipliers[7])
	require.Equal(t, "model", input.Result.Model)
	require.Equal(t, 5, input.Result.Usage.InputTokens)
	require.True(t, input.CyberBlocked)
	require.True(t, input.NativeCompactionV2)
}
