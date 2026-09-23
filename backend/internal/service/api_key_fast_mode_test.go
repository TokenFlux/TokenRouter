package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func fastModeTestContext(policy, model string) context.Context {
	ctx := apikey.WithFastModePolicy(context.Background(), policy)
	ctx = requeststate.WithGroup(ctx, &routing.Group{ID: 11, Platform: capability.PlatformOpenAI})
	return context.WithValue(ctx, telemetry.Model, model)
}

func fastModeTestResolver() *billingcore.PriceResolver {
	pricing := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*billingpricing.LiteLLMModelPricing{
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
	}})
	billing := NewBillingService(&config.Config{}, pricing)
	return billingtestkit.PriceResolver(nil, billing)
}

func TestOpenAIAPIKeyFastModeForceOnAndOff(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	svc.resolver = fastModeTestResolver()
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	forceOnCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.fastModeInput(forceOnCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	forceOffCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5","service_tier":"priority"}`), svc.fastModeInput(forceOffCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// TestOpenAIGroupFastForcesHTTPAndWS 验证没有客户端输入时，组级策略会同时注入 HTTP body 和 WS response.create 帧。
func TestOpenAIGroupFastForcesHTTPAndWS(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	group := &routing.Group{ID: 12, Platform: capability.PlatformOpenAI, Status: billingcore.StatusActive, Hydrated: true, ForceOpenAIFast: true}
	ctx := requeststate.WithGroup(context.Background(), group)

	body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.fastModeInput(ctx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(body, "service_tier").String())

	frame, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5"}`), "gpt-5.5", svc.fastModeInput(ctx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(frame, "service_tier").String())
}

// TestOpenAIGroupFastStillHonorsGlobalAndKeyPolicy 验证组级强制不会绕过全局过滤或 API Key ForceOff 策略。
func TestOpenAIGroupFastStillHonorsGlobalAndKeyPolicy(t *testing.T) {
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	group := &routing.Group{ID: 13, Platform: capability.PlatformOpenAI, Status: billingcore.StatusActive, Hydrated: true, ForceOpenAIFast: true}
	base := requeststate.WithGroup(context.Background(), group)

	filtered := newOpenAIGatewayServiceWithSettings(t, openAIFastFilterPriorityPolicy())
	body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), filtered.fastModeInput(base, account, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())

	forceOff := apikey.WithFastModePolicy(base, apikey.APIKeyFastModePolicyForceOff)
	passed := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	body, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), passed.fastModeInput(forceOff, account, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())
}

// TestOpenAIGroupFastRequiresTrustedOpenAIContext 防止不可信或非 OpenAI 上下文改变请求语义。
func TestOpenAIGroupFastRequiresTrustedOpenAIContext(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	for _, group := range []*routing.Group{
		{ID: 14, Platform: capability.PlatformOpenAI, Status: billingcore.StatusActive, ForceOpenAIFast: true},
		{ID: 15, Platform: capability.PlatformAnthropic, Status: billingcore.StatusActive, Hydrated: true, ForceOpenAIFast: true},
	} {
		ctx := requeststate.WithGroup(context.Background(), group)
		body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.fastModeInput(ctx, &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI}}, "gpt-5.5"))
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(body, "service_tier").Exists())
	}
}

