package selection

import (
	"context"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// withAdvancedSchedulerTestGroup 为高级调度测试明确注入最终目标分组。
// 生产代码不再读取全局开关，测试也必须声明该分组使用 advanced。

// ListModelAvailabilityCandidates 模拟只按持久配置筛选模型诊断候选账号。

// noSlotSchedulerTestConcurrencyCache 在辅助选择错误触碰真实并发槽时立即暴露问题。

func TestOpenAIGatewayService_MessagesRoutingModelUsesFullMappingChain(t *testing.T) {
	for _, advancedScheduler := range []bool{false, true} {
		name := "旧版调度"
		if advancedScheduler {
			name = "高级调度"
		}
		t.Run(name, func(t *testing.T) {
			groupID := int64(4211)
			price := 0.01
			pricingConfig := routingtestkit.Configuration{
				ID:                 77,
				Status:             billing.StatusActive,
				RestrictModels:     true,
				BillingModelSource: routing.BillingModelSourceUpstream,
				ModelMapping: map[string]map[string]string{
					capability.PlatformOpenAI: {"client-alias": "group-model"},
				},
				ModelPricing: []routing.ModelPricingEntry{{
					Platform:   capability.PlatformOpenAI,
					Models:     []string{"allowed-upstream"},
					InputPrice: &price,
				}},
			}
			accounts := []gatewayprovider.ExecutionAccount{
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 42111,
						Platform:    capability.PlatformOpenAI,
						Type:        capability.AccountTypeAPIKey,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 1,
						Priority:    0,
						Credentials: map[string]any{
							"model_mapping":   map[string]any{"group-model": "blocked-upstream"},
							"model_whitelist": []any{"blocked-upstream"},
						},
					},
				},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 42112,
						Platform:    capability.PlatformOpenAI,
						Type:        capability.AccountTypeAPIKey,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 1,
						Priority:    1,
						Credentials: map[string]any{
							"model_mapping":   map[string]any{"dispatch-model": "allowed-upstream"},
							"model_whitelist": []any{"allowed-upstream"},
						},
					},
				},
			}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = false
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"sticky": 42111}}
			svc := newCompatibleSelectionForTest(CompatibleDependencies{
				Reads: Reads{Accounts: schedulerTestOpenAIAccountRepo{accounts: accounts}},
				Shared: Shared{
					Cache:       cache,
					Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
					GroupPolicies: routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI,
						pricingConfig),
				},
			}, cfg)

			if advancedScheduler {
				svc.healthObserver = gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{})
				svc.schedulerParameters = newAdvancedSchedulerParametersForTest(cfg, "true")
			}

			selection, _, err := svc.SelectAccountWithSchedulerForCapabilityAndRoutingModel(
				context.Background(),
				&groupID,
				"",
				"sticky",
				"client-alias",
				"dispatch-model",
				nil, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityTextGeneration,
				false,
				false,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, int64(42112), selection.Account.Record.ID)
			require.Equal(t, "allowed-upstream", gatewayprovider.ExecutionModelPolicy(selection.Account).ForwardModel("group-model", "dispatch-model"))
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}
