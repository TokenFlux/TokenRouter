package selection

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
)

// mixedSessionLimits records Anthropic session registration and rejects one
// configured provider in mixed-group tests.
type mixedSessionLimits struct {
	schedulercore.SessionLimitCache                    // unused session operations panic
	blocked                         int64              // provider rejected during registration
	calls                           map[int64][]string // recorded provider session hashes
}

// mixedSnapshot supplies candidate records separately from later full reads in
// mixed-group fixtures.
type mixedSnapshot struct {
	values []gatewayprovider.ExecutionProvider // cached mixed-group target fixtures
}

// TestAdvancedSchedulerUsesRoutingModelAndKeepsRequestedModel gives the request
// different client and routing names with a final provider mapping.
// Compatibility uses the routing identity and retains the client field,
// protecting their distinct roles.
func TestAdvancedSchedulerUsesRoutingModelAndKeepsRequestedModel(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 72, Status: billing.StatusActive, Schedulable: true,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
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
	require.True(t, scheduler.isProviderRequestCompatible(context.Background(), provider, req))
}

// TestOpenAIGatewayService_MessagesRoutingModelUsesFullMappingChain supplies
// client, group, dispatch, and final upstream names with one allowed target.
// Both fixture dependency variants choose the dispatch-compatible provider;
// their admitted fixture group remains basic.
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
				ModelMapping:       map[string]string{"client-alias": "group-model"},
				ModelPricing: []routing.ModelPricingEntry{{
					Models:     []string{"allowed-upstream"},
					InputPrice: &price,
				}},
			}
			providers := []gatewayprovider.ExecutionProvider{
				{
					Record: providercore.Record{
						LoadLocation: time.LoadLocation, ID: 42111,
						Platform:    capability.PlatformOpenAI,
						Type:        capability.ProviderTypeAPIKey,
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
					Record: providercore.Record{
						LoadLocation: time.LoadLocation, ID: 42112,
						Platform:    capability.PlatformOpenAI,
						Type:        capability.ProviderTypeAPIKey,
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
			cfg.Scheduling.LoadBatch.Enabled = false
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"sticky": 42111}}
			svc := newCompatibleSelectionForTest(CompatibleDependencies{
				Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
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

			selection, _, err := svc.SelectProviderWithSchedulerForCapabilityAndRoutingModel(
				context.Background(),
				&groupID,
				"",
				"sticky",
				"client-alias",
				"dispatch-model",
				nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
				false,
				false,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Provider)
			require.Equal(t, int64(42112), selection.Provider.Record.ID)
			require.Equal(t, "allowed-upstream", gatewayprovider.ExecutionModelPolicy(selection.Provider).ForwardModel("group-model", "dispatch-model"))
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

// TestMixedGroupSelectsModelOnActualProviderPlatform tries three client
// protocols and provider model families in basic and advanced groups. Selection
// chooses the actual matching platform within the group, protecting
// cross-platform model and protocol eligibility.
func TestMixedGroupSelectsModelOnActualProviderPlatform(t *testing.T) {
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		t.Run(string(mode), func(t *testing.T) {
			group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode}
			repo := &mixedGroupProviders{values: []gatewayprovider.ExecutionProvider{
				mixedGroupProvider(1, capability.PlatformAnthropic, "claude-test", group.ID),
				mixedGroupProvider(2, capability.PlatformOpenAI, "gpt-test", group.ID),
				mixedGroupProvider(3, capability.PlatformGemini, "gemini-test", group.ID),
				mixedGroupProvider(4, capability.PlatformOpenAI, "gpt-test", 92),
			}}
			options := DefaultOptions()

			selector := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: repo}}, options)
			for _, source := range []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions} {
				ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), source)
				for i, model := range []string{"claude-test", "gpt-test", "gemini-test"} {
					selected, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", model, nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
					require.NoError(t, err, "%s %s", source, model)
					require.NotNil(t, selected)
					require.Equal(t, int64(i+1), selected.Provider.Record.ID)
					if selected.ReleaseFunc != nil {
						selected.ReleaseFunc()
					}
				}
			}
			for _, queried := range repo.groupQueries {
				require.Equal(t, group.ID, queried)
			}
		})
	}
}

// TestMixedGroupRequiresExplicitGroupAndHonorsForcedPlatform supplies eligible
// mixed-platform members with an admitted snapshot. A missing requested group
// is rejected before pool reads, and a platform override chooses the matching
// member.
func TestMixedGroupRequiresExplicitGroupAndHonorsForcedPlatform(t *testing.T) {
	group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive}
	repo := &mixedGroupProviders{values: []gatewayprovider.ExecutionProvider{mixedGroupProvider(1, capability.PlatformAnthropic, "shared", group.ID), mixedGroupProvider(2, capability.PlatformOpenAI, "shared", group.ID)}}
	selector := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: repo}}, DefaultOptions())
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), protocol.ProtocolAnthropicMessages)
	_, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, nil, "", "", "shared", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
	require.Error(t, err)
	require.Empty(t, repo.groupQueries)
	selected, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", "shared", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityTextGeneration, false, false, capability.PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Provider.Record.ID)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
}

// RegisterSession records the provider and hash before applying the fixture's
// blocked ID. Other providers admit, while capacity and timeout inputs are
// ignored so the scenario can isolate session-registration rejection.
func (s *mixedSessionLimits) RegisterSession(_ context.Context, id int64, hash string, _ int, _ time.Duration) (bool, error) {
	if s.calls == nil {
		s.calls = map[int64][]string{}
	}
	s.calls[id] = append(s.calls[id], hash)
	return id != s.blocked, nil
}

// TestMixedGroupRespectsAnthropicSessionLimit rejects session registration for
// the preferred Anthropic provider. Selection advances to the next group member
// and attempts registration there, protecting provider retry after
// session-capacity rejection.
func TestMixedGroupRespectsAnthropicSessionLimit(t *testing.T) {
	group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive}
	first := mixedGroupProvider(1, capability.PlatformAnthropic, "*", 91)
	first.Record.Type = capability.ProviderTypeOAuth
	first.Record.Extra = map[string]any{"max_sessions": 1}
	second := mixedGroupProvider(2, capability.PlatformAnthropic, "*", 91)
	second.Record.Type = capability.ProviderTypeOAuth
	second.Record.Priority = 1
	second.Record.Extra = map[string]any{"max_sessions": 1}
	limits := &mixedSessionLimits{blocked: 1}
	generic := NewGeneric(GenericDependencies{Sessions: limits}, DefaultOptions())
	repo := &mixedGroupProviders{values: []gatewayprovider.ExecutionProvider{first, second}}
	selector := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: repo}, Generic: generic}, DefaultOptions())
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), protocol.ProtocolAnthropicMessages)
	selected, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "same-session", "claude-test", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Provider.Record.ID)
	require.Equal(t, []string{"same-session"}, limits.calls[1])
	require.Equal(t, []string{"same-session"}, limits.calls[2])
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
}

// ListProviders converts the fixture targets to provider records and reports a
// nonmixed snapshot. It ignores requested group and platform filters, leaving
// the selector's fresh/database membership checks observable in the scenario.
func (s mixedSnapshot) ListProviders(_ context.Context, _ *int64, _ string, _ bool) ([]providercore.Record, bool, error) {
	return gatewayprovider.ExecutionRecords(s.values), false, nil
}

// GetProvider returns a converted fixture record for the matching target ID.
// Missing IDs return nil without error, allowing snapshot-based selection to
// exercise a full provider lookup miss.
func (s mixedSnapshot) GetProvider(_ context.Context, id int64) (*providercore.Record, error) {
	for _, v := range s.values {
		if v.Record.ID == id {
			return gatewayprovider.ExecutionRecord(&v), nil
		}
	}
	return nil, nil
}

// TestMixedGroupRechecksMembershipAfterSnapshot keeps a moved provider in the
// cached candidate pool for both group modes. Database revalidation chooses the
// remaining member, protecting membership changes after ranking.
func TestMixedGroupRechecksMembershipAfterSnapshot(t *testing.T) {
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		t.Run(string(mode), func(t *testing.T) {
			group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode}
			moved := mixedGroupProvider(1, capability.PlatformAnthropic, "*", 91)
			ready := mixedGroupProvider(2, capability.PlatformOpenAI, "*", 91)
			ready.Record.Priority = 1
			snapshot := mixedSnapshot{values: []gatewayprovider.ExecutionProvider{moved, ready}}
			moved.Record.GroupIDs = []int64{92}
			repo := &mixedGroupProviders{values: []gatewayprovider.ExecutionProvider{moved, ready}}
			selector := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: repo, Snapshot: snapshot}}, DefaultOptions())
			ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), protocol.ProtocolAnthropicMessages)
			selected, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", "shared", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selected.Provider.Record.ID)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
		})
	}
}

// TestOpenAISelectProviderWithScheduler_GroupModelUnsupportedError supplies an
// eligible compatible provider with a narrower model allowlist. Selection
// returns a typed rejection and its available model names, protecting
// configured-model diagnosis.
func TestOpenAISelectProviderWithScheduler_GroupModelUnsupportedError(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformOpenAI,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{
						"model_whitelist": []any{"gpt-5.4", "gpt-5.4-mini"},
					},
				},
			},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{}}, nil)

	selection, _, err := svc.SelectProviderWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"o1-preview",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
	)
	if err == nil {
		t.Fatalf("expected group model unsupported error")
	}
	if selection != nil {
		t.Fatalf("expected nil selection")
	}
	var modelErr *routing.GroupModelUnsupportedError
	if !errors.As(err, &modelErr) {
		t.Fatalf("expected GroupModelUnsupportedError, got %T: %v", err, err)
	}
	require.Equal(t, "o1-preview", modelErr.RequestedModel)
	require.Equal(t, []string{"gpt-5.4", "gpt-5.4-mini"}, modelErr.AvailableModels)
	require.Contains(t, err.Error(), `The current group does not support the requested model "o1-preview"`)
	require.Contains(t, err.Error(), "Available models: gpt-5.4, gpt-5.4-mini")
}

// TestCompactSchedulingRechecksAdministratorSwitchFromDatabase makes cached and
// database Compact switches disagree. Both fixture parameter variants follow
// the database switch; the default admitted group remains basic, so these cases
// protect revalidation in that mode.
func TestCompactSchedulingRechecksAdministratorSwitchFromDatabase(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced=%v/enabled=%v", advanced, enabled), func(t *testing.T) {
				groupID := int64(91090)
				cachedMode, dbMode := "force_on", "force_off"
				if enabled {
					cachedMode, dbMode = dbMode, cachedMode
				}
				cached := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71990, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive,
					Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Extra: map[string]any{"openai_compact_mode": cachedMode},
				}}
				fresh := *cached
				fresh.Record.Extra = map[string]any{"openai_compact_mode": dbMode}
				svc := newCompatibleSelectionForTest(CompatibleDependencies{
					Reads: Reads{
						Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{fresh}},
						Snapshot:  schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(&openAISnapshotCacheStub{snapshotProviders: []*gatewayprovider.ExecutionProvider{cached}, providersByID: map[int64]*gatewayprovider.ExecutionProvider{cached.Record.ID: cached}}, nil, nil, nil, nil)),
					},
					Shared: Shared{
						Cache:       &schedulerTestGatewayCache{},
						Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),

						Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
						Parameters: newAdvancedSchedulerParametersForTest(&config.Config{}, fmt.Sprint(advanced)),
					},
				}, &config.Config{})

				selected, _, err := svc.SelectProviderWithScheduler(context.Background(), &groupID, "", "", "gpt-5.4", nil, egress.OpenAIUpstreamTransportAny, true)
				if enabled {
					require.NoError(t, err)
					require.NotNil(t, selected)
					require.Equal(t, cached.Record.ID, selected.Provider.Record.ID)
				} else {
					require.ErrorIs(t, err, schedulercore.ErrNoAvailableCompactProviders)
					require.Nil(t, selected)
				}
			})
		}
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_CompactSelectsEnabledProvider
// gives equal-priority API Key targets opposite Compact switches. Basic
// selection chooses the enabled target, protecting administrator endpoint
// eligibility before ordering.
func TestOpenAIGatewayService_SelectProviderWithScheduler_CompactSelectsEnabledProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(91001)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra:       map[string]any{"openai_compact_mode": "force_off"},
			}, // Disabled by the administrator.
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra:       map[string]any{"openai_compact_mode": "force_on"},
			}, // Enabled by the administrator.
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: providers},
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, _, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(71002), selection.Provider.Record.ID, "disabled provider must be excluded")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_CompactRejectsDisabled
// disables Compact on every eligible API Key target. Selection returns no
// result with ErrNoAvailableCompactProviders, protecting the endpoint-specific
// exhaustion identity.
func TestOpenAIGatewayService_SelectProviderWithScheduler_CompactRejectsDisabled(t *testing.T) {
	ctx := context.Background()
	groupID := int64(91002)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71010,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra:       map[string]any{"openai_compact_mode": providercore.OpenAICompactModeForceOff},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71011,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra:       map[string]any{"openai_compact_mode": "force_off"},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: providers},
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, _, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, schedulercore.ErrNoAvailableCompactProviders), "compact-only providers should rejected explicitly unsupported and return compact error")
	require.Nil(t, selection)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_CompactUsesDefaultEnabledProvider
