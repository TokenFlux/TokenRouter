package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	keytestkit "github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// sse_keepalive_routes_scenario_test.go exercises gateway_middleware.go,
// gateway_messages_http.go, gateway_openai_text_http.go and httpapi/routes.go
// with production handlers and transport observation. Authentication and storage
// are local fixtures; the delayed upstream and client use real HTTP connections.

// heartbeatRouteTransport connects fixture providers through the production pool.
// Unused transport methods remain embedded; each requested exchange uses the
// real dispatch listener, response observer and response-body lifetime tracking.
type heartbeatRouteTransport struct {
	httpclient.UpstreamTransport // unused transport interface methods
	// pool is created per route fixture. Cleanup closes its idle connections
	// after the downstream test server has finished all outstanding requests.
	pool          *httpclient.UpstreamPool // owned test connection pool
	headerTimeout time.Duration            // fixture response header budget
}

// heartbeatRouteOptions adjusts model and transport budgets for SDK route fixtures.
type heartbeatRouteOptions struct {
	FallbackURL   string        // optional second provider URL
	Model         string        // optional fixture model
	Stall         time.Duration // upstream idle budget
	HeaderTimeout time.Duration // upstream header budget
}

// heartbeatRouteNoSearch disables optional search without replacing forwarding logic.
type heartbeatRouteNoSearch struct{}

// Do delegates one fixture exchange through real pool timing and response hooks.
// Tests use local HTTP endpoints and own response closure, while cleanup closes
// idle connections after the downstream request has completed.
func (t *heartbeatRouteTransport) Do(req *http.Request, proxy string, id int64, _ int) (*http.Response, error) {
	return t.pool.Do(req, httpclient.UpstreamRequestOptions{ProxyURL: proxy, ProviderID: id, Settings: httpclient.UpstreamSettings{ResponseHeaderTimeout: t.headerTimeout}})
}

// DoWithTLS retains the same local HTTP pool for fingerprinted fixture dispatch.
// TLS negotiation is outside this liveness test; dispatch and body observation
// must still follow the same production pool path as ordinary requests.
func (t *heartbeatRouteTransport) DoWithTLS(req *http.Request, proxy string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return t.Do(req, proxy, id, n)
}

// Current reports that this fixture has no configured external searcher.
func (heartbeatRouteNoSearch) Current() searchtools.Searcher { return nil }

// TestGatewayRoutesKeepaliveBeforeFirstEvent registers the actual gateway routes
// and handlers across client protocols and native upstream families. Delaying
// either headers or the first payload by twenty-five idle intervals must produce
// a local heartbeat before upstream output is released. Responses
// header visibility also determines the expected provider turn-state ownership.
func TestGatewayRoutesKeepaliveBeforeFirstEvent(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
				for _, platform := range []string{"anthropic", "openai", "gemini", "grok", "openai_passthrough", "openai_chat"} {
					t.Run(path+"/"+platform, func(t *testing.T) { testGatewayRouteKeepalive(t, path, platform, phase == "headers") })
				}
			}
			t.Run("gemini native", func(t *testing.T) {
				testGatewayRouteKeepalive(t, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", "gemini", phase == "headers")
			})
			for _, path := range []string{"/responses", "/backend-api/codex/responses", "/chat/completions"} {
				t.Run("alias"+path, func(t *testing.T) { testGatewayRouteKeepalive(t, path, "openai", phase == "headers") })
			}
		})
	}
}

