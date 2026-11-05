package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// User-scoped Fast policy needs the authenticated key owner to reach outbound
// request preparation. The scenario crosses gateway/httpapi/authorization.go,
// gateway/httpapi/openai_responses_execution.go, and
// gateway/provider/execution_fast_policy.go, then inspects two captured
// upstream JSON bodies for the allowed user's tier and the other user's
// filtered request.

// openAIFastPolicyForwardingAPIKeyRepo serves exact credential lookup and
// accepts diagnostic touches for the forwarding scenario.
type openAIFastPolicyForwardingAPIKeyRepo struct {
	// APIKeyRepository supplies the remaining method shape. This fixture leaves it
	// nil, so calling an operation not overridden here panics through that
	// embedded interface.
	apikey.APIKeyRepository
	// apiKeys retains credential-indexed fixture pointers. Lookup copies the outer
	// record while user, group, and other references remain shared and read-only
	// during requests.
	apiKeys map[string]*apikey.APIKey
}

// openAIFastPolicyForwardingSettingRepo returns one serialized policy string
// for runtime reader lookups.
type openAIFastPolicyForwardingSettingRepo struct {
	// Repository supplies unoverridden operations through its embedded interface.
	// This fixture leaves it nil and configures GetValue alone for runtime policy
	// reads.
	settingscore.Repository
	// value is returned for every settings key requested by the runtime readers.
	// Tests set the serialized policy before forwarding begins and keep it
	// unchanged.
	value string
}

// openAIFastPolicyForwardingHTTPUpstream delegates forwarding to the local test
// server's ordinary HTTP client.
type openAIFastPolicyForwardingHTTPUpstream struct {
	// client remains usable while the scenario forwards requests. It owns HTTP
	// transport behavior; callers close returned response bodies after their
	// execution consumer finishes.
	client *http.Client
}

