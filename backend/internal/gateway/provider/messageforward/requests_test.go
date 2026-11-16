package messageforward

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	catalogprovider "github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// stubIdentityCache records copied fingerprint values for OAuth header
// preparation tests.
type stubIdentityCache struct {
	// fingerprint and lastSet share the outer copy saved by SetFingerprint;
	// setCalls counts those replacements. GetFingerprint returns another
	// outer copy, and nil storage reports no cached identity.
	fingerprint *anthropic.Fingerprint // current stored fingerprint value
	setCalls    int                    // recorded fingerprint write count
	lastSet     *anthropic.Fingerprint // last written fingerprint copy
}

// newVertexBetaTestContext installs the supplied beta in a preparation request
// fixture. Empty beta input leaves the header absent, allowing tests to
// exercise body-implied feature handling and builder defaults.
func newVertexBetaTestContext(t *testing.T, anthropicBeta string) *requestBoundaryFixture {
	t.Helper()

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if anthropicBeta != "" {
		c.Request.Header.Set("Anthropic-Beta", anthropicBeta)
	}
	return c
}

// newVertexServiceAccount creates an Anthropic Vertex target with test project
// and location.
func newVertexServiceAccount(id int64) *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeServiceAccount,
			Credentials: map[string]any{
				"project_id": "vertex-proj",
				"location":   "us-east5",
			},
		},
	}
}

// TestVertexBetaFilter_StripsUnsupportedClaudeCodeTokens constructs a Vertex
// request with client identity and supported feature betas together. The
// outgoing header omits the listed unsupported tokens and retains both
// allowlisted features.
func TestVertexBetaFilter_StripsUnsupportedClaudeCodeTokens(t *testing.T) {
	c := newVertexBetaTestContext(t,
		"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,"+
			"advisor-tool-2026-03-01,prompt-caching-scope-2026-01-05,"+
			"redact-thinking-2026-02-12,thinking-token-count-2026-05-13,"+
			"context-management-2025-06-27")

	body := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)

	svc := NewRuntime(Dependencies{}, Options{})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{}, newVertexServiceAccount(401), body,
		"vertex-token", "service_account", "claude-opus-4-7@20260417", false, false,
	)
	require.NoError(t, err)

	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")

	// These unsupported tokens must be removed from the outgoing header.
	for _, bad := range []string{
		"advisor-tool-2026-03-01",
		"prompt-caching-scope-2026-01-05",
		"redact-thinking-2026-02-12",
		"thinking-token-count-2026-05-13",
		// Client identity betas are absent from the Vertex service
		// account allowlist.
		"claude-code-20250219",
		"oauth-2025-04-20",
	} {
		require.False(t, anthropic.AnthropicBetaTokensContains(outBeta, bad),
			"token %q 必须被剥离；实际 outgoing beta=%q", bad, outBeta)
	}

	// Allowlisted tokens remain in the outgoing header.
	for _, keep := range []string{
		"interleaved-thinking-2025-05-14",
		"context-management-2025-06-27",
	} {
		require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, keep),
			"token %q 应保留；实际 outgoing beta=%q", keep, outBeta)
	}
}

// TestVertexBetaFilter_DropsHeaderWhenAllUnsupported constructs a Vertex
// request whose client betas all fail its allowlist. The resulting request has
// an empty outgoing anthropic-beta header after filtering finishes.
func TestVertexBetaFilter_DropsHeaderWhenAllUnsupported(t *testing.T) {
	c := newVertexBetaTestContext(t,
		"prompt-caching-scope-2026-01-05,redact-thinking-2026-02-12")

	body := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)

	svc := NewRuntime(Dependencies{}, Options{})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{}, newVertexServiceAccount(402), body,
		"vertex-token", "service_account", "claude-opus-4-7@20260417", false, false,
	)
	require.NoError(t, err)

	require.Empty(t, anthropic.GetHeaderRaw(req.Header, "anthropic-beta"),
		"所有 token 被剥离后不应残留 anthropic-beta header")
}

// TestVertexBetaFilter_BodySanitizeKeysOnFinalBeta supplies a context-
// management body field with a client beta filtered by Vertex. The outgoing
// body removes that field and the final beta header is empty.
func TestVertexBetaFilter_BodySanitizeKeysOnFinalBeta(t *testing.T) {
	c := newVertexBetaTestContext(t, "prompt-caching-scope-2026-01-05")

	body := []byte(`{"model":"claude-opus-4-7","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[{"role":"user","content":"hi"}]}`)

	svc := NewRuntime(Dependencies{}, Options{})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{}, newVertexServiceAccount(403), body,
		"vertex-token", "service_account", "claude-opus-4-7@20260417", false, false,
	)
	require.NoError(t, err)

	got := readRequestBodyForTest(t, req)
	require.False(t, gjson.GetBytes(got, "context_management").Exists(),
		"最终 beta 不含 context-management 时 body.context_management 必须被 strip")
	require.Empty(t, anthropic.GetHeaderRaw(req.Header, "anthropic-beta"))
}

