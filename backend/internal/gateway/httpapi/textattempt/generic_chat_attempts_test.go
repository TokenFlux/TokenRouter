package textattempt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// generic_chat_attempts_test.go checks generic Chat provider compatibility and
// admission timing before forwarding. Timing scenarios use the real scheduler
// and HTTP collector to distinguish successful acquisition, retry waiting and
// rejection without deriving acceptance from later upstream output.

// TestGenericChatAcquireAdmissionTiming exercises selected capacity, waiting and
// rejection through generic Chat acquisition. Provider retries must preserve the
// first admission timestamp and keep subsequent waits separate.
func TestGenericChatAcquireAdmissionTiming(t *testing.T) {
	testProviderAdmissionTiming(t, "/v1/chat/completions", func(base messageAttemptBridge) admissionTimingAttempt {
		bridge := &genericChatAttemptBridge{messageAttemptBridge: base}
		return admissionTimingAttempt{acquire: bridge.Acquire, release: &bridge.providerReleaseFunc}
	})
}

// TestShouldUseAntigravityCompat compares provider platforms and credential
// kinds with an absent target. The compatibility branch must select Antigravity
// OAuth while rejecting setup token, API Key, upstream and unrelated OAuth
// targets.
func TestShouldUseAntigravityCompat(t *testing.T) {
	tests := []struct {
		name     string                             // provider eligibility scenario
		provider *gatewayprovider.ExecutionProvider // selected execution target
		want     bool                               // Antigravity OAuth compatibility expectation
	}{
		{"oauth", &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeOAuth}}, true},
		{"setup token", &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeSetupToken}}, false},
		{"upstream", &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeUpstream}}, false},
		{"api key", &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeAPIKey}}, false},
		{"anthropic oauth", &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldUseAntigravityCompat(tt.provider))
		})
	}
}
