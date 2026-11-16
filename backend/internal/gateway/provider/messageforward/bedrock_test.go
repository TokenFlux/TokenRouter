package messageforward

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// bedrockCaptureTransport records prepared Bedrock request payloads and returns
// an in-memory Messages JSON response without network access.
type bedrockCaptureTransport struct {
	bodies [][]byte // captured outbound Bedrock request bodies
}

// Do delegates to DoWithTLS with a nil TLS fingerprint profile.
func (t *bedrockCaptureTransport) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	return t.DoWithTLS(req, proxyURL, providerID, providerConcurrency, nil)
}

// DoWithTLS captures req.Body bytes and returns a valid nonstreaming Bedrock response.
func (t *bedrockCaptureTransport) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	t.bodies = append(t.bodies, raw)
	resp := `{"id":"msg_bedrock","type":"message","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(resp)),
	}, nil
}

// TestResolveBedrockBetaTokensForRequest_BlocksOnOriginalAnthropicToken
// supplies an Anthropic beta that policy blocks before Bedrock conversion.
// Request preparation returns the configured block message even though the
// platform later translates tokens.
func TestResolveBedrockBetaTokensForRequest_BlocksOnOriginalAnthropicToken(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "advanced-tool-use-2025-11-20",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "advanced tool use is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock}}

	_, err = svc.bedrockBetaTokens(
		context.Background(),
		provider,
		"advanced-tool-use-2025-11-20",
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err == nil {
		t.Fatal("expected raw advanced-tool-use token to be blocked before Bedrock transform")
	}
	if err.Error() != "advanced tool use is blocked" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveBedrockBetaTokensForRequest_FiltersAfterBedrockTransform filters
// the Bedrock tool-search token produced from an Anthropic beta. The translated
// removal target disappears from the result while request preparation completes
// successfully.
func TestResolveBedrockBetaTokensForRequest_FiltersAfterBedrockTransform(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken: "tool-search-tool-2025-10-19",
				Action:    anthropic.BetaPolicyActionFilter,
				Scope:     anthropic.BetaPolicyScopeAll,
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock}}

	betaTokens, err := svc.bedrockBetaTokens(
		context.Background(),
		provider,
		"advanced-tool-use-2025-11-20",
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, token := range betaTokens {
		if token == "tool-search-tool-2025-10-19" {
			t.Fatalf("expected transformed Bedrock token to be filtered")
		}
	}
}

// TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedComputerUse
// supplies a computer-use tool with an empty beta header. Preparation returns
// the policy block message for the beta inferred from the body tool.
func TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedComputerUse(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "computer-use-2025-11-24",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "computer use is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock}}

	// The body supplies the tool while the header omits its beta.
	_, err = svc.bedrockBetaTokens(
		context.Background(),
		provider,
		"", // Empty beta header.
		[]byte(`{"tools":[{"type":"computer_20250124","name":"computer"}],"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err == nil {
		t.Fatal("expected body-injected computer-use to be blocked")
	}
	if err.Error() != "computer use is blocked" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedToolSearch
// supplies a tool-search body tool with an empty beta header. The configured
// block message is returned for the platform token inferred during preparation.
func TestResolveBedrockBetaTokensForRequest_BlocksBodyAutoInjectedToolSearch(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "tool-search-tool-2025-10-19",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "tool search is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock}}

	// The body supplies tool search while the header omits its beta.
	_, err = svc.bedrockBetaTokens(
		context.Background(),
		provider,
		"",
		[]byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"}],"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-sonnet-4-6",
	)
	if err == nil {
		t.Fatal("expected body-injected tool-search-tool to be blocked")
	}
	if err.Error() != "tool search is blocked" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveBedrockBetaTokensForRequest_PassesWhenNoBlockRuleMatches blocks
// context-1m while the body requests a computer-use tool. The resolved
// computer-use beta remains in the result because its token has no matching
// block rule.
func TestResolveBedrockBetaTokensForRequest_PassesWhenNoBlockRuleMatches(t *testing.T) {
	settings := &anthropic.BetaPolicySettings{
		Rules: []anthropic.BetaPolicyRule{
			{
				BetaToken:    "context-1m-2025-08-07",
				Action:       anthropic.BetaPolicyActionBlock,
				Scope:        anthropic.BetaPolicyScopeAll,
				ErrorMessage: "context is blocked",
			},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	svc := NewRuntime(Dependencies{Settings: newBetaRuntime(map[string]string{gateway.SettingKeyBetaPolicySettings: string(raw)})}, Options{})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeBedrock}}

	// The body injects computer-use, but the block rule targets context-1m.
	tokens, err := svc.bedrockBetaTokens(
		context.Background(),
		provider,
		"",
		[]byte(`{"tools":[{"type":"computer_20250124","name":"computer"}],"messages":[{"role":"user","content":"hi"}]}`),
		"us.anthropic.claude-opus-4-6-v1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, token := range tokens {
		if token == "computer-use-2025-11-24" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected computer-use token to be present")
	}
}

// TestBedrockAttempt_ForceClaude1hCacheByModelCapability runs Bedrock request
// preparation with forced one-hour Claude caching across TTL-capable Claude 4.6
// and older Claude 3.5 routes. It checks Claude 4.6 retains 1h on top-level,
// system, tool, and message markers while Claude 3.5 strips unsupported TTLs.
func TestBedrockAttempt_ForceClaude1hCacheByModelCapability(t *testing.T) {
	ctx := gatewayprovider.WithAPIKeyForceClaude1hCache(context.Background(), true)
	transport := &bedrockCaptureTransport{}
	svc := NewRuntime(Dependencies{Transport: transport}, Options{ResponseReadLimit: 1 << 20})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           701,
			Name:         "bedrock-apikey",
			Platform:     capability.PlatformAnthropic,
			Type:         capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"auth_mode":  "apikey",
				"api_key":    "bedrock-key",
				"aws_region": "us-east-1",
			},
		},
	}

	makeBody := func(model string) []byte {
		return []byte(`{
			"model":"` + model + `",
			"max_tokens":64,
			"cache_control":{"type":"ephemeral","ttl":"5m"},
			"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],
			"tools":[{"name":"bash","cache_control":{"type":"ephemeral","ttl":"5m"},"input_schema":{"type":"object"}}],
			"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]
		}`)
	}

	boundary, err := newPrivateHTTPFixture(httptest.NewRecorder())
	require.NoError(t, err)
	boundary.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	opusBody := makeBody("claude-opus-4-6")
	_, err = svc.bedrock(ctx, boundary, &AttemptState{}, provider, &requeststate.ParsedRequest{
		Body:  requeststate.NewRequestBodyRef(opusBody),
		Model: "claude-opus-4-6",
	}, time.Now())
	require.NoError(t, err)
	require.Len(t, transport.bodies, 1)
	require.Equal(t, "1h", gjson.GetBytes(transport.bodies[0], "cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(transport.bodies[0], "system.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(transport.bodies[0], "tools.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(transport.bodies[0], "messages.0.content.0.cache_control.ttl").String())

	boundaryOld, err := newPrivateHTTPFixture(httptest.NewRecorder())
	require.NoError(t, err)
	boundaryOld.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	oldBody := makeBody("anthropic.claude-3-5-sonnet-20241022-v2:0")
	_, err = svc.bedrock(ctx, boundaryOld, &AttemptState{}, provider, &requeststate.ParsedRequest{
		Body:  requeststate.NewRequestBodyRef(oldBody),
		Model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
	}, time.Now())
	require.NoError(t, err)
	require.Len(t, transport.bodies, 2)
	require.False(t, gjson.GetBytes(transport.bodies[1], "cache_control.ttl").Exists())
	require.False(t, gjson.GetBytes(transport.bodies[1], "system.0.cache_control.ttl").Exists())
	require.False(t, gjson.GetBytes(transport.bodies[1], "tools.0.cache_control.ttl").Exists())
	require.False(t, gjson.GetBytes(transport.bodies[1], "messages.0.content.0.cache_control.ttl").Exists())
}
