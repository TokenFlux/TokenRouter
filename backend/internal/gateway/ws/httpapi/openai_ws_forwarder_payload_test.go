package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestBuildOpenAIWSHeadersSendsOAuthRoutingHintOnly builds OAuth and API Key
// WS handshakes with several tiers. OAuth hints use the final model and
// canonical priority or ultrafast tier, while API Key headers omit the hint.
func TestBuildOpenAIWSHeadersSendsOAuthRoutingHintOnly(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	svc := newWSFixture(wsFixtureInputs{})
	decision := egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}

	build := func(t *testing.T, provider *gatewayprovider.ExecutionProvider, tier string) http.Header {
		headers, _, err := svc.buildOpenAIWSHeaders(
			context.Background(),
			c,
			provider,
			"test-token",
			decision,
			true,
			"",
			"",
			"",
			"gpt-5.6-codex",
			tier,
		)
		require.NoError(t, err)
		return headers
	}

	oauthProvider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
			Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"chatgpt_account_id": "test-provider",
			},
		},
	}
	require.Equal(t, "model=gpt-5.6-codex;tier=priority", build(t, oauthProvider, "fast").Get("x-codex-routing-hint"))
	require.Equal(t, "model=gpt-5.6-codex;tier=ultrafast", build(t, oauthProvider, "ultrafast").Get("x-codex-routing-hint"))
	require.Equal(t, "model=gpt-5.6-codex", build(t, oauthProvider, "default").Get("x-codex-routing-hint"))
	require.Empty(t, build(t, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}}, "priority").Get("x-codex-routing-hint"))
}

// TestOpenAIRoutingDiagnosticsUseFinalDerivedValuesOnly injects caller
// credentials and a forged routing hint into HTTP and WS preparation. Captured
// diagnostics contain the derived model, canonical tier, transport and
// affinity labels, with none of those supplied credential fields.
func TestOpenAIRoutingDiagnosticsUseFinalDerivedValuesOnly(t *testing.T) {
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 917,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"chatgpt_account_id": "chatgpt-provider",
			},
		},
	}
	body := []byte(`{"model":"gpt-5.6-codex","service_tier":"fast"}`)
	svc := newWSFixture(wsFixtureInputs{})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Authorization", "Bearer caller-secret")
	c.Request.Header.Set("x-codex-routing-hint", "model=caller-secret")
	_, err := svc.Requests.Build(context.Background(), c, provider, body, "oauth-secret", false, "", true)
	require.NoError(t, err)

	decision := egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}
	_, _, err = svc.buildOpenAIWSHeaders(
		context.Background(), c, provider, "oauth-secret", decision, true,
		"", "", "", "gpt-5.6-codex", "fast",
	)
	require.NoError(t, err)

	require.True(t, logSink.ContainsMessageAtLevel("openai routing decision", "debug"))
	require.True(t, logSink.ContainsFieldValue("provider_id", "917"))
	require.True(t, logSink.ContainsFieldValue("final_model", "gpt-5.6-codex"))
	require.True(t, logSink.ContainsFieldValue("final_service_tier", "priority"))
	require.True(t, logSink.ContainsFieldValue("routing_hint_generated", "true"))
	require.True(t, logSink.ContainsFieldValue("transport", "http"))
	require.True(t, logSink.ContainsFieldValue("transport", string(egress.OpenAIUpstreamTransportResponsesWebsocketV2)))
	require.True(t, logSink.ContainsFieldValue("ws_affinity_decision", "not_applicable"))
	require.True(t, logSink.ContainsFieldValue("ws_affinity_decision", "soft_routing_hint"))
	require.False(t, logSink.ContainsFieldValue("authorization", "caller-secret"))
	require.False(t, logSink.ContainsFieldValue("credentials", "oauth-secret"))
	require.False(t, logSink.ContainsFieldValue("routing_hint", "caller-secret"))
}

// TestValidateOpenAIWSBearerTokenAllowsAgentIdentityWithoutStoredToken
// compares assertion-authenticated Agent Identity with bearer OAuth, PAT and
// API Key providers. Only Agent Identity accepts an empty token for dial-time
// authentication.
func TestValidateOpenAIWSBearerTokenAllowsAgentIdentityWithoutStoredToken(t *testing.T) {
	t.Run("Given Agent Identity When a WS path receives no bearer token Then dial-time assertion auth is allowed", func(t *testing.T) {
		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
				Type: capability.ProviderTypeOAuth,
				Credentials: map[string]any{
					"auth_mode": providercore.OpenAIAuthModeAgentIdentity,
				},
			},
		}

		require.NoError(t, validateOpenAIWSBearerToken(provider, ""))
	})

	t.Run("Given bearer credentials When a WS path receives no token Then the request is rejected", func(t *testing.T) {
		providers := []*gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}},
			{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Credentials: map[string]any{"auth_mode": providercore.OpenAIAuthModePersonalAccessToken}}},
			{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}},
		}

		for _, provider := range providers {
			require.EqualError(t, validateOpenAIWSBearerToken(provider, ""), "token is empty")
		}
	})
}

