package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

// grokModelStateAccountRepo 记录 Grok 模型级状态，避免测试依赖真实存储库。
type grokModelStateAccountRepo struct {
	gatewayprovider.ExecutionAccountStore

	modelRateLimitCalls []grokModelRateLimitCall
}

// grokModelRateLimitCall 保存一次模型限流写入的关键字段。
type grokModelRateLimitCall struct {
	accountID int64
	scope     string
	resetAt   time.Time
	reason    string
}

// SetModelRateLimit 记录 Grok 模型级状态写入，供规范模型键回归测试断言。
func (r *grokModelStateAccountRepo) SetModelRateLimit(_ context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	call := grokModelRateLimitCall{accountID: id, scope: scope, resetAt: resetAt}
	if len(reason) > 0 {
		call.reason = reason[0]
	}
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, call)
	return nil
}

func TestGrokFinalUpstreamModelNormalization(t *testing.T) {
	tests := []struct {
		name    string
		account *gatewayprovider.ExecutionAccount
		model   string
		want    string
	}{
		{
			name:    "oauth normalizes builtin alias",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
			model:   "grok",
			want:    xai.DefaultResponsesModel,
		},
		{
			name:    "api key normalizes builtin alias",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}},
			model:   " grok-latest ",
			want:    xai.DefaultResponsesModel,
		},
		{
			name:    "grok oauth does not use codex normalization",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
			model:   "gpt-5.6",
			want:    "gpt-5.6",
		},
		{
			name:    "unknown model passes through",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}},
			model:   "custom-grok-model",
			want:    "custom-grok-model",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, gatewayprovider.ExecutionModelPolicy(test.account).NormalizeOpenAI(test.model))
		})
	}
}

// TestGrokExplicitMappingPrecedesBuiltinNormalization 验证账号映射目标随后才执行平台别名解析。
func TestGrokExplicitMappingPrecedesBuiltinNormalization(t *testing.T) {
	direct := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
			Type: capability.AccountTypeOAuth,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"grok": "grok-4.3"},
			},
		},
	}
	require.Equal(t, "grok-4.3", gatewayprovider.ExecutionModelPolicy(direct).OpenAIUpstream("grok", false, true))

	aliasTarget := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
			Type: capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"client-alias": "grok-latest"},
			},
		},
	}
	require.Equal(t, xai.DefaultResponsesModel, gatewayprovider.ExecutionModelPolicy(aliasTarget).OpenAIUpstream("client-alias", false, true))
	require.Equal(t, xai.DefaultResponsesModel, gatewayprovider.ExecutionModelPolicy(aliasTarget).UpstreamModel(context.Background(), "client-alias"))
}

// TestGrokRuntimeModelKeysUseFinalUpstreamID 验证封禁与限流状态不会按别名重复建键。
func TestGrokRuntimeModelKeysUseFinalUpstreamID(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
			Type: capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"client-alias": "grok-latest",
					"grok-4.5":     "grok-4.3",
				},
			},
		},
	}

	require.Equal(t, xai.DefaultResponsesModel, gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("grok"))
	require.Equal(t, xai.DefaultResponsesModel, gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel("client-alias"))
	require.Equal(t, []string{xai.DefaultResponsesModel}, gatewayprovider.ExecutionModelPolicy(account).LimitKeys(context.Background(), "grok"))
	require.Equal(t, xai.DefaultResponsesModel, (&accountprovider.ModelHealth{CodexRules: gatewayprovider.CodexModelRules()}).LimitKey(gatewayprovider.ExecutionRecord(account), "grok", nil))
	// 状态处理接收最终上游模型后不得再次命中 grok-4.5 -> grok-4.3。
	require.Equal(t, xai.DefaultResponsesModel, (&accountprovider.ModelHealth{CodexRules: gatewayprovider.CodexModelRules()}).LimitKey(gatewayprovider.ExecutionRecord(account), xai.DefaultResponsesModel, nil))
}

