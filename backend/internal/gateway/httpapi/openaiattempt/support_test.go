package openaiattempt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Compatible text entries can dispatch through Chat or Responses independently
// of their inbound URL. The fixtures check result/context/default endpoint
// precedence, request cancellation and explicit post-write recovery permission,
// and proxy observations that expose selected connection metadata without
// credential fields.

// TestHandleOpenAISelectionBusinessErrorWritesChatFunctionToolError verifies
// that exhaustion due to unsupported Chat function tools is a local client
// error. The response directs callers to the Responses endpoint.
func TestHandleOpenAISelectionBusinessErrorWritesChatFunctionToolError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	err := apperror.BadRequest(gatewayprovider.NativeChatFunctionToolsUnsupported, "Use /v1/responses instead.")
	require.True(t, (&Support{}).HandleOpenAISelectionBusinessError(c, err, false))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request_error")
	require.Contains(t, recorder.Body.String(), "/v1/responses")
}

// TestResolveOpenAIUpstreamEndpointPrefersForwardResult varies inbound,
// recorded, and returned endpoint evidence across providers. It checks the
// result wins, then attempt context, then the selected platform's default for
// compatible execution.
func TestResolveOpenAIUpstreamEndpointPrefersForwardResult(t *testing.T) {
	tests := []struct {
		name            string                             // endpoint precedence scenario label
		provider        *gatewayprovider.ExecutionProvider // selected provider platform fixture
		result          *forwardcore.OpenAIResult          // returned execution endpoint evidence
		inboundEndpoint string                             // incoming client endpoint path
		runtimeEndpoint string                             // recorded actual attempt endpoint
		want            string                             // expected resolved upstream endpoint
	}{
		{
			name:            "grok raw chat result overrides stale context",
			provider:        &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}},
			result:          &forwardcore.OpenAIResult{UpstreamEndpoint: gatewayhttp.EndpointChatCompletions},
			runtimeEndpoint: gatewayhttp.EndpointResponses,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name:     "grok chat bridged to responses",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}},
			result:   &forwardcore.OpenAIResult{UpstreamEndpoint: gatewayhttp.EndpointResponses},
			want:     gatewayhttp.EndpointResponses,
		},
		{
			name:     "grok empty result keeps responses default",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}},
			result:   &forwardcore.OpenAIResult{},
			want:     gatewayhttp.EndpointResponses,
		},
		{
			name:            "grok raw error uses runtime endpoint",
			provider:        &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}},
			runtimeEndpoint: gatewayhttp.EndpointChatCompletions,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name:     "openai behavior remains responses",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}},
			result:   &forwardcore.OpenAIResult{},
			want:     gatewayhttp.EndpointResponses,
		},
		{
			name:            "openai api key chat attempt records runtime endpoint",
			provider:        &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}},
			result:          &forwardcore.OpenAIResult{},
			runtimeEndpoint: gatewayhttp.EndpointChatCompletions,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name: "openai api key responses attempt records runtime endpoint",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
					Type:  capability.ProviderTypeAPIKey,
					Extra: map[string]any{"openai_text_route_mode": "force_responses"},
				},
			},
			result:          &forwardcore.OpenAIResult{},
			runtimeEndpoint: gatewayhttp.EndpointResponses,
			want:            gatewayhttp.EndpointResponses,
		},
		{
			name:            "responses fallback records runtime chat endpoint",
			provider:        &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}},
			result:          &forwardcore.OpenAIResult{},
			inboundEndpoint: gatewayhttp.EndpointResponses,
			runtimeEndpoint: gatewayhttp.EndpointChatCompletions,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name:            "messages native path records runtime responses endpoint",
			provider:        &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}},
			result:          &forwardcore.OpenAIResult{},
			inboundEndpoint: gatewayhttp.EndpointMessages,
			runtimeEndpoint: gatewayhttp.EndpointResponses,
			want:            gatewayhttp.EndpointResponses,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			inboundEndpoint := tt.inboundEndpoint
			if inboundEndpoint == "" {
				inboundEndpoint = gatewayhttp.EndpointChatCompletions
			}
			c.Request = httptest.NewRequest(http.MethodPost, inboundEndpoint, nil)
			c.Set("_gateway_inbound_endpoint", inboundEndpoint)
			gatewayhttp.SetActualOpenAIUpstreamEndpoint(c, tt.runtimeEndpoint)
			require.Equal(t, tt.want, ResolveOpenAIUpstreamEndpoint(c, tt.provider, tt.result))
		})
	}
}

// TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite writes and flushes an
// SSE comment before checking recovery. It verifies the explicit
// SafeToFailoverAfterWrite flag permits replay after changed output size, while
// its absence rejects that path.
func TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	before := gatewayhttp.HeartbeatAdjustedWrittenSize(c)

	_, err := fmt.Fprint(c.Writer, ":\n\n")
	require.NoError(t, err)
	c.Writer.Flush()

	require.True(t, OpenAIForwardMayFailover(c, before, &forwardcore.UpstreamFailoverError{
		SafeToFailoverAfterWrite: true,
	}))
	require.False(t, OpenAIForwardMayFailover(c, before, &forwardcore.UpstreamFailoverError{}))
}

// TestOpenAIRequestAllowsFailoverReplayStopsCanceledClient supplies missing and
// active request contexts, then cancels the active one. It checks missing input
// rejects replay and cancellation changes the active request from allowed to
// denied.
func TestOpenAIRequestAllowsFailoverReplayStopsCanceledClient(t *testing.T) {
	require.False(t, OpenAIRequestAllowsFailoverReplay(nil))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestCtx)

	require.True(t, OpenAIRequestAllowsFailoverReplay(c))
	// End the request before another replay check, isolating cancellation
	// from output-size evidence so no disconnected client can authorize an
	// additional upstream attempt.
	cancel()
	require.False(t, OpenAIRequestAllowsFailoverReplay(c))
}

// TestAppendOpenAIProviderProxyLogFields logs a provider with a loaded
// credentialed proxy. It checks selected ID/name/host/port are present and
// username, password, and URL fields are absent from the captured observation.
func TestAppendOpenAIProviderProxyLogFields(t *testing.T) {
	proxyID := int64(17)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ProxyID: &proxyID,
			Proxy: &egress.Proxy{
				ID:       proxyID,
				Name:     "openai-egress",
				Host:     "proxy.example.com",
				Port:     8443,
				Username: "proxy-user-secret",
				Password: "proxy-password-secret",
			},
		},
	}
	core, logs := observer.New(zap.WarnLevel)
	log := zap.New(core)

	log.Warn("openai.websocket_proxy_failed", AppendOpenAIProviderProxyLogFields(nil, provider)...)

	entries := logs.All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.EqualValues(t, proxyID, fields["proxy_id"])
	require.Equal(t, "openai-egress", fields["proxy_name"])
	require.Equal(t, "proxy.example.com", fields["proxy_host"])
	require.EqualValues(t, 8443, fields["proxy_port"])
	require.NotContains(t, fields, "proxy_username")
	require.NotContains(t, fields, "proxy_password")
	require.NotContains(t, fields, "proxy_url")
}

// TestAppendOpenAIProviderProxyLogFields_FallsBackToProxyID supplies a provider
// with only its proxy reference loaded. It checks the log contains that
// identifier as the sole proxy field for the partial record.
func TestAppendOpenAIProviderProxyLogFields_FallsBackToProxyID(t *testing.T) {
	proxyID := int64(23)
	core, logs := observer.New(zap.WarnLevel)
	log := zap.New(core)

	log.Warn("openai.websocket_proxy_failed", AppendOpenAIProviderProxyLogFields(nil, &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ProxyID: &proxyID}})...)

	entries := logs.All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.EqualValues(t, proxyID, fields["proxy_id"])
	require.Len(t, fields, 1)
}
