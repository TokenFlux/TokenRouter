package service

import (
	"context"
	"errors"
	"testing"
	"time"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestGatewayAccountLayerUsesChannelMappedModelForSupportAndRateLimit(t *testing.T) {
	groupID := int64(4201)
	channel := routing.Channel{
		ID:     71,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformAnthropic: {"client-alias": "channel-model"},
		},
	}
	svc := &GatewayService{channelService: routingtestkit.Channel(groupID, capability.PlatformAnthropic, channel)}
	ctx := svc.withGroupContext(context.Background(), &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	})
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	account := &Account{
		Platform:    capability.PlatformAnthropic,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"upstream-model": map[string]any{"rate_limit_reset_at": future},
			},
		},
	}

	require.True(t, svc.isModelSupportedByAccountWithContext(ctx, account, "client-alias"))
	require.False(t, svc.isAccountSchedulableForModelSelection(ctx, account, "client-alias"))
	require.True(t, svc.shouldClearStickySessionForAccountLayer(ctx, account, "client-alias"))
}

func TestGatewayAnthropicAccountSupportMapsBeforePlatformNormalization(t *testing.T) {
	tests := []struct {
		name           string
		accountType    string
		finalModel     string
		whitelistModel string
	}{
		{
			name:           "OAuth",
			accountType:    capability.AccountTypeOAuth,
			finalModel:     "claude-sonnet-4-5-20250929",
			whitelistModel: "claude-sonnet-4-5-20250929",
		},
		{
			name:           "ServiceAccount",
			accountType:    capability.AccountTypeServiceAccount,
			finalModel:     "claude-sonnet-4-5@20250929",
			whitelistModel: "claude-sonnet-4-5@20250929",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				Platform: capability.PlatformAnthropic,
				Type:     tt.accountType,
				Credentials: map[string]any{
					"model_mapping":   map[string]any{"channel-model": "claude-sonnet-4-5"},
					"model_whitelist": []any{tt.whitelistModel},
				},
			}
			svc := &GatewayService{}

			require.True(t, svc.isModelSupportedByAccount(account, "channel-model"))
			require.Equal(t, tt.finalModel, resolveAccountUpstreamModel(context.Background(), account, "channel-model"))
		})
	}
}

func TestAdvancedSchedulerUsesRoutingModelAndKeepsRequestedModel(t *testing.T) {
	account := &Account{
		ID:       72,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}}
	req := OpenAIAccountScheduleRequest{
		Platform:       capability.PlatformOpenAI,
		RequestedModel: "client-alias",
		RoutingModel:   "channel-model",
	}

	require.Equal(t, "client-alias", req.RequestedModel)
	require.Equal(t, "channel-model", req.routingModel())
	require.True(t, scheduler.isAccountRequestCompatible(context.Background(), account, req))
}

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
	svc := &OpenAIGatewayService{channelService: routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)}
	account := &Account{
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"channel-model": "upstream-model"},
		},
	}

	require.False(t, svc.isUpstreamModelRestrictedByChannel(context.Background(), groupID, account, "client-alias", false))
}

// TestOpenAIUpstreamRestrictionUsesActuallyForwardedOAuthModel 验证 OAuth 归一化和自动透传都按真实上游模型限制。
func TestOpenAIUpstreamRestrictionUsesActuallyForwardedOAuthModel(t *testing.T) {
	price := 0.01
	tests := []struct {
		name            string
		groupID         int64
		channelModel    string
		pricingModel    string
		account         *Account
		restricted      bool
		httpPassthrough bool
	}{
		{
			name:         "OAuth 归一化后的模型命中定价",
			groupID:      4204,
			channelModel: "gpt-5.6-sol-high",
			pricingModel: "gpt-5.6-sol",
			account:      &Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
		},
		{
			name:         "OAuth 归一化前的模型不能冒充最终模型",
			groupID:      4205,
			channelModel: "gpt-5.6-sol-high",
			pricingModel: "gpt-5.6-sol-high",
			account:      &Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
			restricted:   true,
		},
		{
			name:         "裸名称不能隐式使用 Sol 的上游定价",
			groupID:      4207,
			channelModel: "gpt-5.6",
			pricingModel: "gpt-5.6-sol",
			account:      &Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
			restricted:   true,
		},
		{
			name:         "自动透传不执行普通账号映射",
			groupID:      4206,
			channelModel: "passthrough-model",
			pricingModel: "passthrough-model",
			account: &Account{
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Extra:    map[string]any{"openai_passthrough": true},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"passthrough-model": "mapped-model"},
				},
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
			svc := &OpenAIGatewayService{channelService: routingtestkit.Channel(tt.groupID, capability.PlatformOpenAI, channel)}

			ctx := context.Background()
			if tt.httpPassthrough {
				ctx = WithOpenAIHTTPPassthroughRouting(ctx)
			}
			restricted := svc.isUpstreamModelRestrictedByChannel(ctx, tt.groupID, tt.account, "client-alias", false)
			require.Equal(t, tt.restricted, restricted)
		})
	}
}

