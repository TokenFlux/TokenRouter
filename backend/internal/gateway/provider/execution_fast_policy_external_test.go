package provider_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// TestOpenAIAPIKeyFastModeForceOnAndOff applies Key force-on and force-off to
// a priced OpenAI API Key model. The first adds priority and the second
// removes the requested Fast tier.
func TestOpenAIAPIKeyFastModeForceOnAndOff(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	svc.Prices = fastModeTestResolver()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	forceOnCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.Input(forceOnCtx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	forceOffCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5","service_tier":"priority"}`), svc.Input(forceOffCtx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// TestOpenAIGroupFastForcesHTTPAndWS binds an admitted hydrated group forcing
// Fast when client tier is omitted. Both HTTP body and WS frame receive
// priority without a blocking result.
func TestOpenAIGroupFastForcesHTTPAndWS(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	group := &routing.Group{ID: 12, Status: billingcore.StatusActive, Hydrated: true, ForceOpenAIFast: true}
	ctx := requeststate.WithGroup(context.Background(), group)

	body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.Input(ctx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(body, "service_tier").String())

	frame, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5"}`), "gpt-5.5", svc.Input(ctx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(frame, "service_tier").String())
}

// TestOpenAIGroupFastStillHonorsGlobalAndKeyPolicy combines group forced Fast
// with global filtering or Key force-off. Each policy removes tier injection
// despite the group's Fast request.
func TestOpenAIGroupFastStillHonorsGlobalAndKeyPolicy(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	group := &routing.Group{ID: 13, Status: billingcore.StatusActive, Hydrated: true, ForceOpenAIFast: true}
	base := requeststate.WithGroup(context.Background(), group)

	filtered := newFastPolicyContract(t, openAIFastFilterPriorityPolicy())
	body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), filtered.Input(base, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())

	forceOff := apikey.WithFastModePolicy(base, apikey.APIKeyFastModePolicyForceOff)
	passed := newFastPolicyContract(t, tierpolicy.Default())
	body, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), passed.Input(forceOff, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())
}

// TestOpenAIGroupFastRequiresTrustedContextAndCapableProvider varies group
// hydration and provider platform with group force enabled. Injection occurs
// only for the hydrated OpenAI case in this matrix.
func TestOpenAIGroupFastRequiresTrustedContextAndCapableProvider(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	for _, tc := range []struct {
		hydrated bool   // group authorization hydration flag
		platform string // selected provider platform name
		want     bool   // expected Fast field injection
	}{
		{false, capability.PlatformOpenAI, false},
		{true, capability.PlatformOpenAI, true},
		{true, capability.PlatformGrok, false},
	} {
		group := &routing.Group{ID: 14, Status: billingcore.StatusActive, Hydrated: tc.hydrated, ForceOpenAIFast: true}
		ctx := requeststate.WithGroup(context.Background(), group)
		body, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.Input(ctx, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: tc.platform, Type: capability.ProviderTypeAPIKey}}, "gpt-5.5"))
		require.NoError(t, err)
		require.Equal(t, tc.want, gjson.GetBytes(body, "service_tier").Exists())
	}
}