// supplies an explicitly disabled target and another with no switch. Selection
// chooses the default-enabled record, protecting absent-versus-disabled Compact
// configuration.
func TestOpenAIGatewayService_SelectProviderWithScheduler_CompactUsesDefaultEnabledProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(91003)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71020,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra:       map[string]any{"openai_compact_mode": "force_off"},
			}, // Disabled by the administrator.
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71021,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra:       map[string]any{},
			}, // Enabled by default.
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: providers},
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, _, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(71021), selection.Provider.Record.ID, "default-enabled provider should be selected")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_CompactAllowsGrok
// supplies a configured Grok model and Compact request. Basic compatible
// selection returns that member, protecting Grok's current Compact capability
// rule.
func TestOpenAIGatewayService_SelectProviderWithScheduler_CompactAllowsGrok(t *testing.T) {
	ctx := context.Background()
	groupID := int64(91004)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 71030,
				Platform:    capability.PlatformGrok,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
				},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: providers},
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, _, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"grok-4.5",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
		true,
		false,
		capability.PlatformGrok,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(71030), selection.Provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_NativeCompactionSeparatesLegacyCompactSupport
// gives one target a forced Chat route and another disabled legacy Compact.
// Ordinary Responses selects the second target while legacy Compact rejects the
// pool, protecting independent capability gates.
func TestOpenAIGatewayService_SelectProviderWithScheduler_NativeCompactionSeparatesLegacyCompactSupport(t *testing.T) {
	groupID := int64(91005)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71050,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    10,
				Extra: map[string]any{
					"openai_compact_mode":    "force_on",
					"openai_text_route_mode": "force_chat_completions",
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71051,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Extra: map[string]any{
					"openai_compact_mode": providercore.OpenAICompactModeForceOff,
				},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: providers},
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	nativeSelection, _, err := svc.SelectProviderWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityResponses,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, nativeSelection)
	require.Equal(t, int64(71051), nativeSelection.Provider.Record.ID)

	legacySelection, _, err := svc.SelectProviderWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityResponses,
		true,
		false,
	)
	require.ErrorIs(t, err, schedulercore.ErrNoAvailableCompactProviders)
	require.Nil(t, legacySelection)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_NativeCompactionV2Mode
// mixes observed v2 support with opposite administrator overrides. Selection
// follows the enabled override, protecting mode precedence when a Responses
// request requires remote compaction v2.
func TestOpenAIGatewayService_SelectProviderWithScheduler_NativeCompactionV2Mode(t *testing.T) {
	groupID := int64(91006)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71060,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra: map[string]any{
					providercore.OpenAINativeCompactionV2ModeExtraKey: providercore.OpenAICompactModeForceOff,
					"openai_native_compaction_v2_supported":           true,
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71061,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				Extra: map[string]any{
					"openai_native_compaction_v2_supported":           false,
					providercore.OpenAINativeCompactionV2ModeExtraKey: providercore.OpenAICompactModeForceOff,
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 71062,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    2,
				Extra: map[string]any{
					providercore.OpenAINativeCompactionV2ModeExtraKey: providercore.OpenAICompactModeForceOn,
					"openai_native_compaction_v2_supported":           false,
				},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: providers},
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, _, err := svc.SelectProviderWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityRemoteCompactionV2,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(71062), selection.Provider.Record.ID)
}

// TestAllowsOpenAICompatibleCompact varies nil targets, provider platforms,
// absent switches, and explicit Compact modes. The compatibility predicate
// returns each expected result, protecting defaults and administrator override
// precedence over observed support.
func TestAllowsOpenAICompatibleCompact(t *testing.T) {
	tests := []struct {
		name     string                             // named Compact scenario
		provider *gatewayprovider.ExecutionProvider // configured Compact eligibility fixture
		want     bool                               // expected Compact admission result
	}{
		{name: "nil", provider: nil, want: false},
		{name: "non openai", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic}}, want: false},
		{name: "grok", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok}}, want: true},
		{name: "openai default enabled", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Extra: map[string]any{}}}, want: true},
		{name: "openai enabled", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": "force_on"}}}, want: true},
		{name: "openai disabled", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": "force_off"}}}, want: false},
		{name: "force on", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": providercore.OpenAICompactModeForceOn}}}, want: true},
		{name: "force off overrides probe true", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": providercore.OpenAICompactModeForceOff, "openai_compact_supported": true}}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gatewayprovider.AllowsCompatibleCompact(tt.provider); got != tt.want {
				t.Fatalf("allowsOpenAICompatibleCompact(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSparkRoutingByModel varies normal and shadow providers with explicit
// Spark model scopes and a usable parent. Compatibility follows the configured
// model identity, with empty model requests skipping model filtering.
func TestSparkRoutingByModel(t *testing.T) {
	ctx := context.Background()
	sparkModel := "gpt-5.3-codex-spark"
	normalModel := "gpt-5.3-codex"
	sparkCreds := map[string]any{"model_mapping": provideradapter.DefaultSparkShadowModels()}

	newScheduler := func(snapshot map[int64]*gatewayprovider.ExecutionProvider) *compatiblePicker {
		return &compatiblePicker{
			service: newCompatibleSelectionForTest(CompatibleDependencies{
				Reads: Reads{Snapshot: schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(
					&openAISnapshotCacheStub{providersByID: snapshot}, nil, nil, nil, nil))},
				Shared: Shared{},
			}, &config.Config{}),
		}
	}
	sparkReq := schedulercore.PlatformSelectionInput{RequestedModel: sparkModel, Platform: capability.PlatformOpenAI}
	normalReq := schedulercore.PlatformSelectionInput{RequestedModel: normalModel, Platform: capability.PlatformOpenAI}

	t.Run("normal_provider_with_spark_mapping_accepts_spark", func(t *testing.T) {
		acc := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Credentials: sparkCreds}}
		require.True(t, newScheduler(nil).isProviderRequestCompatible(ctx, acc, sparkReq),
			"普通提供商配了 spark → 可承接 spark（类型门已移除）")
	})

	t.Run("normal_provider_without_spark_rejects_spark", func(t *testing.T) {
		acc := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_whitelist": []string{normalModel}, "model_mapping": map[string]any{normalModel: normalModel}},
		}}
		require.False(t, newScheduler(nil).isProviderRequestCompatible(ctx, acc, sparkReq),
			"普通提供商未配 spark → 拒 spark（按配置而非类型）")
	})

	t.Run("shadow_with_spark_mapping_accepts_spark_rejects_non_spark", func(t *testing.T) {
		pid := int64(100)
		parent := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 100, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
		shadow := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 200, ParentProviderID: &pid, QuotaDimension: providercore.QuotaDimensionSpark,
			Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: sparkCreds,
		}}
		s := newScheduler(map[int64]*gatewayprovider.ExecutionProvider{100: parent})
		require.True(t, s.isProviderRequestCompatible(ctx, shadow, sparkReq), "影子配 spark + 健康母 → 接 spark")
		require.False(t, s.isProviderRequestCompatible(ctx, shadow, normalReq), "影子（仅 spark mapping）→ 拒非 spark")
	})

	t.Run("empty_model_shadow_is_eligible_under_a2", func(t *testing.T) {
		// An empty model skips model filtering, allowing shadow and ordinary providers.
		pid := int64(100)
		parent := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 100, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
		shadow := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 200, ParentProviderID: &pid, QuotaDimension: providercore.QuotaDimensionSpark,
			Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: sparkCreds,
		}}
		emptyReq := schedulercore.PlatformSelectionInput{RequestedModel: "", Platform: capability.PlatformOpenAI}
		s := newScheduler(map[int64]*gatewayprovider.ExecutionProvider{100: parent})
		require.True(t, s.isProviderRequestCompatible(ctx, shadow, emptyReq),
			"空 model 时影子可被选中（有意的纯 A2 行为：类型门移除后无 opt-in 排除）")
	})
}

// TestParentHealthSchedulerIntegration varies the shadow parent's active state,
// manual scheduling flag, and global cooldown. The parent-health rule rejects
// inactive credentials while retaining shadow eligibility for independent
// parent scheduling restrictions.
func TestParentHealthSchedulerIntegration(t *testing.T) {
	ctx := context.Background()
	pid := int64(78100)
	sparkModel := "gpt-5.3-codex-spark"

	shadow := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 78200,
			ParentProviderID: &pid,
			QuotaDimension:   providercore.QuotaDimensionSpark,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.ProviderTypeOAuth,
			Status:           billing.StatusActive,
			Schedulable:      true,
			Concurrency:      1,
		},
	}

	req := schedulercore.PlatformSelectionInput{
		RequestedModel: sparkModel,
		Platform:       capability.PlatformOpenAI,
	}

	makeScheduler := func(parent *gatewayprovider.ExecutionProvider) *compatiblePicker {
		snapshotCache := &openAISnapshotCacheStub{
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{parent.Record.ID: parent},
		}
		snapshotSvc := schedulercore.NewSnapshotService(snapshotCache, nil, nil, nil, nil)
		svc := newCompatibleSelectionForTest(CompatibleDependencies{
			Reads:  Reads{Snapshot: schedulerredis.NewSnapshotReader(snapshotSvc)},
			Shared: Shared{},
		}, &config.Config{})

		return &compatiblePicker{service: svc}
	}

	t.Run("unhealthy_parent_status_error_rejects_shadow", func(t *testing.T) {
		unhealthyParent := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 78100,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      providercore.StatusError, // Inactive parent credentials reject the shadow.
				Schedulable: true,
			},
		}
		require.False(t, unhealthyParent.View().IsSchedulable(), "前提：Status=error 的母提供商不可调度")
		scheduler := makeScheduler(unhealthyParent)
		require.False(t, scheduler.isProviderRequestCompatible(ctx, shadow, req),
			"母提供商不可调度时，影子提供商必须被调度器拒绝")
	})

	t.Run("manual_schedulable_false_parent_does_not_reject_shadow", func(t *testing.T) {
		// Manually pausing the parent leaves its credentials usable by the shadow.
		manualPausedParent := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 78100,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: false,
			}, // Manually paused.
		}
		require.False(t, manualPausedParent.View().IsSchedulable(), "前提：手动暂停的母提供商自身不可调度")
		scheduler := makeScheduler(manualPausedParent)
		require.True(t, scheduler.isProviderRequestCompatible(ctx, shadow, req),
			"母提供商手动暂停不应连坐影子(凭据仍可用)")
	})

	t.Run("global_rate_limited_parent_does_not_reject_shadow", func(t *testing.T) {
		// The parent's global 429 cooldown is independent of Spark shadow eligibility.
		resetAt := time.Now().Add(1 * time.Hour)
		rateLimitedParent := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 78100,
				Platform:         capability.PlatformOpenAI,
				Type:             capability.ProviderTypeOAuth,
				Status:           billing.StatusActive,
				Schedulable:      true,
				RateLimitResetAt: &resetAt,
			},
		}
		require.False(t, rateLimitedParent.View().IsSchedulable(), "前提：global 限流母提供商自身不可调度")
		scheduler := makeScheduler(rateLimitedParent)
		require.True(t, scheduler.isProviderRequestCompatible(ctx, shadow, req),
			"母提供商 global 限流不应连坐 spark 影子")
	})

	t.Run("healthy_parent_accepts_shadow_control", func(t *testing.T) {
		healthyParent := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 78100,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
			},
		}
		require.True(t, healthyParent.View().IsSchedulable(), "前提：健康母提供商必须可调度")
		scheduler := makeScheduler(healthyParent)
		require.True(t, scheduler.isProviderRequestCompatible(ctx, shadow, req),
			"健康母提供商时，影子提供商必须被调度器接受（对照组）")
	})
}