// TestVertexBetaFilter_BlocksViaBetaPolicy constructs a Vertex request with a
// feature blocked by current settings. Construction returns BetaBlockedError
// and the configured message for the selected feature token.
func TestVertexBetaFilter_BlocksViaBetaPolicy(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "context-management-2025-06-27",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "context management is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})

	c := newVertexBetaTestContext(t,
		"interleaved-thinking-2025-05-14,context-management-2025-06-27")
	body := []byte(`{"model":"claude-opus-4-7","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)

	_, _, err = svc.buildRequest(
		context.Background(), c, &AttemptState{}, newVertexServiceAccount(404), body,
		"vertex-token", "service_account", "claude-opus-4-7@20260417", false, false,
	)
	require.Error(t, err)
	var blocked *anthropic.BetaBlockedError
	require.True(t, errors.As(err, &blocked), "expected *BetaBlockedError, got %T", err)
	require.Equal(t, "context management is blocked", err.Error())
}

// TestFilterVertexBetaTokens compares Vertex allowlist filtering, an additional
// drop set, duplicate tokens, and empty input. The returned header contains the
// admitted unique tokens in the exercised order.
func TestFilterVertexBetaTokens(t *testing.T) {
	t.Run("whitelist filters unsupported", func(t *testing.T) {
		out := vertex.FilterBetaTokens(
			"interleaved-thinking-2025-05-14,prompt-caching-scope-2026-01-05,context-management-2025-06-27",
			nil,
		)
		require.Equal(t, "interleaved-thinking-2025-05-14,context-management-2025-06-27", out)
	})

	t.Run("drop set strips before whitelist", func(t *testing.T) {
		out := vertex.FilterBetaTokens(
			"interleaved-thinking-2025-05-14,context-management-2025-06-27",
			map[string]struct{}{"context-management-2025-06-27": {}},
		)
		require.Equal(t, "interleaved-thinking-2025-05-14", out)
	})

	t.Run("dedupe", func(t *testing.T) {
		out := vertex.FilterBetaTokens(
			"context-1m-2025-08-07,context-1m-2025-08-07",
			nil,
		)
		require.Equal(t, "context-1m-2025-08-07", out)
	})

	t.Run("empty input", func(t *testing.T) {
		require.Empty(t, vertex.FilterBetaTokens("", nil))
		require.Empty(t, vertex.FilterBetaTokens("prompt-caching-scope-2026-01-05", nil))
	})
}

// TestGatewayService_BuildAnthropicVertexServiceAccountRequest prepares a
// service-account request with inbound authorization and Anthropic headers. The
// wire URL selects the configured Vertex project/location, bearer token
// replaces client auth, and body carries the Vertex version.
func TestGatewayService_BuildAnthropicVertexServiceAccountRequest(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Anthropic-Version", "2023-06-01")
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 301,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeServiceAccount,
			Credentials: map[string]any{
				"project_id": "vertex-proj",
				"location":   "us-east5",
			},
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-5","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)

	svc := NewRuntime(Dependencies{}, Options{})
	req, _, err := svc.buildRequest(
		context.Background(),
		c, &AttemptState{},
		provider,
		body,
		"vertex-token",
		"service_account",
		"claude-sonnet-4-5@20250929",
		false,
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "https://us-east5-aiplatform.googleapis.com/v1/projects/vertex-proj/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-5@20250929:rawPredict", req.URL.String())
	require.Equal(t, "Bearer vertex-token", anthropic.GetHeaderRaw(req.Header, "authorization"))
	require.Empty(t, anthropic.GetHeaderRaw(req.Header, "x-api-key"))
	require.Empty(t, anthropic.GetHeaderRaw(req.Header, "anthropic-version"))
	require.Equal(t, "interleaved-thinking-2025-05-14", anthropic.GetHeaderRaw(req.Header, "anthropic-beta"))

	got := readRequestBodyForTest(t, req)
	require.Equal(t, "", gjson.GetBytes(got, "model").String())
	require.Equal(t, vertex.AnthropicVersion, gjson.GetBytes(got, "anthropic_version").String())
	require.Equal(t, "hello", gjson.GetBytes(got, "messages.0.content").String())
}

// readRequestBodyForTest consumes prepared request bytes and fails the test on
// missing body or read failure. It retains caller ownership of closing the
// request reader after assertions finish.
func readRequestBodyForTest(t *testing.T, req *http.Request) []byte {
	t.Helper()
	require.NotNil(t, req.Body)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return body
}

// TestGatewayService_BuildAnthropicVertexServiceAccount_StripsContextManagementWhenBetaMissing
// supplies context-management edits while the incoming beta header omits their
// feature token. Vertex output removes the body field and keeps its outgoing
// beta set consistent.
func TestGatewayService_BuildAnthropicVertexServiceAccount_StripsContextManagementWhenBetaMissing(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 302, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeServiceAccount,
			Credentials: map[string]any{"project_id": "vertex-proj", "location": "us-east5"},
		},
	}
	// The body can carry context_management from the client or an earlier
	// normalization step.
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[{"role":"user","content":"hi"}]}`)

	svc := NewRuntime(Dependencies{}, Options{})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{}, provider, body,
		"vertex-token", "service_account", "claude-haiku-4-5@20251001", false, false,
	)
	require.NoError(t, err)

	got := readRequestBodyForTest(t, req)
	require.False(t, gjson.GetBytes(got, "context_management").Exists(),
		"Vertex 路径下客户端 header 缺 context-management beta 时，必须 strip body 同名字段")
	// The final beta header and context_management body field must agree.
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")
	require.False(t, anthropic.AnthropicBetaTokensContains(outBeta, "context-management-2025-06-27"),
		"与 body 对称：outgoing anthropic-beta header 也不含 context-management beta")
}