// testGatewayRouteKeepalive holds upstream events while a real downstream reads.
// The selected phase delays either response headers or the first payload by
// twenty-five heartbeat intervals. A beat observed only after release cannot pass this
// test, even if the final response content otherwise looks correct. Successful
// cases keep both servers alive until the client consumes terminal output;
// failures release the client and servers through the registered cleanup.
func testGatewayRouteKeepalive(t *testing.T, path, kind string, delayHeaders bool) {
	t.Helper()
	const interval = 10 * time.Millisecond
	var released atomic.Bool
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		// Read request content before flushing HTTP/1 response headers, which may
		// otherwise discard the unread body before the protocol fixture inspects it.
		r.Body = io.NopCloser(bytes.NewReader(requestBody))
		if delayHeaders {
			select {
			case <-time.After(25 * interval):
			case <-r.Context().Done():
				return
			}
			released.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", "route-upstream-request")
		w.Header().Set("Request-ID", "route-anthropic-request")
		w.Header().Set(gatewayhttp.CodexTurnStateHeader, "route-state")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("fixture lacks HTTP flushing")
			return
		}
		flusher.Flush()
		if !delayHeaders {
			select {
			case <-time.After(25 * interval):
			case <-r.Context().Done():
				return
			}
			released.Store(true)
		}
		if strings.Contains(r.URL.Path, "chat/completions") {
			_, _ = io.WriteString(w, "data: {\"id\":\"chat-route\",\"model\":\"gpt-5.4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok-mixed\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"chat-route\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\ndata: [DONE]\n\n")
			return
		}
		mixedUpstreamResponse(w, r, false)
	}))
	t.Cleanup(upstreamServer.Close)
	router, origins := newHeartbeatRouteRouter(t, upstreamServer.URL, kind, interval)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	model := map[string]string{"anthropic": "claude-sonnet-4-5-20250929", "gemini": "gemini-2.5-flash", "grok": "grok-4.5", "openai": "gpt-5.4", "openai_passthrough": "gpt-5.4", "openai_chat": "gpt-5.4"}[kind]
	body := fmt.Sprintf(`{"model":%q,"max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`, model)
	if strings.HasSuffix(path, "/responses") {
		body = fmt.Sprintf(`{"model":%q,"input":"hello","stream":true}`, model)
	}
	if strings.Contains(path, "streamGenerateContent") {
		body = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
	}
	client := server.Client()
	client.Timeout = 3 * time.Second
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Session-Id", "route-header-state")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	reader := bufio.NewReader(resp.Body)
	var received strings.Builder
	heartbeat := false
	for !heartbeat {
		line, readErr := reader.ReadString('\n')
		_, _ = received.WriteString(line)
		if line == ": keepalive\n" || line == "event: ping\n" || (strings.Contains(path, "streamGenerateContent") && received.String() == "\n\n") {
			heartbeat = true
			require.False(t, released.Load(), "heartbeat arrived only after the first upstream event")
			break
		}
		if readErr != nil {
			break
		}
		if strings.Contains(line, "ok-mixed") {
			break
		}
	}
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, _ = received.Write(rest)
	require.True(t, heartbeat, received.String())
	require.Equal(t, http.StatusOK, resp.StatusCode, received.String())
	require.Contains(t, received.String(), "ok-mixed")
	if strings.HasSuffix(path, "/responses") {
		owner, known := origins.Owner("8\x00route-header-state")
		if resp.Header.Get(gatewayhttp.CodexTurnStateHeader) == "" {
			require.False(t, known, "undelivered upstream state claimed provider ownership")
		} else {
			require.True(t, known)
			require.Equal(t, int64(1), owner)
		}
	}
}