// TestOpenAIAPIKeyFastModeIgnoresUnsupportedModel forces Fast on for an OpenAI
// API Key model absent from the price fixture. Body policy succeeds while the
// resulting service_tier field stays absent.
func TestOpenAIAPIKeyFastModeIgnoresUnsupportedModel(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	svc.Prices = fastModeTestResolver()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "unknown-provider-model")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"unknown-provider-model"}`), svc.Input(ctx, provider, "unknown-provider-model"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// TestOpenAIAPIKeyFastModeForceOffIgnoresMissingCapabilityMetadata forces Fast
// off for a model lacking price metadata. HTTP and WS remove priority without
// needing capability evidence or producing a block.
func TestOpenAIAPIKeyFastModeForceOffIgnoresMissingCapabilityMetadata(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	svc.Prices = fastModeTestResolver()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "unknown-provider-model")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"unknown-provider-model","service_tier":"priority"}`), svc.Input(ctx, provider, "unknown-provider-model"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	updated, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"unknown-provider-model","service_tier":"priority"}`), "unknown-provider-model", svc.Input(ctx, provider, "unknown-provider-model"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// TestOpenAIAPIKeyFastModeForceOffPreservesNonFastTiers supplies flex, auto,
// default, and scale on an unknown-model request under Key force-off. HTTP
// bodies and WS frames retain each listed non-Fast tier.
func TestOpenAIAPIKeyFastModeForceOffPreservesNonFastTiers(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "unknown-provider-model")

	for _, tier := range []string{"flex", "auto", "default", "scale"} {
		t.Run(tier, func(t *testing.T) {
			body := []byte(`{"model":"unknown-provider-model","service_tier":"` + tier + `"}`)
			updated, err := tierpolicy.ApplyBody(body, svc.Input(ctx, provider, "unknown-provider-model"))
			require.NoError(t, err)
			require.Equal(t, tier, gjson.GetBytes(updated, "service_tier").String())

			wsBody := []byte(`{"type":"response.create","model":"unknown-provider-model","service_tier":"` + tier + `"}`)
			updated, blocked, err := gatewayws.ApplyServiceTierFrame(wsBody, "unknown-provider-model", svc.Input(ctx, provider, "unknown-provider-model"))
			require.NoError(t, err)
			require.Nil(t, blocked)
			require.Equal(t, tier, gjson.GetBytes(updated, "service_tier").String())
		})
	}
}

// TestOpenAIAPIKeyFastModeForceOffRemovesFastAlias supplies the fast alias
// under force-off for an unknown model. Body policy removes the normalized
// Fast field without a price lookup requirement.
func TestOpenAIAPIKeyFastModeForceOffRemovesFastAlias(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "unknown-provider-model")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"unknown-provider-model","service_tier":"fast"}`), svc.Input(ctx, provider, "unknown-provider-model"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// TestOpenAIAPIKeyFastModeCannotBypassSystemPolicy combines Key forcing with
// global filter, block, and force-priority actions. Global filtering/blocking
// still applies, and an earlier global flex rewrite precedes Key force-off.
func TestOpenAIAPIKeyFastModeCannotBypassSystemPolicy(t *testing.T) {
	svc := newFastPolicyContract(t, openAIFastFilterPriorityPolicy())
	svc.Prices = fastModeTestResolver()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")

	updated, err := tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), svc.Input(ctx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	blockSvc := newFastPolicyContract(t, &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{
		ServiceTier: tierpolicy.OpenAIFastTierPriority,
		Action:      claude.BetaPolicyActionBlock,
		Scope:       claude.BetaPolicyScopeAll,
	}}})
	blockSvc.Prices = fastModeTestResolver()
	_, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5"}`), blockSvc.Input(ctx, provider, "gpt-5.5"))
	var blocked *tierpolicy.BlockedError
	require.ErrorAs(t, err, &blocked)

	// A global rule forcing priority from flex takes precedence over Key force_off.
	svc = newFastPolicyContract(t, &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{
		ServiceTier: tierpolicy.OpenAIFastTierFlex,
		Action:      tierpolicy.OpenAIFastPolicyActionForcePriority,
		Scope:       claude.BetaPolicyScopeAll,
	}}})
	svc.Prices = fastModeTestResolver()
	ctx = fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, err = tierpolicy.ApplyBody([]byte(`{"model":"gpt-5.5","service_tier":"flex"}`), svc.Input(ctx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())
}

// TestOpenAIAPIKeyFastModeAppliesToRealtimeFrames applies Key force-on/off
// through response.create frame policy for a priced model. Frames receive or
// lose priority with no blocking observation.
func TestOpenAIAPIKeyFastModeAppliesToRealtimeFrames(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	svc.Prices = fastModeTestResolver()
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	forceOnCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "gpt-5.5")
	updated, blocked, err := gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5"}`), "gpt-5.5", svc.Input(forceOnCtx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	forceOffCtx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOff, "gpt-5.5")
	updated, blocked, err = gatewayws.ApplyServiceTierFrame([]byte(`{"type":"response.create","model":"gpt-5.5","service_tier":"priority"}`), "gpt-5.5", svc.Input(forceOffCtx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())
}