// TestParentHealthSchedulerFallsBackToRepoWhenSnapshotMissesParent omits the
// parent snapshot and disables snapshot-service database fallback. The picker
// obtains its healthy parent through the repository, protecting the separate
// shadow-health lookup recovery path.
func TestParentHealthSchedulerFallsBackToRepoWhenSnapshotMissesParent(t *testing.T) {
	ctx := context.Background()
	parentID := int64(79100)
	parent := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: parentID,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
	shadow := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 79200,
			ParentProviderID: &parentID,
			QuotaDimension:   providercore.QuotaDimensionSpark,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.ProviderTypeOAuth,
			Status:           billing.StatusActive,
			Schedulable:      true,
			Concurrency:      1,
		},
	}

	repo := schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{parent}}
	scheduler := &compatiblePicker{service: newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
			Snapshot:  schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(&openAISnapshotCacheStub{}, nil, hydrationProviderSource{source: repo}, nil, &schedulercore.SnapshotOptions{DbFallbackEnabled: false})),
		},
		Shared: Shared{},
	}, &config.Config{})}

	require.True(t, scheduler.isProviderRequestCompatible(ctx, shadow, schedulercore.PlatformSelectionInput{
		RequestedModel: "gpt-5.3-codex-spark",
		Platform:       capability.PlatformOpenAI,
	}), "快照缺失母提供商且调度快照 DB fallback 关闭时，应回退 repo 解析健康母提供商")
}

// newSchedulerTestSubscriptionPriorityConfig supplies Top-K one and
// priority/load/queue weights for subscription scenarios.
func newSchedulerTestSubscriptionPriorityConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 1
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 1
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0
	return cfg
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabledUsesLegacyLoadAwareness
// supplies a prior-response binding in a basic group with a higher-priority
// alternative. Choice follows the basic pool and reports load_balance,
// protecting final-group mode before advanced affinity.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabledUsesLegacyLoadAwareness(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10106)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	cache := &schedulerTestGatewayCache{}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache: cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			}),
		},
	}, cfg)

	store := svc.ResponseStateStore()
	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_disabled_001", 36001, time.Hour))

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"resp_disabled_001",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(36002), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickyPreviousHit)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_LoadBatchReportsFilterReasons
// supplies quota, model, and request-excluded failures to a basic load-batch
// pool. Exhaustion includes deterministic rejection counts, protecting
// diagnostic detail from that selection path.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_LoadBatchReportsFilterReasons(t *testing.T) {
	ctx := gatewayprovider.WithQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIProviderQuotaAutoPauseSettings{DefaultThreshold7d: 0.9})
	groupID := int64(10107)
	quotaPaused := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36003,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Extra: map[string]any{
				"codex_7d_used_percent":  95.0,
				"codex_7d_reset_at":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at": time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	mappingMiss := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 36004,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-4o": "gpt-4o"},
			},
		},
	}
	excluded := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36005,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = true
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{quotaPaused, mappingMiss, excluded}}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	require.False(t, svc.groupUsesAdvancedScheduler(ctx, &groupID))
	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4-mini",
		map[int64]struct{}{excluded.Record.ID: {}}, egress.OpenAIUpstreamTransportAny, false,
	)

	require.Error(t, err)
	require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
	require.Nil(t, selection)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.EqualError(t, err, "no available OpenAI providers supporting model: gpt-5.4-mini (pool=3, filtered: excluded=1 model_not_supported=1 quota_auto_pause_7d=1)")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_RequiredWSV2_SkipsHTTPOnlyProvider
// supplies HTTP-only and WS-capable API Key targets in basic mode. Selection
// chooses the WS target, protecting transport eligibility after basic choice.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_RequiredWSV2_SkipsHTTPOnlyProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10108)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "upstream_protocols": []string{"openai_responses"}}, LoadLocation: time.LoadLocation, ID: 36011,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36012,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
	}
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(36012), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_RequiredWSV2_NoAvailableProvider
// supplies an HTTP-only pool while requiring WSv2. Basic selection returns no
// target with a load_balance decision, protecting transport exhaustion
// handling.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_RequiredWSV2_NoAvailableProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10109)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "upstream_protocols": []string{"openai_responses"}}, LoadLocation: time.LoadLocation, ID: 36021,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
	}
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, false,
	)
	require.ErrorContains(t, err, "no available OpenAI providers")
	require.Nil(t, selection)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_EmbeddingsSkipsChatOnlyProvider
// supplies text-only and embedding-capable API Key members. Basic selection
// chooses the embedding target, protecting endpoint capability before execution
// capacity is returned.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_EmbeddingsSkipsChatOnlyProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10110)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 36031,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Credentials: map[string]any{
					"model_whitelist":              []string{"*"},
					"openai_workload_capabilities": []any{"text_generation"},
				},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 36032,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				Credentials: map[string]any{
					"model_whitelist":              []string{"*"},
					"openai_workload_capabilities": []any{"text_generation", "embeddings"},
				},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"text-embedding-3-small",
		nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityEmbeddings,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(36032), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_ResponsesCapabilityExcludesUnsupportedAPIKey
// forces one API Key target to Chat while another supports Responses. Responses
// requests use the supported target or exhaust, while text requests can still
// use the Chat target.
func TestOpenAIGatewayService_SelectProviderWithScheduler_ResponsesCapabilityExcludesUnsupportedAPIKey(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10120)

	newSvc := func(providers []gatewayprovider.ExecutionProvider) *Compatible {
		cfg := &config.Config{}
		cfg.Scheduling.LoadBatch.Enabled = false
		return newCompatibleSelectionForTest(CompatibleDependencies{
			Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
			Shared: Shared{
				Cache:       &schedulerTestGatewayCache{},
				Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)
	}

	supported := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0,
		},
	}
	// This forced-Chat target cannot accept the Responses endpoint requirement.
	unsupported := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5,
			Extra: map[string]any{"openai_text_route_mode": "force_chat_completions"},
		},
	}

	t.Run("生图意图仅选中支持 responses 的提供商", func(t *testing.T) {
		svc := newSvc([]gatewayprovider.ExecutionProvider{supported, unsupported})
		selection, _, err := svc.SelectProviderWithSchedulerForCapability(
			ctx, &groupID, "", "", "gpt-image-2", nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityResponses,
			false, false,
		)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Provider)
		require.Equal(t, int64(37001), selection.Provider.Record.ID)
	})

	t.Run("仅有不支持 responses 的提供商时生图意图无可用提供商", func(t *testing.T) {
		svc := newSvc([]gatewayprovider.ExecutionProvider{unsupported})
		selection, _, err := svc.SelectProviderWithSchedulerForCapability(
			ctx, &groupID, "", "", "gpt-image-2", nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityResponses,
			false, false,
		)
		require.Error(t, err)
		require.Nil(t, selection)
	})

	t.Run("非生图路径仍可选中不支持 responses 的提供商", func(t *testing.T) {
		svc := newSvc([]gatewayprovider.ExecutionProvider{unsupported})
		selection, _, err := svc.SelectProviderWithSchedulerForCapability(
			ctx, &groupID, "", "", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
			false, false,
		)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Provider)
		require.Equal(t, int64(37002), selection.Provider.Record.ID)
	})
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_AlphaSearchAllowsAPIKeyProvider
// supplies one eligible API Key provider for AlphaSearch. Basic choice returns
// that target, protecting capability admission for this authentication path.
func TestOpenAIGatewayService_SelectProviderWithScheduler_AlphaSearchAllowsAPIKeyProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10125)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Cache:       &schedulerTestGatewayCache{},
		},
	}, cfg)

	selection, _, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityAlphaSearch,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(38001), selection.Provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_AllowsGrokChatProvider
// supplies an eligible Grok OAuth member in basic mode with platform override.
// Selection returns it with a load_balance decision, protecting compatible text
// choice for that platform.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DefaultDisabled_AllowsGrokChatProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10113)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36041,
				Platform:    capability.PlatformGrok,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"grok-4.3",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
		capability.PlatformGrok,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(36041), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_GrokMediaCapabilityFiltersIneligibleProviders
// varies recorded media eligibility among Grok members. Media requests choose
// eligible targets or exhaust, while ordinary text remains routable on an
// ineligible media target.
func TestOpenAIGatewayService_SelectProviderWithScheduler_GrokMediaCapabilityFiltersIneligibleProviders(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10114)
	ineligible := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36051, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5,
			Extra: map[string]any{providercore.GrokMediaEligibleExtraKey: false},
		},
	}
	eligible := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36052, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0,
			Extra: map[string]any{providercore.GrokMediaEligibleExtraKey: true},
		},
	}
	newService := func(providers []gatewayprovider.ExecutionProvider) *Compatible {
		cfg := &config.Config{}
		cfg.Scheduling.LoadBatch.Enabled = false
		return newCompatibleSelectionForTest(CompatibleDependencies{
			Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
			Shared: Shared{
				Cache:       &schedulerTestGatewayCache{},
				Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)
	}

	t.Run("media generation skips higher priority ineligible provider", func(t *testing.T) {
		selection, _, err := newService([]gatewayprovider.ExecutionProvider{ineligible, eligible}).SelectProviderWithSchedulerForCapability(
			ctx, &groupID, "", "", "grok-imagine-video", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityGrokMediaGeneration,
			false, false, capability.PlatformGrok,
		)

		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Provider)
		require.Equal(t, eligible.Record.ID, selection.Provider.Record.ID)
	})

	t.Run("media generation fails closed when all providers are ineligible", func(t *testing.T) {
		selection, _, err := newService([]gatewayprovider.ExecutionProvider{ineligible}).SelectProviderWithSchedulerForCapability(
			ctx, &groupID, "", "", "grok-imagine-video", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityGrokMediaGeneration,
			false, false, capability.PlatformGrok,
		)

		require.Error(t, err)
		require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
		require.Nil(t, selection)
	})

	t.Run("chat remains routable on media-ineligible provider", func(t *testing.T) {
		selection, _, err := newService([]gatewayprovider.ExecutionProvider{ineligible}).SelectProviderWithSchedulerForCapability(
			ctx, &groupID, "", "", "grok-4.3", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityTextGeneration,
			false, false, capability.PlatformGrok,
		)

		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Provider)
		require.Equal(t, ineligible.Record.ID, selection.Provider.Record.ID)
	})
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorReportsQuotaAutoPauseExclusion
// pauses the entire advanced pool by seven-day quota. Exhaustion retains
// ErrNoAvailableProviders and the quota rejection count, protecting hard-filter
// diagnostics before scoring.
func TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorReportsQuotaAutoPauseExclusion(t *testing.T) {
	ctx := gatewayprovider.WithQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIProviderQuotaAutoPauseSettings{DefaultThreshold7d: 0.9})
	groupID := int64(101201)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38101,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Extra: map[string]any{
					"codex_7d_used_percent":  95.0,
					"codex_7d_reset_at":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
					"codex_usage_updated_at": time.Now().Add(-time.Minute).Format(time.RFC3339),
				},
			},
		},
	}
	cfg := &config.Config{}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),

			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx, &groupID, "", "", "gpt-5.4-mini", nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
	require.Nil(t, selection)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.EqualError(t, err, "no available OpenAI providers supporting model: gpt-5.4-mini (pool=1, filtered: quota_auto_pause_7d=1)")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorPreservesModelBusinessError
// requests a Grok model from the OpenAI fixture pool. The result remains a
// typed model rejection and compatibility reports model_not_supported,
// protecting business diagnosis during exhaustion.
func TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorPreservesModelBusinessError(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101202)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38111,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
			},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Cache:       &schedulerTestGatewayCache{},
		},
	}, &config.Config{})

	selection, _, err := svc.SelectProviderWithScheduler(
		ctx, &groupID, "", "", "grok-4.5", nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.Error(t, err)
	require.Nil(t, selection)
	var modelErr *routing.GroupModelUnsupportedError
	require.ErrorAs(t, err, &modelErr)

	scheduler := &compatiblePicker{service: svc}
	compatible, reason := scheduler.isProviderRequestCompatibleReason(ctx, &providers[0], schedulercore.PlatformSelectionInput{RequestedModel: "grok-4.5"})
	require.False(t, compatible)
	require.Equal(t, "model_not_supported", reason)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorAggregatesReasonsDeterministically
// combines quota pause, unsupported model, and excluded candidates in an
// advanced group. The error orders their counts predictably, protecting stable
// filter diagnostics.
func TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorAggregatesReasonsDeterministically(t *testing.T) {
	ctx := gatewayprovider.WithQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIProviderQuotaAutoPauseSettings{DefaultThreshold7d: 0.9})
	groupID := int64(101203)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	quotaPaused := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38121,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Extra: map[string]any{
				"codex_7d_used_percent":  95.0,
				"codex_7d_reset_at":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at": time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	mappingMiss := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 38122,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-4o": "gpt-4o"},
			},
		},
	}
	excluded := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38123,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{quotaPaused, mappingMiss, excluded}}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
		},
	}, &config.Config{})

	selection, _, err := svc.SelectProviderWithScheduler(
		ctx, &groupID, "", "", "gpt-5.4-mini", map[int64]struct{}{38123: {}}, egress.OpenAIUpstreamTransportAny, false,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
	require.Nil(t, selection)
	require.EqualError(t, err, "no available OpenAI providers supporting model: gpt-5.4-mini (pool=3, filtered: excluded=1 model_not_supported=1 quota_auto_pause_7d=1)")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorReportsEmptyPool
// gives an advanced group no provider records. Exhaustion reports pool zero
// with ErrNoAvailableProviders, protecting the empty-source diagnostic before
// ranking can run.
func TestOpenAIGatewayService_SelectProviderWithScheduler_NoAvailableErrorReportsEmptyPool(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101204)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{}},
		Shared: Shared{
			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Cache:      &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			}),
		},
	}, &config.Config{})

	selection, _, err := svc.SelectProviderWithScheduler(
		ctx, &groupID, "", "", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
	require.Nil(t, selection)
	require.EqualError(t, err, "no available OpenAI providers supporting model: gpt-5.1 (pool=0)")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_EnabledUsesAdvancedPreviousResponseRouting
// binds a lower-priority target to a previous response in an advanced group.
// Selection uses that provider and reports prior-response affinity, protecting
// continuation before ordinary scores.
func TestOpenAIGatewayService_SelectProviderWithScheduler_EnabledUsesAdvancedPreviousResponseRouting(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10107)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds = 3600
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),

			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	store := svc.ResponseStateStore()
	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_enabled_001", 37001, time.Hour))

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"resp_enabled_001",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37001), selection.Provider.Record.ID)
	require.Equal(t, "previous_response_id", decision.Layer)
	require.True(t, decision.StickyPreviousHit)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_StickyWeightedSessionUsesTopKSampling
// supplies two advanced candidates and repeated distinct session hashes bound
// to one target. Both targets appear in sampled results, protecting weighted
// affinity from forced-first selection.
func TestOpenAIGatewayService_SelectProviderWithScheduler_StickyWeightedSessionUsesTopKSampling(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101071)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37101,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    100,
				GroupIDs:    []int64{groupID},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37102,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 2
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0.7
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.8
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.5
	cfg.Scheduling.Advanced.ScoreWeights.SessionSticky = 3
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{}}
	for index := range 128 {
		cache.sessionBindings["openai:"+fmt.Sprintf("session_hash_weighted_topk_%d", index)] = 37101
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true", "true"),
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, cfg)

	// Distinct session inputs exercise both eligible Top-K choices.
	// The bound target receives a bonus while another target retains positive sampling weight.
	var observedSticky, observedNonSticky bool
	for index := range 128 {
		selection, decision, err := svc.SelectProviderWithScheduler(
			ctx,
			&groupID,
			"",
			fmt.Sprintf("session_hash_weighted_topk_%d", index),
			"gpt-5.1",
			nil, egress.OpenAIUpstreamTransportAny, false,
		)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Provider)
		require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
		require.Equal(t, 2, decision.TopK)
		observedSticky = observedSticky || selection.Provider.Record.ID == 37101
		observedNonSticky = observedNonSticky || selection.Provider.Record.ID == 37102
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}
	require.True(t, observedSticky, "粘性加分提供商仍应参与抽样")
	require.True(t, observedNonSticky, "OpenAI 选择器不能把加权粘性强制置首")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_StickyWeightedPreviousRequiresMovableContext
// binds a lower-priority previous-response target in an advanced weighted
// group. Fixed continuation stays there, while movable input can select the
// scored alternative, protecting caller-controlled reconstruction eligibility.
func TestOpenAIGatewayService_SelectProviderWithScheduler_StickyWeightedPreviousRequiresMovableContext(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101072)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37111,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    100,
				GroupIDs:    []int64{groupID},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37112,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
	}
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Scheduling.Advanced.LBTopK = 1
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0.7
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.8
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.5
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),

			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true", "true"),
		},
	}, cfg)

	store := svc.ResponseStateStore()
	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_weighted_unmovable", 37111, time.Hour))

	selection, decision, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"resp_weighted_unmovable",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
		capability.PlatformOpenAI,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37111), selection.Provider.Record.ID)
	require.Equal(t, "previous_response_id", decision.Layer)
	require.True(t, decision.StickyPreviousHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	selection, decision, err = svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"resp_weighted_unmovable",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration,
		false,
		true,
		capability.PlatformOpenAI,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37112), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickyPreviousHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_PreviousResponseCompactUnsupportedDeletesBinding
// disables Compact on the response-bound provider while a backup permits it.
// Selection chooses the backup and deletes prior affinity, protecting endpoint
// rejection cleanup before ranking.
func TestOpenAIGatewayService_SelectProviderWithScheduler_PreviousResponseCompactUnsupportedDeletesBinding(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101073)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37121,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"openai_compact_mode":                           providercore.OpenAICompactModeForceOff,
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37122,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    10,
				GroupIDs:    []int64{groupID},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"openai_compact_mode":                           providercore.OpenAICompactModeForceOn,
				},
			},
		},
	}
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Scheduling.Advanced.LBTopK = 2
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0.7
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.8
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.5
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:      &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			}),
		},
	}, cfg)

	store := svc.ResponseStateStore()
	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_compact_unsupported", 37121, time.Hour))

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"resp_compact_unsupported",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37122), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickyPreviousHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	providerID, err := store.GetResponseProvider(ctx, groupID, "resp_compact_unsupported")
	require.NoError(t, err)
	require.Zero(t, providerID)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_Enabled_EmbeddingsSkipsChatOnlyProvider
// supplies an advanced group with text-only and embedding-capable records.
// Choice returns the embedding target with one eligible candidate, protecting
// endpoint filtering before advanced ordering.
func TestOpenAIGatewayService_SelectProviderWithScheduler_Enabled_EmbeddingsSkipsChatOnlyProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10111)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 37011,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Credentials: map[string]any{
					"model_whitelist":              []string{"*"},
					"openai_workload_capabilities": []any{"text_generation"},
				},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 37012,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				Credentials: map[string]any{
					"model_whitelist":              []string{"*"},
					"openai_workload_capabilities": []any{"text_generation", "embeddings"},
				},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),

			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"text-embedding-3-small",
		nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityEmbeddings,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37012), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.Equal(t, 1, decision.CandidateCount)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_Enabled_EmbeddingsSkipsChatOnlyStickyBindings
// binds both response and session affinity to a text-only target. Advanced
// embedding choice uses the capable backup and updates session affinity,
// protecting endpoint rules across both shortcuts.
func TestOpenAIGatewayService_SelectProviderWithScheduler_Enabled_EmbeddingsSkipsChatOnlyStickyBindings(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10112)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 37021,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Credentials: map[string]any{
					"model_whitelist":              []string{"*"},
					"openai_workload_capabilities": []any{"text_generation"},
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 37022,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				Credentials: map[string]any{
					"model_whitelist":              []string{"*"},
					"openai_workload_capabilities": []any{"text_generation", "embeddings"},
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
	}
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Scheduling.LoadBatch.Enabled = false
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_embeddings": 37021,
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache: cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			}),
			Health: gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg,
				"true"),
		},
	}, cfg)

	store := svc.ResponseStateStore()
	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_embeddings_chat_only", 37021, time.Hour))

	selection, decision, err := svc.SelectProviderWithSchedulerForCapability(
		ctx,
		&groupID,
		"resp_embeddings_chat_only",
		"session_hash_embeddings",
		"text-embedding-3-small",
		nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityEmbeddings,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37022), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickyPreviousHit)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, int64(37022), cache.sessionBindings["openai:session_hash_embeddings"])
}

// TestOpenAIGatewayService_OpenAIProviderSchedulerMetrics_DisabledNoOp omits
// the health observer before reporting a target result and switch. Metrics
// remain empty, protecting the wrapper's optional-observer behavior
// independently of selector construction.
func TestOpenAIGatewayService_OpenAIProviderSchedulerMetrics_DisabledNoOp(t *testing.T) {
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	ttft := 120
	svc.ReportOpenAIProviderScheduleResult(&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 10}}, "", true, &ttft)
	svc.RecordOpenAIProviderSwitch()

	snapshot := svc.SnapshotOpenAIProviderSchedulerMetrics()
	require.Equal(t, schedulercore.PlatformMetricsSnapshot{}, snapshot)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SkipsQuarantinedSharedProxy