// TestOpenAIHTTPPassthroughIgnoresStoredAccountModelRules 验证自动透传账号不会被保留的旧白名单误拒绝。
func TestOpenAIHTTPPassthroughIgnoresStoredAccountModelRules(t *testing.T) {
	account := Account{
		ID:          76,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Extra:       map[string]any{"openai_passthrough": true},
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"client-model": "mapped-model"},
			"model_whitelist": []any{"other-model"},
		},
	}
	plainCtx := context.Background()
	passthroughCtx := WithOpenAIHTTPPassthroughRouting(plainCtx)

	require.True(t, openAIAccountSupportsRoutingModel(plainCtx, &account, "client-model"))
	require.True(t, openAIAccountSupportsRoutingModel(passthroughCtx, &account, "client-model"))
	require.True(t, isOpenAICompatibleAccountEligibleForRequest(plainCtx, &account, capability.PlatformOpenAI, "client-model", false, ""))
	require.True(t, isOpenAICompatibleAccountEligibleForRequest(passthroughCtx, &account, capability.PlatformOpenAI, "client-model", false, ""))

	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}, stats: newOpenAIAccountRuntimeStats()}
	req := OpenAIAccountScheduleRequest{Platform: capability.PlatformOpenAI, RequestedModel: "client-model", RoutingModel: "client-model"}
	require.True(t, scheduler.isAccountRequestCompatible(plainCtx, &account, req))
	require.True(t, scheduler.isAccountRequestCompatible(passthroughCtx, &account, req))

	plainErr := noAvailableOpenAISelectionErrorForRouting(plainCtx, "client-model", "client-model", false, []Account{account})
	var modelErr *routing.GroupModelUnsupportedError
	require.False(t, errors.As(plainErr, &modelErr))
	passthroughErr := noAvailableOpenAISelectionErrorForRouting(passthroughCtx, "client-model", "client-model", false, []Account{account})
	modelErr = nil
	require.False(t, errors.As(passthroughErr, &modelErr))

	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
	require.True(t, svc.DiagnoseRoutingModelAvailabilityForPlatform(plainCtx, nil, "client-model", capability.PlatformOpenAI).HasModelSupport)
	require.True(t, svc.DiagnoseRoutingModelAvailabilityForPlatform(passthroughCtx, nil, "client-model", capability.PlatformOpenAI).HasModelSupport)
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
	account := Account{
		ID:          75,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo:    schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		channelService: routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel),
	}

	diagnosis := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), &groupID, "client-alias", capability.PlatformOpenAI)
	require.True(t, diagnosis.HasAccountsInPool)
	require.True(t, diagnosis.HasModelSupport)
}

// TestResolveOpenAIWSRoutingModelForAccountStrictlyFollowsBillingBasis 验证长连接每轮都严格按所选依据检查 R、C 或 U。
func TestResolveOpenAIWSRoutingModelForAccountStrictlyFollowsBillingBasis(t *testing.T) {
	price := 0.01
	tests := []struct {
		name           string
		billingSource  string
		pricingModel   string
		expectRejected bool
	}{
		{name: "requested", billingSource: routing.BillingModelSourceRequested, pricingModel: "client-alias"},
		{name: "channel_mapped", billingSource: routing.BillingModelSourceChannelMapped, pricingModel: "channel-model"},
		{name: "upstream", billingSource: routing.BillingModelSourceUpstream, pricingModel: "upstream-model"},
		{name: "upstream_rejected", billingSource: routing.BillingModelSourceUpstream, pricingModel: "other-model", expectRejected: true},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(4300 + index)
			channel := routing.Channel{
				ID:                 int64(80 + index),
				Status:             billing.StatusActive,
				RestrictModels:     true,
				BillingModelSource: tt.billingSource,
				ModelMapping: map[string]map[string]string{
					capability.PlatformOpenAI: {"client-alias": "channel-model"},
				},
				ModelPricing: []routing.ChannelModelPricing{{
					Platform:   capability.PlatformOpenAI,
					Models:     []string{tt.pricingModel},
					InputPrice: &price,
				}},
			}
			svc := &OpenAIGatewayService{channelService: routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)}
			account := &Account{
				ID:          90,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping":   map[string]any{"channel-model": "upstream-model"},
					"model_whitelist": []any{"upstream-model"},
				},
			}

			routingModel, err := svc.ResolveOpenAIWSRoutingModelForAccount(
				context.Background(), &groupID, account, "client-alias", accountcore.OpenAIEndpointCapabilityTextGeneration,
			)
			if tt.expectRejected {
				require.Error(t, err)
				require.Empty(t, routingModel)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "channel-model", routingModel)
		})
	}
}

// TestResolveOpenAIWSRoutingModelForAccountRejectsUnsupportedMappedModel 验证后续 turn 不能绕过固定账号的最终白名单。
func TestResolveOpenAIWSRoutingModelForAccountRejectsUnsupportedMappedModel(t *testing.T) {
	account := &Account{
		ID:          91,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"different-upstream-model"},
		},
	}
	svc := &OpenAIGatewayService{}

	routingModel, err := svc.ResolveOpenAIWSRoutingModelForAccount(
		context.Background(), nil, account, "channel-model", accountcore.OpenAIEndpointCapabilityTextGeneration,
	)
	require.Error(t, err)
	require.Empty(t, routingModel)
}
