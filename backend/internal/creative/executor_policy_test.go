package creative

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

// 同时设置两跳陷阱，确保实际出站模型只经过一次分组和账号映射。
func TestCreativeExecutorGroupPolicyUsesFinalModel(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			final := map[string]string{PlatformOpenAI: "gpt-image-2", PlatformGemini: "gemini-3-pro-image", PlatformGrok: "grok-imagine-image-2.0"}[platform]
			group := &ExecutionGroup{Platform: platform, RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceUpstream,
				ModelMapping:  map[string]map[string]string{platform: {"draw": "account-alias", "account-alias": "forbidden-group-hop"}},
				AllowedModels: map[string][]string{platform: {final}},
			}}
			mapping := map[string]string{"account-alias": final, final: "forbidden-account-hop"}
			calls := 0
			selectAccount := func(_ context.Context, run CreativeRun) (*Selection, error) {
				require.Equal(t, "draw", run.Model, "调度器仍从原始请求模型解析分组规则")
				return &Selection{
					AccountID: 55, Platform: platform, Acquired: true,
					ResolveModel: func(_ context.Context, model string) string {
						require.Equal(t, "account-alias", model)
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
				OpenAI: selectAccount, Gemini: selectAccount, Grok: selectAccount,
			}
			run := CreativeRun{GroupID: 12, Model: "draw", Operation: CreativeOperationGenerate}
			prepared, err := executor.Prepare(context.Background(), run)
			require.NoError(t, err)
			require.Equal(t, final, prepared.UpstreamModel)
			// 准备后固定执行模型，不因其他请求修改共享规则而重新映射。
			mapping["account-alias"] = "changed-after-prepare"
			_, err = prepared.Target.Execute(context.Background(), run, CreativeRunPayload{})
			require.NoError(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

// 执行器必须独立复核白名单，覆盖排队后策略变更及已占槽时的释放。
func TestCreativeExecutorGroupPolicyRestrictionStages(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		allowed      []string
		blocked      bool
		disabled     bool
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
			group := &ExecutionGroup{Platform: PlatformOpenAI, RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: !tc.disabled, RestrictModels: true, RestrictionModelSource: tc.source,
				ModelMapping:  map[string]map[string]string{PlatformOpenAI: {"gpt-image-1": "group-model"}},
				AllowedModels: map[string][]string{PlatformOpenAI: tc.allowed},
			}}
			executor := &Executor{
				Group: func(context.Context, int64) (*ExecutionGroup, error) { return group, nil },
				OpenAI: func(context.Context, CreativeRun) (*Selection, error) {
					selected++
					return &Selection{
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

func TestCreativeExecutorPolicyReadFailureStopsSelection(t *testing.T) {
	reads := 0
	executor := &Executor{Group: func(context.Context, int64) (*ExecutionGroup, error) {
		reads++
		if reads == 1 {
			return &ExecutionGroup{Platform: PlatformOpenAI}, nil
		}
		return nil, errors.New("group store unavailable")
	}, OpenAI: func(context.Context, CreativeRun) (*Selection, error) {
		t.Fatal("无法取得分组策略时不能继续选账号")
		return nil, nil
	}}
	_, err := executor.Prepare(context.Background(), CreativeRun{GroupID: 12, Model: "gpt-image-1"})
	require.ErrorContains(t, err, "policy is unavailable")
}
