package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// testing_protocol_test.go checks routed test admission and operation selection.
// Provider snapshots distinguish gateway routes from the endpoints implemented
// by test executors, while a recording target observes the validated route.

// TestGroupProbeProtocolRoutes covers native and converted entries across test platforms.
// Unsupported endpoints and disabled native protocols fail before target execution can occur.
func TestGroupProbeProtocolRoutes(t *testing.T) {
	for _, tc := range []struct {
		name     string                // route scenario name
		platform string                // provider platform identity
		kind     string                // provider credential type
		client   capability.ProtocolID // selected client entry
		upstream capability.ProtocolID // resolved upstream endpoint
		allowed  bool                  // expected executor admission
	}{
		{"messages native", PlatformAnthropic, ProviderTypeAPIKey, capability.ProtocolAnthropicMessages, capability.ProtocolAnthropicMessages, true},
		{"messages converted", PlatformOpenAI, ProviderTypeAPIKey, capability.ProtocolAnthropicMessages, capability.ProtocolOpenAIChatCompletions, true},
		{"responses", PlatformOpenAI, ProviderTypeOAuth, capability.ProtocolOpenAIResponses, capability.ProtocolOpenAIResponses, true},
		{"chat to gemini", PlatformGemini, ProviderTypeAPIKey, capability.ProtocolOpenAIChatCompletions, capability.ProtocolGeminiGenerateContent, true},
		{"gemini", PlatformGemini, ProviderTypeAPIKey, capability.ProtocolGeminiGenerateContent, capability.ProtocolGeminiGenerateContent, true},
		{"images", PlatformOpenAI, ProviderTypeAPIKey, capability.ProtocolImagesGenerations, capability.ProtocolImagesGenerations, true},
		{"oauth images", PlatformOpenAI, ProviderTypeOAuth, capability.ProtocolImagesGenerations, capability.ProtocolOpenAIResponses, true},
		{"systemone", PlatformJev, ProviderTypeAPIKey, capability.ProtocolSystemOne, capability.ProtocolSystemOne, true},
		{"qoder conversion", PlatformQoder, ProviderTypeCosy, capability.ProtocolOpenAIResponses, capability.ProtocolQoderChat, true},
		{"grok chat lacks tester", PlatformGrok, ProviderTypeAPIKey, capability.ProtocolOpenAIChatCompletions, capability.ProtocolOpenAIChatCompletions, false},
		{"compact", PlatformOpenAI, ProviderTypeAPIKey, capability.ProtocolResponsesCompact, capability.ProtocolResponsesCompact, false},
		{"embeddings", PlatformOpenAI, ProviderTypeAPIKey, capability.ProtocolEmbeddings, capability.ProtocolEmbeddings, false},
		{"unknown", PlatformOpenAI, ProviderTypeAPIKey, "unknown", capability.ProtocolOpenAIResponses, false},
		{"partial route", PlatformOpenAI, ProviderTypeAPIKey, capability.ProtocolOpenAIResponses, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := ProviderSnapshot{Platform: tc.platform, Type: tc.kind, EnabledProtocols: []capability.ProtocolID{tc.upstream}}
			route := TestProtocolRoute{ClientProtocol: tc.client, UpstreamProtocol: tc.upstream}
			require.Equal(t, tc.allowed, route.SupportsProvider(snapshot, ""))
			executed := false
			loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{ProviderSnapshot: snapshot}, run: func(ctx context.Context, request PreparedTestRequest, _ TestEventSink) error {
				executed = true
				require.Equal(t, route, TestProtocolRouteFromContext(ctx))
				require.Equal(t, route, request.ProtocolRoute)
				return nil
			}}}
			err := NewTestService(loader, TestOptions{}).Test(t.Context(), TestRequest{ProtocolRoute: route}, &testSinkStub{})
			require.Equal(t, tc.allowed, err == nil)
			require.Equal(t, tc.allowed, executed)
			snapshot.EnabledProtocols = nil
			require.False(t, route.SupportsProvider(snapshot, ""))
		})
	}
}

// TestGroupProbeOpenCodeRoutesFollowModelProtocol routes Chat and Gemini
// entries to an OpenCode provider whose aliased model declares Gemini. Only the
// declared protocol is testable, including through conversion, while unknown
// models and other enabled protocols fail before target execution.
func TestGroupProbeOpenCodeRoutesFollowModelProtocol(t *testing.T) {
	record := &Record{Platform: PlatformOpenCode, Type: ProviderTypeAPIKey, Credentials: map[string]any{
		"model_mapping":           map[string]any{"alias": "flash"},
		OpenCodeModelProtocolsKey: map[string]any{"payg": map[string]any{"flash": "gemini_generate_content"}},
	}}
	snapshot := record.RoutingSnapshot()
	for _, tc := range []struct {
		name     string                // route scenario name
		model    string                // model sent before provider mapping
		client   capability.ProtocolID // selected client entry
		upstream capability.ProtocolID // resolved upstream endpoint
		allowed  bool                  // expected executor admission
	}{
		{"native gemini", "alias", capability.ProtocolGeminiGenerateContent, capability.ProtocolGeminiGenerateContent, true},
		{"chat to gemini", "alias", capability.ProtocolOpenAIChatCompletions, capability.ProtocolGeminiGenerateContent, true},
		{"chat endpoint", "alias", capability.ProtocolOpenAIChatCompletions, capability.ProtocolOpenAIChatCompletions, false},
		{"unknown model", "missing", capability.ProtocolGeminiGenerateContent, capability.ProtocolGeminiGenerateContent, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route := TestProtocolRoute{ClientProtocol: tc.client, UpstreamProtocol: tc.upstream}
			require.Equal(t, tc.allowed, route.SupportsProvider(snapshot, tc.model))
			executed := false
			loader := &testLoaderStub{target: testTargetStub{info: TestTargetInfo{ProviderSnapshot: snapshot}, run: func(_ context.Context, request PreparedTestRequest, _ TestEventSink) error {
				executed = true
				require.Equal(t, TestRouteDefault, request.Route)
				return nil
			}}}
			err := NewTestService(loader, TestOptions{}).Test(t.Context(), TestRequest{Model: tc.model, ProtocolRoute: route}, &testSinkStub{})
			require.Equal(t, tc.allowed, err == nil)
			require.Equal(t, tc.allowed, executed)
		})
	}
}
