package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// gateway_opencode_http_scenario_test.go exercises application authorization,
// gateway_openai_attempts.go selection bindings, unified text execution, and
// completion recording against a local OpenCode upstream. Each public text
// entry resolves its configured model through production selection before
// transport can observe credentials or return billable usage.

// TestOpenCodePublicTextHTTP exercises authenticated public text requests through
// production selection and completion recording. Missing authentication, disabled
// groups, and disabled protocols must reject before transport or settlement.
// Native Responses requests retain their client output cap and storage choice.
func TestOpenCodePublicTextHTTP(t *testing.T) {
	for _, endpoint := range []struct {
		path     string              // public client entry path
		protocol protocol.ProtocolID // selected native protocol identity
		upstream string              // expected upstream endpoint path
	}{
		{"/v1/messages", protocol.ProtocolAnthropicMessages, "/anthropic/v1/messages"},
		{"/v1/responses", protocol.ProtocolOpenAIResponses, "/openai/v1/responses"},
		{"/v1/chat/completions", protocol.ProtocolOpenAIChatCompletions, "/openai/v1/chat/completions"},
	} {
		for _, scenario := range []string{"allowed", "missing authentication", "disabled group", "disabled client protocol", "disabled provider protocol"} {
			t.Run(string(endpoint.protocol)+"/"+scenario, func(t *testing.T) {
				observed := make(chan openCodeHTTPExchange, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					// Publish the complete request before responding so handler return
					// establishes an observable exchange without sharing mutable request
					// storage. Reject repeats instead of blocking the server before the
					// outer assertion can report the unexpected transport call count.
					select {
					case observed <- openCodeHTTPExchange{path: r.URL.Path, headers: r.Header.Clone(), body: body}:
					default:
						http.Error(w, "unexpected repeated upstream request", http.StatusBadGateway)
						return
					}
					r.Body = io.NopCloser(strings.NewReader(string(body)))
					mixedUpstreamResponse(w, r, false)
				}))
				t.Cleanup(upstream.Close)
				groupID := int64(42)
				group := &routing.Group{
					ID: groupID, Hydrated: true, Name: "OpenCode", Status: "active",
					RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{endpoint.protocol},
				}
				if scenario == "disabled group" {
					group.Status = "disabled"
				}
				if scenario == "disabled client protocol" {
					group.AllowedProtocols = []protocol.ProtocolID{}
				}
				cfg := &config.Config{}
				cfg.Billing.FallbackRateMultiplier = 1
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.Failover.MaxProviderSwitches = 1
				record := &provider.Record{
					ID: 31, Name: "OpenCode", Platform: provider.PlatformOpenCode,
					Type: provider.ProviderTypeAPIKey, Status: "active", Schedulable: true,
					Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID},
					Credentials: map[string]any{
						"api_key": "upstream-secret", "base_url": upstream.URL, "provider_mode": "go",
						"upstream_protocols": []string{string(endpoint.protocol)},
						"model_mapping": map[string]any{
							"gpt-5.4": "accepted-model", "accepted-model": "wrong-second-hop",
						},
						provider.OpenCodeCatalogProtocolsKey: map[string]any{
							"go": map[string]any{"accepted-model": string(endpoint.protocol)},
						},
					},
				}
				if scenario == "disabled provider protocol" {
					record.Credentials["upstream_protocols"] = []string{}
				}
				providers := &mixedHTTPProviders{values: []gatewayprovider.ExecutionProvider{*gatewayprovider.NewExecutionProvider(record)}}
				transport := &mixedHTTPTransport{}
				source, choices, credentials := newOpenAIExecutionAndSelectionFixture(
					providers, nil, cfg, nil, nil, nil, transport, nil, nil, nil,
					nil, nil, nil, nil, nil, nil, nil, nil, nil,
				)
				logs := &gatewaytestkit.UsageLogStore{Inserted: true}
				funds := &gatewaytestkit.SettlementStore{}
				recording := gatewaytestkit.NewRecording(logs, funds, nil, false)
				recording.Options.DefaultMultiplier = 1
				source.Recorder = recording.Core(nil, true)
				user := &identity.User{ID: 7, Balance: 100, Status: "active"}
				keys := testkit.NewService(&openCodeHTTPKeys{key: &apikey.APIKey{
					ID: 8, Key: "client-secret", Status: "active", UserID: user.ID,
					User: user, GroupID: &groupID, Group: group, BillingMode: "balance",
				}}, nil, nil, nil, nil, nil, cfg)
				t.Cleanup(keys.Stop)
				eligibility := newBillingEligibilityFixture(cfg)
				t.Cleanup(eligibility.Stop)
				resources := provideOpenAIHTTPResources(scheduler.NewConcurrencyService(nil), cfg)
				endpoints := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
					Source: source, Credentials: credentials, Choices: choices, Config: cfg,
					Funding: newFundingAdmissionFixture(eligibility, cfg), Keys: keys,
					Concurrency: resources.Concurrency, MaxSwitches: 1,
				})
				auth := provideAPIKeyAuth(keys, nil, cfg)
				guards := gatewayhttp.NewRouteGuards(legacyRouteMiddleware(auth, keys, nil, nil, nil, cfg))
				format := gatewayhttp.GroupClientProtocolErrorOpenAI
				handler := endpoints.ChatCompletions
				if endpoint.protocol == protocol.ProtocolAnthropicMessages {
					format = gatewayhttp.GroupClientProtocolErrorAnthropic
					handler = endpoints.Messages
				}
				if endpoint.protocol == protocol.ProtocolOpenAIResponses {
					handler = endpoints.Responses
				}
				router := gin.New()
				router.POST(endpoint.path, gin.HandlerFunc(auth), guards.RequireGroupClientProtocol(endpoint.protocol, format), handler)
				body := `{"model":"gpt-5.4","max_tokens":20,"messages":[{"role":"user","content":"hello"}],"stream":false}`
				if endpoint.protocol == protocol.ProtocolOpenAIResponses {
					body = `{"model":"gpt-5.4","input":"hello","max_output_tokens":20,"store":true,"stream":false}`
				}
				request := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("User-Agent", "OpenCodeAcceptance/1")
				request.Header.Set("X-OpenCode-Session", "acceptance-session")
				if scenario != "missing authentication" {
					request.Header.Set("Authorization", "Bearer client-secret")
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if scenario != "allowed" {
					expected := http.StatusForbidden
					if scenario == "missing authentication" {
						expected = http.StatusUnauthorized
					}
					if scenario == "disabled provider protocol" {
						require.GreaterOrEqual(t, response.Code, 400, response.Body.String())
					} else {
						require.Equal(t, expected, response.Code, response.Body.String())
					}
					require.Empty(t, transport.calls())
					require.Empty(t, observed)
					require.Zero(t, funds.Calls)
					require.Zero(t, logs.Calls)
					return
				}
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "ok-mixed")
				require.Equal(t, []int64{record.ID}, transport.calls())
				exchange := <-observed
				require.Equal(t, endpoint.upstream, exchange.path)
				require.Equal(t, "accepted-model", gjson.GetBytes(exchange.body, "model").String())
				require.NotContains(t, string(exchange.body), "wrong-second-hop")
				require.Equal(t, "Bearer upstream-secret", exchange.headers.Get("Authorization"))
				require.Empty(t, exchange.headers.Get("X-Api-Key"))
				require.Empty(t, exchange.headers.Get("X-Goog-Api-Key"))
				require.Equal(t, "OpenCodeAcceptance/1", exchange.headers.Get("User-Agent"))
				require.Equal(t, "acceptance-session", exchange.headers.Get("X-OpenCode-Session"))
				if endpoint.protocol == protocol.ProtocolOpenAIResponses {
					require.True(t, gjson.GetBytes(exchange.body, "store").Bool())
					require.Equal(t, int64(20), gjson.GetBytes(exchange.body, "max_output_tokens").Int(), string(exchange.body))
				}
				require.Equal(t, 1, funds.Calls)
				require.Equal(t, 1, logs.Calls)
				require.Equal(t, record.ID, logs.LastLog.ProviderID)
				require.Equal(t, provider.PlatformOpenCode, logs.LastLog.Platform)
				require.Equal(t, 10, logs.LastLog.InputTokens)
				require.Equal(t, 3, logs.LastLog.OutputTokens)
			})
		}
	}
}
