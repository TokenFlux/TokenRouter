package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/claude"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func fastModeTestContext(policy, model string) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.APIKeyFastModePolicy, policy)
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 11, Platform: PlatformOpenAI})
	return context.WithValue(ctx, ctxkey.Model, model)
}

func fastModeTestResolver() *ModelPricingResolver {
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-5.5": {
			InputCostPerToken:     5e-6,
			OutputCostPerToken:    30e-6,
			SupportsServiceTier:   true,
			SupportsPromptCaching: true,
		},
		"claude-opus-4-8": {
			InputCostPerToken:     5e-6,
			OutputCostPerToken:    25e-6,
			SupportsServiceTier:   true,
			SupportsPromptCaching: true,
		},
	}}
	billing := NewBillingService(&config.Config{}, pricing)
	return NewModelPricingResolver(nil, billing)
}

func TestOpenAIAPIKeyFastModeForceOnAndOff(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	svc.resolver = fastModeTestResolver()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	forceOnCtx := fastModeTestContext(APIKeyFastModePolicyForceOn, "gpt-5.5")
	updated, err := svc.applyOpenAIFastPolicyToBody(forceOnCtx, account, "gpt-5.5", []byte(`{"model":"gpt-5.5"}`))
	require.NoError(t, err)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	forceOffCtx := fastModeTestContext(APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, err = svc.applyOpenAIFastPolicyToBody(forceOffCtx, account, "gpt-5.5", []byte(`{"model":"gpt-5.5","service_tier":"priority"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

func TestOpenAIAPIKeyFastModeIgnoresUnsupportedModel(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	svc.resolver = fastModeTestResolver()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOn, "unknown-provider-model")

	updated, err := svc.applyOpenAIFastPolicyToBody(ctx, account, "unknown-provider-model", []byte(`{"model":"unknown-provider-model"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// 强制关闭是请求净化策略，不应受模型定价能力元数据影响。
func TestOpenAIAPIKeyFastModeForceOffIgnoresMissingCapabilityMetadata(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	svc.resolver = fastModeTestResolver()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOff, "unknown-provider-model")

	updated, err := svc.applyOpenAIFastPolicyToBody(ctx, account, "unknown-provider-model", []byte(`{"model":"unknown-provider-model","service_tier":"priority"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	updated, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, "unknown-provider-model", []byte(`{"type":"response.create","model":"unknown-provider-model","service_tier":"priority"}`))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// 强制关闭只删除 Fast tier，不能改变客户端选择的其它官方服务层级。
func TestOpenAIAPIKeyFastModeForceOffPreservesNonFastTiers(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOff, "unknown-provider-model")

	for _, tier := range []string{"flex", "auto", "default", "scale"} {
		t.Run(tier, func(t *testing.T) {
			body := []byte(`{"model":"unknown-provider-model","service_tier":"` + tier + `"}`)
			updated, err := svc.applyOpenAIFastPolicyToBody(ctx, account, "unknown-provider-model", body)
			require.NoError(t, err)
			require.Equal(t, tier, gjson.GetBytes(updated, "service_tier").String())

			wsBody := []byte(`{"type":"response.create","model":"unknown-provider-model","service_tier":"` + tier + `"}`)
			updated, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, "unknown-provider-model", wsBody)
			require.NoError(t, err)
			require.Nil(t, blocked)
			require.Equal(t, tier, gjson.GetBytes(updated, "service_tier").String())
		})
	}
}

// 客户端别名 fast 归一化后仍属于 priority，强制关闭必须将其删除。
func TestOpenAIAPIKeyFastModeForceOffRemovesFastAlias(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOff, "unknown-provider-model")

	updated, err := svc.applyOpenAIFastPolicyToBody(ctx, account, "unknown-provider-model", []byte(`{"model":"unknown-provider-model","service_tier":"fast"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

func TestOpenAIAPIKeyFastModeCannotBypassSystemPolicy(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, openAIFastFilterPriorityPolicy())
	svc.resolver = fastModeTestResolver()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOn, "gpt-5.5")

	updated, err := svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.5", []byte(`{"model":"gpt-5.5"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	blockSvc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: OpenAIFastTierPriority,
		Action:      BetaPolicyActionBlock,
		Scope:       BetaPolicyScopeAll,
	}}})
	blockSvc.resolver = fastModeTestResolver()
	_, err = blockSvc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.5", []byte(`{"model":"gpt-5.5"}`))
	var blocked *OpenAIFastBlockedError
	require.ErrorAs(t, err, &blocked)

	// 系统强制 priority 命中原始 flex 后，单 Key force_off 不能删除它。
	svc = newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: OpenAIFastTierFlex,
		Action:      OpenAIFastPolicyActionForcePriority,
		Scope:       BetaPolicyScopeAll,
	}}})
	svc.resolver = fastModeTestResolver()
	ctx = fastModeTestContext(APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, err = svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.5", []byte(`{"model":"gpt-5.5","service_tier":"flex"}`))
	require.NoError(t, err)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())
}