// blocks a proxy shared by the preferred OpenAI members. Selection chooses the
// healthy-proxy backup, protecting shared proxy quarantine before basic
// provider ordering.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SkipsQuarantinedSharedProxy(t *testing.T) {
	proxyA := int64(4698)
	proxyB := int64(4699)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 469801, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, ProxyID: &proxyA}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 469802, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, ProxyID: &proxyA}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 469803, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, ProxyID: &proxyB}},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
		ProxyCircuit: egress.NewProxyStreamCircuit(egress.ProxyStreamCircuitSettings{FailureThreshold: 1, FailureWindow: time.Minute, QuarantineTTL: 10 *
			time.Minute, MaxEntries: 16}),
	}, cfg)

	svc.proxyCircuit.RecordFailure(proxyA, time.Now())

	selection, _, err := svc.SelectProviderWithScheduler(
		context.Background(), selectionFixtureGroupID(context.Background()), "", "", "gpt-5.6-sol", nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(469803), selection.Provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_FailsOpenWhenAllProxiesQuarantined
// blocks the proxy used by every basic candidate. A request-local retry returns
// a target while circuit quarantine remains active, protecting outage admission
// scope.
func TestOpenAIGatewayService_SelectProviderWithScheduler_FailsOpenWhenAllProxiesQuarantined(t *testing.T) {
	proxyID := int64(5056)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 505601, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, ProxyID: &proxyID}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 505602, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, ProxyID: &proxyID}},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
		ProxyCircuit: egress.NewProxyStreamCircuit(egress.ProxyStreamCircuitSettings{FailureThreshold: 1, FailureWindow: time.Minute, QuarantineTTL: 10 *
			time.Minute, MaxEntries: 16}),
	}, cfg)

	tripped, _ := svc.proxyCircuit.RecordFailure(proxyID, time.Now())
	require.True(t, tripped)

	selection, _, err := svc.SelectProviderWithScheduler(
		context.Background(), selectionFixtureGroupID(context.Background()), "", "", "gpt-5.6-sol", nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err, "代理隔离不能导致无可用提供商")
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.NotNil(t, selection.Provider.Record.ProxyID)
	require.Equal(t, proxyID, *selection.Provider.Record.ProxyID)
	require.True(t, svc.proxyCircuit.IsBlocked(proxyID, time.Now()),
		"fail-open 只影响本次调度，不应清除隔离状态")
}

// TestOpenAIGatewayService_SelectProviderWithSchedulerForRouting_FailsOpenWhenAllProxiesQuarantined
// supplies distinct client and resolved routing names through a blocked proxy.
// The request succeeds and returns the fixture's provider, covering quarantine
// bypass with the supplied routing input.
func TestOpenAIGatewayService_SelectProviderWithSchedulerForRouting_FailsOpenWhenAllProxiesQuarantined(t *testing.T) {
	proxyID := int64(5057)
	provider := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 505701, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, ProxyID: &proxyID}}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = false
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:        Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared:       Shared{Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
		ProxyCircuit: egress.NewProxyStreamCircuit(egress.ProxyStreamCircuitSettings{FailureThreshold: 1, FailureWindow: time.Minute, QuarantineTTL: 10 * time.Minute, MaxEntries: 16}),
	}, cfg)

	svc.proxyCircuit.RecordFailure(proxyID, time.Now())

	selection, _, err := svc.SelectProviderWithSchedulerForCapabilityAndRoutingModel(
		context.Background(), selectionFixtureGroupID(context.Background()), "", "", "client-alias", "gpt-5.6-sol", nil, egress.OpenAIUpstreamTransportAny, "", false, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, provider.Record.ID, selection.Provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyRateLimitedProviderFallsBackToFreshCandidate
// leaves the sticky candidate stale while its full snapshot is rate limited.
// Choice uses the fresh backup and reports ordinary pool selection, protecting
// current eligibility before affinity reuse.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyRateLimitedProviderFallsBackToFreshCandidate(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10101)
	rateLimitedUntil := time.Now().Add(30 * time.Minute)
	staleSticky := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 31001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}}
	staleBackup := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 31002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	freshSticky := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 31001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, RateLimitResetAt: &rateLimitedUntil}}
	freshBackup := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 31002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_rate_limited": 31001}}
	snapshotCache := &openAISnapshotCacheStub{snapshotProviders: []*gatewayprovider.ExecutionProvider{staleSticky, staleBackup}, providersByID: map[int64]*gatewayprovider.ExecutionProvider{31001: freshSticky, 31002: freshBackup}}
	snapshotService := schedulercore.NewSnapshotService(snapshotCache, nil, nil, nil, nil)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{*freshSticky, *freshBackup}},
			Snapshot:  schedulerredis.NewSnapshotReader(snapshotService),
		},
		Shared: Shared{
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, &config.Config{})

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_rate_limited", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(31002), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyDBRuntimeRecheckSkipsStaleCachedProvider
// keeps cached sticky records usable while the database blocks the primary.
// Choice returns the valid backup, protecting database runtime checks before
// dispatch.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyDBRuntimeRecheckSkipsStaleCachedProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10103)
	rateLimitedUntil := time.Now().Add(30 * time.Minute)
	staleSticky := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 33001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}}
	staleBackup := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 33002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	dbSticky := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 33001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, RateLimitResetAt: &rateLimitedUntil}}
	dbBackup := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 33002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_db_runtime_recheck": 33001}}
	snapshotCache := &openAISnapshotCacheStub{
		snapshotProviders: []*gatewayprovider.ExecutionProvider{staleSticky, staleBackup},
		providersByID:     map[int64]*gatewayprovider.ExecutionProvider{33001: staleSticky, 33002: staleBackup},
	}
	snapshotService := schedulercore.NewSnapshotService(snapshotCache, nil, nil, nil, nil)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{dbSticky, dbBackup}},
			Snapshot: schedulerredis.NewSnapshotReader(
				snapshotService,
			),
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Cache:       cache,
		},
	}, &config.Config{})

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_db_runtime_recheck", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(33002), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_PreviousResponseSticky
// supplies an eligible bound response in an advanced group and a new session
// hash. Choice returns that provider and binds the session, protecting affinity
// propagation from response continuation.
func TestOpenAIGatewayService_SelectProviderWithScheduler_PreviousResponseSticky(t *testing.T) {
	ctx := context.Background()
	groupID := int64(9)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1001,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 2,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			},
		},
	}
	cache := &schedulerTestGatewayCache{}
	cfg := &config.Config{}
	cfg.Scheduling.SessionAffinity.TTLSeconds = 1800
	cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds = 3600

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	store := svc.ResponseStateStore()
	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_prev_001", provider.Record.ID, time.Hour))

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"resp_prev_001",
		"session_hash_001",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, provider.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, "previous_response_id", decision.Layer)
	require.True(t, decision.StickyPreviousHit)
	require.Equal(t, provider.Record.ID, cache.sessionBindings["openai:session_hash_001"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionSticky supplies
// an eligible group-bound session in advanced mode. Choice retains its provider
// and reports a session hit, protecting hard affinity before ordinary ranking.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionSticky(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2001,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			GroupIDs:    []int64{groupID},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_abc": provider.Record.ID,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared: Shared{
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, &config.Config{})

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_abc",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, provider.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, "session_hash", decision.Layer)
	require.True(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyBusyKeepsSticky
// fills the hard-bound advanced target with sticky escape disabled. Selection
// returns its wait plan despite another target's capacity, protecting
// configured affinity retention under load.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyBusyKeepsSticky(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10100)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    9,
				GroupIDs:    []int64{groupID},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_sticky_busy": 21001,
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.StickyProviderWait.MaxWaiting = 2
	cfg.Scheduling.StickyProviderWait.TimeoutSeconds = 45
	cfg.Scheduling.Advanced.StickyEscapeEnabled = false
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.5

	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{
			21001: false, // The sticky provider is full.
			21002: true,  // The alternative is available while hard session affinity keeps the bound target preferred.
		},
		waitCounts: map[int64]int{
			21001: 999,
		},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			21001: {ProviderID: 21001, LoadRate: 90, WaitingCount: 9},
			21002: {ProviderID: 21002, LoadRate: 1, WaitingCount: 0},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:      cache,

			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,
				schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
			Health: gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, cfg,
	)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_sticky_busy",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21001), selection.Provider.Record.ID, "busy sticky provider should remain selected")
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(21001), selection.WaitPlan.ProviderID)
	require.Equal(t, "session_hash", decision.Layer)
	require.True(t, decision.StickySessionHit)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyEscapeByTTFT
// first keeps latency feedback below the escape threshold, then raises its EWMA
// through slower samples. Choice moves this request to the backup while
// retaining the stored session binding.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyEscapeByTTFT(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10101)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21101,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21102,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				GroupIDs:    []int64{groupID},
			},
		},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_ttft": 21101}}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.StickyEscapeEnabled = true
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.5
	concurrencyCache := schedulerTestConcurrencyCache{acquireResults: map[int64]bool{21102: true}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:       cache,
		},
	}, cfg,
	)

	svc.openaiProviderStats = schedulercore.NewRuntimeStats(time.Now)
	// Latency samples use milliseconds and start just below the configured limit.
	// Later slower observations raise the shared EWMA across that escape threshold.
	fastTTFT := 14999
	svc.openaiProviderStats.Report(21101, true, &fastTTFT)
	stableTTFT := 14999
	svc.openaiProviderStats.Report(21101, true, &stableTTFT)

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_sticky_ttft", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21101), selection.Provider.Record.ID)
	require.Equal(t, "session_hash", decision.Layer)
	require.True(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	// This millisecond sample exceeds the fixture's fifteen-second escape limit.
	slowTTFT := 20000
	for range 3 {
		svc.openaiProviderStats.Report(21101, true, &slowTTFT)
	}

	selection, decision, err = svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_sticky_ttft", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21102), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, int64(21101), cache.sessionBindings["openai:session_hash_sticky_ttft"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyEscapeByErrorRate
// accumulates failures across the configured EWMA threshold. Choice retains
// affinity below the limit and escapes afterward while keeping the binding,
// protecting threshold-sensitive request behavior.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyEscapeByErrorRate(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10102)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21201, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21202, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_error_rate": 21201}}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.StickyEscapeEnabled = true
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.7
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:      cache,

			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{21202: true}}, schedulercore.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			}),
			Health: gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, cfg)

	svc.openaiProviderStats = schedulercore.NewRuntimeStats(time.Now)
	for range 5 {
		svc.openaiProviderStats.Report(21201, false, nil)
	}
	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_sticky_error_rate", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21201), selection.Provider.Record.ID)
	require.Equal(t, "session_hash", decision.Layer)
	require.True(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	svc.openaiProviderStats.Report(21201, false, nil)

	selection, decision, err = svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_sticky_error_rate", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21202), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, int64(21201), cache.sessionBindings["openai:session_hash_sticky_error_rate"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyBusyEscapes
// fills the bound target and its sticky queue while escape is enabled. Choice
// immediately uses the available backup with no wait plan, protecting capacity
// escape.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyBusyEscapes(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10103)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21301, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21302, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_busy_escape": 21301}}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.StickyEscapeEnabled = true
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.5
	cfg.Scheduling.StickyProviderWait.MaxWaiting = 2
	cfg.Scheduling.StickyProviderWait.TimeoutSeconds = 45
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21301: false, 21302: true},
		waitCounts:     map[int64]int{21301: 999},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			21301: {ProviderID: 21301, LoadRate: 95, WaitingCount: 9},
			21302: {ProviderID: 21302, LoadRate: 1, WaitingCount: 0},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache: cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

				schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg,
	)

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_sticky_busy_escape", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21302), selection.Provider.Record.ID)
	require.Nil(t, selection.WaitPlan)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyEscapeDisabledKeepsLegacyBehavior
// supplies slow and failed feedback with escape disabled. Advanced choice
// retains the bound provider's wait plan, protecting the configured switch over
// observed performance.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionStickyEscapeDisabledKeepsLegacyBehavior(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10104)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21401, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21402, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_disabled": 21401}}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.StickyEscapeEnabled = false
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.5
	cfg.Scheduling.StickyProviderWait.MaxWaiting = 2
	cfg.Scheduling.StickyProviderWait.TimeoutSeconds = 45
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21401: false, 21402: true},
		waitCounts:     map[int64]int{21401: 999},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:      cache,

			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,
				schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
			Health: gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, cfg,
	)

	svc.openaiProviderStats = schedulercore.NewRuntimeStats(time.Now)
	// This millisecond sample exceeds the fixture's fifteen-second escape limit.
	slowTTFT := 20000
	svc.openaiProviderStats.Report(21401, true, &slowTTFT)
	for range 5 {
		svc.openaiProviderStats.Report(21401, false, nil)
	}

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_sticky_disabled", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21401), selection.Provider.Record.ID)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(21401), selection.WaitPlan.ProviderID)
	require.Equal(t, "session_hash", decision.Layer)
	require.True(t, decision.StickySessionHit)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityChoosesSubscriptionPoolFirst
// gives an advanced subscription target worse priority and load than an API
// Key. Enabled subscription preference still selects it, protecting pool
// precedence before weighted ranking.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityChoosesSubscriptionPoolFirst(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10120)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	apiKey := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21602, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0,
			GroupIDs: []int64{groupID},
		},
	}
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 21601,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    10,
				GroupIDs:    []int64{groupID},
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "plan_type": "plus"},
			},
		},
		*apiKey,
	}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21601: true, 21602: true},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			21601: {ProviderID: 21601, LoadRate: 90, WaitingCount: 1},
			21602: {ProviderID: 21602, LoadRate: 0, WaitingCount: 0},
		},
	}
	cfg := newSchedulerTestSubscriptionPriorityConfig()
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true", "", "true"),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_subscription_first", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21601), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.Equal(t, 1, decision.TopK)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityFallsBackWhenSubscriptionFull
// fills an eligible subscription target while the regular target can acquire
// capacity. Advanced selection returns the regular target, protecting recovery
// after the preferred pool has no slot.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityFallsBackWhenSubscriptionFull(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10121)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 21611,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "plan_type": "team"},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21612,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    9,
				GroupIDs:    []int64{groupID},
			},
		},
	}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21611: false, 21612: true},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			21611: {ProviderID: 21611, LoadRate: 0, WaitingCount: 0},
			21612: {ProviderID: 21612, LoadRate: 90, WaitingCount: 1},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(newSchedulerTestSubscriptionPriorityConfig(), "true", "", "true"),
		},
	}, newSchedulerTestSubscriptionPriorityConfig())

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_subscription_fallback", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21612), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.True(t, selection.Acquired)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityDisabledUsesScore
// supplies disabled subscription settings and an API Key with better priority
// and load. The fixture's admitted group remains basic; choice returns that API
// Key through its ordinary selection path.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityDisabledUsesScore(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10122)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 21621,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    10,
				GroupIDs:    []int64{groupID},
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "plan_type": "pro"},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21622,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
			},
		},
	}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21621: true, 21622: true},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			21621: {ProviderID: 21621, LoadRate: 90, WaitingCount: 1},
			21622: {ProviderID: 21622, LoadRate: 0, WaitingCount: 0},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(newSchedulerTestSubscriptionPriorityConfig(), "true", "", "false"),
		},
	}, newSchedulerTestSubscriptionPriorityConfig())

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_subscription_disabled", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21622), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_UsesProviderPriorityWithinGroupPool
// supplies explicit group memberships and priority-only advanced scoring.
// Choice returns the provider with better global priority, protecting stored
// provider priority inside the group's pool.
func TestOpenAIGatewayService_SelectProviderWithScheduler_UsesProviderPriorityWithinGroupPool(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10123)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21631,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				ProviderGroups: []providercore.GroupMembership{
					{ProviderID: 21631, GroupID: groupID},
				},
				GroupIDs: []int64{groupID},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21632,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    100000,
				ProviderGroups: []providercore.GroupMembership{
					{ProviderID: 21632, GroupID: groupID},
				},
				GroupIDs: []int64{groupID},
			},
		},
	}
	cfg := newSchedulerTestSubscriptionPriorityConfig()
	cfg.Scheduling.Advanced.LBTopK = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 0
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerGroupAwareOpenAIProviderRepo{schedulerTestOpenAIProviderRepo{providers: providers}}},
		Shared: Shared{
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_group_priority", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(21631), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIProviderScheduler_SkipsProviderBlockedForRequestedModel records two
// failures for one model on a provider. Compatibility rejects that model while
// admitting another, protecting shared transient state under canonical request
// identity.
func TestOpenAIProviderScheduler_SkipsProviderBlockedForRequestedModel(t *testing.T) {
	now := time.Now()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21633, Status: billing.StatusActive, Schedulable: true, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:          Reads{},
		Shared:         Shared{},
		ModelTransient: providercore.NewModelTransientState(128),
	}, nil)

	svc.modelTransient.RecordFailure(provider.Record.ID, "gpt-5.5", now)
	svc.modelTransient.RecordFailure(provider.Record.ID, "gpt-5.5", now.Add(time.Millisecond))
	scheduler := &compatiblePicker{service: svc}

	require.False(t, scheduler.isProviderRequestCompatible(context.Background(), provider, schedulercore.PlatformSelectionInput{RequestedModel: "gpt-5.5"}))
	require.True(t, scheduler.isProviderRequestCompatible(context.Background(), provider, schedulercore.PlatformSelectionInput{RequestedModel: "gpt-5.6-sol"}))
}

// TestReportOpenAIProviderScheduleResult_SuccessClearsModelTransientState
// records an active provider-model cooldown before successful dispatch
// feedback. The same model becomes unblocked afterward, protecting recovery
// independently of optional picker metrics.
func TestReportOpenAIProviderScheduleResult_SuccessClearsModelTransientState(t *testing.T) {
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:          Reads{},
		Shared:         Shared{},
		ModelTransient: providercore.NewModelTransientState(128),
	}, nil)

	now := time.Now()
	svc.modelTransient.RecordFailure(21636, "gpt-5.5", now)
	svc.modelTransient.RecordFailure(21636, "gpt-5.5", now.Add(time.Millisecond))
	require.True(t, svc.modelTransient.IsBlocked(21636, "gpt-5.5", now.Add(2*time.Millisecond)))

	svc.ReportOpenAIProviderScheduleResult(&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 21636}}, "gpt-5.5", true, nil)

	require.False(t, svc.modelTransient.IsBlocked(21636, "gpt-5.5", now.Add(2*time.Millisecond)))
}

// TestReportOpenAIProviderScheduleResultForSelection_BasicClearsTransientState
// reports failed and successful basic requests against an active model cooldown.
// Success clears the cooldown while feedback stays unchanged and the picker
// remains uninitialized, including absent results and basic results carrying alphas.
func TestReportOpenAIProviderScheduleResultForSelection_BasicClearsTransientState(t *testing.T) {
	for name, selection := range map[string]*gatewayprovider.SelectionResult{
		"absent": nil,
		"basic":  {},
		"basic_with_feedback": {
			AdvancedSchedulerFeedback: &policy.FeedbackConfig{ErrorRateAlpha: 1, TtftAlpha: 1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			stats := schedulercore.NewRuntimeStats(time.Now)
			stats.Report(21637, false, intPtrForTest(150))
			before := stats.FeedbackSnapshot(21637)
			svc := &Compatible{
				modelTransient:      providercore.NewModelTransientState(128),
				openaiProviderStats: stats,
			}
			svc.modelTransient.RecordFailure(21637, "gpt-5.5", now)
			svc.modelTransient.RecordFailure(21637, "gpt-5.5", now.Add(time.Millisecond))
			require.True(t, svc.modelTransient.IsBlocked(21637, "gpt-5.5", now.Add(2*time.Millisecond)))

			svc.ReportOpenAIProviderScheduleResultForSelection(selection, 21637, "gpt-5.5", false, intPtrForTest(300))
			require.True(t, svc.modelTransient.IsBlocked(21637, "gpt-5.5", now.Add(2*time.Millisecond)))
			svc.ReportOpenAIProviderScheduleResultForSelection(selection, 21637, " GPT-5.5 ", true, nil)

			require.False(t, svc.modelTransient.IsBlocked(21637, "gpt-5.5", now.Add(2*time.Millisecond)))
			require.Equal(t, before, stats.FeedbackSnapshot(21637))
			require.Nil(t, svc.picker)
		})
	}
}

// TestReportOpenAIProviderScheduleResultForSelection_AdvancedDefaultFeedback
// reports an advanced result that has no captured coefficients.
// Outcomes use scheduler defaults, nil latency leaves TTFT absent, and later
// positive samples blend while successful feedback clears the model cooldown.
func TestReportOpenAIProviderScheduleResultForSelection_AdvancedDefaultFeedback(t *testing.T) {
	now := time.Now()
	stats := schedulercore.NewRuntimeStats(time.Now)
	svc := &Compatible{
		modelTransient:      providercore.NewModelTransientState(128),
		openaiProviderStats: stats,
	}
	svc.modelTransient.RecordFailure(21638, "gpt-5.5", now)
	svc.modelTransient.RecordFailure(21638, "gpt-5.5", now.Add(time.Millisecond))
	selection := &gatewayprovider.SelectionResult{AdvancedScheduler: true}

	svc.ReportOpenAIProviderScheduleResultForSelection(selection, 21638, "gpt-5.5", false, nil)
	first := stats.FeedbackSnapshot(21638)
	require.True(t, first.HasFeedback)
	require.InDelta(t, 0.2, first.ErrorRate, 1e-12)
	require.Equal(t, int64(1), first.ErrorSamples)
	require.False(t, first.HasTTFT)
	require.Zero(t, first.TTFTSamples)
	require.True(t, svc.modelTransient.IsBlocked(21638, "gpt-5.5", now.Add(2*time.Millisecond)))

	svc.ReportOpenAIProviderScheduleResultForSelection(selection, 21638, "gpt-5.5", true, intPtrForTest(100))
	svc.ReportOpenAIProviderScheduleResultForSelection(selection, 21638, "gpt-5.5", true, intPtrForTest(200))
	after := stats.FeedbackSnapshot(21638)
	require.InDelta(t, 0.128, after.ErrorRate, 1e-12)
	require.Equal(t, int64(3), after.ErrorSamples)
	require.True(t, after.HasTTFT)
	require.InDelta(t, 120, after.TTFT, 1e-12)
	require.Equal(t, int64(2), after.TTFTSamples)
	require.False(t, svc.modelTransient.IsBlocked(21638, "gpt-5.5", now.Add(2*time.Millisecond)))

	var absent *Compatible
	require.NotPanics(t, func() {
		absent.ReportOpenAIProviderScheduleResultForSelection(selection, 21638, "gpt-5.5", true, nil)
	})
}