// TestGatewayService_BuildAnthropicVertexServiceAccount_PreservesContextManagementWhenBetaPresent
// supplies a permitted context-management beta and its body edits. Vertex
// request construction retains both the field and its corresponding feature
// token in the final header.
func TestGatewayService_BuildAnthropicVertexServiceAccount_PreservesContextManagementWhenBetaPresent(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14,context-management-2025-06-27")

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 303, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeServiceAccount,
			Credentials: map[string]any{"project_id": "vertex-proj", "location": "us-east5"},
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)

	svc := NewRuntime(Dependencies{}, Options{})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{}, provider, body,
		"vertex-token", "service_account", "claude-sonnet-4-6@20260218", false, false,
	)
	require.NoError(t, err)

	got := readRequestBodyForTest(t, req)
	require.True(t, gjson.GetBytes(got, "context_management").Exists(),
		"Vertex + 客户端 header 包含 context-management beta 时字段必须保留")
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")
	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, "context-management-2025-06-27"),
		"与 body 对称：outgoing anthropic-beta header 同步含 context-management beta")
}

// TestGatewayService_AnthropicAPIKeyPassthrough_BearerAuthScheme selects API
// key bearer authentication for both Messages and counting. The outgoing
// requests use the provider token and paths and omit inbound cookie/API-key
// credentials. The Messages wire bytes retain the supplied JSON body.
func TestGatewayService_AnthropicAPIKeyPassthrough_BearerAuthScheme(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Cookie", "secret=1")

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "ollama-key",
				"base_url": "https://ollama.com",
			},
			Extra: map[string]any{
				"anthropic_passthrough":        true,
				"anthropic_apikey_auth_scheme": providercore.AnthropicAPIKeyAuthSchemeAuthorizationBearer,
			},
		},
	}

	msgReq, wireBody, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		provider, []byte(`{"model":"gpt-oss:20b","messages":[]}`), "ollama-key",
	)
	require.NoError(t, err)
	require.Equal(t, "https://ollama.com/v1/messages?beta=true", msgReq.URL.String())
	require.JSONEq(t, `{"model":"gpt-oss:20b","messages":[]}`, string(wireBody))
	require.Equal(t, "Bearer ollama-key", anthropic.GetHeaderRaw(msgReq.Header, "authorization"))
	require.Empty(t, anthropic.GetHeaderRaw(msgReq.Header, "x-api-key"))
	require.Empty(t, anthropic.GetHeaderRaw(msgReq.Header, "cookie"))

	countReq, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		provider, []byte(`{"model":"gpt-oss:20b","messages":[]}`), "ollama-key", "apikey", "", false, true,
	)
	require.NoError(t, err)
	require.Equal(t, "https://ollama.com/v1/messages/count_tokens?beta=true", countReq.URL.String())
	require.Equal(t, "Bearer ollama-key", anthropic.GetHeaderRaw(countReq.Header, "authorization"))
	require.Empty(t, anthropic.GetHeaderRaw(countReq.Header, "x-api-key"))
	require.Empty(t, anthropic.GetHeaderRaw(countReq.Header, "cookie"))
}

// TestGatewayService_AnthropicAPIKeyPassthrough_BuildRequestRejectsInvalidBaseURL
// supplies a malformed provider base URL to native API key construction. The
// builder reports an error for this URL, protecting validation in the native
// request construction path.
func TestGatewayService_AnthropicAPIKeyPassthrough_BuildRequestRejectsInvalidBaseURL(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "k",
				"base_url": "://invalid-url",
			},
		},
	}

	_, _, err := svc.buildPassthroughRequest(context.Background(), c, &AttemptState{}, provider, []byte(`{}`), "k")
	require.Error(t, err)
}