// TestGroupOpenAIFastPolicyHTTPAndWS compares group policies, incoming tiers,
// Key overrides, and global actions. HTTP and WS produce matching tier
// presence/value or their corresponding block results.
func TestGroupOpenAIFastPolicyHTTPAndWS(t *testing.T) {
	for _, tt := range []struct {
		// group: configured group policy. tier: requested tier value.
		// key: Key override policy. action: global rule action. want:
		// expected outgoing tier.
		group, tier, key, action, want string
		ruleTier                       string // optional system rule tier match
		model                          string // model with test pricing metadata
		// expected policy block result
		blocked bool
	}{
		{group: "follow_request", tier: "ultrafast", want: "ultrafast"},
		{group: "force_priority", tier: "ultrafast", want: "priority"},
		{group: "force_ultrafast", want: "ultrafast"},
		{group: "force_ultrafast", key: "force_on", want: "ultrafast"},
		{group: "force_ultrafast", key: "force_off"},
		{group: "force_off", tier: "ultrafast"},
		{group: "force_off", tier: "fast"},
		{group: "force_off", key: "force_on"},
		{group: "force_off", tier: "flex", want: "flex"},
		{group: "force_ultrafast", action: "filter"},
		{group: "force_ultrafast", action: "block", blocked: true},
		{group: "force_off", tier: "priority", action: "force_ultrafast", want: "ultrafast"},
		{model: "gpt-5.6-sol", key: "force_ultrafast", want: "ultrafast"},
		{model: "gpt-5.6-sol", tier: "priority", key: "force_ultrafast", want: "ultrafast"},
		{model: "gpt-5.6-sol", group: "force_priority", key: "force_ultrafast", want: "ultrafast"},
		{model: "gpt-5.6-sol", group: "force_off", key: "force_ultrafast"},
		{model: "gpt-5.6-sol", key: "force_ultrafast", action: "filter", ruleTier: "ultrafast"},
		{model: "gpt-5.6-sol", key: "force_ultrafast", action: "block", ruleTier: "ultrafast", blocked: true},
		{model: "gpt-5.6-sol", key: "force_ultrafast", action: "force_priority", ruleTier: "ultrafast", want: "priority"},
		{model: "gpt-5.5", tier: "flex", key: "force_ultrafast", want: "flex"},
	} {
		t.Run(tt.model+"/"+tt.group+"/"+tt.tier+"/"+tt.key+"/"+tt.action, func(t *testing.T) {
			model := cmp.Or(tt.model, "gpt-5.5")
			settings := tierpolicy.Default()
			if tt.action != "" {
				settings.Rules = []tierpolicy.OpenAIFastPolicyRule{
					{ServiceTier: cmp.Or(tt.ruleTier, "all"), Scope: "all", Action: tt.action},
				}
			}
			svc := newFastPolicyContract(t, settings)
			svc.Prices = fastModeTestResolver()
			ctx := requeststate.WithGroup(fastModeTestContext(tt.key, model), &routing.Group{ID: 1, Status: billingcore.StatusActive, Hydrated: true, OpenAIFastPolicy: tt.group})
			payload := map[string]any{"model": model, "type": "response.create"}
			if tt.tier != "" {
				payload["service_tier"] = tt.tier
			}
			// This fixture map contains string fields, so encoding cannot introduce
			// unsupported values or cycles; body policy then receives the same serialized frame input.
			body, _ := json.Marshal(payload)
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
			httpBody, httpErr := tierpolicy.ApplyBody(body, svc.Input(ctx, provider, model))
			wsBody, blocked, err := gatewayws.ApplyServiceTierFrame(body, model, svc.Input(ctx, provider, model))
			require.NoError(t, err)
			if tt.blocked {
				require.Error(t, httpErr)
				require.NotNil(t, blocked)
				return
			}
			require.NoError(t, httpErr)
			require.Nil(t, blocked)
			for _, out := range [][]byte{httpBody, wsBody} {
				require.Equal(t, tt.want, gjson.GetBytes(out, "service_tier").String())
				require.Equal(t, tt.want != "", gjson.GetBytes(out, "service_tier").Exists())
			}
		})
	}
}

// openAIFastFilterPriorityPolicy constructs a global priority-filter fixture
// with pass as its model fallback.
func openAIFastFilterPriorityPolicy() *tierpolicy.OpenAIFastPolicySettings {
	return &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{{
			ServiceTier:    tierpolicy.OpenAIFastTierPriority,
			Action:         claude.BetaPolicyActionFilter,
			Scope:          claude.BetaPolicyScopeAll,
			ModelWhitelist: []string{},
			FallbackAction: claude.BetaPolicyActionPass,
		}},
	}
}

