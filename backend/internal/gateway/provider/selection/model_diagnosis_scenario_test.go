package selection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// selectionAvailabilityFixture exposes platform fixture candidates as provider
// records for model diagnostics.
type selectionAvailabilityFixture struct {
	*mockProviderRepoForPlatform /* platform availability fixture */
}

// newBedrockRoutingTestProvider constructs a schedulable Bedrock record with
// supplied region and optional global routing. Fictitious SigV4 credentials let
// selection and diagnosis exercise regional model policy without contacting an
// upstream.
func newBedrockRoutingTestProvider(id int64, region string, forceGlobal bool) gatewayprovider.ExecutionProvider {
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Priority: int(id),
			Credentials: map[string]any{
				"aws_region": region, "auth_mode": "sigv4",
				"aws_access_key_id": "test-akid", "aws_secret_access_key": "test-secret",
			},
		},
	}
	if forceGlobal {
		provider.Record.Credentials["aws_force_global"] = "true"
	}
	return provider
}

// TestBedrockRegionRouting_SchedulerAndDiagnosisAgree supplies an invalid
// regional provider and optional valid backup under both load modes.
// Diagnostics and selection agree on model support, and an incompatible sticky
// target cannot win.
func TestBedrockRegionRouting_SchedulerAndDiagnosisAgree(t *testing.T) {
	groupID := int64(5200)
	invalid := newBedrockRoutingTestProvider(1, "ap-northeast-1", false)
	valid := newBedrockRoutingTestProvider(2, "us-east-1", false)
	for _, loadBatchEnabled := range []bool{false, true} {
		for _, withValid := range []bool{false, true} {
			name := "全部无效"
			if withValid {
				name = "存在有效提供商"
			}
			if loadBatchEnabled {
				name += "批量负载"
			}
			t.Run(name, func(t *testing.T) {
				providers := []gatewayprovider.ExecutionProvider{invalid}
				if withValid {
					providers = append(providers, valid)
				}
				repo := &mockProviderRepoForPlatform{providers: providers, providersByID: map[int64]*gatewayprovider.ExecutionProvider{}}
				for i := range repo.providers {
					repo.providers[i].Record.ProviderGroups = []providercore.GroupMembership{{ProviderID: repo.providers[i].Record.ID, GroupID: groupID}}
					repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
				}
				group := &routing.Group{ID: groupID, Status: billing.StatusActive, Hydrated: true}
				cfg := testConfig()
				cfg.Scheduling.LoadBatch.Enabled = loadBatchEnabled
				gateway := newGenericSelectionForTest(GenericDependencies{
					Reads: Reads{
						Providers: repo,

						Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
					},
					Shared: Shared{
						Cache:       &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 1}},
						Concurrency: schedulercore.NewConcurrencyService(&mockConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
					},
				}, cfg)

				diagnosis := gatewayprovider.NewModelAvailability(selectionAvailabilityFixture{repo}, nil, false).DiagnoseGeneral(context.Background(), &groupID, "claude-sonnet-5", capability.PlatformAnthropic)
				require.True(t, diagnosis.HasProvidersInPool)
				require.Equal(t, withValid, diagnosis.HasModelSupport)
				selected, err := gateway.SelectProviderWithLoadAwareness(context.Background(), &groupID, "sticky", "claude-sonnet-5", nil)
				if withValid {
					require.NoError(t, err)
					require.Equal(t, valid.Record.ID, selected.Provider.Record.ID)
					if selected.ReleaseFunc != nil {
						selected.ReleaseFunc()
					}
				} else {
					require.Error(t, err)
					require.Nil(t, selected)
				}
			})
		}
	}
}

// ListModelAvailabilityCandidates converts the platform fixture's filtered
// candidates to provider record values. It passes group, platform, and
// include-grouped inputs through, with the fixture's read error accompanying
// the converted result.
func (s selectionAvailabilityFixture) ListModelAvailabilityCandidates(ctx context.Context, group *int64, platforms []string, all bool) ([]providercore.Record, error) {
	values, err := s.availabilityRecords(ctx, group, platforms, all)
	out := make([]providercore.Record, len(values))
	for i := range values {
		out[i] = values[i].Record
	}
	return out, err
}

// TestModelAvailabilityDiagnosisAcceptsPricingConfigAlias maps a client alias
// through a group and provider to an allowed model. Persistent-model diagnosis
// finds support using the supplied group policy and final provider allowlist.
func TestModelAvailabilityDiagnosisAcceptsPricingConfigAlias(t *testing.T) {
	groupID := int64(4203)
	pricingConfig := routingtestkit.Configuration{
		ID:           74,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"client-alias": "group-model"},
	}
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 75,
			Platform:    capability.PlatformOpenAI,
			Status:      billing.StatusActive,
			Schedulable: true,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"group-model": "upstream-model"},
				"model_whitelist": []any{"upstream-model"},
			},
		},
	}
	repo := schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: repo},
		Shared: Shared{GroupPolicies: routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig)},
	}, nil)

	diagnosis := gatewayprovider.NewModelAvailability(gatewaytestkit.AvailabilityStore{Source: repo}, svc.groupPolicies, true).DiagnoseCompatible(context.Background(), &groupID, "client-alias", capability.PlatformOpenAI)
	require.True(t, diagnosis.HasProvidersInPool)
	require.True(t, diagnosis.HasModelSupport)
}

// TestOpenAIHTTPPassthroughKeepsExplicitModelScope supplies a passthrough
// provider whose final mapped model misses its allowlist. Eligibility, picker
// checks, and typed rejection observe the scope; the separate diagnosis call
// returns no support with its missing group input.
func TestOpenAIHTTPPassthroughKeepsExplicitModelScope(t *testing.T) {
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 76,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
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

	require.False(t, gatewayprovider.ExecutionModelPolicy(&provider).SupportsCompatibleRouting(plainCtx, "client-model"))
	require.False(t, gatewayprovider.CompatibleProviderEligible(plainCtx, &provider, capability.PlatformOpenAI, "client-model", false, ""))

	scheduler := &compatiblePicker{service: newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{},

		Shared: Shared{},
	}, nil), stats: schedulercore.NewRuntimeStats(time.Now)}
	req := schedulercore.PlatformSelectionInput{Platform: capability.PlatformOpenAI, RequestedModel: "client-model", RoutingModel: "client-model"}
	require.False(t, scheduler.isProviderRequestCompatible(plainCtx, &provider, req))

	plainErr := noAvailableOpenAISelectionErrorForRoutingWithDetails(plainCtx, "client-model", "client-model", false, "", []gatewayprovider.ExecutionProvider{provider})
	var modelErr *routing.GroupModelUnsupportedError
	require.True(t, errors.As(plainErr, &modelErr))

	repo := schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{provider}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{Providers: repo}}, nil)

	require.False(t, gatewayprovider.NewModelAvailability(gatewaytestkit.AvailabilityStore{Source: repo}, svc.groupPolicies, true).DiagnoseCompatibleRouting(plainCtx, nil, "client-model", capability.PlatformOpenAI).HasModelSupport)
}
