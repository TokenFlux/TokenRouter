package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keytestkit "github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// gateway_opencode_native_scenario_test.go exercises gateway_google.go,
// gateway_messages_http.go, gateway_media_http.go, and gateway_systemone.go through
// their public native HTTP handlers. Production authorization, protocol admission,
// provider selection, forwarding, and completion connect an in-memory provider
// repository to a local upstream with independently observed request evidence.

// TestOpenCodePublicNativeHTTP sends Gemini and SystemOne requests through their
// assembled public handlers. Admission failures must stop upstream execution and
// settlement, while accepted requests preserve the selected model and platform.
// A transport-local deadline with a live caller must fail over before committing
// output and charge only the provider that returned a successful answer.
func TestOpenCodePublicNativeHTTP(t *testing.T) {
	for _, endpoint := range []struct {
		protocol protocol.ProtocolID // native request protocol
		path     string              // public request path
		upstream string              // expected upstream path
		body     string              // client request JSON
		response string              // upstream response JSON
	}{
		{
			protocol: protocol.ProtocolGeminiGenerateContent,
			path:     "/v1beta/models/client-alias:generateContent",
			upstream: "/google/v1beta/models/accepted-model:generateContent",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			response: `{"modelVersion":"accepted-model",
				"candidates":[{"content":{"role":"model","parts":[{"text":"native-answer"}]},"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}`,
		},
		{
			protocol: protocol.ProtocolSystemOne,
			path:     "/v1/systemone",
			upstream: "/systemone/v1/systemone",
			body: `{"model":"client-alias","state":"ready",
				"questions":{"ready":{"type":"noul","instructions":"Is it ready?"}}}`,
			response: `{"model":"accepted-model","answers":{"ready":{"type":"noul","noul":1}},
				"usage":{"input_tokens":10,"output_tokens":3}}`,
		},
	} {
		for _, scenario := range []string{
			"allowed", "missing authentication", "disabled group",
			"disabled client protocol", "disabled provider protocol", "unknown provider model", "upstream timeout",
		} {
			t.Run(string(endpoint.protocol)+"/"+scenario, func(t *testing.T) {
				observed := make(chan openCodeHTTPExchange, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					// Publish copied request evidence before responding so assertions
					// never read request-owned headers or buffers while transport can modify them.
					observed <- openCodeHTTPExchange{path: r.URL.Path, headers: r.Header.Clone(), body: body}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Request-Id", "native-request")
					if _, err := io.WriteString(w, endpoint.response); err != nil {
						t.Errorf("write local upstream response: %v", err)
					}
				}))
				t.Cleanup(upstream.Close)
				groupID := int64(72)
				group := &routing.Group{
					ID: groupID, Hydrated: true, Status: "active", RateMultiplier: 1,
					AllowedProtocols: []protocol.ProtocolID{endpoint.protocol},
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
					ID: 71, Name: "OpenCode native", Platform: provider.PlatformOpenCode,
					Type: provider.ProviderTypeAPIKey, Status: "active", Schedulable: true,
					Concurrency: 1, GroupIDs: []int64{groupID},
					Credentials: map[string]any{
						"api_key": "upstream-secret", "base_url": upstream.URL, "provider_mode": "go",
						"upstream_protocols":                 []string{string(endpoint.protocol)},
						"model_mapping":                      map[string]any{"client-alias": "accepted-model", "accepted-model": "wrong-second-hop"},
						provider.OpenCodeCatalogProtocolsKey: map[string]any{"go": map[string]any{"accepted-model": string(endpoint.protocol)}},
					},
				}
				if scenario == "disabled provider protocol" {
					record.Credentials["upstream_protocols"] = []string{}
				}
				if scenario == "unknown provider model" {
					record.Credentials[provider.OpenCodeCatalogProtocolsKey] = map[string]any{"go": map[string]any{}}
				}
				providers := &mixedHTTPProviders{values: []gatewayprovider.ExecutionProvider{*gatewayprovider.NewExecutionProvider(record)}}
				transport := &mixedHTTPTransport{}
				expectedProvider := record.ID
				if scenario == "upstream timeout" {
					alternative := provider.CloneRecord(record)
					alternative.ID = record.ID + 1
					alternative.Priority = record.Priority + 1
					providers.values = append(providers.values, *gatewayprovider.NewExecutionProvider(alternative))
					expectedProvider = alternative.ID
					transport.failure = func(request *http.Request, id int64) error {
						if id == record.ID {
							if err := request.Context().Err(); err != nil {
								return err
							}
							return fmt.Errorf("fixture response-header timeout: %w", context.DeadlineExceeded)
						}
						return nil
					}
				}

				user := &identity.User{ID: 73, Balance: 100, Status: "active"}
				key := &apikey.APIKey{
					ID: 74, Key: "client-secret", Status: "active", UserID: user.ID,
					User: user, GroupID: &groupID, Group: group, BillingMode: "balance",
				}
				keys := keytestkit.NewService(&openCodeHTTPKeys{key: key}, nil, nil, nil, nil, nil, cfg)
				t.Cleanup(keys.Stop)
				eligibility := newBillingEligibilityFixture(cfg)
				t.Cleanup(eligibility.Stop)
				funding := newFundingAdmissionFixture(eligibility, cfg)
				concurrency := scheduler.NewConcurrencyService(&fakeConcurrencyCache{})
				resources := provideOpenAIHTTPResources(concurrency, cfg)
				activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("opencode-native-acceptance")}
				planner := gatewayprovider.NewRoutePlanner(nil)
				calculator := billingtestkit.Calculator(nil, map[string]*pricing.ModelPricing{
					"client-alias":   {InputPricePerToken: 1e-6, OutputPricePerToken: 2e-6},
					"accepted-model": {InputPricePerToken: 1e-6, OutputPricePerToken: 2e-6},
				})
				prices := billingtestkit.ResolverWithCards(t, calculator, nil)
				logs := &gatewaytestkit.UsageLogStore{Inserted: true}
				funds := &gatewaytestkit.SettlementStore{}
				recording := gatewaytestkit.NewRecording(logs, funds, nil, false)
				recording.Options.DefaultMultiplier = 1
				recording.Dependencies.Calculator = calculator
				recording.Dependencies.Prices = prices
				recorders := GatewayCompletionRecorders{Forward: recording.Core(nil, false), OpenAI: recording.Core(nil, true)}
				auth := provideAPIKeyAuth(keys, nil, cfg)
				middleware := provideGatewayRouteMiddleware(auth, keys, nil, nil, cfg, nil, nil, nil, nil)
				guards := gatewayhttp.NewRouteGuards(middleware)
				router := gin.New()
				if endpoint.protocol == protocol.ProtocolGeminiGenerateContent {
					choices := selection.NewGeneric(selection.GenericDependencies{
						Reads:  selection.Reads{Providers: providers},
						Shared: selection.Shared{Concurrency: concurrency},
					}, selection.DefaultOptions())
					shared := provideMessageHTTPBindings(planner, nil, nil, funding, nil, nil, nil, nil, concurrency, cfg, choices, keys, nil)
					digest := session.NewDigestSessionStore()
					gemini := provideGeminiExecutor(provideGeminiForward(providers, nil, nil, nil, transport, nil, cfg, activity, nil))
					runtime := provideMessageAttemptRuntime(nil, digest, nil, nil, gemini, funding, keys, recorders, shared, nil, nil, nil, cfg, nil, choices, nil, nil, prices)
					handler := provideGeminiNativeHTTP(shared, digest, runtime, activity, choices)
					router.POST("/v1beta/models/*modelAction",
						middleware.GoogleAPIKeyAuth, middleware.RequireGroupGoogle,
						guards.RequireGeminiGenerateContentProtocol, handler.GeminiV1BetaModels,
					)
				} else {
					source, choices, credentials := newOpenAIExecutionAndSelectionFixture(providers, nil, cfg, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
					common := provideOpenAIAttemptBindings(source.Responses, keys, resources, nil, nil, nil, recorders, nil, nil, choices, nil, nil, funding, nil, planner, nil)
					executor := provideSystemOneExecutor(cfg, transport, nil, nil, prices, activity)
					runtime := provideMediaRuntime(source.Responses, credentials, keys, funding, executor, common, resources, nil, cfg, source.Grok, nil, source.Auxiliary, nil, planner, nil)
					handler := provideAuxiliaryHTTP(runtime, activity)
					router.POST(endpoint.path, middleware.APIKeyAuth, middleware.RequireGroupAnthropic,
						guards.RequireGroupClientProtocol(endpoint.protocol, gatewayhttp.GroupClientProtocolErrorOpenAI),
						handler.SystemOne,
					)
				}
				request := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("User-Agent", "OpenCodeNativeAcceptance/1")
				request.Header.Set("X-OpenCode-Session", "native-session")
				if scenario != "missing authentication" {
					request.Header.Set("Authorization", "Bearer client-secret")
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if scenario != "allowed" && scenario != "upstream timeout" {
					switch scenario {
					case "missing authentication":
						require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
					case "disabled client protocol", "disabled group":
						require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
					default:
						require.GreaterOrEqual(t, response.Code, 400, response.Body.String())
					}
					require.Empty(t, transport.calls())
					require.Empty(t, observed)
					require.Zero(t, logs.Calls)
					require.Zero(t, funds.Calls)
					return
				}
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				if scenario == "upstream timeout" {
					require.Equal(t, []int64{record.ID, expectedProvider}, transport.calls())
				} else {
					require.Equal(t, []int64{record.ID}, transport.calls())
				}
				exchange := <-observed
				require.Equal(t, endpoint.upstream, exchange.path)
				require.NotContains(t, string(exchange.body), "wrong-second-hop")
				if endpoint.protocol == protocol.ProtocolSystemOne {
					require.Equal(t, "accepted-model", gjson.GetBytes(exchange.body, "model").String())
					require.Equal(t, int64(1), gjson.GetBytes(response.Body.Bytes(), "answers.ready.noul").Int())
				} else {
					require.Contains(t, response.Body.String(), "native-answer")
				}
				require.Equal(t, "Bearer upstream-secret", exchange.headers.Get("Authorization"))
				require.Empty(t, exchange.headers.Get("X-Api-Key"))
				require.Empty(t, exchange.headers.Get("X-Goog-Api-Key"))
				require.Equal(t, "OpenCodeNativeAcceptance/1", exchange.headers.Get("User-Agent"))
				require.Equal(t, "native-session", exchange.headers.Get("X-OpenCode-Session"))
				require.Equal(t, 1, logs.Calls)
				require.Equal(t, 1, funds.Calls)
				require.Equal(t, expectedProvider, logs.LastLog.ProviderID)
				require.Equal(t, provider.PlatformOpenCode, logs.LastLog.Platform)
				require.Equal(t, "client-alias", logs.LastLog.RequestedModel)
				require.NotNil(t, logs.LastLog.UpstreamModel)
				require.Equal(t, "accepted-model", *logs.LastLog.UpstreamModel)
				require.Equal(t, 10, logs.LastLog.InputTokens)
				require.Equal(t, 3, logs.LastLog.OutputTokens)
			})
		}
	}
}