// TestReportOpenAIProviderScheduleResultForSelection_UsesCapturedFeedback
// selects an advanced provider before publishing different runtime coefficients.
// Completion applies the selected coefficients to shared feedback, and a later
// successful observation clears cooldown state while leaving earlier latency
// observations unchanged when its TTFT is absent.
func TestReportOpenAIProviderScheduleResultForSelection_UsesCapturedFeedback(t *testing.T) {
	groupID := int64(21639)
	providerID := int64(21640)
	ctx := withAdvancedSchedulerTestGroup(context.Background(), groupID)
	runtime := schedulercore.NewSettingsRuntime(schedulercore.Diagnostics{})
	runtime.Store(policy.RuntimeSettings{
		EwmaErrorRateAlpha: 0.5, EwmaErrorRateAlphaSet: true,
		EwmaTTFTAlpha: 0.25, EwmaTTFTAlphaSet: true,
	})
	parameters := schedulercore.NewParameters(runtime, nil, schedulercore.DefaultParameters())
	stats := schedulercore.NewRuntimeStats(time.Now)
	stats.Report(providerID, true, intPtrForTest(100))
	provider := gatewayprovider.ExecutionProvider{Record: providercore.Record{
		ID: providerID, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation,
	}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{}),
			Parameters:  parameters,
			Feedback:    stats,
		},
	}, nil)
	selection, _, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "", "gpt-5.5", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	if selection.ReleaseFunc != nil {
		t.Cleanup(selection.ReleaseFunc)
	}
	require.True(t, selection.AdvancedScheduler)
	require.Equal(t, &policy.FeedbackConfig{ErrorRateAlpha: 0.5, TtftAlpha: 0.25}, selection.AdvancedSchedulerFeedback)

	// Publish different coefficients after selection so completion can reveal a
	// settings reread that would associate this request with the later values.
	runtime.Store(policy.RuntimeSettings{
		EwmaErrorRateAlpha: 0.9, EwmaErrorRateAlphaSet: true,
		EwmaTTFTAlpha: 0.9, EwmaTTFTAlphaSet: true,
	})
	require.Equal(t, policy.FeedbackConfig{ErrorRateAlpha: 0.9, TtftAlpha: 0.9}, parameters.Effective(ctx, policy.GroupAdvancedSchedulerOverrides{}).Feedback)
	svc.ReportOpenAIProviderScheduleResultForSelection(selection, providerID, "gpt-5.5", false, intPtrForTest(300))
	afterFailure := stats.FeedbackSnapshot(providerID)
	require.InDelta(t, 0.5, afterFailure.ErrorRate, 1e-12)
	require.InDelta(t, 150, afterFailure.TTFT, 1e-12)
	require.Equal(t, int64(2), afterFailure.ErrorSamples)
	require.Equal(t, int64(2), afterFailure.TTFTSamples)

	now := time.Now()
	svc.modelTransient.RecordFailure(providerID, "gpt-5.5", now)
	svc.modelTransient.RecordFailure(providerID, "gpt-5.5", now.Add(time.Millisecond))
	require.True(t, svc.modelTransient.IsBlocked(providerID, "gpt-5.5", now.Add(2*time.Millisecond)))
	svc.ReportOpenAIProviderScheduleResultForSelection(selection, providerID, "gpt-5.5", true, nil)
	afterSuccess := stats.FeedbackSnapshot(providerID)
	require.InDelta(t, 0.25, afterSuccess.ErrorRate, 1e-12)
	require.InDelta(t, 150, afterSuccess.TTFT, 1e-12)
	require.Equal(t, int64(3), afterSuccess.ErrorSamples)
	require.Equal(t, int64(2), afterSuccess.TTFTSamples)
	require.False(t, svc.modelTransient.IsBlocked(providerID, "gpt-5.5", now.Add(2*time.Millisecond)))
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SessionSticky_ForceHTTP
// binds an advanced OAuth target configured to force HTTP. An unrestricted
// transport request retains session affinity, protecting protocol choice from
// unnecessarily invalidating an eligible binding.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SessionSticky_ForceHTTP(t *testing.T) {
	ctx := context.Background()
	groupID := int64(1010)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2101,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			GroupIDs:    []int64{groupID},
			Extra: map[string]any{
				"openai_ws_force_http": true,
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_force_http": provider.Record.ID,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),

			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Cache:      cache,
		},
	}, &config.Config{})

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_force_http",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, provider.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, "session_hash", decision.Layer)
	require.True(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_RequiredWSV2_SkipsStickyHTTPProvider
// binds a low-load HTTP-only target beside a WS-capable member. Advanced WS
// choice selects the capable target with one eligible candidate, protecting
// transport filtering before sticky ranking.
func TestOpenAIGatewayService_SelectProviderWithScheduler_RequiredWSV2_SkipsStickyHTTPProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(1011)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "upstream_protocols": []string{"openai_responses"}}, LoadLocation: time.LoadLocation, ID: 2201,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2202,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				GroupIDs:    []int64{groupID},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
				},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_ws_only": 2201,
		},
	}
	cfg := newSchedulerTestOpenAIWSV2Config()

	// Give the HTTP-only provider lower load so transport filtering determines the result.
	concurrencyCache := schedulerTestConcurrencyCache{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			2201: {ProviderID: 2201, LoadRate: 0, WaitingCount: 0},
			2202: {ProviderID: 2202, LoadRate: 90, WaitingCount: 5},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Parameters: newAdvancedSchedulerParametersForTest(cfg, "true"),
			Cache:      cache,

			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,
				schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
			Health: gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, cfg,
	)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_ws_only",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(2202), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, 1, decision.CandidateCount)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_ClearsStickyProviderOutsideGroup
// binds a target lacking the requested membership beside an admitted member.
// Selection deletes and replaces that affinity, protecting group isolation
// during compatible session choice.
func TestOpenAIGatewayService_SelectProviderWithScheduler_ClearsStickyProviderOutsideGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(1013)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2401,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2402,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
				ProviderGroups: []providercore.GroupMembership{
					{ProviderID: 2402, GroupID: groupID},
				},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_removed_group": 2401,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerGroupAwareOpenAIProviderRepo{schedulerTestOpenAIProviderRepo{providers: providers}}},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
		},
	}, &config.Config{})

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_removed_group",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(2402), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, 1, cache.deletedSessions["openai:session_hash_removed_group"])
	require.Equal(t, int64(2402), cache.sessionBindings["openai:session_hash_removed_group"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_RequiredWSV2_NoAvailableProvider
// supplies a basic group with an HTTP-only target while WSv2 is required.
// Selection returns no result and zero advanced candidate count, protecting
// transport exhaustion.
func TestOpenAIGatewayService_SelectProviderWithScheduler_RequiredWSV2_NoAvailableProvider(t *testing.T) {
	ctx := context.Background()
	groupID := int64(1012)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "upstream_protocols": []string{"openai_responses"}}, LoadLocation: time.LoadLocation, ID: 2301,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
			},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(newSchedulerTestOpenAIWSV2Config(), "true"),
			Cache:       &schedulerTestGatewayCache{},
		},
	}, newSchedulerTestOpenAIWSV2Config())

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, false,
	)
	require.Error(t, err)
	require.Nil(t, selection)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.Equal(t, 0, decision.CandidateCount)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_LoadBalanceTopKFallback
// ranks three advanced targets with Top-K two and makes the best target's slot
// unavailable. Choice returns the lower-ranked available member and reports
// the pool and Top-K counts, protecting bounded candidate selection.
func TestOpenAIGatewayService_SelectProviderWithScheduler_LoadBalanceTopKFallback(t *testing.T) {
	ctx := context.Background()
	groupID := int64(11)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3003,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
			},
		},
	}

	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 2
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 0.4
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1.0
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 1.0
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.2
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.1

	concurrencyCache := schedulerTestConcurrencyCache{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			3001: {ProviderID: 3001, LoadRate: 95, WaitingCount: 8},
			3002: {ProviderID: 3002, LoadRate: 20, WaitingCount: 1},
			3003: {ProviderID: 3003, LoadRate: 10, WaitingCount: 0},
		},
		acquireResults: map[int64]bool{
			3003: false, // Highest-scoring target has no slot.
			3002: true,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(3002), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.Equal(t, 3, decision.CandidateCount)
	require.Equal(t, 2, decision.TopK)
	require.Greater(t, decision.LoadSkew, 0.0)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_LoadBalanceTopKExcludesQuotaPaused
// puts the better-priority target above its quota threshold with Top-K one. The
// eligible backup remains the sole candidate, protecting hard filtering before
// ranking.
func TestOpenAIGatewayService_SelectProviderWithScheduler_LoadBalanceTopKExcludesQuotaPaused(t *testing.T) {
	ctx := context.Background()
	groupID := int64(110)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Extra: map[string]any{
					"codex_5h_used_percent":   96.0,
					"auto_pause_5h_threshold": 0.95,
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 37002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    5,
			},
		},
	}

	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 1 // With TopK=1, admitting the paused provider would displace the healthy candidate.
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 0.4
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1.0
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 1.0

	concurrencyCache := schedulerTestConcurrencyCache{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			37001: {ProviderID: 37001, LoadRate: 5, WaitingCount: 0},
			37002: {ProviderID: 37002, LoadRate: 5, WaitingCount: 0},
		},
		acquireResults: map[int64]bool{
			37002: true,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(37002), selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	// Initial filtering removes the paused provider and admits the healthy candidate.
	require.Equal(t, 1, decision.CandidateCount)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAIGatewayService_OpenAIProviderSchedulerMetrics selects a hard sticky
// advanced target, reports successful feedback, and records a switch. Snapshot
// counters expose selection, affinity, switching, and retained feedback,
// protecting shared metric reporting.
func TestOpenAIGatewayService_OpenAIProviderSchedulerMetrics(t *testing.T) {
	ctx := context.Background()
	groupID := int64(12)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 4001,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			GroupIDs:    []int64{groupID},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{
			"openai:session_hash_metrics": provider.Record.ID,
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
		},
	}, &config.Config{})

	selection, _, err := svc.SelectProviderWithScheduler(ctx, &groupID, "", "session_hash_metrics", "gpt-5.1", nil, egress.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	svc.ReportOpenAIProviderScheduleResult(&provider, "", true, intPtrForTest(120))
	svc.RecordOpenAIProviderSwitch()

	snapshot := svc.SnapshotOpenAIProviderSchedulerMetrics()
	require.GreaterOrEqual(t, snapshot.SelectTotal, int64(1))
	require.GreaterOrEqual(t, snapshot.StickySessionHitTotal, int64(1))
	require.GreaterOrEqual(t, snapshot.ProviderSwitchTotal, int64(1))
	require.GreaterOrEqual(t, snapshot.SchedulerLatencyMsAvg, float64(0))
	require.GreaterOrEqual(t, snapshot.StickyHitRatio, 0.0)
	require.GreaterOrEqual(t, snapshot.RuntimeStatsProviderCount, 1)
}

// intPtrForTest returns a pointer to the supplied integer for latency fixture
// inputs.
func intPtrForTest(v int) *int {
	return &v
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_LoadBalanceDistributesAcrossSessions
// supplies three equally scored advanced targets and many distinct sessions.
// Choices span multiple providers, protecting Top-K weighted distribution
// independently of one session's affinity.
func TestOpenAIGatewayService_SelectProviderWithScheduler_LoadBalanceDistributesAcrossSessions(t *testing.T) {
	ctx := context.Background()
	groupID := int64(15)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 5101,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 3,
				Priority:    0,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 5102,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 3,
				Priority:    0,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 5103,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 3,
				Priority:    0,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 3
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 1
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 1
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 1

	concurrencyCache := schedulerTestConcurrencyCache{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			5101: {ProviderID: 5101, LoadRate: 20, WaitingCount: 1},
			5102: {ProviderID: 5102, LoadRate: 20, WaitingCount: 1},
			5103: {ProviderID: 5103, LoadRate: 20, WaitingCount: 1},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{sessionBindings: map[string]int64{}},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	// Equal scores leave each Top-K target positive sampling weight. Use distinct
	// session seeds so the scenario observes choices across more than one affinity key.
	selected := make(map[int64]int, len(providers))
	for i := range 60 {
		sessionHash := fmt.Sprintf("session_hash_lb_%d", i)
		selection, decision, err := svc.SelectProviderWithScheduler(
			ctx,
			&groupID,
			"",
			sessionHash,
			"gpt-5.1",
			nil, egress.OpenAIUpstreamTransportAny, false,
		)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Provider)
		require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
		selected[selection.Provider.Record.ID]++
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}

	// Distinct sessions should spread across multiple providers.
	require.GreaterOrEqual(t, len(selected), 2)
}

// TestDefaultOpenAIProviderScheduler_ReportSwitchAndSnapshot supplies
// successful latency feedback and two distinct selection-layer observations.
// The snapshot combines their counts, switch, latency, affinity ratio, and load
// skew, protecting picker metric aggregation.
func TestDefaultOpenAIProviderScheduler_ReportSwitchAndSnapshot(t *testing.T) {
	schedulerAny := newDefaultOpenAIProviderScheduler(newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, nil),

		nil)
	scheduler, ok := schedulerAny.(*compatiblePicker)
	require.True(t, ok)

	ttft := 100
	scheduler.ReportResult(1001, true, &ttft)
	scheduler.ReportSwitch()
	scheduler.metrics.RecordSelect(schedulercore.PlatformDecision{
		Layer:             openAIProviderScheduleLayerLoadBalance,
		LatencyMs:         8,
		LoadSkew:          0.5,
		StickyPreviousHit: true,
	})
	scheduler.metrics.RecordSelect(schedulercore.PlatformDecision{
		Layer:            "session_hash",
		LatencyMs:        6,
		LoadSkew:         0.2,
		StickySessionHit: true,
	})

	snapshot := scheduler.SnapshotMetrics()
	require.Equal(t, int64(2), snapshot.SelectTotal)
	require.Equal(t, int64(1), snapshot.StickyPreviousHitTotal)
	require.Equal(t, int64(1), snapshot.StickySessionHitTotal)
	require.Equal(t, int64(1), snapshot.LoadBalanceSelectTotal)
	require.Equal(t, int64(1), snapshot.ProviderSwitchTotal)
	require.Greater(t, snapshot.SchedulerLatencyMsAvg, 0.0)
	require.Greater(t, snapshot.StickyHitRatio, 0.0)
	require.Greater(t, snapshot.LoadSkewAvg, 0.0)
}