// TestGrokModelNotFoundWritesFinalUpstreamID 验证 Grok 默认错误链路会写入最终上游模型键。
func TestGrokModelNotFoundWritesFinalUpstreamID(t *testing.T) {
	repo := &grokModelStateAccountRepo{}
	svc := newWSFixture(wsFixtureInputs{health: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, ID: 4511,
			Platform: capability.PlatformGrok,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"client-alias": "grok-latest",
					"grok-4.5":     "grok-4.3",
				},
			},
		},
	}
	accountMappedModel := gatewayprovider.ExecutionModelPolicy(account).Mapped("client-alias")
	require.Equal(t, "grok-latest", accountMappedModel)

	decision := gatewayprovider.ApplyGrokExecutionHealth(context.Background(), svc.Output.GrokHealth, account, http.StatusNotFound, nil, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`), "", accountMappedModel)

	require.True(t, decision.StopScheduling)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, xai.DefaultResponsesModel, repo.modelRateLimitCalls[0].scope)
	require.Equal(t, accountcore.ModelNotFoundReason, repo.modelRateLimitCalls[0].reason)
	require.False(t, wsFixtureAccountBlocked(svc, account))
}

// TestGrokTransientErrorBlocksOnlyFinalModel 验证 API Key 的连续瞬态错误只冷却最终模型。
func TestGrokTransientErrorBlocksOnlyFinalModel(t *testing.T) {
	repo := &grokModelStateAccountRepo{}
	svc := newWSFixture(wsFixtureInputs{health: newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{}, nil)})
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, ID: 4512,
			Platform: capability.PlatformGrok,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"client-alias": "grok-latest",
					"grok-4.5":     "grok-4.3",
				},
			},
		},
	}
	canonicalModel := gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(gatewayprovider.ExecutionModelPolicy(account).Mapped("client-alias"))
	body := []byte(`{"error":{"message":"temporary upstream failure"}}`)

	first := gatewayprovider.ApplyGrokExecutionHealth(context.Background(), svc.Output.GrokHealth, account, http.StatusBadGateway, nil, body, "", canonicalModel)
	second := gatewayprovider.ApplyGrokExecutionHealth(context.Background(), svc.Output.GrokHealth, account, http.StatusBadGateway, nil, body, "", canonicalModel)

	require.False(t, first.StopScheduling)
	require.False(t, second.StopScheduling)
	require.False(t, wsFixtureAccountBlocked(svc, account))
	require.True(t, wsFixtureModelBlocked(svc, account, "client-alias"))
	require.False(t, wsFixtureModelBlocked(svc, account, "grok-4.3"))
	require.Empty(t, repo.modelRateLimitCalls)
}

// TestGrokCountTokensUsesCanonicalModel 验证默认目录与内置别名表保持独立。

// TestGrokCountTokensUsesCanonicalModel 验证 count-tokens 转换记录映射模型并发送最终模型。
func TestGrokCountTokensUsesCanonicalModel(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
			Type: capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"claude-sonnet-4-5": "grok-latest"},
			},
		},
	}
	prepared, err := gatewayprovider.PrepareAnthropicInputTokens(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`),
		account,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, "grok-latest", prepared.BillingModel)
	require.Equal(t, xai.DefaultResponsesModel, prepared.UpstreamModel)
	require.Equal(t, xai.DefaultResponsesModel, prepared.Request.Model)
}

// TestGrokWSModelUsesCanonicalID 验证 WebSocket HTTP bridge 使用相同的最终标准化入口。
func TestGrokWSModelUsesCanonicalID(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{}}}
	require.Equal(t, xai.DefaultResponsesModel, resolveGrokWSUpstreamModel(account, []byte(`{"model":"grok"}`), "grok"))
	require.Equal(t, xai.DefaultResponsesModel, resolveGrokWSUpstreamModel(account, []byte(`{"model":"grok"}`), ""))

	billingModel, upstreamModel := resolveGrokWSModels(account, []byte(`{"model":"grok"}`), "")
	require.Equal(t, "grok", billingModel)
	require.Equal(t, xai.DefaultResponsesModel, upstreamModel)

	billingModel, upstreamModel = resolveGrokWSModels(account, []byte(`{"input":"hello"}`), "")
	require.Empty(t, billingModel)
	require.Equal(t, xai.DefaultResponsesModel, upstreamModel)
}
