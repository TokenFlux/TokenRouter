package messageforward

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// TestGatewayCacheTTLGlobalSetting_TargetResolution enables global cache
// injection before adding a provider usage override. The resolved bucket
// changes from five minutes to one hour, protecting provider override
// precedence.
func TestGatewayCacheTTLGlobalSetting_TargetResolution(t *testing.T) {
	repo := &betaSettingsFixture{values: map[string]string{
		gateway.SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
	}}
	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(repo.values)}, Options{})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}}

	target, ok := svc.cacheUsageOverride(context.Background(), provider)
	require.True(t, ok)
	require.Equal(t, "5m", target)

	provider.Record.Extra = map[string]any{
		"cache_ttl_override_enabled": true,
		"cache_ttl_override_target":  "1h",
	}
	target, ok = svc.cacheUsageOverride(context.Background(), provider)
	require.True(t, ok)
	require.Equal(t, claude.CacheTTLTarget1h, target)
}

// TestGatewayCacheTTLGlobalSetting_RequestInjectionScope compares Anthropic
// OAuth, Setup Token, API key, and another platform under the enabled setting.
// Invalidating changed settings disables previously eligible request injection.
func TestGatewayCacheTTLGlobalSetting_RequestInjectionScope(t *testing.T) {
	repo := &betaSettingsFixture{values: map[string]string{
		gateway.SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
	}}
	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(repo.values)}, Options{})

	require.True(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}}))
	require.True(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeSetupToken}}))
	require.False(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey}}))
	require.False(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}))

	repo.values[gateway.SettingKeyEnableAnthropicCacheTTL1hInjection] = "false"
	// Invalidate the settings snapshot after changing the fixture value so
	// the next eligibility check reads the updated flag during the same
	// test.
	svc.dependencies.Settings.InvalidateForwarding()
	require.False(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}}))
}

// TestForceClaude1hCache_InjectsWithoutUsageOverride enables the per-Key Claude
// one-hour cache flag while the global OAuth injection setting remains off. It
// checks eligible Claude targets inject TTL, passback models do not, and
// response cache usage keeps normal one-hour billing instead of a 5m override.
func TestForceClaude1hCache_InjectsWithoutUsageOverride(t *testing.T) {
	svc := NewRuntime(Dependencies{}, Options{})
	ctx := gatewayprovider.WithAPIKeyForceClaude1hCache(context.Background(), true)

	oauthTarget := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth},
	}
	apiKeyTarget := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey},
	}
	vertexTarget := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeServiceAccount},
	}

	require.True(t, svc.injectTTL(ctx, oauthTarget, "claude-sonnet-4-6"))
	require.True(t, svc.injectTTL(ctx, apiKeyTarget, "claude-sonnet-4-6"))
	require.True(t, svc.injectTTL(ctx, vertexTarget, "claude-sonnet-4-6@20260218"))
	require.False(t, svc.injectTTL(ctx, apiKeyTarget, "glm-5", "claude-sonnet-4-6"))
	require.False(t, svc.injectTTL(ctx, apiKeyTarget, "gpt-oss:20b"))

	overrideTarget, ok := svc.cacheUsageOverride(ctx, oauthTarget)
	require.False(t, ok)
	require.Empty(t, overrideTarget)

	overrideTarget, ok = svc.cacheUsageOverride(ctx, apiKeyTarget)
	require.False(t, ok)
	require.Empty(t, overrideTarget)
}

// TestRewriteMessageCacheControlIfEnabled_DefaultKeepsClientAnchors passes
// existing client message cache annotations through an unconfigured runtime.
// Both one-hour and five-minute TTL values remain present in the returned JSON
// body.
func TestRewriteMessageCacheControlIfEnabled_DefaultKeepsClientAnchors(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"ok"}]},
		{"role":"user","content":[{"type":"text","text":"latest","cache_control":{"type":"ephemeral","ttl":"5m"}}]}
	]}`)

	out := NewRuntime(Dependencies{}, Options{}).rewriteCache(context.Background(), body)

	require.JSONEq(t, string(body), string(out))
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.2.content.0.cache_control.ttl").String())
}

// TestRewriteMessageCacheControlIfEnabled_OptInPreservesLegacyRewrite enables
// message breakpoint rewriting for a history carrying client TTL annotations.
// The chosen first and last breakpoints receive five-minute TTLs while the
// intermediate anchor disappears.
func TestRewriteMessageCacheControlIfEnabled_OptInPreservesLegacyRewrite(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"ok"}]},
		{"role":"user","content":[{"type":"text","text":"latest","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"done"}]}
	]}`)
	repo := &betaSettingsFixture{values: map[string]string{
		gateway.SettingKeyRewriteMessageCacheControl: "true",
	}}
	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(repo.values)}, Options{})

	out := svc.rewriteCache(context.Background(), body)

	require.Equal(t, "5m", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(out, "messages.2.content.0.cache_control").Exists())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.3.content.0.cache_control.ttl").String())
}