func TestOpenAIAPIKeyFastModeAppliesToRealtimeFrames(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	svc.resolver = fastModeTestResolver()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	forceOnCtx := fastModeTestContext(APIKeyFastModePolicyForceOn, "gpt-5.5")
	updated, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(forceOnCtx, account, "gpt-5.5", []byte(`{"type":"response.create","model":"gpt-5.5"}`))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	forceOffCtx := fastModeTestContext(APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, blocked, err = svc.applyOpenAIFastPolicyToWSResponseCreate(forceOffCtx, account, "gpt-5.5", []byte(`{"type":"response.create","model":"gpt-5.5","service_tier":"priority"}`))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// WebSocket 每个 turn 都应读取最新策略，不能永久复用握手时的策略快照。
func TestOpenAIWSFastModePolicyContextRefreshesEachTurn(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	svc.resolver = fastModeTestResolver()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	baseCtx := fastModeTestContext(APIKeyFastModePolicyForceOn, "gpt-5.5")
	policies := map[int]string{
		1: APIKeyFastModePolicyForceOn,
		2: APIKeyFastModePolicyForceOff,
	}
	hooks := &OpenAIWSIngressHooks{
		ResolveFastModePolicy: func(turn int) string {
			return policies[turn]
		},
	}

	turnOneCtx := openAIWSFastModePolicyContext(baseCtx, hooks, 1)
	updated, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(turnOneCtx, account, "gpt-5.5", []byte(`{"type":"response.create","model":"gpt-5.5"}`))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	turnTwoCtx := openAIWSFastModePolicyContext(baseCtx, hooks, 2)
	updated, blocked, err = svc.applyOpenAIFastPolicyToWSResponseCreate(turnTwoCtx, account, "gpt-5.5", []byte(`{"type":"response.create","model":"gpt-5.5","service_tier":"priority"}`))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

func TestAPIKeyFastModeIgnoresUnsupportedProviderAdapters(t *testing.T) {
	openAISvc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	openAISvc.resolver = fastModeTestResolver()
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOn, "gpt-5.5")
	body, err := openAISvc.applyOpenAIFastPolicyToBody(ctx, &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}, "gpt-5.5", []byte(`{"model":"gpt-5.5"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())

	claudeSvc := &GatewayService{resolver: fastModeTestResolver()}
	body, headers, err := claudeSvc.applyClaudeAPIKeyFastMode(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}, "claude-opus-4-8", []byte(`{"model":"claude-opus-4-8"}`), http.Header{})
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "speed").Exists())
	require.Empty(t, getHeaderRaw(headers, "anthropic-beta"))
}

func TestClaudeAPIKeyFastModeWireEncoding(t *testing.T) {
	svc := &GatewayService{resolver: fastModeTestResolver()}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}

	forceOnCtx := fastModeTestContext(APIKeyFastModePolicyForceOn, "claude-opus-4-8")
	body, headers, err := svc.applyClaudeAPIKeyFastMode(forceOnCtx, account, "claude-opus-4-8", []byte(`{"model":"claude-opus-4-8"}`), http.Header{})
	require.NoError(t, err)
	require.Equal(t, "fast", gjson.GetBytes(body, "speed").String())
	require.True(t, containsBetaToken(getHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode))

	forceOffCtx := fastModeTestContext(APIKeyFastModePolicyForceOff, "claude-opus-4-8")
	setHeaderRaw(headers, "anthropic-beta", claude.BetaFastMode+",context-management-2025-06-27")
	body, headers, err = svc.applyClaudeAPIKeyFastMode(forceOffCtx, account, "claude-opus-4-8", body, headers)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "speed").Exists())
	require.False(t, containsBetaToken(getHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode))
	require.True(t, containsBetaToken(getHeaderRaw(headers, "anthropic-beta"), "context-management-2025-06-27"))
}

// Anthropic 直连的强制关闭应覆盖所有凭据类型，且不依赖定价解析器。
func TestClaudeAPIKeyFastModeForceOffIgnoresCapabilityAndCredentialType(t *testing.T) {
	svc := &GatewayService{}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOff, "claude-opus-4-8")

	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			headers := http.Header{}
			setHeaderRaw(headers, "anthropic-beta", claude.BetaFastMode+",context-management-2025-06-27")
			body, updatedHeaders, err := svc.applyClaudeAPIKeyFastMode(
				ctx,
				&Account{Platform: PlatformAnthropic, Type: accountType},
				"claude-opus-4-8",
				[]byte(`{"model":"claude-opus-4-8","speed":"fast"}`),
				headers,
			)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(body, "speed").Exists())
			require.False(t, containsBetaToken(getHeaderRaw(updatedHeaders, "anthropic-beta"), claude.BetaFastMode))
			require.True(t, containsBetaToken(getHeaderRaw(updatedHeaders, "anthropic-beta"), "context-management-2025-06-27"))
		})
	}
}

func TestClaudeAPIKeyFastModeCannotBypassSystemFilter(t *testing.T) {
	cfg := &config.Config{}
	svc := &GatewayService{
		cfg:      cfg,
		resolver: fastModeTestResolver(),
	}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	ctx := fastModeTestContext(APIKeyFastModePolicyForceOn, "claude-opus-4-8")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	// 模拟系统 Beta 策略已将 Claude Fast token 标记为过滤。
	c.Set(betaPolicyFilterSetKey, map[string]struct{}{claude.BetaFastMode: {}})

	req, wireBody, err := svc.buildUpstreamRequest(ctx, c, account, []byte(`{"model":"claude-opus-4-8","messages":[]}`), "test-key", "apikey", "claude-opus-4-8", false, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wireBody, "speed").Exists())
	require.False(t, containsBetaToken(getHeaderRaw(req.Header, "anthropic-beta"), claude.BetaFastMode))
}

func TestClaudeUsageSpeedDrivesFastBilling(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	svc := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	groupID := int64(11)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID}}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	base := &ForwardResult{Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 100}, Model: "claude-opus-4-8"}
	fast := *base
	fast.Usage.Speed = "fast"

	baseCost := svc.calculateTokenCost(context.Background(), base, apiKey, account, "claude-opus-4-8", "claude-opus-4-8", "", "", 1, nil)
	fastCost := svc.calculateTokenCost(context.Background(), &fast, apiKey, account, "claude-opus-4-8", "claude-opus-4-8", "", "", 1, nil)
	require.InDelta(t, baseCost.ActualCost*2, fastCost.ActualCost, 1e-12)
	require.Equal(t, OpenAIFastTierPriority, claudeUsageServiceTier(fast.Usage.Speed))
}

func TestClaudeUsageSpeedParsing(t *testing.T) {
	svc := &GatewayService{}
	usage := &ClaudeUsage{}
	svc.parseSSEUsage(`{"type":"message_start","message":{"usage":{"input_tokens":10,"speed":"fast"}}}`, usage)
	require.Equal(t, "fast", usage.Speed)

	parsed := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":10,"output_tokens":2,"speed":"standard"}}`))
	require.Equal(t, "standard", parsed.Speed)
}
