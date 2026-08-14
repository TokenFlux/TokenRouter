package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

// grokModelStateAccountRepo 记录 Grok 模型级状态，避免测试依赖真实存储库。
type grokModelStateAccountRepo struct {
	AccountRepository
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

// TestGrokAccountModelMappingRemainsExplicit 验证平台内置别名不会重新混入账号配置。
func TestGrokAccountModelMappingRemainsExplicit(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
		want        map[string]string
	}{
		{name: "missing credentials"},
		{name: "missing mapping", credentials: map[string]any{}},
		{name: "empty mapping", credentials: map[string]any{"model_mapping": map[string]any{}}},
		{name: "invalid mapping", credentials: map[string]any{"model_mapping": map[string]any{"grok": 45}}},
		{
			name: "explicit mapping is preserved",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"grok":         "grok-4.3",
					"client-alias": "grok-latest",
				},
			},
			want: map[string]string{
				"grok":         "grok-4.3",
				"client-alias": "grok-latest",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := &Account{Platform: PlatformGrok, Credentials: test.credentials}
			require.Equal(t, test.want, account.GetModelMapping())
		})
	}
}

// TestGrokWhitelistRunsBeforeBuiltinNormalization 验证白名单只检查账号映射后的模型。
func TestGrokWhitelistRunsBeforeBuiltinNormalization(t *testing.T) {
	unrestricted := &Account{Platform: PlatformGrok, Credentials: map[string]any{}}
	require.True(t, unrestricted.IsModelSupported("custom-grok-model"))
	require.True(t, unrestricted.IsModelSupported("grok"))

	strict := &Account{
		Platform: PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []any{"grok-4.5"},
		},
	}
	require.True(t, strict.IsModelSupported("grok-4.5"))
	require.False(t, strict.IsModelSupported("grok"))

	mapped := &Account{
		Platform: PlatformGrok,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"grok": "grok-4.5"},
			"model_whitelist": []any{"grok-4.5"},
		},
	}
	require.True(t, mapped.IsModelSupported("grok"))

	legacy := &Account{
		Platform: PlatformGrok,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
		},
	}
	require.True(t, legacy.IsModelSupported("grok-4.5"))
	require.False(t, legacy.IsModelSupported("grok"))
}

// TestGrokFinalUpstreamModelNormalization 验证 OAuth 和 API Key 共用 Grok 最终标准化。
func TestGrokFinalUpstreamModelNormalization(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		model   string
		want    string
	}{
		{
			name:    "oauth normalizes builtin alias",
			account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth},
			model:   "grok",
			want:    xai.DefaultResponsesModel,
		},
		{
			name:    "api key normalizes builtin alias",
			account: &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey},
			model:   " grok-latest ",
			want:    xai.DefaultResponsesModel,
		},
		{
			name:    "grok oauth does not use codex normalization",
			account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth},
			model:   "gpt-5.6",
			want:    "gpt-5.6",
		},
		{
			name:    "unknown model passes through",
			account: &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey},
			model:   "custom-grok-model",
			want:    "custom-grok-model",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, normalizeOpenAIModelForUpstream(test.account, test.model))
		})
	}
}

// TestGrokExplicitMappingPrecedesBuiltinNormalization 验证账号映射目标随后才执行平台别名解析。
func TestGrokExplicitMappingPrecedesBuiltinNormalization(t *testing.T) {
	direct := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"grok": "grok-4.3"},
		},
	}
	require.Equal(t, "grok-4.3", resolveOpenAIAccountUpstreamModelForRequest(direct, "grok", false, true))

	aliasTarget := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"client-alias": "grok-latest"},
		},
	}
	require.Equal(t, xai.DefaultResponsesModel, resolveOpenAIAccountUpstreamModelForRequest(aliasTarget, "client-alias", false, true))
	require.Equal(t, xai.DefaultResponsesModel, resolveAccountUpstreamModel(context.Background(), aliasTarget, "client-alias"))
}

// TestGrokRuntimeModelKeysUseFinalUpstreamID 验证封禁与限流状态不会按别名重复建键。
func TestGrokRuntimeModelKeysUseFinalUpstreamID(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"client-alias": "grok-latest",
				"grok-4.5":     "grok-4.3",
			},
		},
	}

	require.Equal(t, xai.DefaultResponsesModel, canonicalOpenAIAccountSchedulingModel(account, "grok"))
	require.Equal(t, xai.DefaultResponsesModel, canonicalOpenAIAccountSchedulingModel(account, "client-alias"))
	require.Equal(t, []string{xai.DefaultResponsesModel}, account.modelRateLimitKeysForRequest(context.Background(), "grok"))
	require.Equal(t, xai.DefaultResponsesModel, modelRateLimitKeyForUpstreamModelNotFound(context.Background(), account, "grok"))
	// 状态处理接收最终上游模型后不得再次命中 grok-4.5 -> grok-4.3。
	require.Equal(t, xai.DefaultResponsesModel, modelRateLimitKeyForUpstreamModelNotFound(context.Background(), account, xai.DefaultResponsesModel))
}

