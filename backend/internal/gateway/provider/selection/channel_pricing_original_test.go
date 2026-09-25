package selection

import (
	"context"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAIUpstreamRestrictionAppliesChannelThenAccountMapping(t *testing.T) {
	groupID := int64(4202)
	price := 0.01
	channel := routing.Channel{
		ID:                 73,
		Status:             billing.StatusActive,
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:   capability.PlatformOpenAI,
			Models:     []string{"upstream-model"},
			InputPrice: &price,
		}},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{},
		Shared: Shared{Channels: routingtestkit.Channel(groupID, capability.PlatformOpenAI,
			channel)},
	}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"channel-model": "upstream-model"},
		}},
	}

	require.False(t, upstreamRestrictedForTest(svc, context.Background(), groupID, account, "client-alias", false))
}

// TestOpenAIUpstreamRestrictionUsesActuallyForwardedOAuthModel 验证 OAuth 归一化和自动透传都按真实上游模型限制。
func TestOpenAIUpstreamRestrictionUsesActuallyForwardedOAuthModel(t *testing.T) {
	price := 0.01
	tests := []struct {
		name            string
		groupID         int64
		channelModel    string
		pricingModel    string
		account         *gatewayprovider.ExecutionAccount
		restricted      bool
		httpPassthrough bool
	}{
		{
			name:         "OAuth 归一化后的模型命中定价",
			groupID:      4204,
			channelModel: "gpt-5.6-sol-high",
			pricingModel: "gpt-5.6-sol",
			account:      &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}},
		},
		{
			name:         "OAuth 归一化前的模型不能冒充最终模型",
			groupID:      4205,
			channelModel: "gpt-5.6-sol-high",
			pricingModel: "gpt-5.6-sol-high",
			account:      &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}},
			restricted:   true,
		},
		{
			name:         "裸名称不能隐式使用 Sol 的上游定价",
			groupID:      4207,
			channelModel: "gpt-5.6",
			pricingModel: "gpt-5.6-sol",
			account:      &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}},
			restricted:   true,
		},
		{
			name:         "自动透传不执行普通账号映射",
			groupID:      4206,
			channelModel: "passthrough-model",
			pricingModel: "passthrough-model",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
				Type:  capability.AccountTypeOAuth,
				Extra: map[string]any{"openai_passthrough": true},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"passthrough-model": "mapped-model"},
				}},
			},
			httpPassthrough: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := routing.Channel{
				ID:                 tt.groupID,
				Status:             billing.StatusActive,
				RestrictModels:     true,
				BillingModelSource: routing.BillingModelSourceUpstream,
				ModelMapping: map[string]map[string]string{
					capability.PlatformOpenAI: {"client-alias": tt.channelModel},
				},
				ModelPricing: []routing.ChannelModelPricing{{
					Platform:   capability.PlatformOpenAI,
					Models:     []string{tt.pricingModel},
					InputPrice: &price,
				}},
			}
			svc := newCompatibleSelectionForTest(CompatibleDependencies{
				Reads:  Reads{},
				Shared: Shared{Channels: routingtestkit.Channel(tt.groupID, capability.PlatformOpenAI, channel)},
			}, nil)

			ctx := context.Background()
			if tt.httpPassthrough {
				ctx = requeststate.WithOpenAIHTTPPassthroughRouting(ctx)
			}
			restricted := upstreamRestrictedForTest(svc, ctx, tt.groupID, tt.account, "client-alias", false)
			require.Equal(t, tt.restricted, restricted)
		})
	}
}

func TestModelAvailabilityDiagnosisAcceptsChannelAlias(t *testing.T) {
	groupID := int64(4203)
	channel := routing.Channel{
		ID:     74,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
	}
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 75,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		}},
	}
	repo := schedulerTestOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Accounts: repo},
		Shared: Shared{Channels: routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)},
	}, nil)

	diagnosis := gatewayprovider.NewModelAvailability(gatewaytestkit.AvailabilityStore{Source: repo}, svc.channelService, false, true).DiagnoseCompatible(context.Background(), &groupID, "client-alias", capability.PlatformOpenAI)
	require.True(t, diagnosis.HasAccountsInPool)
	require.True(t, diagnosis.HasModelSupport)
}

// TestResolveOpenAIWSRoutingModelForAccountStrictlyFollowsBillingBasis 验证长连接每轮都严格按所选依据检查 R、C 或 U。

// TestResolveOpenAIWSRoutingModelForAccountRejectsUnsupportedMappedModel 验证后续 turn 不能绕过固定账号的最终白名单。

// upstreamRestrictedForTest 保留渠道映射先于账号层限制的原组合顺序。
func upstreamRestrictedForTest(s *Compatible, ctx context.Context, group int64, value *gatewayprovider.ExecutionAccount, model string, compact bool) bool {
	return s.UpstreamRoutingModelRestricted(ctx, group, value, s.resolveChannelRoutingModel(ctx, &group, model), compact)
}
