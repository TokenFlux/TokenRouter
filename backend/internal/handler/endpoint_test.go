package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldUseAntigravityCompat(t *testing.T) {
	tests := []struct {
		name    string
		account *gatewayprovider.ExecutionAccount
		want    bool
	}{
		{"oauth", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeOAuth}}, true},
		{"setup token", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeSetupToken}}, false},
		{"upstream", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeUpstream}}, false},
		{"api key", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeAPIKey}}, false},
		{"anthropic oauth", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldUseAntigravityCompat(tt.account))
		})
	}
}

func TestResolveOpenAIUpstreamEndpointPrefersForwardResult(t *testing.T) {
	tests := []struct {
		name            string
		account         *gatewayprovider.ExecutionAccount
		result          *forwardcore.OpenAIResult
		inboundEndpoint string
		runtimeEndpoint string
		want            string
	}{
		{
			name:            "grok raw chat result overrides stale context",
			account:         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
			result:          &forwardcore.OpenAIResult{UpstreamEndpoint: gatewayhttp.EndpointChatCompletions},
			runtimeEndpoint: gatewayhttp.EndpointResponses,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name:    "grok chat bridged to responses",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
			result:  &forwardcore.OpenAIResult{UpstreamEndpoint: gatewayhttp.EndpointResponses},
			want:    gatewayhttp.EndpointResponses,
		},
		{
			name:    "grok empty result keeps responses default",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
			result:  &forwardcore.OpenAIResult{},
			want:    gatewayhttp.EndpointResponses,
		},
		{
			name:            "grok raw error uses runtime endpoint",
			account:         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
			runtimeEndpoint: gatewayhttp.EndpointChatCompletions,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name:    "openai behavior remains responses",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}},
			result:  &forwardcore.OpenAIResult{},
			want:    gatewayhttp.EndpointResponses,
		},
		{
			name:            "openai api key chat attempt records runtime endpoint",
			account:         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}},
			result:          &forwardcore.OpenAIResult{},
			runtimeEndpoint: gatewayhttp.EndpointChatCompletions,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name: "openai api key responses attempt records runtime endpoint",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
				Type:  capability.AccountTypeAPIKey,
				Extra: map[string]any{"openai_text_route_mode": "force_responses"}},
			},
			result:          &forwardcore.OpenAIResult{},
			runtimeEndpoint: gatewayhttp.EndpointResponses,
			want:            gatewayhttp.EndpointResponses,
		},
		{
			name:            "responses fallback records runtime chat endpoint",
			account:         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}},
			result:          &forwardcore.OpenAIResult{},
			inboundEndpoint: gatewayhttp.EndpointResponses,
			runtimeEndpoint: gatewayhttp.EndpointChatCompletions,
			want:            gatewayhttp.EndpointChatCompletions,
		},
		{
			name:            "messages native path records runtime responses endpoint",
			account:         &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}},
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
			require.Equal(t, tt.want, resolveOpenAIUpstreamEndpoint(c, tt.account, tt.result))
		})
	}
}