// TestOpenAIGatewayService_SchedulerWrappersAndDefaults compares unconfigured
// wrappers with supplied process weights, Top-K, and sticky lifetime. Metrics
// stay empty without health observation, while resolved defaults reflect those
// fixture inputs.
func TestOpenAIGatewayService_SchedulerWrappersAndDefaults(t *testing.T) {
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	ttft := 120
	svc.ReportOpenAIProviderScheduleResult(&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 10}}, "", true, &ttft)
	svc.RecordOpenAIProviderSwitch()
	snapshot := svc.SnapshotOpenAIProviderSchedulerMetrics()
	require.Equal(t, schedulercore.PlatformMetricsSnapshot{}, snapshot)
	require.Equal(t, 7, svc.schedulerParameters.Defaults().TopK)
	require.Equal(t, openaiStickySessionTTL, svc.SessionStickyTTL())

	defaultWeights := svc.schedulerParameters.Defaults().Weights
	require.Equal(t, 1.0, defaultWeights.Priority)
	require.Equal(t, 1.0, defaultWeights.Load)
	require.Equal(t, 0.7, defaultWeights.Queue)
	require.Equal(t, 0.8, defaultWeights.ErrorRate)
	require.Equal(t, 0.5, defaultWeights.TTFT)
	require.Equal(t, 0.0, defaultWeights.Reset)

	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 9
	cfg.Scheduling.SessionAffinity.TTLSeconds = 180
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 0.2
	cfg.Scheduling.Advanced.ScoreWeights.Load = 0.3
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0.4
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.5
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.6
	cfg.Scheduling.Advanced.ScoreWeights.Reset = 0.7
	svcWithCfg := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, cfg)

	require.Equal(t, 9, svcWithCfg.schedulerParameters.Defaults().TopK)
	require.Equal(t, 180*time.Second, svcWithCfg.SessionStickyTTL())
	customWeights := svcWithCfg.schedulerParameters.Defaults().Weights
	require.Equal(t, 0.2, customWeights.Priority)
	require.Equal(t, 0.3, customWeights.Load)
	require.Equal(t, 0.4, customWeights.Queue)
	require.Equal(t, 0.5, customWeights.ErrorRate)
	require.Equal(t, 0.6, customWeights.TTFT)
	require.Equal(t, 0.7, customWeights.Reset)
}

// TestDefaultOpenAIProviderScheduler_IsProviderTransportCompatible_Branches
// varies absent services, transport requirements, WS support, and the Responses
// protocol set. Compatibility follows direct or ingress conversion rules,
// protecting the transport distinction for eligible targets.
func TestDefaultOpenAIProviderScheduler_IsProviderTransportCompatible_Branches(t *testing.T) {
	scheduler := &compatiblePicker{}
	require.True(t, scheduler.isProviderTransportCompatible(nil, egress.OpenAIUpstreamTransportAny))
	require.True(t, scheduler.isProviderTransportCompatible(nil, egress.OpenAIUpstreamTransportHTTPSSE))
	require.False(t, scheduler.isProviderTransportCompatible(nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2))

	cfg := newSchedulerTestOpenAIWSV2Config()
	scheduler.service = newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, cfg)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 8801,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			},
		},
	}
	require.True(t, scheduler.isProviderTransportCompatible(provider, egress.OpenAIUpstreamTransportResponsesWebsocketV2))
	require.True(t, scheduler.isProviderTransportCompatible(provider, egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress))

	// HTTP conversion eligibility comes from the provider's protocol set.
	scheduler.service = newCompatibleSelectionForTest(CompatibleDependencies{}, cfg)
	provider.Record.Credentials = map[string]any{"upstream_protocols": []string{"openai_responses"}}
	require.False(t, scheduler.isProviderTransportCompatible(provider, egress.OpenAIUpstreamTransportResponsesWebsocketV2))
	require.True(t, scheduler.isProviderTransportCompatible(provider, egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress))

	provider.Record.Credentials["upstream_protocols"] = []string{}
	require.False(t, scheduler.isProviderTransportCompatible(provider, egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress))
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_StickyWeightedDoesNotFallbackOutsideTopK
// binds a lower-scoring provider outside a one-member Top-K pool. A busy Top-K
// target returns its own wait plan, protecting weighted affinity from adding an
// out-of-pool fallback.
func TestOpenAIGatewayService_SelectProviderWithScheduler_StickyWeightedDoesNotFallbackOutsideTopK(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101081)
	ctx = withAdvancedSchedulerTestGroup(ctx, groupID)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38001,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				GroupIDs:    []int64{groupID},
			},
		},
		{
			// The sticky provider remains in the group but scores below Top-K.
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38002,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    100,
				GroupIDs:    []int64{groupID},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 1
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0.7
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.8
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.5
	cfg.Scheduling.Advanced.ScoreWeights.SessionSticky = 0
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
		"openai:session_weighted_out_of_group": 38002,
	}}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{38001: false, 38002: true},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerGroupAwareOpenAIProviderRepo{schedulerTestOpenAIProviderRepo{providers: providers}}},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true", "true"),
			Cache:       cache,
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_weighted_out_of_group",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	// A full Top-K candidate 38001 returns its own wait plan.
	require.Equal(t, int64(38001), selection.Provider.Record.ID)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(38001), selection.WaitPlan.ProviderID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityWaitsOnBusySubscriptionWhenRegularUnusable
// supplies a busy subscription target and a regular target with Compact
// disabled. The fixture's basic group waits on the usable subscription member,
// protecting endpoint eligibility before waiting.
func TestOpenAIGatewayService_SelectProviderWithScheduler_SubscriptionPriorityWaitsOnBusySubscriptionWhenRegularUnusable(t *testing.T) {
	ctx := context.Background()
	groupID := int64(101091)
	providers := []gatewayprovider.ExecutionProvider{
		{
			// The subscription provider supports compact but has no free slot.
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 38011,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				GroupIDs:    []int64{groupID},
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "plan_type": "team"},
				Extra:       map[string]any{"openai_compact_mode": "force_on"},
			},
		},
		{
			// The regular provider has compact disabled and cannot serve this request.
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 38012,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    9,
				GroupIDs:    []int64{groupID},
				Extra:       map[string]any{"openai_compact_mode": "force_off"},
			},
		},
	}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{38011: false, 38012: true},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(newSchedulerTestSubscriptionPriorityConfig(), "true", "", "true"),
		},
	}, newSchedulerTestSubscriptionPriorityConfig())

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_subscription_wait",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	// An unusable regular pool leaves a wait plan for the busy subscription provider.
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(38011), selection.Provider.Record.ID)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(38011), selection.WaitPlan.ProviderID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_UsesWSPassthroughSnapshotFlags
// supplies a cached OAuth record with per-session WS connection mode. Basic
// selection returns that target for WSv2, protecting the snapshot flags used by
// transport eligibility.
func TestOpenAIGatewayService_SelectProviderWithScheduler_UsesWSPassthroughSnapshotFlags(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10105)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 35001,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			GroupIDs:    []int64{groupID},
			Extra: map[string]any{
				"responses_ws_connection_mode": "per_session",
			},
		},
	}

	snapshotCache := &openAISnapshotCacheStub{
		snapshotProviders: []*gatewayprovider.ExecutionProvider{provider},
		providersByID:     map[int64]*gatewayprovider.ExecutionProvider{provider.Record.ID: provider},
	}
	cfg := &config.Config{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{*provider}},
			Snapshot: schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(snapshotCache, nil,
				nil, nil, nil)),
		},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			Health:      gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:  newAdvancedSchedulerParametersForTest(cfg, "true"),
		},
	}, cfg)

	selection, decision, err := svc.SelectProviderWithScheduler(
		ctx,
		&groupID,
		"",
		"session_hash_ws_passthrough",
		"gpt-5.1",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, provider.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, openAIProviderScheduleLayerLoadBalance, decision.Layer)
}

// TestOpenAIGatewayService_SelectProviderByPreviousResponseIDUsesResolvedRoutingModel
// supplies different group and dispatch names with only the dispatch mapping
// allowed upstream. Basic selection returns the configured target using that
// resolved model; the populated response binding does not activate advanced
// affinity in this fixture.
func TestOpenAIGatewayService_SelectProviderByPreviousResponseIDUsesResolvedRoutingModel(t *testing.T) {
	ctx := context.Background()
	groupID := int64(26)
	price := 0.01
	pricingConfig := routingtestkit.Configuration{
		ID:                 78,
		Status:             billing.StatusActive,
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping:       map[string]string{"client-alias": "group-model"},
		ModelPricing: []routing.ModelPricingEntry{{
			Models:     []string{"allowed-upstream"},
			InputPrice: &price,
		}},
	}
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 32,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"group-model":    "blocked-upstream",
					"dispatch-model": "allowed-upstream",
				},
				"model_whitelist": []any{"blocked-upstream", "allowed-upstream"},
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			},
		},
	}
	cache := &responseCacheFixture{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	svc := NewCompatible(CompatibleDependencies{
		Reads: Reads{Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{provider}}},
		Shared: Shared{
			Cache: cache,
			Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{
				Logf: logging.LegacyPrintf,

				Event: logging.Event,
			},
			),
			GroupPolicies: routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig),
			Health:        gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters:    responseSelectionParameters(),
		},
		Responses: store,
	}, responseSelectionOptions())

	require.NoError(t, store.BindResponseProvider(ctx, groupID, "resp_dispatch_model", provider.Record.ID, time.Hour))
	selection, _, err := svc.SelectProviderWithSchedulerForCapabilityAndRoutingModel(
		ctx,
		&groupID,
		"resp_dispatch_model",
		"",
		"client-alias",
		"dispatch-model",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, providercore.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, provider.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, "allowed-upstream", gatewayprovider.ExecutionModelPolicy(selection.Provider).OpenAIUpstream("dispatch-model", false))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestSystemOneSelectionHonorsProtocol supplies Jev and OpenAI group members
// under basic and advanced modes. SystemOne and Responses requests choose their
// matching provider, protecting endpoint protocol eligibility within a
// mixed-platform group.
func TestSystemOneSelectionHonorsProtocol(t *testing.T) {
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		t.Run(string(mode), func(t *testing.T) {
			group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolSystemOne, protocol.ProtocolOpenAIResponses}}
			repo := &mixedGroupProviders{values: []gatewayprovider.ExecutionProvider{mixedGroupProvider(1, capability.PlatformJev, "shared", group.ID), mixedGroupProvider(2, capability.PlatformOpenAI, "shared", group.ID)}}
			selector := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: repo}}, DefaultOptions())
			for _, tc := range []struct {
				protocol protocol.ProtocolID // requested client protocol identity
				id       int64               // expected matching provider identifier
			}{{protocol.ProtocolSystemOne, 1}, {protocol.ProtocolOpenAIResponses, 2}} {
				ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), tc.protocol)
				result, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", "shared", nil, egress.OpenAIUpstreamTransportHTTPSSE, "", false, false)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tc.id, result.Provider.Record.ID)
				if result.ReleaseFunc != nil {
					result.ReleaseFunc()
				}
			}
		})
	}
}
