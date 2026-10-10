package textattempt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// nativeGeminiPricingFixture 记录转发、槽位释放和调度反馈的次数。
type nativeGeminiPricingFixture struct {
	bridge   *nativeGeminiAttemptBridge
	response *httptest.ResponseRecorder
	forwards int
	releases int
	reports  int
}

// TestNativeGeminiForwardRejectsMissingPricing 覆盖两种生成动作及 Gemini 的三种凭据和 Antigravity。
func TestNativeGeminiForwardRejectsMissingPricing(t *testing.T) {
	for _, target := range []struct{ platform, kind string }{
		{provider.PlatformGemini, "apikey"},
		{provider.PlatformGemini, "oauth"},
		{provider.PlatformGemini, "service_account"},
		{provider.PlatformAntigravity, "oauth"},
	} {
		for _, action := range []string{"generateContent", "streamGenerateContent"} {
			t.Run(target.platform+"/"+target.kind+"/"+action, func(t *testing.T) {
				fixture := newNativeGeminiPricingFixture(t, target.platform, action)
				fixture.bridge.provider.Record.Type = target.kind
				outcome := fixture.bridge.Forward(text.AttemptState{})
				require.ErrorIs(t, outcome.Err, admission.ErrModelPricingRejected)
				require.True(t, outcome.Stop)
				require.False(t, outcome.HasResult)
				require.Nil(t, outcome.Failure)
				require.Zero(t, fixture.forwards)
				require.Zero(t, fixture.reports)
				require.Equal(t, 1, fixture.releases)
				require.Equal(t, http.StatusBadRequest, fixture.response.Code)
				require.JSONEq(t, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"`+admission.ModelPricingUnavailableMessage+`"}}`, fixture.response.Body.String())
				require.True(t, fixture.bridge.c.GetBool(gatewayhttp.OpsClientBusinessLimitedKey))
			})
		}
	}
}

// TestNativeGeminiForwardPricingModels 使用价卡和映射核对实际准入的计费型号。
func TestNativeGeminiForwardPricingModels(t *testing.T) {
	input, zero, multiplier := 2e-6, 0.0, 2.0
	for _, platform := range []string{provider.PlatformGemini, provider.PlatformAntigravity} {
		for _, tc := range []struct {
			name, requested, mapped, upstream, source string
			card                                      *routing.ModelPricingEntry
			missing                                   bool
		}{
			{name: "catalog", mapped: "gemini-2.5-flash"},
			{name: "manual", card: &routing.ModelPricingEntry{InputPrice: &input}},
			{name: "free", card: &routing.ModelPricingEntry{InputPrice: &zero, OutputPrice: &zero}},
			{name: "interval", card: &routing.ModelPricingEntry{Intervals: []pricing.PricingInterval{{InputPrice: &input}}}},
			{name: "image_card", card: &routing.ModelPricingEntry{BillingMode: pricing.BillingModeImage, PerRequestPrice: &input}},
			{name: "multiplier_missing", card: &routing.ModelPricingEntry{FastMultiplier: &multiplier}, missing: true},
			{name: "group_mapping", mapped: "gemini-2.5-flash", source: routing.BillingModelSourceGroupMapped},
			{name: "provider_mapping", upstream: "gemini-2.5-flash", source: routing.BillingModelSourceUpstream},
			{name: "requested_missing", mapped: "gemini-2.5-flash", source: routing.BillingModelSourceRequested, missing: true},
			{name: "requested_priced", requested: "gemini-2.5-flash", source: routing.BillingModelSourceRequested},
			{name: "upstream_missing", mapped: "gemini-2.5-flash", upstream: "private-gemini-model", source: routing.BillingModelSourceUpstream, missing: true},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				var cards []routing.ModelPricingEntry
				if tc.card != nil {
					card := *tc.card
					card.Models = []string{"unpriced-gemini-model"}
					cards = append(cards, card)
				}
				fixture := newNativeGeminiPricingFixture(t, platform, "generateContent", cards...)
				bridge := fixture.bridge
				if tc.requested != "" {
					bridge.reqModel = tc.requested
				}
				if tc.mapped != "" {
					bridge.modelName = tc.mapped
				}
				if tc.upstream != "" {
					bridge.provider.Record.Credentials = map[string]any{"model_mapping": map[string]any{bridge.modelName: tc.upstream}}
				}
				mapping := routing.GroupMappingResult{Mapped: bridge.reqModel != bridge.modelName, MappedModel: bridge.modelName, BillingModelSource: tc.source}
				plan := routing.Plan(routing.PlanInput{GroupID: bridge.apiKey.GroupID, RequestedModel: bridge.reqModel, GroupMapping: mapping})
				bridge.c.Request = bridge.c.Request.WithContext(requeststate.WithRoutePlan(bridge.Context(), plan))
				outcome := bridge.Forward(text.AttemptState{})
				require.Equal(t, 1, fixture.releases)
				if tc.missing {
					require.ErrorIs(t, outcome.Err, admission.ErrModelPricingRejected)
					require.True(t, outcome.Stop)
					require.Zero(t, fixture.forwards)
					require.Zero(t, fixture.reports)
					return
				}
				require.NoError(t, outcome.Err)
				require.False(t, outcome.Stop)
				require.Equal(t, 1, fixture.forwards)
				require.Equal(t, 1, fixture.reports)
			})
		}
	}
}

// TestNativeGeminiForwardPricingScope 计数和动作校验交给平台执行器，图片型号遵循共享检查规则。
func TestNativeGeminiForwardPricingScope(t *testing.T) {
	for _, tc := range []struct{ action, model string }{
		{"countTokens", "unpriced-gemini-model"},
		{"unsupportedAction", "unpriced-gemini-model"},
		{"generateContent", "gpt-image-1"},
	} {
		t.Run(tc.action+"/"+tc.model, func(t *testing.T) {
			fixture := newNativeGeminiPricingFixture(t, provider.PlatformGemini, tc.action)
			fixture.bridge.modelName = tc.model
			outcome := fixture.bridge.Forward(text.AttemptState{})
			require.NoError(t, outcome.Err)
			require.False(t, outcome.Stop)
			require.Equal(t, 1, fixture.forwards)
			require.Equal(t, 1, fixture.releases)
		})
	}
}

// TestNativeGeminiForwardRechecksProviderPricing 换号后按新提供商的映射再次查价。
func TestNativeGeminiForwardRechecksProviderPricing(t *testing.T) {
	fixture := newNativeGeminiPricingFixture(t, provider.PlatformGemini, "generateContent")
	bridge := fixture.bridge
	plan := routing.Plan(routing.PlanInput{GroupID: bridge.apiKey.GroupID, RequestedModel: bridge.reqModel, GroupMapping: routing.GroupMappingResult{MappedModel: bridge.modelName, BillingModelSource: routing.BillingModelSourceUpstream}})
	bridge.c.Request = bridge.c.Request.WithContext(requeststate.WithRoutePlan(bridge.Context(), plan))
	bridge.provider.Record.Credentials = map[string]any{"model_mapping": map[string]any{bridge.modelName: "gemini-2.5-flash"}}
	require.NoError(t, bridge.Forward(text.AttemptState{}).Err)
	bridge.provider = gatewayprovider.NewExecutionProvider(&provider.Record{ID: 2, Platform: provider.PlatformGemini, Type: "apikey"})
	outcome := bridge.Forward(text.AttemptState{SwitchCount: 1})
	require.ErrorIs(t, outcome.Err, admission.ErrModelPricingRejected)
	require.True(t, outcome.Stop)
	require.Equal(t, 1, fixture.forwards)
	require.Equal(t, 1, fixture.reports)
	require.Equal(t, 2, fixture.releases)
}

// TestNativeGeminiForwardRouteErrorReleasesSlot 路由校验失败时结束尝试并归还已取得的槽位。
func TestNativeGeminiForwardRouteErrorReleasesSlot(t *testing.T) {
	fixture := newNativeGeminiPricingFixture(t, provider.PlatformAntigravity, "generateContent")
	bridge := fixture.bridge
	bridge.provider.Record.Type = "apikey"
	plan := routing.Plan(routing.PlanInput{GroupID: bridge.apiKey.GroupID, RequestedModel: bridge.reqModel})
	bridge.c.Request = bridge.c.Request.WithContext(requeststate.WithRoutePlan(bridge.Context(), plan))
	outcome := bridge.Forward(text.AttemptState{})
	require.Error(t, outcome.Err)
	require.NotErrorIs(t, outcome.Err, admission.ErrModelPricingRejected)
	require.True(t, outcome.Stop)
	require.Zero(t, fixture.forwards)
	require.Zero(t, fixture.reports)
	require.Equal(t, 1, fixture.releases)
	require.Equal(t, http.StatusInternalServerError, fixture.response.Code)
	require.Contains(t, fixture.response.Body.String(), "Failed to check model pricing")
	require.False(t, bridge.c.GetBool(gatewayhttp.OpsClientBusinessLimitedKey))
}

// newNativeGeminiPricingFixture 用固定目录价和可选分组价卡构造单次尝试。
func newNativeGeminiPricingFixture(t *testing.T, platform, action string, cards ...routing.ModelPricingEntry) *nativeGeminiPricingFixture {
	t.Helper()
	fixture := &nativeGeminiPricingFixture{response: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(fixture.response)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/unpriced-gemini-model:"+action, nil)
	c.Request = c.Request.WithContext(requeststate.WithClientProtocol(c.Request.Context(), protocol.ProtocolGeminiGenerateContent))
	calculator := testkit.Calculator(nil, map[string]*pricing.ModelPricing{
		"gemini-2.5-flash": {InputPricePerToken: 2e-6, OutputPricePerToken: 12e-6},
	})
	forwardCall := func() (*forward.MessagesResult, error) {
		fixture.forwards++
		return &forward.MessagesResult{Model: fixture.bridge.modelName}, nil
	}
	runtime := New(Bindings{
		Pricing: &admission.ModelPricing{Resolver: testkit.ResolverWithCards(t, calculator, cards)},
		Forward: ForwardPorts{
			ForwardGeminiNative: func(context.Context, *gin.Context, *gatewayprovider.ExecutionProvider, string, string, bool, []byte) (*forward.MessagesResult, error) {
				require.Equal(t, provider.PlatformGemini, platform)
				return forwardCall()
			},
			ForwardAntigravityGemini: func(context.Context, *gin.Context, *gatewayprovider.ExecutionProvider, string, string, bool, []byte, bool, ...forward.GeminiSessionOption) (*forward.MessagesResult, error) {
				require.Equal(t, provider.PlatformAntigravity, platform)
				return forwardCall()
			},
		},
		Selection: SelectionPorts{ReportSchedule: func(*gatewayprovider.SelectionResult, int64, bool, *forward.MessagesResult) {
			fixture.reports++
		}},
	})
	fixture.bridge = &nativeGeminiAttemptBridge{
		messageAttemptBridge: messageAttemptBridge{
			fixed:               runtime.dependencies,
			c:                   c,
			apiKey:              &apikey.APIKey{GroupID: testkit.GroupID()},
			reqModel:            "unpriced-gemini-model",
			body:                []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`),
			provider:            gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: platform, Type: "apikey"}),
			providerReleaseFunc: func() { fixture.releases++ },
			reqLog:              zap.NewNop(),
		},
		modelName: "unpriced-gemini-model",
		action:    action,
		stream:    action == "streamGenerateContent",
	}
	if platform == provider.PlatformAntigravity {
		fixture.bridge.provider.Record.Type = "oauth"
	}
	return fixture
}