func TestOpenAIAPIKeyFastModeIgnoresUnsupportedModel(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	svc.resolver = fastModeTestResolver()
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "unknown-provider-model")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"unknown-provider-model"}`), svc.fastModeInput(ctx, account, "unknown-provider-model"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// 强制关闭是请求净化策略，不应受模型定价能力元数据影响。
func TestOpenAIAPIKeyFastModeForceOffIgnoresMissingCapabilityMetadata(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	svc.resolver = fastModeTestResolver()
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "unknown-provider-model")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"unknown-provider-model","service_tier":"priority"}`), svc.fastModeInput(ctx, account, "unknown-provider-model"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	updated, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"unknown-provider-model","service_tier":"priority"}`), "unknown-provider-model", svc.fastModeInput(ctx, account, "unknown-provider-model"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// 强制关闭只删除 Fast tier，不能改变客户端选择的其它官方服务层级。
func TestOpenAIAPIKeyFastModeForceOffPreservesNonFastTiers(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "unknown-provider-model")

	for _, tier := range []string{"flex", "auto", "default", "scale"} {
		t.Run(tier, func(t *testing.T) {
			body := []byte(`{"model":"unknown-provider-model","service_tier":"` + tier + `"}`)
			updated, err := tierpolicy.ApplyBody(body, svc.fastModeInput(ctx, account, "unknown-provider-model"))
			require.NoError(t, err)
			require.Equal(t, tier, gjson.GetBytes(updated, "service_tier").String())

			wsBody := []byte(`{"type":"response.create","model":"unknown-provider-model","service_tier":"` + tier + `"}`)
			updated, blocked, err := gatewayws.ApplyServiceTierFrame(wsBody, "unknown-provider-model", svc.fastModeInput(ctx, account, "unknown-provider-model"))
			require.NoError(t, err)
			require.Nil(t, blocked)
			require.Equal(t, tier, gjson.GetBytes(updated, "service_tier").String())
		})
	}
}

// 客户端别名 fast 归一化后仍属于 priority，强制关闭必须将其删除。
func TestOpenAIAPIKeyFastModeForceOffRemovesFastAlias(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "unknown-provider-model")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"unknown-provider-model","service_tier":"fast"}`), svc.fastModeInput(ctx, account, "unknown-provider-model"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

func TestOpenAIAPIKeyFastModeCannotBypassSystemPolicy(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, openAIFastFilterPriorityPolicy())
	svc.resolver = fastModeTestResolver()
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.fastModeInput(ctx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	blockSvc := newOpenAIGatewayServiceWithSettings(t, &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{
		ServiceTier: tierpolicy.OpenAIFastTierPriority,
		Action:      claude.BetaPolicyActionBlock,
		Scope:       claude.BetaPolicyScopeAll,
	}}})
	blockSvc.resolver = fastModeTestResolver()
	_, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), blockSvc.fastModeInput(ctx, account, "gpt-5.5"))
	var blocked *tierpolicy.BlockedError
	require.ErrorAs(t, err, &blocked)

	// 系统强制 priority 命中原始 flex 后，单 Key force_off 不能删除它。
	svc = newOpenAIGatewayServiceWithSettings(t, &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{
		ServiceTier: tierpolicy.OpenAIFastTierFlex,
		Action:      tierpolicy.OpenAIFastPolicyActionForcePriority,
		Scope:       claude.BetaPolicyScopeAll,
	}}})
	svc.resolver = fastModeTestResolver()
	ctx = fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5","service_tier":"flex"}`), svc.fastModeInput(ctx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())
}

func TestOpenAIAPIKeyFastModeAppliesToRealtimeFrames(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	svc.resolver = fastModeTestResolver()
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	forceOnCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	updated, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5"}`), "gpt-5.5", svc.fastModeInput(forceOnCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	forceOffCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, blocked, err = gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5","service_tier":"priority"}`), "gpt-5.5", svc.fastModeInput(forceOffCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// WebSocket 每个 turn 都应读取最新策略，不能永久复用握手时的策略快照。
func TestOpenAIWSFastModePolicyContextRefreshesEachTurn(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	svc.resolver = fastModeTestResolver()
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	baseCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	policies := map[int]string{
		1: apikey.APIKeyFastModePolicyForceOn,
		2: apikey.APIKeyFastModePolicyForceOff,
	}
	hooks := &gatewayws.OpenAIIngressHooks{
		ResolveFastModePolicy: func(turn int) string {
			return policies[turn]
		},
	}

	turnOneCtx := openAIWSFastModePolicyContext(baseCtx, hooks, 1)
	updated, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5"}`), "gpt-5.5", svc.fastModeInput(turnOneCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	turnTwoCtx := openAIWSFastModePolicyContext(baseCtx, hooks, 2)
	updated, blocked, err = gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5","service_tier":"priority"}`), "gpt-5.5", svc.fastModeInput(turnTwoCtx, account, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

func TestAPIKeyFastModeIgnoresUnsupportedProviderAdapters(t *testing.T) {
	openAISvc := newOpenAIGatewayServiceWithSettings(t, tierpolicy.Default())
	openAISvc.resolver = fastModeTestResolver()
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), openAISvc.fastModeInput(ctx, &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}}, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())

	claudeSvc := withSchedulerParametersForTest(&GatewayService{resolver: fastModeTestResolver()})
	body, headers, err := claudeSvc.applyClaudeAPIKeyFastMode(ctx, &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock}}, "claude-opus-4-8", []byte(`{"model":"claude-opus-4-8"}`), http.Header{})
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "speed").Exists())
	require.Empty(t, claude.GetHeaderRaw(headers, "anthropic-beta"))
}

func TestClaudeAPIKeyFastModeWireEncoding(t *testing.T) {
	svc := withSchedulerParametersForTest(&GatewayService{resolver: fastModeTestResolver()})
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}

	forceOnCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "claude-opus-4-8")
	body, headers, err := svc.applyClaudeAPIKeyFastMode(forceOnCtx, account, "claude-opus-4-8", []byte(`{"model":"claude-opus-4-8"}`), http.Header{})
	require.NoError(t, err)
	require.Equal(t, "fast", gjson.GetBytes(body, "speed").String())
	require.True(t, claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode))

	forceOffCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "claude-opus-4-8")
	claude.SetHeaderRaw(headers, "anthropic-beta", claude.BetaFastMode+",context-management-2025-06-27")
	body, headers, err = svc.applyClaudeAPIKeyFastMode(forceOffCtx, account, "claude-opus-4-8", body, headers)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "speed").Exists())
	require.False(t, claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), claude.BetaFastMode))
	require.True(t, claude.ContainsBetaToken(claude.GetHeaderRaw(headers, "anthropic-beta"), "context-management-2025-06-27"))
}

