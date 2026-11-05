package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// gateway_opencode_continuation_scenario_test.go exercises public Responses
// admission, responses_attempts.go, continuation selection, and transform.go.
// Two HTTP turns use the production response-owner and provider-affinity store
// so the second request can prove both authorization and upstream reference retention.

// TestOpenCodePublicResponsesContinuation creates response lineage on its first
// public request. The second turn retains the upstream identifier only for an
// owned, eligible provider binding, in both basic and advanced scheduler modes.
func TestOpenCodePublicResponsesContinuation(t *testing.T) {
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		for _, scenario := range []string{"owned", "unknown", "foreign owner", "unavailable provider", "missing affinity", "converted chat", "converted messages", "converted gemini"} {
			t.Run(string(mode)+"/"+scenario, func(t *testing.T) {
				observed := make(chan openCodeHTTPExchange, 2)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					// Record copied request evidence before returning so each completed public
					// call can inspect its upstream payload without racing the server goroutine.
					observed <- openCodeHTTPExchange{path: r.URL.Path, headers: r.Header.Clone(), body: body}
					id := "resp_first"
					if gjson.GetBytes(body, "previous_response_id").String() != "" {
						id = "resp_second"
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"id":%q,"object":"response","status":"completed","model":"accepted-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"continued"}]}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}`, id)
				}))
				t.Cleanup(upstream.Close)
				groupID := int64(42)
				group := &routing.Group{ID: groupID, Hydrated: true, Name: "OpenCode", Status: "active", RateMultiplier: 1, SchedulerType: mode, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, ProtocolFallbacks: map[protocol.ProtocolID][]protocol.ProtocolID{protocol.ProtocolOpenAIResponses: {protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolAnthropicMessages, protocol.ProtocolGeminiGenerateContent}}}
				cfg := &config.Config{}
				cfg.Billing.FallbackRateMultiplier = 1
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.Failover.MaxProviderSwitches = 2
				record := &provider.Record{
					ID: 31, Name: "OpenCode", Platform: provider.PlatformOpenCode,
					Type: provider.ProviderTypeAPIKey, Status: "active", Schedulable: true,
					Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID},
					Credentials: map[string]any{
						"api_key": "upstream-secret", "base_url": upstream.URL,
						"upstream_protocols":               []string{string(protocol.ProtocolOpenAIResponses)},
						"model_mapping":                    map[string]any{"gpt-5.4": "accepted-model"},
						provider.OpenCodeModelProtocolsKey: map[string]any{"payg": map[string]any{"accepted-model": string(protocol.ProtocolOpenAIResponses)}},
					},
				}
				providers := &mixedHTTPProviders{values: []gatewayprovider.ExecutionProvider{*gatewayprovider.NewExecutionProvider(record)}}
				transport := &mixedHTTPTransport{}
				source, choices, credentials := newOpenAIExecutionAndSelectionFixture(providers, nil, cfg, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				logs := &gatewaytestkit.UsageLogStore{Inserted: true}
				funds := &gatewaytestkit.SettlementStore{}
				recording := gatewaytestkit.NewRecording(logs, funds, nil, false)
				recording.Options.DefaultMultiplier = 1
				source.Recorder = recording.Core(nil, true)
				user := &identity.User{ID: 7, Balance: 100, Status: "active"}
				keys := testkit.NewService(&openCodeHTTPKeys{key: &apikey.APIKey{ID: 8, Key: "client-secret", Status: "active", UserID: user.ID, User: user, GroupID: &groupID, Group: group, BillingMode: "balance"}}, nil, nil, nil, nil, nil, cfg)
				t.Cleanup(keys.Stop)
				eligibility := newBillingEligibilityFixture(cfg)
				t.Cleanup(eligibility.Stop)
				resources := provideOpenAIHTTPResources(scheduler.NewConcurrencyService(nil), cfg)
				endpoints := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Source: source, Credentials: credentials, Choices: choices, Config: cfg, Funding: newFundingAdmissionFixture(eligibility, cfg), Keys: keys, Concurrency: resources.Concurrency, MaxSwitches: 2})
				auth := provideAPIKeyAuth(keys, nil, cfg)
				guards := gatewayhttp.NewRouteGuards(legacyRouteMiddleware(auth, keys, nil, nil, nil, cfg))
				router := gin.New()
				router.POST("/v1/responses", gin.HandlerFunc(auth), guards.RequireGroupClientProtocol(protocol.ProtocolOpenAIResponses, gatewayhttp.GroupClientProtocolErrorOpenAI), endpoints.Responses)
				send := func(body string) *httptest.ResponseRecorder {
					request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer client-secret")
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					return response
				}
				first := send(`{"model":"gpt-5.4","input":"hello","store":true,"stream":false}`)
				require.Equal(t, http.StatusOK, first.Code, first.Body.String())
				require.Equal(t, "resp_first", gjson.GetBytes(first.Body.Bytes(), "id").String())
				firstExchange := <-observed
				require.True(t, gjson.GetBytes(firstExchange.body, "store").Bool())
				previous := "resp_first"
				store := source.Responses.Lineage.Store
				switch scenario {
				case "unknown":
					previous = "resp_unknown"
				case "foreign owner":
					require.NoError(t, store.BindHTTPResponseOwner(context.Background(), groupID, previous, 99, 98, time.Hour))
				case "unavailable provider":
					providers.values[0].Record.Schedulable = false
				case "missing affinity":
					require.NoError(t, store.DeleteResponseProvider(context.Background(), groupID, previous))
				case "converted chat", "converted messages", "converted gemini":
					native := protocol.ProtocolOpenAIChatCompletions
					if scenario == "converted messages" {
						native = protocol.ProtocolAnthropicMessages
					}
					if scenario == "converted gemini" {
						native = protocol.ProtocolGeminiGenerateContent
					}
					providers.values[0].Record.Credentials["upstream_protocols"] = []string{string(native)}
					providers.values[0].Record.Credentials[provider.OpenCodeModelProtocolsKey] = map[string]any{"payg": map[string]any{"accepted-model": string(native)}}
				}
				// Add another eligible credential only after the first response has established
				// its source, so rejected continuations cannot pass by selecting this alternative.
				alternative := provider.CloneRecord(record)
				alternative.ID = 32
				alternative.Platform = provider.PlatformOpenAI
				alternative.Credentials["api_key"] = "other-upstream-secret"
				providers.values = append(providers.values, *gatewayprovider.NewExecutionProvider(alternative))
				second := send(fmt.Sprintf(`{"model":"gpt-5.4","input":"continue","previous_response_id":%q,"store":true,"stream":false}`, previous))
				if scenario != "owned" {
					require.GreaterOrEqual(t, second.Code, http.StatusBadRequest, second.Body.String())
					require.Equal(t, []int64{record.ID}, transport.calls())
					require.Empty(t, observed)
					require.Equal(t, 1, funds.Calls)
					return
				}
				require.Equal(t, http.StatusOK, second.Code, second.Body.String())
				exchange := <-observed
				require.Equal(t, "/openai/v1/responses", exchange.path)
				require.Equal(t, previous, gjson.GetBytes(exchange.body, "previous_response_id").String())
				require.True(t, gjson.GetBytes(exchange.body, "store").Bool())
				require.Equal(t, "Bearer upstream-secret", exchange.headers.Get("Authorization"))
				require.Equal(t, []int64{record.ID, record.ID}, transport.calls())
				require.Equal(t, 2, funds.Calls)
			})
		}
	}
}
