package messageforward

import (
	"context"

	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheTTLGlobalSetting_TargetResolution(t *testing.T) {
	repo := &betaSettingsFixture{values: map[string]string{
		gateway.SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
	}}
	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(repo.values)}, Options{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}

	target, ok := svc.cacheUsageOverride(context.Background(), account)
	require.True(t, ok)
	require.Equal(t, "5m", target)

	account.Record.Extra = map[string]any{
		"cache_ttl_override_enabled": true,
		"cache_ttl_override_target":  "1h",
	}
	target, ok = svc.cacheUsageOverride(context.Background(), account)
	require.True(t, ok)
	require.Equal(t, claude.CacheTTLTarget1h, target)
}

func TestGatewayCacheTTLGlobalSetting_RequestInjectionScope(t *testing.T) {
	repo := &betaSettingsFixture{values: map[string]string{
		gateway.SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
	}}
	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(repo.values)}, Options{})

	require.True(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}))
	require.True(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeSetupToken}}))
	require.False(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey}}))
	require.False(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}))

	repo.values[gateway.SettingKeyEnableAnthropicCacheTTL1hInjection] = "false"
	svc.dependencies.Settings.InvalidateForwarding()
	require.False(t, svc.injectTTL(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}))
}