// newHeartbeatRouteRouter assembles production protocol receivers over fixture data.
// Only authentication, persistent stores and timer durations are substituted;
// route admission, handler preparation, provider execution and output wrappers
// use the same registration and construction paths as the running application.
// Positive overrides replace model and transport defaults. An optional fallback
// receives priority one after the primary provider's priority zero.
func newHeartbeatRouteRouter(t *testing.T, url, kind string, interval time.Duration, overrides ...heartbeatRouteOptions) (*gin.Engine, *session.CodexTurnOrigins) {
	t.Helper()
	platform := kind
	if strings.HasPrefix(kind, "openai_") {
		platform = capability.PlatformOpenAI
	}
	model := map[string]string{"anthropic": "claude-sonnet-4-5-20250929", "openai": "gpt-5.4", "gemini": "gemini-2.5-flash", "grok": "grok-4.5"}[platform]
	limits := heartbeatRouteOptions{Model: model, Stall: time.Second, HeaderTimeout: 2 * time.Second}
	if len(overrides) > 0 {
		limits.FallbackURL = overrides[0].FallbackURL
		if overrides[0].Model != "" {
			limits.Model = overrides[0].Model
		}
		if overrides[0].Stall > 0 {
			limits.Stall = overrides[0].Stall
		}
		if overrides[0].HeaderTimeout > 0 {
			limits.HeaderTimeout = overrides[0].HeaderTimeout
		}
	}
	model = limits.Model
	groupID := int64(42)
	group := &routing.Group{ID: groupID, Hydrated: true, Name: "heartbeat", Status: "active", RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolGeminiGenerateContent}}
	cfg := &config.Config{}
	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Gateway.Failover.MaxProviderSwitches = 1
	cfg.Gateway.MaxRequestBodyBytes = 1 << 20
	cfg.Gateway.TextMaxRequestBodyBytes = 1 << 20
	record := &providercore.Record{ID: 1, Name: platform, Platform: platform, Type: capability.ProviderTypeAPIKey, Status: "active", Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "fixture-key", "base_url": url, "model_whitelist": []string{model}}}
	if kind == "openai_passthrough" {
		record.Extra = map[string]any{"openai_passthrough": true}
	}
	if kind == "openai_chat" {
		record.Extra = map[string]any{"openai_text_route_mode": "force_chat_completions"}
	}
	providers := &mixedHTTPProviders{values: []gatewayprovider.ExecutionProvider{*gatewayprovider.NewExecutionProvider(record)}}
	if limits.FallbackURL != "" {
		fallback := *record
		fallback.ID = 2
		fallback.Name = platform + "-fallback"
		fallback.Priority = 1
		fallback.Credentials = maps.Clone(record.Credentials)
		fallback.Credentials["base_url"] = limits.FallbackURL
		providers.values = append(providers.values, *gatewayprovider.NewExecutionProvider(&fallback))
	}
	transport := &heartbeatRouteTransport{pool: httpclient.NewUpstreamPool(), headerTimeout: limits.HeaderTimeout}
	t.Cleanup(transport.pool.CloseIdleConnections)
	source, choices, credentials := newOpenAIExecutionAndSelectionFixture(providers, nil, cfg, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	origins := session.NewCodexTurnOrigins(time.Now)
	turns := &gatewayhttp.CodexTurnStateHeaders{Origins: origins}
	source.Requests.Turns = turns
	source.Text.Output.Turns = turns
	source.Responses.Output.Turns = turns
	recording := gatewaytestkit.NewRecording(&gatewaytestkit.UsageLogStore{Inserted: true}, &gatewaytestkit.SettlementStore{}, nil, false)
	recording.Options.DefaultMultiplier = 1
	calculator := billingtestkit.Calculator(nil, map[string]*pricing.ModelPricing{model: {InputPricePerToken: 1e-6, OutputPricePerToken: 2e-6}})
	prices := billingtestkit.ResolverWithCards(t, calculator, nil)
	recording.Dependencies.Calculator = calculator
	recording.Dependencies.Prices = prices
	source.Recorder = recording.Core(nil, true)
	native := &gatewayhttp.UnifiedTextExecutor{OpenAI: source.Responses, Anthropic: gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(messageforward.Dependencies{Credentials: &providercore.MessageCredentialSource{}, Transport: transport, Deferred: &providercore.DeferredService{}, Search: searchtools.NewEmulator(heartbeatRouteNoSearch{}, nil, nil, nil, nil, nil)}, messageforward.Options{Configured: true, AllowInsecureHTTP: true, ResponseReadLimit: 1 << 20}), nil), Gemini: &gatewayhttp.GeminiExecutor{Runtime: &googleforward.Gemini{Transport: transport, Errors: &provideradapter.GeminiErrorObserver{SetRateLimited: func(context.Context, int64, time.Time) error { return nil }, ResetTime: func([]byte) *int64 { return nil }, DailyReset: func() *int64 { return nil }}, Options: googleforward.Options{Configured: true, AllowInsecureHTTP: true, ResponseReadLimit: 1 << 20, StreamInterval: int(limits.Stall / time.Second)}}}}
	eligibility := newBillingEligibilityFixture(cfg)
	t.Cleanup(eligibility.Stop)
	funding := newFundingAdmissionFixture(eligibility, cfg)
	keys := keytestkit.NewService(nil, nil, nil, nil, nil, nil, cfg)
	t.Cleanup(keys.Stop)
	concurrency := scheduler.NewConcurrencyService(&fakeConcurrencyCache{})
	resources := provideOpenAIHTTPResources(concurrency, cfg)
	endpoints := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Source: source, Native: native, Credentials: credentials, Choices: choices, Config: cfg, Funding: funding, Keys: keys, Concurrency: resources.Concurrency, MaxSwitches: 1})
	user := &identity.User{ID: 7, Balance: 100, Status: "active"}
	key := &apikey.APIKey{ID: 8, UserID: 7, User: user, GroupID: &groupID, Group: group}
	auth := keyhttp.APIKeyAuthMiddleware(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), key)
		authctx.SetPrincipal(c, identity.Principal{UserID: 7}, 0, "")
		c.Next()
	})
	middleware := legacyRouteMiddleware(auth, keys, nil, nil, nil, cfg)
	middleware.GoogleAPIKeyAuth = gin.HandlerFunc(auth)
	middleware.DownstreamKeepalive = gatewayhttp.DownstreamSSEKeepalive(interval, limits.Stall)
	generic := selection.NewGeneric(selection.GenericDependencies{Reads: selection.Reads{Providers: providers}, Shared: selection.Shared{Concurrency: concurrency}}, selection.DefaultOptions())
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("heartbeat-route-fixture")}
	planner := gatewayprovider.NewRoutePlanner(nil)
	shared := provideMessageHTTPBindings(planner, nil, nil, funding, nil, nil, nil, nil, concurrency, cfg, generic, keys, nil)
	digest := session.NewDigestSessionStore()
	runtime := provideMessageAttemptRuntime(nil, digest, nil, nil, native.Gemini, funding, keys, GatewayCompletionRecorders{Forward: recording.Core(nil, false), OpenAI: source.Recorder}, shared, nil, nil, nil, cfg, nil, generic, nil, nil, prices)
	gemini := provideGeminiNativeHTTP(shared, digest, runtime, activity, generic)
	router := gin.New()
	gatewayhttp.RegisterGatewayRoutes(router, gatewayhttp.RouteEndpoints{OpenAIText: endpoints.textHandler(), GeminiNative: gemini, ResponsesWS: func(*gin.Context) {}, PublicUsage: func(*gin.Context) {}, QoderChat: func(*gin.Context) {}}, middleware, func(*gin.RouterGroup) {})
	return router, origins
}
