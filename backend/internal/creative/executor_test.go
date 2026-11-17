package creative

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// Chained mapping fixtures distinguish the public request name, group mapping,
// and final upstream image name. Selection and release counters show which
// restriction stage rejects a route, while executing a prepared target checks
// that later mapping mutation cannot change its fixed model.

// TestCreativeExecutorGroupPolicyUsesFinalModel prepares chained group and
// provider mappings for each image platform. Executing after the mapping
// changes still sends the prepared final model and resolves it only once.
func TestCreativeExecutorGroupPolicyUsesFinalModel(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			final := map[string]string{PlatformOpenAI: "gpt-image-2", PlatformGemini: "gemini-3-pro-image", PlatformGrok: "grok-imagine-image-2.0"}[platform]
			group := &ExecutionGroup{RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceUpstream,
				ModelMapping:  map[string]string{"draw": "provider-alias", "provider-alias": "forbidden-group-hop"},
				AllowedModels: []string{final},
			}}
			mapping := map[string]string{"provider-alias": final, final: "forbidden-provider-hop"}
			calls := 0
			selectProvider := func(_ context.Context, run CreativeRun) (*Selection, error) {
				require.Equal(t, "draw", run.Model, "调度器仍从原始请求模型解析分组规则")
				return &Selection{
					ResolveAttributes: func(_ context.Context, model string) (modelrules.Resolution, error) {
						return testkit.OfflineView().Resolve(modelrules.Target{Model: model}, modelcatalog.Attributes{}), nil
					},
					ProviderID: 55, Platform: platform, Acquired: true,
					ResolveModel: func(_ context.Context, model string) string {
						require.Equal(t, "provider-alias", model)
						calls++
						return mapping[model]
					},
					Execute: func(_ context.Context, _ CreativeRun, _ CreativeRunPayload, model string) ([]CreativeOutput, error) {
						require.Equal(t, final, model, "发送的必须是通过上游白名单检查的模型")
						return []CreativeOutput{{Mime: "image/png", Bytes: []byte("image")}}, nil
					},
				}, nil
			}
			executor := &Executor{
				Group:  func(context.Context, int64) (*ExecutionGroup, error) { return group, nil },
				OpenAI: selectProvider, Gemini: selectProvider, Grok: selectProvider,
			}
			run := CreativeRun{Platform: platform, GroupID: 12, Model: "draw", Operation: CreativeOperationGenerate}
			prepared, err := executor.Prepare(context.Background(), run)
			require.NoError(t, err)
			require.Equal(t, final, prepared.UpstreamModel)
			require.Equal(t, platform, prepared.Platform)
			// Change the provider mapping after preparation to
			// detect any remapping during
			// execution; the target callback still expects the
			// final model already selected.
			mapping["provider-alias"] = "changed-after-prepare"
			_, err = prepared.Target.Execute(context.Background(), run, CreativeRunPayload{})
			require.NoError(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

// TestCreativeExecutorGroupPolicyRestrictionStages exercises requested,
// group-mapped, upstream, empty-list, and disabled policies during
// preparation. Rejected upstream routes release the acquired slot, while
// earlier restriction failures skip provider selection.
func TestCreativeExecutorGroupPolicyRestrictionStages(t *testing.T) {
	for _, tc := range []struct {
		name, source string   // name labels the case; source selects restriction stage
		allowed      []string // configured group model allowlist
		blocked      bool     // expected route rejection flag
		disabled     bool     // disabled policy draft flag
	}{
		{name: "请求阶段允许", source: routing.BillingModelSourceRequested, allowed: []string{"GPT-IMAGE-1"}},
		{name: "请求阶段拒绝", source: routing.BillingModelSourceRequested, allowed: []string{"gpt-image-2"}, blocked: true},
		{name: "分组阶段允许", source: routing.BillingModelSourceGroupMapped, allowed: []string{"group-*"}},
		{name: "分组阶段空白名单", source: routing.BillingModelSourceGroupMapped, blocked: true},
		{name: "上游阶段允许", source: routing.BillingModelSourceUpstream, allowed: []string{"gpt-image-2"}},
		{name: "上游阶段拒绝", source: routing.BillingModelSourceUpstream, allowed: []string{"group-model"}, blocked: true},
		{name: "上游阶段空白名单", source: routing.BillingModelSourceUpstream, blocked: true},
		{name: "关闭策略保留草稿", source: routing.BillingModelSourceUpstream, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, released := 0, 0
			group := &ExecutionGroup{RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: !tc.disabled, RestrictModels: true, RestrictionModelSource: tc.source,
				ModelMapping:  map[string]string{"gpt-image-1": "group-model"},
				AllowedModels: tc.allowed,
			}}
			executor := &Executor{
				Group: func(context.Context, int64) (*ExecutionGroup, error) { return group, nil },
				OpenAI: func(context.Context, CreativeRun) (*Selection, error) {
					selected++
					return &Selection{
						ResolveAttributes: func(_ context.Context, model string) (modelrules.Resolution, error) {
							return testkit.OfflineView().Resolve(modelrules.Target{Model: model}, modelcatalog.Attributes{}), nil
						},
						Acquired: true, Platform: PlatformOpenAI,
						Release: func() { released++ },
						ResolveModel: func(_ context.Context, model string) string {
							if tc.disabled {
								require.Equal(t, "gpt-image-1", model)
								return model
							}
							require.Equal(t, "group-model", model)
							return "gpt-image-2"
						},
					}, nil
				},
			}
			prepared, err := executor.Prepare(context.Background(), CreativeRun{GroupID: 12, Model: "gpt-image-1"})
			if tc.blocked {
				require.Error(t, err)
				require.False(t, IsRetryableCreativeError(err))
				require.Nil(t, prepared)
				if tc.source == routing.BillingModelSourceUpstream {
					require.Equal(t, 1, selected)
					require.Equal(t, 1, released)
				} else {
					require.Zero(t, selected)
				}
				return
			}
			require.NoError(t, err)
			require.Zero(t, released)
			prepared.ReleaseFunc()
			require.Equal(t, 1, released)
		})
	}
}

// TestCreativeExecutorPolicyReadFailureStopsSelection makes the current group
// reader fail before admission. Preparation returns a policy-unavailable error
// and the provider-selection callback fails the test if invoked.
func TestCreativeExecutorPolicyReadFailureStopsSelection(t *testing.T) {
	executor := &Executor{Group: func(context.Context, int64) (*ExecutionGroup, error) {
		return nil, errors.New("group store unavailable")
	}, OpenAI: func(context.Context, CreativeRun) (*Selection, error) {
		t.Fatal("无法取得分组策略时不能继续选提供商")
		return nil, nil
	}}
	_, err := executor.Prepare(context.Background(), CreativeRun{GroupID: 12, Model: "gpt-image-1"})
	require.ErrorContains(t, err, "policy is unavailable")
}