// Anthropic 直连的强制关闭应覆盖所有凭据类型，且不依赖定价解析器。
func TestClaudeAPIKeyFastModeForceOffIgnoresCapabilityAndCredentialType(t *testing.T) {
	svc := withSchedulerParametersForTest(&GatewayService{})
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "claude-opus-4-8")

	for _, accountType := range []string{capability.AccountTypeAPIKey, capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			headers := http.Header{}
			claude.SetHeaderRaw(headers, "anthropic-beta", claude.BetaFastMode+",context-management-2025-06-27")
			body, updatedHeaders, err := svc.applyClaudeAPIKeyFastMode(
				ctx,
				&gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: accountType}},
				"claude-opus-4-8",
				[]byte(`{"model":"claude-opus-4-8","speed":"fast"}`),
				headers,
			)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(body, "speed").Exists())
			require.False(t, claude.ContainsBetaToken(claude.GetHeaderRaw(updatedHeaders, "anthropic-beta"), claude.BetaFastMode))
			require.True(t, claude.ContainsBetaToken(claude.GetHeaderRaw(updatedHeaders, "anthropic-beta"), "context-management-2025-06-27"))
		})
	}
}

func TestClaudeAPIKeyFastModeCannotBypassSystemFilter(t *testing.T) {
	cfg := &config.Config{}
	svc := withSchedulerParametersForTest(&GatewayService{
		cfg:      cfg,
		resolver: fastModeTestResolver(),
	})
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "claude-opus-4-8")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	// 模拟系统 Beta 策略已将 Claude Fast token 标记为过滤。
	c.Set(betaPolicyFilterSetKey, map[string]struct{}{claude.BetaFastMode: {}})

	req, wireBody, err := svc.buildUpstreamRequest(ctx, c, account, []byte(`{"model":"claude-opus-4-8","messages":[]}`), "test-key", "apikey", "claude-opus-4-8", false, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wireBody, "speed").Exists())
	require.False(t, claude.ContainsBetaToken(claude.GetHeaderRaw(req.Header, "anthropic-beta"), claude.BetaFastMode))
}

func TestClaudeUsageSpeedDrivesFastBilling(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	svc := completion.NewRecorder(completion.Dependencies{Calculator: billing, Prices: billingtestkit.PriceResolver(nil, billing)}, completion.RecorderOptions{DefaultMultiplier: 1})

	groupID := int64(11)
	apiKey := &apikey.APIKey{GroupID: &groupID, Group: &routing.Group{ID: groupID}}
	account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}
	base := &forwardcore.MessagesResult{Usage: upstream.TokenUsage{InputTokens: 1000, OutputTokens: 100}, Model: "claude-opus-4-8"}
	fast := *base
	fast.Usage.Speed = "fast"

	baseCost := svc.CalculateTokenCost(context.Background(), gatewaycapture.ProjectMessagesCompletionResult(base, gatewaycapture.ExecutionCompletionRecord(account)), gatewaycapture.ProjectCompletionKey(apiKey), gatewaycapture.ProjectCompletionAccount(gatewaycapture.ExecutionCompletionRecord(account)), "claude-opus-4-8", "claude-opus-4-8", "", "", 1, nil)
	fastCost := svc.CalculateTokenCost(context.Background(), gatewaycapture.ProjectMessagesCompletionResult(&fast, gatewaycapture.ExecutionCompletionRecord(account)), gatewaycapture.ProjectCompletionKey(apiKey), gatewaycapture.ProjectCompletionAccount(gatewaycapture.ExecutionCompletionRecord(account)), "claude-opus-4-8", "claude-opus-4-8", "", "", 1, nil)
	require.InDelta(t, baseCost.ActualCost*2, fastCost.ActualCost, 1e-12)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, completion.ClaudeServiceTier(fast.Usage.Speed))
}

func TestClaudeUsageSpeedParsing(t *testing.T) {
	svc := withSchedulerParametersForTest(&GatewayService{})
	usage := &upstream.TokenUsage{}
	svc.parseSSEUsage(`{"type":"message_start","message":{"usage":{"input_tokens":10,"speed":"fast"}}}`, usage)
	require.Equal(t, "fast", usage.Speed)

	parsed := anthropic.ParseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":10,"output_tokens":2,"speed":"standard"}}`))
	require.Equal(t, "standard", parsed.Speed)
}