// TestGrokModelNotFoundWritesFinalUpstreamID 验证 Grok 默认错误链路会写入最终上游模型键。
func TestGrokModelNotFoundWritesFinalUpstreamID(t *testing.T) {
	repo := &grokModelStateAccountRepo{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := &Account{
		ID:       4511,
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"client-alias": "grok-latest",
				"grok-4.5":     "grok-4.3",
			},
		},
	}
	accountMappedModel := account.GetMappedModel("client-alias")
	require.Equal(t, "grok-latest", accountMappedModel)

	decision := svc.applyGrokAccountUpstreamError(
		context.Background(),
		account,
		http.StatusNotFound,
		nil,
		[]byte(`{"error":{"code":"model_not_found","message":"model not found"}}`),
		accountMappedModel,
	)

	require.True(t, decision.StopScheduling)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, xai.DefaultResponsesModel, repo.modelRateLimitCalls[0].scope)
	require.Equal(t, upstreamModelNotFoundReason, repo.modelRateLimitCalls[0].reason)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

// TestGrokTransientErrorBlocksOnlyFinalModel 验证 API Key 的连续瞬态错误只冷却最终模型。
func TestGrokTransientErrorBlocksOnlyFinalModel(t *testing.T) {
	repo := &grokModelStateAccountRepo{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := &Account{
		ID:       4512,
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"client-alias": "grok-latest",
				"grok-4.5":     "grok-4.3",
			},
		},
	}
	canonicalModel := normalizeOpenAIModelForUpstream(account, account.GetMappedModel("client-alias"))
	body := []byte(`{"error":{"message":"temporary upstream failure"}}`)

	first := svc.applyGrokAccountUpstreamError(context.Background(), account, http.StatusBadGateway, nil, body, canonicalModel)
	second := svc.applyGrokAccountUpstreamError(context.Background(), account, http.StatusBadGateway, nil, body, canonicalModel)

	require.False(t, first.StopScheduling)
	require.False(t, second.StopScheduling)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "client-alias"))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "grok-4.3"))
	require.Empty(t, repo.modelRateLimitCalls)
}

// TestGrokRequestableModelsExcludeBuiltinAliases 验证默认目录与内置别名表保持独立。
func TestGrokRequestableModelsExcludeBuiltinAliases(t *testing.T) {
	groupID := int64(4510)
	account := Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{}}
	service := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
	}

	result := service.ResolveRequestableModels(context.Background(), &groupID, PlatformGrok)
	require.Equal(t, xai.DefaultModelIDs(), RequestableModelIDs(result.Models))
	require.NotContains(t, RequestableModelIDs(result.Models), "grok")
	require.NotContains(t, RequestableModelIDs(result.Models), "grok-latest")

	account.Credentials["model_mapping"] = map[string]any{"grok": "grok-4.3"}
	service.accountRepo = &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}}
	result = service.ResolveRequestableModels(context.Background(), &groupID, PlatformGrok)
	require.Contains(t, RequestableModelIDs(result.Models), "grok")
}

// TestGrokMarketplaceDefaultsReuseXAICatalog 验证模型广场直接复用 xAI 默认目录及展示名。
func TestGrokMarketplaceDefaultsReuseXAICatalog(t *testing.T) {
	definitions := defaultMarketplaceModelDefs(PlatformGrok)
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	require.Equal(t, xai.DefaultModelIDs(), ids)
	require.NotContains(t, ids, "grok")

	displayNames := marketplaceDisplayNameLookup(PlatformGrok)
	for _, model := range xai.DefaultModels() {
		require.Equal(t, model.DisplayName, displayNames[model.ID])
	}
}

// TestGrokCountTokensUsesCanonicalModel 验证 count-tokens 转换记录映射模型并发送最终模型。
func TestGrokCountTokensUsesCanonicalModel(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"claude-sonnet-4-5": "grok-latest"},
		},
	}
	prepared, err := prepareOpenAIInputTokensCountRequest(
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
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{}}
	require.Equal(t, xai.DefaultResponsesModel, resolveGrokWSUpstreamModel(account, []byte(`{"model":"grok"}`), "grok"))
	require.Equal(t, xai.DefaultResponsesModel, resolveGrokWSUpstreamModel(account, []byte(`{"model":"grok"}`), ""))

	billingModel, upstreamModel := resolveGrokWSModels(account, []byte(`{"model":"grok"}`), "")
	require.Equal(t, "grok", billingModel)
	require.Equal(t, xai.DefaultResponsesModel, upstreamModel)

	billingModel, upstreamModel = resolveGrokWSModels(account, []byte(`{"input":"hello"}`), "")
	require.Empty(t, billingModel)
	require.Equal(t, xai.DefaultResponsesModel, upstreamModel)
}