// TestBuildOpenAIWSHeadersNamespacesCodexIdentityByOAuthProvider sends the
// same client identity through two OAuth providers. Repeated builds for one
// provider match, different providers derive different identities, and HTTP
// and WS derive the same session header for one provider.
func TestBuildOpenAIWSHeadersNamespacesCodexIdentityByOAuthProvider(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("api_key_id", int64(77))
	c.Request.Header.Set("x-codex-installation-id", "client-installation")
	c.Request.Header.Set("thread-id", "client-thread")
	c.Request.Header.Set("x-codex-window-id", "client-window")
	c.Request.Header.Set("x-client-request-id", "client-request")

	provider11 := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-provider-11"}}}
	provider19 := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 19, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-provider-19"}}}
	service := newWSFixture(wsFixtureInputs{})
	build := func(provider *gatewayprovider.ExecutionProvider) http.Header {
		headers, _, err := service.buildOpenAIWSHeaders(
			context.Background(), c, provider, "token", egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "client-session", "", "",
		)
		require.NoError(t, err)
		return headers
	}

	first := build(provider11)
	firstAgain := build(provider11)
	second := build(provider19)
	for _, header := range []string{"session_id", "x-codex-installation-id", "thread-id", "x-codex-window-id", "x-client-request-id"} {
		require.NotEmpty(t, first.Get(header), header)
		require.Equal(t, first.Get(header), firstAgain.Get(header), header)
		require.NotEqual(t, first.Get(header), second.Get(header), header)
	}

	httpRequest, err := service.Requests.Build(
		context.Background(), c, provider11,
		[]byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`),
		"token", true, "client-session", true,
	)
	require.NoError(t, err)
	require.Equal(t, httpRequest.Header.Get("session_id"), first.Get("session_id"), "HTTP and WS must derive the same identity from the raw client key")
}

// TestOpenAISetupTokenWSCompatibility builds an OpenAI setup-token WS endpoint
// and handshake alongside a foreign setup-token endpoint. It checks Codex
// authentication and session scoping, the foreign platform URL, and disabled-
// store handling even when the payload requests storage.
func TestOpenAISetupTokenWSCompatibility(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	c.Request.Header.Set("session_id", "session-one")
	c.Set("api_key", &apikey.APIKey{ID: 17})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
			Type: capability.ProviderTypeSetupToken,
			Credentials: map[string]any{
				"access_token":       "setup-token-value",
				"chatgpt_account_id": "chatgpt-setup",
			},
		},
	}
	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{}})

	wsURL, err := svc.buildOpenAIResponsesWSURL(provider)
	require.NoError(t, err)
	require.Equal(t, "wss://chatgpt.com/backend-api/codex/responses", wsURL)
	foreignURL, err := svc.buildOpenAIResponsesWSURL(&gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeSetupToken}})
	require.NoError(t, err)
	require.Equal(t, "wss://api.openai.com/v1/responses", foreignURL)

	headers, session, err := svc.buildOpenAIWSHeaders(
		context.Background(), c, provider, "setup-token-value", egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "gpt-5.1-codex", "",
	)
	require.NoError(t, err)
	require.Equal(t, "Bearer setup-token-value", headers.Get("authorization"))
	require.Equal(t, "chatgpt-setup", headers.Get("chatgpt-account-id"))
	require.NotEmpty(t, headers.Get("originator"))
	require.Equal(t, "session-one", session.SessionID)
	require.NotEqual(t, session.SessionID, headers.Get("session_id"))

	require.True(t, svc.isOpenAIWSStoreDisabledInRequestRaw([]byte(`{"store":true}`), provider))
}

// TestWSOutboundEffortStripPreservesPreStripPayload exercises wsPassthroughAdapter
// ModelAttributes and StripOutboundEffort on a provider configured with
// effort_model_mapping and effort_model_mapping_strip_effort. Preparation must
// retain reasoning.effort for usage capture and failover snapshots, while
// StripOutboundEffort removes it only on the returned outbound wire copy.
func TestWSOutboundEffortStripPreservesPreStripPayload(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           77,
			Platform:     capability.PlatformOpenAI,
			Type:         capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key": "ws-key",
				providercore.EffortModelMappingCredentialKey: map[string]any{
					"claude-opus-5-5": map[string]any{"high": "claude-opus-5-5-high"},
				},
				providercore.EffortModelMappingStripEffortCredentialKey: true,
			},
		},
	}
	adapter := &wsPassthroughAdapter{
		service:  newWSFixture(wsFixtureInputs{}).OpenAIWebSocketExecutor,
		provider: provider,
	}
	payload := []byte(`{"type":"response.create","model":"claude-opus-5-5-high","reasoning":{"effort":"high"}}`)

	prepared, err := adapter.ModelAttributes(context.Background(), 1, "claude-opus-5-5-high", payload)
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(prepared, "reasoning.effort").String(), "ModelAttributes must preserve reasoning.effort for usage and failover")

	wire := adapter.StripOutboundEffort(context.Background(), "claude-opus-5-5", prepared)
	require.False(t, gjson.GetBytes(wire, "reasoning").Exists(), "StripOutboundEffort must remove reasoning.effort on matched effort rule")
	require.Equal(t, "high", gjson.GetBytes(prepared, "reasoning.effort").String(), "StripOutboundEffort must not mutate the pre-strip payload slice")
}