// TestGatewayService_AnthropicOAuth_NotAffectedByAPIKeyPassthroughToggle sets
// the passthrough extra on an OAuth target. The provider remains in the OAuth
// path and construction supplies bearer authentication with the OAuth beta
// token.
func TestGatewayService_AnthropicOAuth_NotAffectedByAPIKeyPassthroughToggle(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeOAuth,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		},
	}

	require.False(t, provider.View().IsAnthropicAPIKeyPassthroughEnabled())

	req, _, err := svc.buildRequest(context.Background(), c, &AttemptState{}, provider, []byte(`{"model":"claude-3-7-sonnet-20250219"}`), "oauth-token", "oauth", "claude-3-7-sonnet-20250219", true, false)
	require.NoError(t, err)
	require.Equal(t, "Bearer oauth-token", anthropic.GetHeaderRaw(req.Header, "authorization"))
	require.Contains(t, anthropic.GetHeaderRaw(req.Header, "anthropic-beta"), anthropic.BetaOAuth, "OAuth 链路仍应按原逻辑补齐 oauth beta")
}

// TestBuildOAuthRequest_BillingMatchesWireUserAgent compares mimic and cached
// fingerprint headers across Messages and counting. The billing text uses the
// final user-agent version and fingerprint, and returned wire bytes match the
// request reader.
func TestBuildOAuthRequest_BillingMatchesWireUserAgent(t *testing.T) {
	for _, endpoint := range []string{"messages", "count_tokens"} {
		for _, tc := range []struct {
			name      string // table scenario name
			mimic     bool   // Claude Code mimic switch
			identity  bool   // fingerprint service installation flag
			disableFP bool   // fingerprint unification disable flag
		}{
			{name: "mimic_overrides_cached_version", mimic: true, identity: true},
			{name: "mimic_without_identity", mimic: true},
			{name: "mimic_with_fingerprint_disabled", mimic: true, identity: true, disableFP: true},
			{name: "passthrough_uses_cached_version", identity: true},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				c := &requestBoundaryFixture{}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				body := []byte(`{"model":"claude-haiku-4-5","system":[{"type":"text","text":""}],"messages":[{"role":"user","content":"hello world"}]}`)
				billing, err := anthropic.BuildBillingAttributionText(body, "2.1.81")
				require.NoError(t, err)
				body, err = sjson.SetBytes(body, "system.0.text", billing)
				require.NoError(t, err)

				svc := NewRuntime(Dependencies{}, Options{Configured: true})
				cachedUA := "claude-cli/2.9.0 (external, cli)"
				if tc.identity {
					svc.dependencies.Fingerprint = anthropic.NewRequestFingerprint(&stubIdentityCache{fingerprint: &anthropic.Fingerprint{
						UserAgent: cachedUA, ClientID: "test-client", UpdatedAt: time.Now().Unix(),
					}})
				}
				if tc.disableFP {
					svc.dependencies.Settings = newBetaRuntime(map[string]string{
						gateway.SettingKeyEnableFingerprintUnification: "false",
					})
				}
				provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}}
				var req *http.Request
				var wireBody []byte
				if endpoint == "messages" {
					req, wireBody, err = svc.buildRequest(context.Background(), c, &AttemptState{},

						provider,
						body, "test-token", "oauth", "claude-haiku-4-5", false, tc.mimic)
				} else {
					req, wireBody, err = svc.buildCountRequest(context.Background(), c, &AttemptState{},

						provider,
						body, "test-token", "oauth", "claude-haiku-4-5", tc.mimic, false)
				}
				require.NoError(t, err)
				defer func() { require.NoError(t, req.Body.Close()) }()
				wantUA := cachedUA
				if tc.mimic {
					wantUA = anthropic.DefaultHeaders["User-Agent"]
				}
				require.Equal(t, wantUA, anthropic.GetHeaderRaw(req.Header, "User-Agent"))
				version := anthropic.ExtractCLIVersion(wantUA)
				require.Contains(t, gjson.GetBytes(wireBody, "system.0.text").String(),
					"cc_version="+version+"."+anthropic.ComputeClaudeCodeFingerprint(wireBody, version)+";")
				actualBody, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, wireBody, actualBody)
			})
		}
	}
}

// newAnthropicAPIKeyPassthroughProviderForBetaTest creates a schedulable native
// API key target for beta tests.
func newAnthropicAPIKeyPassthroughProviderForBetaTest() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 501,
			Name:     "anthropic-apikey-passthrough-ctxmgmt-test",
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key": "upstream-key",
			},
			Extra:       map[string]any{"anthropic_passthrough": true},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
}

// readUpstreamBodyForTest consumes the constructed upstream body and fails on
// unavailable bytes. It reports helper location through t.Helper, while callers
// retain responsibility for closing the reader.
func readUpstreamBodyForTest(t *testing.T, req *http.Request) []byte {
	t.Helper()
	require.NotNil(t, req.Body)
	b, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return b
}

// TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_StripsContextManagementWhenClientHeaderMissingBeta
// supplies native Messages context-management edits without their client beta.
// The prepared API key request removes the unsupported body field before the
// upstream receives it.
func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_StripsContextManagementWhenClientHeaderMissingBeta(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// The client supplies no context-management beta.
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughProviderForBetaTest(), body, "token",
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"API-key passthrough + 客户端未带 context-management beta → strip body 字段")
}

// TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_PreservesContextManagementWhenClientHeaderHasBeta
// supplies native Messages context-management edits with the matching client
// beta. API key passthrough keeps that body field available in the prepared
// upstream request.
func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_PreservesContextManagementWhenClientHeaderHasBeta(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,context-management-2025-06-27")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughProviderForBetaTest(), body, "token",
	)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"API-key passthrough + 客户端带 context-management beta → 字段保留（不过度删除）")
}

// TestBuildRequests_ForceClaude1hCacheAcrossAnthropicPaths prepares Vertex,
// ordinary API Key, and API Key passthrough requests with forced one-hour
// Claude caching enabled. It checks Claude models rewrite 5m and default
// ephemeral markers to 1h while non-Claude passthrough models keep 5m.
func TestBuildRequests_ForceClaude1hCacheAcrossAnthropicPaths(t *testing.T) {
	ctx := gatewayprovider.WithAPIKeyForceClaude1hCache(context.Background(), true)
	c := &requestBoundaryFixture{Request: httptest.NewRequest(http.MethodPost, "/v1/messages", nil)}
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	claudeBody := []byte(`{
		"model":"claude-sonnet-4-6",
		"cache_control":{"type":"ephemeral","ttl":"5m"},
		"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],
		"tools":[{"name":"bash","cache_control":{"type":"ephemeral","ttl":"5m"},"input_schema":{"type":"object"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]
	}`)

	apiKeyProvider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 601,
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "sk-ant"},
		},
	}
	apiKeyReq, _, err := svc.buildRequest(ctx, c, &AttemptState{}, apiKeyProvider, claudeBody, "sk-ant", "apikey", "claude-sonnet-4-6", false, false)
	require.NoError(t, err)
	apiKeyOut := readUpstreamBodyForTest(t, apiKeyReq)
	require.Equal(t, "1h", gjson.GetBytes(apiKeyOut, "cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(apiKeyOut, "system.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(apiKeyOut, "tools.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(apiKeyOut, "messages.0.content.0.cache_control.ttl").String())

	vertexReq, _, err := svc.buildRequest(ctx, c, &AttemptState{}, newVertexServiceAccount(602), claudeBody, "vertex-token", "service_account", "claude-sonnet-4-6@20260218", false, false)
	require.NoError(t, err)
	vertexOut := readUpstreamBodyForTest(t, vertexReq)
	require.Equal(t, "1h", gjson.GetBytes(vertexOut, "cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(vertexOut, "system.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(vertexOut, "tools.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(vertexOut, "messages.0.content.0.cache_control.ttl").String())

	passReq, passWire, err := svc.buildPassthroughRequest(ctx, c, &AttemptState{}, newAnthropicAPIKeyPassthroughProviderForBetaTest(), claudeBody, "upstream-key")
	require.NoError(t, err)
	_ = readUpstreamBodyForTest(t, passReq)
	require.Equal(t, "1h", gjson.GetBytes(passWire, "cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(passWire, "system.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(passWire, "tools.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(passWire, "messages.0.content.0.cache_control.ttl").String())

	nonClaudeBody := []byte(`{"model":"glm-5","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)
	nonClaudeReq, nonClaudeWire, err := svc.buildPassthroughRequest(ctx, c, &AttemptState{}, newAnthropicAPIKeyPassthroughProviderForBetaTest(), nonClaudeBody, "upstream-key")
	require.NoError(t, err)
	_ = readUpstreamBodyForTest(t, nonClaudeReq)
	require.Equal(t, "5m", gjson.GetBytes(nonClaudeWire, "messages.0.content.0.cache_control.ttl").String())
}

// TestBuildCountTokensRequestAnthropicAPIKeyPassthrough_StripsContextManagementWhenClientHeaderMissingBeta
// supplies context-management edits to native counting without their beta. The
// prepared counting request removes that field while using the API key
// passthrough builder.
func TestBuildCountTokensRequestAnthropicAPIKeyPassthrough_StripsContextManagementWhenClientHeaderMissingBeta(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,token-counting-2024-11-01")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughProviderForBetaTest(), body, "token", "apikey", "", false, true,
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"count_tokens passthrough + 客户端未带 context-management beta → strip")
}

// TestBuildUpstreamRequest_OAuthMimicHaiku_PreservesContextManagementEndToEnd
// builds a Haiku OAuth mimic request with context-management edits. Prepared
// body retains the feature while outgoing betas include context-management and
// the Claude Code identity token.
func TestBuildUpstreamRequest_OAuthMimicHaiku_PreservesContextManagementEndToEnd(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 401, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "oauth-tok"},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
	// Haiku mimicry sends the full beta set, including context-management,
	// so the body field stays.
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", false, true, // mimicClaudeCode=true
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"OAuth mimic + Haiku 端到端：outgoing body 必须保留 context_management")
	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaContextManagement),
		"对称约束：outgoing anthropic-beta header 必须包含 context-management beta")
	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaClaudeCode),
		"Haiku mimic 必须携带 claude-code beta")
}

// TestBuildUpstreamRequest_APIKeyHaiku_RemainsUnmimicked builds an API key
// Haiku request carrying client system text and thinking. The prepared system
// remains client-owned, API key beta selection applies, and OAuth billing text
// is absent.
func TestBuildUpstreamRequest_APIKeyHaiku_RemainsUnmimicked(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "sk-ant-xxx"},
			Status:      billing.StatusActive, Schedulable: true,
		},
	}
	body := []byte(`{"model":"claude-haiku-4-5","system":"API-key client system","thinking":{"type":"enabled"},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true, InjectAPIKeyBeta: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"sk-ant-xxx", "apikey", "claude-haiku-4-5", false, false,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.Equal(t, "API-key client system", gjson.GetBytes(outBody, "system").String())
	require.Equal(t, anthropic.APIKeyHaikuBetaHeader, anthropic.GetHeaderRaw(req.Header, "anthropic-beta"))
	require.False(t, anthropic.AnthropicBetaTokensContains(anthropic.GetHeaderRaw(req.Header, "anthropic-beta"), anthropic.BetaOAuth))
	require.NotContains(t, string(outBody), "x-anthropic-billing-header:")
}

// TestBuildUpstreamRequest_OAuthMimicNonHaiku_PreservesContextManagementEndToEnd
// builds a Sonnet OAuth mimic request with context-management edits. The
// outgoing body and beta header both retain that feature under the full mimic
// beta set.
func TestBuildUpstreamRequest_OAuthMimicNonHaiku_PreservesContextManagementEndToEnd(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 402, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "oauth-tok"},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
	// Sonnet mimicry sends FullClaudeCodeMimicryBetas, including
	// context-management, so the corresponding body field stays.
	body := []byte(`{"model":"claude-sonnet-4-6","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"oauth-tok", "oauth", "claude-sonnet-4-6", false, true,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"OAuth mimic + non-haiku：outgoing body 必须保留 context_management。")
	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaContextManagement),
		"对称约束：outgoing anthropic-beta header 同时含 context-management beta")
}

// TestBuildUpstreamRequest_OAuthTransparentHaikuWithRealCCBeta_PreservesField
// builds a transparent Haiku OAuth request with a real client context-
// management beta. Both the outgoing beta and body feature remain present after
// request preparation.
func TestBuildUpstreamRequest_OAuthTransparentHaikuWithRealCCBeta_PreservesField(t *testing.T) {
	// A real Claude Code client supplies the context-management beta for
	// Haiku. Passing the beta through must also keep its body field.

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta",
		"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27")

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 403, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "oauth-tok"},
			Status:      billing.StatusActive, Schedulable: true,
		},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", false, false, // mimicClaudeCode=false for a real Claude Code client.
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaContextManagement),
		"真 CC 透传路径：客户端 header 中的 context-management beta 必须保留")
	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"回归保护：真 CC + haiku + 客户端带 beta token 时，clear_thinking_20251015 功能不能静默失效")
}

// TestBuildCountTokensRequest_OAuthMimicHaiku_PreservesContextManagementEndToEnd
// builds Haiku counting through OAuth mimic preparation with context-
// management edits. The final request retains that feature together with
// context-management and token-counting beta tokens.
func TestBuildCountTokensRequest_OAuthMimicHaiku_PreservesContextManagementEndToEnd(t *testing.T) {
	// Counting injects BetaContextManagement and BetaTokenCounting.
	// Sanitization keeps the body field because the final header contains
	// its beta token.

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 411, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "oauth-tok"},
			Status:      billing.StatusActive, Schedulable: true,
		},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", true, false, // mimicClaudeCode=true
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaContextManagement),
		"count_tokens mimic 始终注入 context-management beta")
	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"对称约束：final beta 含 token 时 body 字段保留")
	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaTokenCounting),
		"count_tokens 路径必须含 token-counting beta")
}

// TestBuildCountTokensRequest_OAuthMimic_DropsInjectedMaxTokens first
// normalizes an OAuth generation request with its default max_tokens. Counting
// preparation removes that generation parameter from the outgoing request body
// before sending it.
func TestBuildCountTokensRequest_OAuthMimic_DropsInjectedMaxTokens(t *testing.T) {
	// OAuth normalization injects max_tokens=128000 for generation;
	// count_tokens rejects that field.

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 413, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "oauth-tok"},
			Status:      billing.StatusActive, Schedulable: true,
		},
	}
	normalized := anthropic.NormalizeClaudeOAuthRequestBody(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[]}`), anthropic.ClaudeOAuthNormalizeOptions{},
	)
	require.Equal(t, int64(128000), gjson.GetBytes(normalized, "max_tokens").Int(),
		"前置条件：OAuth mimic 注入 Claude Code 默认 max_tokens")

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		provider, normalized,
		"oauth-tok", "oauth", "claude-sonnet-4-5", true, false,
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "max_tokens").Exists(),
		"count_tokens 上游请求体不得包含 max_tokens")
}

// TestBuildCountTokensRequest_APIKeyHaiku_StripsContextManagementEndToEnd
// builds Haiku API key counting with context-management edits and an unrelated
// beta. Prepared counting bytes omit the field because its feature token is
// absent.
func TestBuildCountTokensRequest_APIKeyHaiku_StripsContextManagementEndToEnd(t *testing.T) {
	// API key counting strips context_management when the client omits its
	// beta token.

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 412, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "sk-ant-xxx"},
			Status:      billing.StatusActive, Schedulable: true,
		},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"sk-ant-xxx", "apikey", "claude-haiku-4-5", false, false,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.False(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"count_tokens API-key + 客户端未带 beta token → body strip")
}

// TestBuildCountTokensRequestAnthropicAPIKeyPassthrough_PreservesContextManagementWhenClientHeaderHasBeta
// builds native API key counting with context- management beta and edits
// together. The prepared outgoing body retains that field under the
// client-selected feature header.
func TestBuildCountTokensRequestAnthropicAPIKeyPassthrough_PreservesContextManagementWhenClientHeaderHasBeta(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,context-management-2025-06-27,token-counting-2024-11-01")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughProviderForBetaTest(), body, "token", "apikey", "", false, true,
	)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"count_tokens passthrough + 客户端带 context-management beta → 字段保留")
}

// TestBuildUpstreamRequest_APIKeyHaikuWithContextManagement_StripsField builds
// an API key Haiku generation request with context-management edits and an
// unrelated beta. The prepared body removes the unsupported feature before
// reaching upstream transport.
func TestBuildUpstreamRequest_APIKeyHaikuWithContextManagement_StripsField(t *testing.T) {
	// The API key request carries context_management without its beta
	// token. Sanitization must remove the body field.

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "sk-ant-xxx"},
			Status:      billing.StatusActive, Schedulable: true,
		},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"sk-ant-xxx", "apikey", "claude-haiku-4-5", false, false,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.False(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"API-key + haiku + 客户端未带 beta token → body 字段必须被 strip")
}