// TestAPIKeyAuthForwardsUserScopedOpenAIFastPolicyToUpstream authenticates two
// key owners under one priority-tier filter with a user-specific pass rule.
// Captured outbound JSON retains priority for user 42 and removes that field
// for user 43 after forwarding.
func TestAPIKeyAuthForwardsUserScopedOpenAIFastPolicyToUpstream(t *testing.T) {
	// Buffer the two expected upstream bodies because the test reads them after
	// both HTTP requests return. Server handlers can then enqueue captured bytes
	// before writing their responses.
	upstreamBodies := make(chan []byte, 2)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusInternalServerError)
			return
		}
		upstreamBodies <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","model":"gpt-5","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	// Keep the upstream server available throughout both forwarded requests and
	// body assertions. Closing it on return releases its listener and waits for
	// outstanding requests to finish.
	defer upstreamServer.Close()

	settings := &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{
			{
				ServiceTier: tierpolicy.OpenAIFastTierPriority,
				Action:      anthropic.BetaPolicyActionFilter,
				Scope:       anthropic.BetaPolicyScopeAll,
			},
			{
				ServiceTier: tierpolicy.OpenAIFastTierPriority,
				Action:      anthropic.BetaPolicyActionPass,
				Scope:       anthropic.BetaPolicyScopeAll,
				UserIDs:     []int64{42},
			},
		},
	}
	settingsJSON, err := json.Marshal(settings)
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	settingService := gatewaytestkit.RuntimeReaders(&openAIFastPolicyForwardingSettingRepo{
		value: string(settingsJSON),
	})

	// Obtain HTTP and lineage lifetimes from compatible selection options and
	// reuse the response, block, and circuit instances when wiring output. This
	// fixture forwards its predefined provider over HTTP.
	responses := session.NewOpenAIWSStateStore(nil, gatewayprovider.LogOpenAIWSModeInfo)
	transient := providercore.NewModelTransientState(0)
	circuit := egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings())
	blocks := providercore.NewRuntimeBlockState(time.Now)
	choices := selection.NewCompatible(selection.CompatibleDependencies{Responses: responses, ModelTransient: transient, ProxyCircuit: circuit, RuntimeBlocks: blocks}, selection.Options{})
	output := &gatewayhttp.OpenAIResponseOutput{Options: gatewayhttp.OpenAIResponseOptions{Configured: true, ReadLimit: config.DefaultUpstreamResponseReadMaxBytes}, Health: &provideradapter.OpenAIResponseHealth{Runtime: blocks, ModelTransient: transient}, Corrector: openai.NewCodexToolCorrector(), ProxyCircuit: circuit, Responses: responses, ResponseTTL: choices.OpenAIHTTPResponseStickyTTL, Headers: responseHeaderFilterForTest(cfg)}
	transport := &openAIFastPolicyForwardingHTTPUpstream{client: upstreamServer.Client()}
	requests := &gatewayhttp.OpenAIRequests{Options: gatewayhttp.OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{AllowInsecureHTTP: true}}, Transport: transport, Readers: settingService, Credentials: &providercore.OpenAIExecutionCredentials{}, Identity: gatewayprovider.NewExecutionAgentIdentity(&providercore.OpenAITaskCoordinator{}, nil, nil, nil), ClientPolicy: &provideradapter.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent}}
	text := &gatewayhttp.OpenAITextExecutor{Requests: requests, Output: output, FastPolicy: &gatewayprovider.ExecutionFastPolicy{Readers: settingService}, CodexUsage: &provideradapter.CodexUsageObserver{}, ResponseTTL: choices.OpenAIHTTPResponseStickyTTL, Compact: &gatewayhttp.CompactExecutor{}}
	executor := &gatewayhttp.OpenAIResponsesExecutor{Requests: requests, Output: output, Text: text, Lineage: &gatewayhttp.OpenAIEncryptedLineage{Store: responses, TTL: choices.SessionStickyTTL}, ImageBridge: &gatewayprovider.ResponseImagePolicy{}}

	groupID := int64(101)
	group := &routing.Group{
		ID:     groupID,
		Name:   "openai",
		Status: billing.StatusActive,

		Hydrated: true,
	}
	apiKeys := map[string]*apikey.APIKey{
		"key-user-42": newOpenAIFastPolicyForwardingAPIKey(1, "key-user-42", 42, groupID, group),
		"key-user-43": newOpenAIFastPolicyForwardingAPIKey(2, "key-user-43", 43, groupID, group),
	}
	apiKeyService := testkit.NewService(&openAIFastPolicyForwardingAPIKeyRepo{apiKeys: apiKeys}, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 900,
			Name:        "openai-upstream",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": upstreamServer.URL,
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.POST("/v1/responses", func(c *gin.Context) {
		body, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)
		if _, forwardErr := executor.Forward(c.Request.Context(), c, provider, body); forwardErr != nil {
			c.Status(http.StatusBadGateway)
			return
		}
		c.Status(http.StatusOK)
	})

	send := func(apiKey string) {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			bytes.NewBufferString(`{"model":"gpt-5","stream":false,"service_tier":"priority","input":"hi"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("x-api-key", apiKey)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
	}

	send("key-user-42")
	send("key-user-43")

	allowedUserBody := <-upstreamBodies
	otherUserBody := <-upstreamBodies
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(allowedUserBody, "service_tier").String())
	require.False(t, gjson.GetBytes(otherUserBody, "service_tier").Exists())
}

// newOpenAIFastPolicyForwardingAPIKey returns an active key and active user
// with positive fixture funding. It retains the supplied group pointer, so both
// users can share the read-only group configuration while exercising their
// distinct policy identities.
func newOpenAIFastPolicyForwardingAPIKey(id int64, key string, userID, groupID int64, group *routing.Group) *apikey.APIKey {
	return &apikey.APIKey{
		ID:      id,
		UserID:  userID,
		Key:     key,
		Status:  billing.StatusActive,
		GroupID: &groupID,
		User: &identity.User{
			ID:          userID,
			Role:        identity.RoleUser,
			Status:      billing.StatusActive,
			Balance:     10,
			Concurrency: 1,
		},
		Group: group,
	}
}

// GetByKeyForAuth returns a shallow outer copy of an exact credential match, or
// ErrAPIKeyNotFound. Nested user, group, and other references stay shared, so
// the scenario keeps mapped fixture data unchanged during authentication.
func (r *openAIFastPolicyForwardingAPIKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*apikey.APIKey, error) {
	apiKey, ok := r.apiKeys[key]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *apiKey
	return &clone, nil
}

// UpdateLastUsed accepts authentication's diagnostic touch and leaves fixture
// key timestamps unchanged.
func (r *openAIFastPolicyForwardingAPIKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

// GetValue returns the fixture's serialized policy string for every requested
// key. The context and key are ignored; configure this string before runtime
// readers begin forwarding so they observe stable fixture data.
func (r *openAIFastPolicyForwardingSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

// Do performs the request through the configured local HTTP client and returns
// its response or transport error. Proxy, provider ID, and concurrency
// parameters are ignored here; the execution consumer owns reading and closing
// the response body.
func (u *openAIFastPolicyForwardingHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	// #nosec G704 -- The target is a local httptest.Server.
	return u.client.Do(req)
}

// DoWithTLS delegates to the fixture's ordinary Do path and ignores the
// fingerprint profile. It accepts production transport parameters for the
// interface, while the configured HTTP client determines this local request's
// actual transport.
func (u *openAIFastPolicyForwardingHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// responseHeaderFilterForTest compiles the supplied configuration's
// response-header policy for the forwarding executor. Nil configuration returns
// nil; otherwise enablement, extra allowed fields, and forced removals are
// delegated to the egress compiler.
func responseHeaderFilterForTest(cfg *config.Config) *egress.CompiledHeaderFilter {
	if cfg == nil {
		return nil
	}
	return egress.CompileHeaderFilter(egress.ResponseHeaderOptions{Enabled: cfg.Gateway.ResponseHeaders.Enabled, AdditionalAllowed: cfg.Gateway.ResponseHeaders.AdditionalAllowed, ForceRemove: cfg.Gateway.ResponseHeaders.ForceRemove})
}