// TestApplyOpenAIFastPolicyToBody_DefaultPassesPriorityAndFast uses default
// rules for priority, fast alias, an ordinary model, and omitted tier. Known
// priority remains, fast canonicalizes to priority, and omitted tier leaves
// bytes unchanged.
func TestApplyOpenAIFastPolicyToBody_DefaultPassesPriorityAndFast(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	body := []byte(`{"model":"gpt-5.5","service_tier":"priority","messages":[]}`)
	updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, string(body), string(updated))

	body = []byte(`{"model":"gpt-5.5","service_tier":"fast"}`)
	updated, err = tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, "priority", gjson.GetBytes(updated, "service_tier").String())

	body = []byte(`{"model":"gpt-4","service_tier":"priority"}`)
	updated, err = tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-4"))
	require.NoError(t, err)
	require.Equal(t, string(body), string(updated))

	// Missing service_tier leaves the body unchanged.
	body = []byte(`{"model":"gpt-5.5"}`)
	updated, err = tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, string(body), string(updated))
}

// TestApplyOpenAIFastPolicyToBody_ExplicitFilterRemovesField filters priority
// through an explicit global rule. Both priority and the fast alias disappear
// from the resulting request body.
func TestApplyOpenAIFastPolicyToBody_ExplicitFilterRemovesField(t *testing.T) {
	svc := newFastPolicyContract(t, openAIFastFilterPriorityPolicy())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	body := []byte(`{"model":"gpt-5.5","service_tier":"priority","messages":[]}`)
	updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.NoError(t, err)
	require.NotContains(t, string(updated), `"service_tier"`)

	body = []byte(`{"model":"gpt-5.5","service_tier":"fast"}`)
	updated, err = tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.NoError(t, err)
	require.NotContains(t, string(updated), `"service_tier"`)
}

// TestApplyOpenAIFastPolicyToBody_UserScopedRuleOverridesGlobalRule places a
// payer-specific pass rule after a global filter. The matching payer retains
// priority while another payer receives filtered output.
func TestApplyOpenAIFastPolicyToBody_UserScopedRuleOverridesGlobalRule(t *testing.T) {
	settings := &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{
			{
				ServiceTier: tierpolicy.OpenAIFastTierPriority,
				Action:      claude.BetaPolicyActionFilter,
				Scope:       claude.BetaPolicyScopeAll,
			},
			{
				ServiceTier: tierpolicy.OpenAIFastTierPriority,
				Action:      claude.BetaPolicyActionPass,
				Scope:       claude.BetaPolicyScopeAll,
				UserIDs:     []int64{42},
			},
		},
	}
	svc := newFastPolicyContract(t, settings)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	body := []byte(`{"model":"gpt-5.5","service_tier":"priority"}`)

	allowedUserCtx := apikey.WithAccessSnapshot(context.Background(), apikey.AccessSnapshot{PayerUserID: int64(42)})
	updated, err := tierpolicy.ApplyBody(body, svc.Input(allowedUserCtx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, "priority", gjson.GetBytes(updated, "service_tier").String())

	otherUserCtx := apikey.WithAccessSnapshot(context.Background(), apikey.AccessSnapshot{PayerUserID: int64(43)})
	updated, err = tierpolicy.ApplyBody(body, svc.Input(otherUserCtx, provider, "gpt-5.5"))
	require.NoError(t, err)
	require.NotContains(t, string(updated), `"service_tier"`)
}

// TestApplyOpenAIFastPolicyToBody_PriorityFilterLeavesUltrafast applies a
// priority-only filtering rule to a GPT model request carrying ultrafast. The
// different tier remains in the resulting body without a blocking error.
func TestApplyOpenAIFastPolicyToBody_PriorityFilterLeavesUltrafast(t *testing.T) {
	svc := newFastPolicyContract(t, openAIFastFilterPriorityPolicy())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}
	body := []byte(`{"model":"gpt-5.6-sol","service_tier":"ultrafast"}`)

	updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.6-sol"))
	require.NoError(t, err)
	require.Equal(t, tierpolicy.OpenAIFastTierUltrafast, gjson.GetBytes(updated, "service_tier").String())
}

// TestApplyOpenAIFastPolicyToBody_ForcePriorityRewritesKnownTier applies an
// all-tier force-priority action to the listed recognized tier values and fast
// alias. Every resulting GPT request carries canonical priority after body
// policy.
func TestApplyOpenAIFastPolicyToBody_ForcePriorityRewritesKnownTier(t *testing.T) {
	settings := &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{{
			ServiceTier: tierpolicy.OpenAIFastTierAny,
			Action:      tierpolicy.OpenAIFastPolicyActionForcePriority,
			Scope:       claude.BetaPolicyScopeAll,
		}},
	}
	svc := newFastPolicyContract(t, settings)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	for _, tier := range []string{"flex", "auto", "default", "scale", "fast", "priority", "ultrafast"} {
		body := []byte(`{"model":"gpt-5.5","service_tier":"` + tier + `"}`)
		updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
		require.NoError(t, err)
		require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String(),
			"tier %q should be forced to priority", tier)
	}
}

