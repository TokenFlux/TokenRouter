package selection

import (
	"context"
	"errors"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestGatewayAccountLayerUsesGroupMappedModelForSupportAndRateLimit(t *testing.T) {
	groupID := int64(4201)
	pricingConfig := routingtestkit.Configuration{
		ID:     71,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformAnthropic: {"client-alias": "group-model"},
		},
	}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{},
		Shared: Shared{GroupPolicies: routingtestkit.PricingConfig(groupID, capability.PlatformAnthropic,

			pricingConfig)},
	}, nil)

	ctx := svc.withGroupContext(context.Background(), &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	})
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"group-model": "upstream-model"},
				"model_whitelist": []any{"upstream-model"},
			},
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					"upstream-model": map[string]any{"rate_limit_reset_at": future},
				},
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
			account := &gatewayprovider.ExecutionAccount{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
					Type: tt.accountType,
					Credentials: map[string]any{
						"model_mapping":   map[string]any{"group-model": "claude-sonnet-4-5"},
						"model_whitelist": []any{tt.whitelistModel},
					},
				},
			}
			svc := newGenericSelectionForTest(GenericDependencies{
				Reads: Reads{}, Shared: Shared{},
			}, nil)

			require.True(t, svc.isModelSupportedByAccount(account, "group-model"))
			require.Equal(t, tt.finalModel, resolveAccountUpstreamModel(context.Background(), account, "group-model"))
		})
	}
}

func TestAdvancedSchedulerUsesRoutingModelAndKeepsRequestedModel(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, ID: 72,
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"group-model": "upstream-model"},
				"model_whitelist": []any{"upstream-model"},
			},
		},
	}
	scheduler := &compatiblePicker{
		service: newCompatibleSelectionForTest(CompatibleDependencies{
			Reads: Reads{},

			Shared: Shared{},
		}, nil),
	}
	req := schedulercore.PlatformSelectionInput{
		Platform:       capability.PlatformOpenAI,
		RequestedModel: "client-alias",
		RoutingModel:   "group-model",
	}

	require.Equal(t, "client-alias", req.RequestedModel)
	require.Equal(t, "group-model", requestRoutingModel(req))
	require.True(t, scheduler.isAccountRequestCompatible(context.Background(), account, req))
}

// TestOpenAIHTTPPassthroughIgnoresStoredAccountModelRules 验证 OAuth 归一化和自动透传都按真实上游模型限制。

// TestOpenAIHTTPPassthroughIgnoresStoredAccountModelRules 验证自动透传账号不会被保留的旧白名单误拒绝。
func TestOpenAIHTTPPassthroughIgnoresStoredAccountModelRules(t *testing.T) {
	account := gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, ID: 76,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Extra:       map[string]any{"openai_passthrough": true},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"client-model": "mapped-model"},
				"model_whitelist": []any{"other-model"},
			},
		},
	}
	plainCtx := context.Background()
	passthroughCtx := requeststate.WithOpenAIHTTPPassthroughRouting(plainCtx)

	require.True(t, gatewayprovider.ExecutionModelPolicy(&account).SupportsCompatibleRouting(plainCtx, "client-model"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(&account).SupportsCompatibleRouting(passthroughCtx, "client-model"))
	require.True(t, gatewayprovider.CompatibleAccountEligible(plainCtx, &account, capability.PlatformOpenAI, "client-model", false, ""))
	require.True(t, gatewayprovider.CompatibleAccountEligible(passthroughCtx, &account, capability.PlatformOpenAI, "client-model", false, ""))

	scheduler := &compatiblePicker{service: newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{},

		Shared: Shared{},
	}, nil), stats: schedulercore.NewRuntimeStats(time.Now)}
	req := schedulercore.PlatformSelectionInput{Platform: capability.PlatformOpenAI, RequestedModel: "client-model", RoutingModel: "client-model"}
	require.True(t, scheduler.isAccountRequestCompatible(plainCtx, &account, req))
	require.True(t, scheduler.isAccountRequestCompatible(passthroughCtx, &account, req))

	plainErr := noAvailableOpenAISelectionErrorForRouting(plainCtx, "client-model", "client-model", false, []gatewayprovider.ExecutionAccount{account})
	var modelErr *routing.GroupModelUnsupportedError
	require.False(t, errors.As(plainErr, &modelErr))
	passthroughErr := noAvailableOpenAISelectionErrorForRouting(passthroughCtx, "client-model", "client-model", false, []gatewayprovider.ExecutionAccount{account})
	modelErr = nil
	require.False(t, errors.As(passthroughErr, &modelErr))

	repo := schedulerTestOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{Accounts: repo}}, nil)

	require.True(t, gatewayprovider.NewModelAvailability(gatewaytestkit.AvailabilityStore{Source: repo}, svc.groupPolicies, false, true).DiagnoseCompatibleRouting(plainCtx, nil, "client-model", capability.PlatformOpenAI).HasModelSupport)
	require.True(t, gatewayprovider.NewModelAvailability(gatewaytestkit.AvailabilityStore{Source: repo}, svc.groupPolicies, false, true).DiagnoseCompatibleRouting(passthroughCtx, nil, "client-model", capability.PlatformOpenAI).HasModelSupport)
}

// TestResolveOpenAIWSRoutingModelForAccountStrictlyFollowsBillingBasis 验证长连接每轮都严格按所选依据检查 R、C 或 U。

// TestResolveOpenAIWSRoutingModelForAccountRejectsUnsupportedMappedModel 验证后续 turn 不能绕过固定账号的最终白名单。
