package provider

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

// 原生捕获在提交时读取输入，提交后不会再随请求对象、档位或价卡变化。
func TestCompletionCaptureKeepsTurnTimeAndIndependentInputs(t *testing.T) {
	price, multiplier := 0.25, 1.5
	groupID := int64(17)
	key := &apikey.APIKey{ID: 2, GroupID: &groupID, Group: &routing.Group{
		ID: groupID, ModelPricing: []routing.ChannelModelPricing{{Models: []string{"model"}, InputPrice: &price}},
	}}
	user := &identity.User{ID: 3, Balance: 9}
	target := &account.Record{ID: 4, RateMultiplier: &multiplier, Extra: map[string]any{account.AccountExtraUpstreamRequestIDHeader: "X-Request-ID"}}
	result := &forward.OpenAIResult{RequestID: "upstream", Model: "model", ImageOutputSizes: []string{"1K"}, ImageSizeBreakdown: map[string]int{"1K": 1}}
	turnAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	ctx := context.WithValue(context.Background(), telemetry.RequestID, "local")
	in := &OpenAICapture{APIKey: key, User: user, Account: target, Result: result, PricingAt: turnAt,
		RequestBody: []byte(`{"reasoning":{"effort":"high"}}`), QuotaPlatform: "openai", Subscription: &billing.UserSubscription{ID: 5}}
	// 捕获之前的合法输入变化应生效，不能在构造输入时提前拍快照。
	user.Balance = 10
	out := CaptureOpenAI(ctx, in)
	user.Balance = 90
	price = 9
	multiplier = 8
	groupID = 99
	result.ImageOutputSizes[0] = "4K"
	result.ImageSizeBreakdown["1K"] = 20
	in.RequestBody[0] = '!'
	require.Equal(t, "local:local", out.RequestID)
	require.Equal(t, turnAt, out.PricingAt)
	require.Equal(t, "openai", out.QuotaPlatform)
	require.Equal(t, 10.0, out.User.Balance)
	require.Equal(t, int64(17), *out.APIKey.GroupID)
	require.Equal(t, 0.25, *out.APIKey.Group.Price.ModelPricing[0].InputPrice)
	require.Equal(t, 1.5, out.Account.RateMultiplier)
	require.Equal(t, []string{"1K"}, out.Result.ImageOutputSizes)
	require.Equal(t, 1, out.Result.ImageSizeBreakdown["1K"])
	require.Equal(t, "high", *out.RequestedReasoningEffort)
}

// 原接口中的带类型 nil 仍表示已提供能力；不能在迁移时按具体指针重解释。
func TestCompletionCapturePreservesQuotaCapabilityPresence(t *testing.T) {
	var absent QuotaUpdater
	var typedNil *apikey.APIKeyService
	for _, tc := range []struct {
		name  string
		value QuotaUpdater
		want  bool
	}{{"absent", absent, false}, {"typed-nil", typedNil, true}} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, CaptureMessages(context.Background(), &MessagesCapture{APIKeyService: tc.value}).QuotaUpdates)
			require.Equal(t, tc.want, CaptureOpenAI(context.Background(), &OpenAICapture{APIKeyService: tc.value}).QuotaUpdates)
		})
	}
}