// TestApplyOpenAIFastPolicyToBody_OfficialTiersBypassDefaultRule checks auto,
// default, and scale through body policy and direct evaluation with default
// settings. Body values survive and direct evaluation returns pass.
func TestApplyOpenAIFastPolicyToBody_OfficialTiersBypassDefaultRule(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	for _, tier := range []string{"auto", "default", "scale"} {
		body := []byte(`{"model":"gpt-5.5","service_tier":"` + tier + `"}`)
		updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
		require.NoError(t, err, "tier %q should pass without error", tier)
		require.Contains(t, string(updated), `"service_tier":"`+tier+`"`,
			"tier %q should be preserved in body under default policy", tier)
	}

	// Default settings contain no rules, so evaluation returns pass.
	for _, tier := range []string{"auto", "default", "scale"} {
		action, _ := svc.Evaluate(context.Background(), provider, "gpt-5.5", tier)
		require.Equal(t, claude.BetaPolicyActionPass, action, "tier %q should evaluate to pass", tier)
	}
}

// TestApplyOpenAIFastPolicyToBody_AllRuleStripsOfficialTiers uses an all-tier
// global filtering action with auto, default, scale, priority, and flex on a
// GPT request. Each resulting body omits the supplied service_tier field.
func TestApplyOpenAIFastPolicyToBody_AllRuleStripsOfficialTiers(t *testing.T) {
	settings := &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{{
			ServiceTier: tierpolicy.OpenAIFastTierAny,
			Action:      claude.BetaPolicyActionFilter,
			Scope:       claude.BetaPolicyScopeAll,
		}},
	}
	svc := newFastPolicyContract(t, settings)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	for _, tier := range []string{"auto", "default", "scale", "priority", "flex"} {
		body := []byte(`{"model":"gpt-5.5","service_tier":"` + tier + `"}`)
		updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
		require.NoError(t, err)
		require.NotContains(t, string(updated), `"service_tier"`,
			"tier %q should be stripped under ServiceTier=all + filter rule", tier)
	}
}

// TestApplyOpenAIFastPolicyToBody_UnknownTierStripped distinguishes
// service-tier normalization from policy body application for xxx.
// Normalization returns nil, while default body policy leaves the original
// unknown field unchanged.
func TestApplyOpenAIFastPolicyToBody_UnknownTierStripped(t *testing.T) {
	svc := newFastPolicyContract(t, tierpolicy.Default())
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	// Normalization rejects unknown values.
	require.Nil(t, protocolopenai.NormalizeServiceTier("xxx"))

	// Policy application alone accepts the body unchanged.
	// Request preparation is responsible for removing the unknown field.
	body := []byte(`{"model":"gpt-5.5","service_tier":"xxx"}`)
	updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.NoError(t, err)
	require.Equal(t, string(body), string(updated))
}

// TestApplyOpenAIFastPolicyToBody_BlockReturnsTypedError blocks priority on a
// whitelisted model with configured text. Body application returns a typed
// BlockedError carrying that text and retains the original body.
func TestApplyOpenAIFastPolicyToBody_BlockReturnsTypedError(t *testing.T) {
	settings := &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{{
			ServiceTier:    tierpolicy.OpenAIFastTierPriority,
			Action:         claude.BetaPolicyActionBlock,
			Scope:          claude.BetaPolicyScopeAll,
			ErrorMessage:   "fast mode is blocked for gpt-5.5",
			ModelWhitelist: []string{"gpt-5.5"},
			FallbackAction: claude.BetaPolicyActionPass,
		}},
	}
	svc := newFastPolicyContract(t, settings)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}

	body := []byte(`{"model":"gpt-5.5","service_tier":"priority"}`)
	updated, err := tierpolicy.ApplyBody(body, svc.Input(context.Background(), provider, "gpt-5.5"))
	require.Error(t, err)
	var blocked *tierpolicy.BlockedError
	require.True(t, errors.As(err, &blocked))
	require.Contains(t, blocked.Message, "fast mode is blocked")
	require.Equal(t, string(body), string(updated)) // body not mutated on block
}