// TestBuildUpstreamRequest_OAuthMimicHaiku_StripsFallbacksEndToEnd supplies a
// default fallbacks value to Haiku OAuth mimic construction. The outgoing body
// omits fallbacks and its beta while keeping the mimic context-management
// token.
func TestBuildUpstreamRequest_OAuthMimicHaiku_StripsFallbacksEndToEnd(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 601, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "oauth-tok"},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
	// Simulate a client sending the default fallbacks value.
	body := []byte(`{"model":"claude-haiku-4-5","fallbacks":"default","messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		provider, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", false, true, // mimicClaudeCode=true
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := anthropic.GetHeaderRaw(req.Header, "anthropic-beta")

	require.False(t, gjson.GetBytes(outBody, "fallbacks").Exists(),
		"OAuth mimic 端到端：mimic beta 集合不含 fallback beta → outgoing body 必须没有 fallbacks，"+
			"否则上游报 fallbacks: Extra inputs are not permitted")
	require.False(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaServerSideFallback),
		"修复策略是剥字段而非注入 beta：outgoing anthropic-beta 不得含 server-side-fallback beta")
	require.True(t, anthropic.AnthropicBetaTokensContains(outBeta, anthropic.BetaContextManagement),
		"mimic beta 集合本身不受影响")
}

// TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_StripsFallbacksWhenClientHeaderMissingBeta
// supplies native API key fallbacks without the client feature beta. The
// prepared Messages body removes that field before the upstream request is
// constructed.
func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_StripsFallbacksWhenClientHeaderMissingBeta(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// The client supplies no server-side-fallback beta.
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20")

	body := []byte(`{"model":"claude-haiku-4-5","fallbacks":"default","messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughProviderForBetaTest(), body, "token",
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "fallbacks").Exists(),
		"API-key passthrough + 客户端未带 fallback beta → strip body 字段")
}

// TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_PreservesFallbacksWhenClientHeaderHasBeta
// supplies native API key fallback models with their client beta. The prepared
// body retains both entries and their order in the upstream array.
func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_PreservesFallbacksWhenClientHeaderHasBeta(t *testing.T) {
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,server-side-fallback-2026-07-01")

	// The model-array form stays intact when its beta token is present.
	body := []byte(`{"model":"claude-opus-4-7","fallbacks":["claude-opus-4-6","claude-sonnet-4-6"],"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughProviderForBetaTest(), body, "token",
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.True(t, gjson.GetBytes(outBody, "fallbacks").Exists(),
		"客户端 header 带 server-side-fallback beta → 字段保留（不过度删除）")
	fallbacks := gjson.GetBytes(outBody, "fallbacks").Array()
	require.Len(t, fallbacks, 2)
	require.Equal(t, "claude-opus-4-6", fallbacks[0].String())
	require.Equal(t, "claude-sonnet-4-6", fallbacks[1].String())
}

// TestClaudeAPIKeyFastModeCannotBypassSystemFilter forces Fast mode while the
// attempt filter removes its beta. Request construction leaves both body speed
// and Fast header token absent after the policy is applied.
func TestClaudeAPIKeyFastModeCannotBypassSystemFilter(t *testing.T) {
	svc := NewRuntime(Dependencies{Prices: fastModeTestResolver()}, Options{Configured: true})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey}}
	ctx := fastModeTestContext(apikey.APIKeyFastModePolicyForceOn, "claude-opus-4-8")
	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	// Simulate a system beta policy that filters Claude Fast.
	state := &AttemptState{BetaEvaluated: true, BetaFilters: map[string]struct{}{anthropic.BetaFastMode: {}}}

	req, wireBody, err := svc.buildRequest(ctx, c, state, provider, []byte(`{"model":"claude-opus-4-8","messages":[]}`), "test-key", "apikey", "claude-opus-4-8", false, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wireBody, "speed").Exists())
	require.False(t, anthropic.ContainsBetaToken(anthropic.GetHeaderRaw(req.Header, "anthropic-beta"), anthropic.BetaFastMode))
}

// fastModeTestContext installs Fast policy, group identity, and model values in
// context.
func fastModeTestContext(policy, model string) context.Context {
	ctx := apikey.WithFastModePolicy(context.Background(), policy)
	ctx = requeststate.WithGroup(ctx, &routing.Group{ID: 11})
	return context.WithValue(ctx, telemetry.Model, model)
}

// fastModeTestResolver builds offline price fixtures supporting the tested Fast
// mode requests.
func fastModeTestResolver() *billing.PriceResolver {
	pricing := catalogprovider.NewServiceFromSnapshot(catalogprovider.Options{ModelLookupCandidates: modelidentity.CandidatesFactory}, nil, catalogprovider.Snapshot{Data: map[string]*billingpricing.CatalogModelPricing{
		"gpt-5.5": {
			InputCostPerToken:     5e-6,
			OutputCostPerToken:    30e-6,
			SupportsServiceTier:   true,
			SupportsPromptCaching: true,
		},
		"claude-opus-4-8": {
			InputCostPerToken:     5e-6,
			OutputCostPerToken:    25e-6,
			SupportsServiceTier:   true,
			SupportsPromptCaching: true,
		},
	}})
	billing := billingtestkit.Calculator(pricing, nil)
	return billingtestkit.PriceResolver(nil, billing)
}

// GetFingerprint returns an outer value copy of the stored fixture fingerprint.
// A missing fingerprint returns nil, allowing request preparation to exercise
// fingerprint creation or default header selection.
func (s *stubIdentityCache) GetFingerprint(_ context.Context, _ int64) (*anthropic.Fingerprint, error) {
	if s.fingerprint == nil {
		return nil, nil
	}
	clone := *s.fingerprint
	return &clone, nil
}

// SetFingerprint counts the write and saves an outer copy for current and last-
// set assertions. The supplied fingerprint must exist, and both saved pointers
// reference the same copied value.
func (s *stubIdentityCache) SetFingerprint(_ context.Context, _ int64, fingerprint *anthropic.Fingerprint) error {
	s.setCalls++
	clone := *fingerprint
	s.lastSet = &clone
	s.fingerprint = &clone
	return nil
}

// GetMaskedSessionID returns an empty identity with success in this header
// fixture. Session masking persistence is outside these assertions, so callers
// receive its missing-value result during request preparation.
func (s *stubIdentityCache) GetMaskedSessionID(_ context.Context, _ int64) (string, error) {
	return "", nil
}

// SetMaskedSessionID accepts session identity writes with success for this
// header fixture. The fixture records fingerprint writes separately, and these
// tests do not retain masked-session values for later reads.
func (s *stubIdentityCache) SetMaskedSessionID(_ context.Context, _ int64, _ string) error {
	return nil
}
