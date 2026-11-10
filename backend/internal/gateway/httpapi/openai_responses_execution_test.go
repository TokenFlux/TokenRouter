package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	sessiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/session/testkit"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	openaicore "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// An admitted Responses request can be prepared differently for each provider
// while retaining the client's model and tool identity. These fixtures compare
// captured upstream bodies and returned attribution with client JSON or SSE,
// covering model mappings, effort and tier policy, namespace conversion and
// exact numeric text.
//
// HTTP errors and failed stream events have different effects before client
// output and after keepalive commits it. Scripted transports expose those
// decisions through typed failover, direct error output, usage and body
// closure. The blocking header transport coordinates request cancellation
// against a guard timer, while the loopback transport exercises model
// restoration over a loopback HTTP exchange.

const (
	// openAIResponsesInputTextMaxChars sets the ASCII prefix length for the
	// oversized tool-output fixture, which appends one multibyte character to it.
	openAIResponsesInputTextMaxChars = 10000000

	// codexNamespaceRequestBody supplies namespace declarations and historical
	// calls alongside a stale namespace on an ordinary message, supporting
	// preservation and stripping assertions.
	codexNamespaceRequestBody = `{
	"model":"gpt-5.6-terra",
	"stream":false,
	"instructions":"test",
	"tools":[
		{"type":"namespace","name":"collaboration","description":"Tools for spawning and managing sub-agents.","tools":[
			{"type":"function","name":"spawn_agent","description":"Call as to=functions.collaboration.spawn_agent","parameters":{"type":"object"}},
			{"type":"function","name":"wait_agent","parameters":{"type":"object"}}
		]},
		{"type":"function","name":"exec","parameters":{"type":"object"}}
	],
	"input":[
		{"type":"function_call","namespace":"collaboration","name":"spawn_agent","call_id":"call_1","arguments":"{}"},
		{"type":"message","role":"user","namespace":"leftover","content":[{"type":"input_text","text":"hello"}]}
	]
}`

	// namespaceForwardOKResponse supplies a successful usage-bearing body for
	// namespace forwarding tests, allowing their assertions to focus on captured
	// request preparation.
	namespaceForwardOKResponse = `{"id":"resp_ns","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`
)

// blockingOpenAIResponseHeaderUpstream models a header wait cancelled by the
// first-output timer.
type blockingOpenAIResponseHeaderUpstream struct {
	// canceled and once report the first request-cancellation branch selected by
	// Do. Tests initialize the channel before forwarding and inspect it after the
	// call returns.
	canceled chan struct{} // upstream cancellation signal
	once     sync.Once     // cancellation signal close guard
}

// reasoningCacheStub supplies preloaded reasoning reads and captures reasoning
// writes for Chat history conversion.
type reasoningCacheStub struct {
	sessiontestkit.StickyCache // inherited session binding fixture
	// sets captures written item text and getResp supplies preloaded item text;
	// writes leave the read fixture unchanged. Tests serialize these maps and the
	// fixture ignores expiration.
	sets    map[string]string // captured item reasoning text
	getResp map[string]string // preloaded item reasoning text
}

// passthroughErrReadCloser injects body read failure after a successful
// Compact response establishment.
type passthroughErrReadCloser struct {
	// err is returned on every Read; nil selects io.ErrUnexpectedEOF so the
	// fixture still fails after establishment.
	err error // injected body read error
}

// openAIPassthroughFailoverRepo records provider health deadlines produced by
// passthrough failure handling.
type openAIPassthroughFailoverRepo struct {
	gatewaytestkit.HealthStoreBase // inherited health storage defaults
	// rateLimitCalls and overloadCalls retain supplied deadlines in call order
	// for their respective health updates. Provider IDs are ignored and tests
	// serialize recording with inspection.
	rateLimitCalls []time.Time // captured rate-limit reset deadlines
	overloadCalls  []time.Time // captured overload cooldown deadlines
}

// mappingHTTPTransport redirects prepared upstream requests to the loopback
// fixture server.
type mappingHTTPTransport struct {
	// client sends redirected requests and endpoint supplies their loopback
	// scheme and host. Both references are supplied before forwarding and kept
	// stable while a fixture exchange runs.
	client   *http.Client // fixture HTTP exchange client
	endpoint *url.URL     // loopback server URL
}

// tlsRouterTestStore supplies router configuration for synchronous startup in
// identity-selection tests.
type tlsRouterTestStore struct {
	// TLSFingerprintRouterRepository supplies the other repository methods
	// through embedding. This fixture calls List and leaves that embedded
	// interface unset.
	egress.TLSFingerprintRouterRepository // unused inherited repository methods
	// values is returned directly by List, including a nil slice; callers keep
	// its router pointers stable during startup reload.
	values []*egress.TLSFingerprintRouter // supplied router configuration
}

// TestAdaptiveProtocolRoutesKimiResponsesToNativeResponses captures adaptive
// Kimi Responses input with effort and continuation controls. It checks native
// endpoint/shape, retained effort and disabled store/previous-response fields
// before transport failure.
func TestAdaptiveProtocolRoutesKimiResponsesToNativeResponses(t *testing.T) {
	body := []byte(`{"model":"k3-256k","input":"hello","reasoning":{"effort":"none"},"store":true,"previous_response_id":"resp_old","stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	provider := adaptiveProtocolTestProvider(capability.PlatformKimi, map[string]any{
		providercore.APIProtocolChatCompletions: "http://chat.example",
		providercore.APIProtocolAnthropic:       "http://anthropic.example",
		providercore.APIProtocolResponses:       "http://responses.example/v1",
	})

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), provider, body)
	require.Error(t, err)
	require.Equal(t, "http://responses.example/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
}

// TestAdaptiveProtocolRoutesKimiCodingResponsesToNativeResponses captures Kimi
// coding-mode Responses input under configured protocol bases. It checks the
// coding Responses URL and retained input before the deliberate fixture
// transport failure.
func TestAdaptiveProtocolRoutesKimiCodingResponsesToNativeResponses(t *testing.T) {
	body := []byte(`{"model":"k3-256k","input":"hello","stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	provider := adaptiveProtocolTestProvider(capability.PlatformKimi, map[string]any{
		providercore.APIProtocolChatCompletions: "https://api.kimi.com/coding/v1",
		providercore.APIProtocolAnthropic:       "https://api.kimi.com/coding",
		providercore.APIProtocolResponses:       "https://api.kimi.com/coding/v1",
	})
	provider.Record.Credentials["provider_mode"] = providercore.ProviderModeCoding

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), provider, body)
	require.Error(t, err)
	require.Equal(t, "https://api.kimi.com/coding/v1/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

// TestAdaptiveProtocolRoutesDeepSeekResponsesToNativeResponses captures
// adaptive DeepSeek input with output limit and continuation controls. It
// checks native unversioned endpoint, retained output limit, removed
// continuation and absent injected instructions.
func TestAdaptiveProtocolRoutesDeepSeekResponsesToNativeResponses(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4","input":"hello","max_output_tokens":32,"store":true,"previous_response_id":"resp_old","stream":false}`)
	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	provider := adaptiveProtocolTestProvider(capability.PlatformDeepseek, map[string]any{
		providercore.APIProtocolChatCompletions: "http://chat.example",
		providercore.APIProtocolAnthropic:       "http://anthropic.example",
		providercore.APIProtocolResponses:       "http://responses.example",
	})

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), provider, body)
	require.Error(t, err)
	require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.Equal(t, int64(32), gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
}

// TestForward_ResponsesServiceTierFastNormalizedToPriorityUpstream sends the
// fast alias through native Responses preparation. It checks outgoing priority
// and returned requested-tier attribution after the successful fixture
// exchange.
func TestForward_ResponsesServiceTierFastNormalizedToPriorityUpstream(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-st"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}, transport: upstream, readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{}}, nil)})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test"},
			Extra:       map[string]any{},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastBody)
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String(),
		"client alias fast must reach the upstream as priority")
	// The Forward result records the normalized requested tier.
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
}

// TestForward_ResponsesServiceTierOmittedStaysOmitted omits tier from the
// admitted Responses body. It checks absent outgoing field and nil result tier
// after successful forwarding of the fixture response.
func TestForward_ResponsesServiceTierOmittedStaysOmitted(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-st2"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_2","object":"response","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}, transport: upstream, readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{}}, nil)})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test"},
			Extra:       map[string]any{},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastBody)
	require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists(),
		"omitted service_tier must stay omitted")
	require.Nil(t, result.ServiceTier)
}

// TestForwardStreaming_ServiceTierPropagatedToResult requests fast while the
// fixture stream omits echoed tier. It checks normalized priority attribution
// and retained text with no injected client service-tier field.
func TestForwardStreaming_ServiceTierPropagatedToResult(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	streamPayload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"it_1\",\"output_index\":0,\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n"

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-resp-stream-st"}},
		Body:       io.NopCloser(strings.NewReader(streamPayload)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}, transport: upstream, readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{}}, nil)})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test"},
			Extra:       map[string]any{},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier, "streaming billing context must carry the normalized tier")
	// The upstream stream omits service_tier while the result retains requested
	// priority. Inspect client output separately to detect accidental insertion
	// of that requested tier.
	require.Contains(t, rec.Body.String(), `"delta":"hi"`, "streamed content must reach the client")
	require.NotContains(t, rec.Body.String(), `"service_tier"`, "upstream did not return service_tier, client stream must stay untouched")
}

// TestForward_ResponsesKeepsOutboundAndObservedServiceTiersSeparate requests
// fast while buffered output echoes default. It checks requested priority,
// observed default and the retained default field in client JSON.
func TestForward_ResponsesKeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	// Return default from the fixture despite requested fast so result assertions
	// distinguish requested priority from the observed upstream tier and the
	// client-visible field.
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-echo"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.5","service_tier":"default","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}, transport: upstream, readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{}}, nil)})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test"},
			Extra:       map[string]any{},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	// Client JSON retains the observed upstream default tier.
	require.Contains(t, rec.Body.String(), `"service_tier":"default"`)
	require.NotContains(t, rec.Body.String(), `"service_tier":"priority"`)
}

// TestForwardStreaming_KeepsOutboundAndObservedServiceTiersSeparate requests
// fast while the streaming terminal echoes default. It checks requested
// priority, separate observed default and default in downstream terminal
// output.
func TestForwardStreaming_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	streamPayload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"service_tier\":\"default\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n"

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-resp-echo-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamPayload)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}, transport: upstream, readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{}}, nil)})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test"},
			Extra:       map[string]any{},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	// The client terminal event retains the observed default tier.
	require.Contains(t, rec.Body.String(), `"service_tier":"default"`)
}

// TestForward_ServiceTierFilteredByPolicyBillsStandard filters a priority
// field before the selected Responses exchange. It checks absent outgoing
// field and nil result tier; this fixture does not run billing completion.
func TestForward_ServiceTierFilteredByPolicyBillsStandard(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"priority","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	// The administrator priority-to-filter rule removes the outbound field.
	settings := &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{
		ServiceTier: tierpolicy.OpenAIFastTierPriority,
		Action:      anthropic.BetaPolicyActionFilter,
		Scope:       anthropic.BetaPolicyScopeAll,
	}}}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	repo := &gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{gateway.SettingKeyOpenAIFastPolicySettings: string(raw)}}

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-filter"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}, transport: upstream, readers: newHTTPReadersFixture(repo, nil)})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test"},
			Extra:       map[string]any{},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	// The filtered body and result leave requested-tier attribution absent. This
	// assertion observes forwarding metadata; it does not execute billing
	// completion or select a rate.
	require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists(),
		"policy filter must strip service_tier from the outbound body")
	require.Nil(t, result.ServiceTier, "filtered request must not bill as fast")
}

// TestOpenAIAgentIdentityTaskInvalidRetriesExactlyOnce returns invalid-task
// evidence around local registration exchanges in standard and passthrough
// execution. It checks bounded request/registration counts and replacement
// assertion task identity in the captured retry.
func TestOpenAIAgentIdentityTaskInvalidRetriesExactlyOnce(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 23,
			Name:        "agent-identity",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billingcore.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"auth_mode":          providercore.OpenAIAuthModeAgentIdentity,
				"agent_runtime_id":   key.RuntimeID,
				"agent_private_key":  privateKey,
				"task_id":            "task-old",
				"chatgpt_account_id": "provider-agent-retry",
			},
		},
	}
	repo := &agentIdentityForwardRepo{provider: provider}
	registerCalls := 0
	// Reuse the registration endpoint across sequential Forward calls so
	// cumulative registration counts expose retries and assertion identity
	// replacement in the same provider state.
	registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalls++
		// The callback returns the replacement task fixture after counting
		// registration and discards write errors. The test checks Forward results
		// and captured retry identity afterward.
		_, _ = io.WriteString(w, `{"task_id":"task-new"}`)
	}))
	// Close the local registration server after all three forwarding scenarios
	// finish so each scenario can inspect the same cumulative counter.
	defer registerServer.Close()

	successBody := `{"id":"resp-agent-retry","object":"response","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(successBody))},
	}}
	require.True(t, openaicore.IsAgentTaskInvalidHTTPResponse(http.StatusUnauthorized, []byte(`{"error":{"code":"invalid_task_id"}}`)))
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, providers: repo, registerTaskURL: registerServer.URL, transport: upstream})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))

	_, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	require.NoError(t, err)
	require.Equal(t, 1, registerCalls)
	require.Len(t, upstream.requests, 2)
	require.NotEqual(t, upstream.requests[0].Header.Get("Authorization"), upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "task-new", decodeAgentAssertionTask(t, upstream.requests[1].Header.Get("Authorization")))

	// Replace the consumed response script with two invalid tasks to check
	// recovery stops after its single retry, using the same provider and
	// registration endpoint.
	upstream.responses = []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
	}
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	_, err = svc.Forward(context.Background(), c2, provider, []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	require.Error(t, err)
	require.Equal(t, 2, registerCalls)
	require.Len(t, upstream.requests, 4)

	// Reset the provider task and response script for passthrough after managed
	// forwarding finishes; cumulative counters reveal one additional registration
	// and two additional attempts.
	provider.Record.Extra = map[string]any{"openai_passthrough": true}
	provider.Record.Credentials["task_id"] = "task-old-passthrough"
	upstream.responses = []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"))},
	}
	rec3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(rec3)
	c3.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	_, err = svc.Forward(context.Background(), c3, provider, []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	require.NoError(t, err)
	require.Equal(t, 3, registerCalls)
	require.Len(t, upstream.requests, 6)
}

// decodeAgentAssertionTask decodes the fixture AgentAssertion header and
// requires valid JSON before returning task_id. It uses the raw URL Base64
// variant after prefix trimming; the helper does not verify signature or
// authenticated ownership.
func decodeAgentAssertionTask(t *testing.T, header string) string {
	t.Helper()
	encoded := strings.TrimPrefix(header, "AgentAssertion ")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var envelope struct {
		TaskID string `json:"task_id"` // decoded assertion task
	}
	require.NoError(t, json.Unmarshal(decoded, &envelope))
	return envelope.TaskID
}

// TestOpenAIGatewayForwardUsesGlobalCompactModelOnInitialLegacyRequest
// supplies a legacy Compact request with a global model setting. It checks one
// successful exchange uses that model and the Compact endpoint path.
func TestOpenAIGatewayForwardUsesGlobalCompactModelOnInitialLegacyRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"instructions":"compact-test","input":[]}`)
	c := newOpenAICompactFallbackTestContext(t, "/v1/responses/compact")
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_compact","status":"completed","model":"global-compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{compactModel: "global-compact", transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
			Status:      billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, "global-compact", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Contains(t, upstream.requests[0].URL.Path, "/compact")
}

// TestOpenAIGatewayForwardRetriesExplicitNativeCompactHTTPFailureOnce rejects
// native compaction over HTTP before a successful model recovery. It checks
// that the retry supplies the replacement model's prompt even if the first
// model omitted it, preserves caller guidance and the trigger, and records one
// retry without changing the endpoint.
func TestOpenAIGatewayForwardRetriesExplicitNativeCompactHTTPFailureOnce(t *testing.T) {
	const sol = "gpt-6.1-sol"
	solPrompt := openaicore.DefaultCodexSynthInstructions(sol)
	retryPrompt := openaicore.DefaultCodexSynthInstructions("gpt-6.1-terra")
	for _, tc := range []struct {
		name        string // forwarding scenario name
		model       string // first upstream model
		passthrough bool   // provider passthrough enabled
		explicit    bool   // top-level prompt supplied by caller
		systemText  string // caller system text to promote
		lite        bool   // developer input supplies instructions
	}{
		{name: "managed", model: sol, systemText: solPrompt},
		{name: "passthrough", model: sol, passthrough: true},
		{name: "passthrough-without-initial-default", model: "gpt-5.5", passthrough: true},
		{name: "passthrough-lite-without-initial-default", model: "gpt-5.5", passthrough: true, lite: true},
		{name: "managed-explicit", model: sol, explicit: true},
		{name: "passthrough-explicit", model: sol, passthrough: true, explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := []map[string]any{
				{"type": "message", "role": "user", "content": "hello"},
				{"type": "compaction_trigger"},
			}
			if tc.systemText != "" {
				input = append([]map[string]any{{"type": "message", "role": "system", "content": tc.systemText}}, input...)
			}
			if tc.lite {
				input = append([]map[string]any{{"type": "message", "role": "developer", "content": "Client base instructions."}}, input...)
			}
			request := map[string]any{"model": tc.model, "stream": tc.passthrough, "input": input}
			if tc.explicit {
				request["instructions"] = solPrompt
			}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			c := newOpenAICompactFallbackTestContext(t, "/v1/responses")
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.lite {
				c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			}
			MarkOpenAINativeCompactionV2(c)

			completed := `{"id":"resp_compact","status":"completed","model":"gpt-6.1-terra","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
			contentType := "application/json"
			if tc.passthrough {
				completed = `data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hi"}` + "\n\n" +
					`data: {"type":"response.completed","response":` + completed + "}\n\ndata: [DONE]\n\n"
				contentType = "text/event-stream"
			}
			upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
				{
					StatusCode: http.StatusBadRequest,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"context_length_exceeded","message":"context window exceeded"}}`)),
				},
				{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{contentType}},
					Body:       io.NopCloser(strings.NewReader(completed)),
				},
			}}
			svc := newResponsesFixture(responsesFixtureInputs{compactModel: "gpt-6.1-terra", transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 1, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
					Extra:       map[string]any{"openai_passthrough": tc.passthrough},
					Status:      billingcore.StatusActive, Schedulable: true,
				},
			}

			result, err := svc.Forward(context.Background(), c, provider, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, tc.model, gjson.GetBytes(upstream.bodies[0], "model").String())
			require.Equal(t, "gpt-6.1-terra", gjson.GetBytes(upstream.bodies[1], "model").String())
			firstInstructions, secondInstructions := solPrompt, retryPrompt
			if tc.model != sol {
				firstInstructions = ""
				require.False(t, gjson.GetBytes(upstream.bodies[0], "instructions").Exists())
			}
			if tc.explicit {
				secondInstructions = solPrompt
			}
			if tc.systemText != "" {
				firstInstructions = tc.systemText + "\n\n" + firstInstructions
				secondInstructions = tc.systemText + "\n\n" + secondInstructions
			}
			require.True(t, firstInstructions == gjson.GetBytes(upstream.bodies[0], "instructions").String(), "unexpected first attempt instructions")
			if tc.lite {
				secondInstructions = ""
				require.False(t, gjson.GetBytes(upstream.bodies[1], "instructions").Exists())
				require.Equal(t, "Client base instructions.", gjson.GetBytes(upstream.bodies[1], `input.#(role=="developer").content`).String())
			}
			require.True(t, secondInstructions == gjson.GetBytes(upstream.bodies[1], "instructions").String(), "unexpected retry instructions")
			require.True(t, protocolopenai.HasCompactionTriggerInInput(upstream.bodies[1]))
			require.Equal(t, upstream.requests[0].URL.Path, upstream.requests[1].URL.Path)
			require.NotContains(t, upstream.requests[1].URL.Path, "/compact")
			rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, events, 1)
			require.Equal(t, "retry", events[0].Kind)
			require.Equal(t, "compact_model_fallback", events[0].Reason)
			require.Equal(t, http.StatusBadRequest, events[0].UpstreamStatusCode)
		})
	}
}

// TestOpenAIGatewayForwardRetriesExplicitNativeCompactSSEFailureBeforeOutput
// returns context-window failure inside native compaction SSE before success.
// It checks two prepared models and reuse of the ordinary Responses path on
// both attempts.
func TestOpenAIGatewayForwardRetriesExplicitNativeCompactSSEFailureBeforeOutput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"instructions":"compact-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
	c := newOpenAICompactFallbackTestContext(t, "/v1/responses")
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	MarkOpenAINativeCompactionV2(c)

	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("event: response.failed\n" +
				`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded","message":"context window exceeded"}}}` + "\n\n")),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_compact","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{compactModel: "gpt-5.4", transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
			Status:      billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.bodies[1], "model").String())
	require.Equal(t, upstream.requests[0].URL.Path, upstream.requests[1].URL.Path)
	require.NotContains(t, upstream.requests[1].URL.Path, "/compact")
}

// TestOpenAIGatewayForwardRetriesStreamingCompactFailureBeforeOutput returns a
// streamed compact failure followed by recovered output. It checks the second
// model, observed recovered model and successful terminal with no leaked
// context-window error.
func TestOpenAIGatewayForwardRetriesStreamingCompactFailureBeforeOutput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":true,"instructions":"compact-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	MarkOpenAINativeCompactionV2(c)

	failed := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","model":"failed-runtime","error":{"code":"context_length_exceeded","message":"context window exceeded"}}}` + "\n\n"
	completed := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_compact","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed))},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{compactModel: "gpt-5.4", transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
			Status:      billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.UpstreamResponseModel)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.bodies[1], "model").String())
	require.NotContains(t, recorder.Body.String(), "context_length_exceeded")
	require.Contains(t, recorder.Body.String(), "response.completed")
}

// TestOpenAIGatewayForwardDoesNotRecurseWhenCompactFallbackAlsoFails rejects
// both the requested and recovery models during native compaction. It checks
// forwarding stops after two attempts and reports the second error with the
// retry and final-error Ops events.
func TestOpenAIGatewayForwardDoesNotRecurseWhenCompactFallbackAlsoFails(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":true,"instructions":"compact-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	MarkOpenAINativeCompactionV2(c)

	failed := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"model_not_found","message":"model not found"}}}` + "\n\n"
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{compactModel: "gpt-5.4", transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
			Status:      billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.Error(t, err)
	require.Nil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.bodies[1], "model").String())
	var compactSignal *compact.Failure
	require.False(t, errors.As(err, &compactSignal))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "model not found")
	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 2)
	require.Equal(t, "retry", events[0].Kind)
	require.Equal(t, "compact_model_fallback", events[0].Reason)
	require.Equal(t, "http_error", events[1].Kind)
}

// TestResponseModelMissingAfterCompactRetryDoesNotInheritFailure fails with
// one echoed model before a successful terminal omits model. It checks the
// retry uses the recovery model while the result leaves observed model empty.
func TestResponseModelMissingAfterCompactRetryDoesNotInheritFailure(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":true,"instructions":"compact-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	MarkOpenAINativeCompactionV2(c)

	failed := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","model":"failed-runtime","error":{"code":"context_length_exceeded","message":"context window exceeded"}}}` + "\n\n"
	completed := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_compact","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed))},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{compactModel: "gpt-5.4", transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
			Status:      billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.UpstreamResponseModel)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.bodies[1], "model").String())
	require.NotContains(t, recorder.Body.String(), "context_length_exceeded")
	require.Contains(t, recorder.Body.String(), "response.completed")
}

// TestOpenAIGatewayService_Forward_CompactOnlyModelMappingOverridesOAuthUpstreamModel
// configures both ordinary and Compact mappings for an OAuth request. It
// checks the Compact mapping in outgoing body, result and Ops attribution
// while retaining the client model.
func TestOpenAIGatewayService_Forward_CompactOnlyModelMappingOverridesOAuthUpstreamModel(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"compact-test","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-compact-map"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_123","status":"completed","model":"gpt-5.4-openai-compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Name:        "openai-oauth",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":          "oauth-token",
				"chatgpt_account_id":    "chatgpt-acc",
				"model_mapping":         map[string]any{"gpt-5.4": "gpt-5.3-codex"},
				"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.4-openai-compact", result.UpstreamModel)
	require.Equal(t, "gpt-5.4-openai-compact", gjson.GetBytes(upstream.lastBody, "model").String())
	opsModel, exists := c.Get(OpsUpstreamModelKey)
	require.True(t, exists)
	require.Equal(t, "gpt-5.4-openai-compact", opsModel)
}

// TestOpenAIGatewayService_Forward_APIKeyCompactSanitizesStatelessReplayAfterStoreWasDropped
// sends API-key Compact input with server reasoning, an item reference and a
// user message. It checks outgoing input keeps the user message and omits
// parallel-tool controls.
func TestOpenAIGatewayService_Forward_APIKeyCompactSanitizesStatelessReplayAfterStoreWasDropped(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","parallel_tool_calls":true,"input":[` +
		`{"type":"reasoning","id":"rs_server_only","summary":[]},` +
		`{"type":"item_reference","id":"rs_server_only"},` +
		`{"type":"message","role":"user","content":"compact this"}` +
		`]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_compact","status":"completed","output":[{"type":"compaction","encrypted_content":"cipher"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7, Name: "azure-openai", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
			Credentials: map[string]any{"api_key": "test-key"}, Status: billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Exists())
	require.Equal(t, int64(1), gjson.GetBytes(upstream.lastBody, "input.#").Int())
	require.Equal(t, "message", gjson.GetBytes(upstream.lastBody, "input.0.type").String())
}

// TestOpenAIGatewayService_Forward_NormalizesCompactionTriggerAfterHistoryCleanup
// places the native compaction trigger before retained tool history. It checks
// outgoing input orders the function call, its result, user message and
// compaction trigger.
func TestOpenAIGatewayService_Forward_NormalizesCompactionTriggerAfterHistoryCleanup(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","stream":false,"input":[{"type":"compaction_trigger"},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"},{"type":"message","role":"user","content":"visible"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_trigger","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 4, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Status:      billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	items := gjson.GetBytes(upstream.lastBody, "input").Array()
	require.Len(t, items, 4)
	require.Equal(t, "function_call", items[0].Get("type").String())
	require.Equal(t, "function_call_output", items[1].Get("type").String())
	require.Equal(t, "message", items[2].Get("type").String())
	require.Equal(t, "compaction_trigger", items[3].Get("type").String())
}

// TestOpenAIGatewayService_Forward_NonCompactRequestIgnoresCompactOnlyModelMapping
// provides a Compact-only mapping on an ordinary Responses path. It checks
// client and upstream model attribution and captured body retain the requested
// model.
func TestOpenAIGatewayService_Forward_NonCompactRequestIgnoresCompactOnlyModelMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"normal-test","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-normal-map"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_124","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 2,
			Name:        "openai-oauth",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":          "oauth-token",
				"chatgpt_account_id":    "chatgpt-acc",
				"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.4", result.UpstreamModel)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
}

// TestOpenAIGatewayService_OAuthPassthrough_CompactOnlyModelMappingOverridesUpstreamModel
// maps an OAuth passthrough Compact request to another upstream model. It
// checks outgoing and Ops attribution use that model while client output and
// result retain the requested identity.
func TestOpenAIGatewayService_OAuthPassthrough_CompactOnlyModelMappingOverridesUpstreamModel(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Content-Type", "application/json")

	originalBody := []byte(`{"model":"gpt-5.4","stream":true,"store":true,"instructions":"compact-pass","input":[{"type":"text","text":"compact me"}]}`)
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-compact-pass-map"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"cmp_124","model":"gpt-5.4-openai-compact","usage":{"input_tokens":2,"output_tokens":3}}`)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 3,
			Name:        "openai-oauth-pass",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":          "oauth-token",
				"chatgpt_account_id":    "chatgpt-acc",
				"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
			},
			Extra:       map[string]any{"openai_passthrough": true},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.4-openai-compact", result.UpstreamModel)
	require.Equal(t, "gpt-5.4-openai-compact", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(rec.Body.Bytes(), "model").String())
	opsModel, exists := c.Get(OpsUpstreamModelKey)
	require.True(t, exists)
	require.Equal(t, "gpt-5.4-openai-compact", opsModel)
}

// TestOpenAIGatewayService_Forward_FailoverReparsesCachedBodyForNextProvider
// separately forwards the same admitted bytes through two differently mapped
// providers. It checks the second body uses its own mapping despite injected
// cached content from the failed first attempt.
func TestOpenAIGatewayService_Forward_FailoverReparsesCachedBodyForNextProvider(t *testing.T) {
	tests := []struct {
		name         string // case display name
		requestModel string // admitted client model
		// firstMapping and secondMapping configure the respective provider attempts;
		// nil leaves that provider unmapped. wantFirst and wantSecond assert
		// captured models after each separately invoked forward.
		firstMapping  map[string]any // first provider model rules
		secondMapping map[string]any // second provider model rules
		wantFirst     string         // first captured upstream model
		wantSecond    string         // second captured upstream model
	}{
		{
			name:          "both providers have mapping",
			firstMapping:  map[string]any{"alias-model": "base-model-a"},
			secondMapping: map[string]any{"alias-model": "base-model-b"},
			wantFirst:     "base-model-a",
			wantSecond:    "base-model-b",
		},
		{
			name:         "first provider has mapping second provider has none",
			requestModel: "gpt-5.4-high",
			firstMapping: map[string]any{"gpt-5.4-high": "gpt-5.4"},
			wantFirst:    "gpt-5.4",
			wantSecond:   "gpt-5.4-high",
		},
		{
			name:          "first provider has no mapping second provider has mapping",
			secondMapping: map[string]any{"alias-model": "base-model-b"},
			wantFirst:     "alias-model",
			wantSecond:    "base-model-b",
		},
		{
			name:          "legacy context cache is ignored when mappings differ",
			firstMapping:  map[string]any{"alias-model": "base-model-a"},
			secondMapping: map[string]any{"alias-model": "base-model-b"},
			wantFirst:     "base-model-a",
			wantSecond:    "base-model-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestModel := tt.requestModel
			if requestModel == "" {
				requestModel = "alias-model"
			}
			body := []byte(`{"model":"` + requestModel + `","stream":false,"instructions":"cache-test","input":"hello"}`)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-failover-a"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error","message":"rate limited"}}`)),
				},
				{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-ok-b"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_123","status":"completed","model":"ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
				},
			}}
			svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

			firstProvider := openAIFailoverCachedBodyTestProvider(1, "provider-a", tt.firstMapping)
			secondProvider := openAIFailoverCachedBodyTestProvider(2, "provider-b", tt.secondMapping)

			_, err := svc.Forward(context.Background(), c, firstProvider, body)
			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.True(t, errors.As(err, &failoverErr))
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, tt.wantFirst, gjson.GetBytes(upstream.bodies[0], "model").String())

			// Inject the first provider's legacy parsed-body cache before the explicit
			// second forward; its captured model must come from admitted bytes and the
			// second provider's rules.
			c.Set("openai_parsed_request_body", map[string]any{"model": tt.wantFirst, "stream": true})
			result, err := svc.Forward(context.Background(), c, secondProvider, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, tt.wantSecond, gjson.GetBytes(upstream.bodies[1], "model").String())
		})
	}
}

// openAIFailoverCachedBodyTestProvider creates an active OAuth fixture
// provider with the supplied identity and optional model mapping. The
// credentials retain the supplied mapping map, so callers keep it stable
// during forwarding.
func openAIFailoverCachedBodyTestProvider(id int64, name string, mapping map[string]any) *gatewayprovider.ExecutionProvider {
	credentials := map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"}
	if mapping != nil {
		credentials["model_mapping"] = mapping
	}
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Name:           name,
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    credentials,
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}
}

// Do waits for request cancellation while modeling a missing upstream response
// header. The cancellation branch closes the fixture signal through once and
// returns the context error. A 1500-millisecond guard returns a fixture
// failure when forwarding has not cancelled in time.
//
// Tests initialize canceled before invoking Do and wait for forwarding to
// return before inspecting it. If both select cases are ready, the selected
// branch determines the returned error; the timer branch leaves the
// cancellation signal open.
func (u *blockingOpenAIResponseHeaderUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	select {
	case <-req.Context().Done():
		// Close the cancellation signal once so repeated transport calls cannot
		// panic, and tests can distinguish cancellation from the guard timeout.
		u.once.Do(func() { close(u.canceled) })
		return nil, req.Context().Err()
	// Bound this fixture header wait so missing first-output cancellation reports
	// a test failure instead of leaving the synchronous forwarding call blocked
	// indefinitely.
	case <-time.After(1500 * time.Millisecond):
		return nil, errors.New("test upstream was not canceled before response headers")
	}
}

// DoWithTLS delegates the same header-wait fixture to Do for transports
// selecting TLS. Proxy, provider, concurrency and TLS-profile arguments are
// ignored; request context cancellation and the initialized signal follow Do's
// rules.
func (u *blockingOpenAIResponseHeaderUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, "", 0, 0)
}

// TestOpenAIForwardFirstOutputTimeoutIncludesResponseHeaderWait delays
// response headers beyond the configured first-output allowance. It checks
// prompt typed timeout, cancelled upstream context and empty client body while
// the transport fixture records cancellation.
func TestOpenAIForwardFirstOutputTimeoutIncludesResponseHeaderWait(t *testing.T) {
	upstream := &blockingOpenAIResponseHeaderUpstream{canceled: make(chan struct{})}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Response: OpenAIResponseOptions{OpenAIFirstOutputTimeoutSeconds: 1, MaxLineSize: OpenAIResponseDefaultMaxLineSize}}, transport: upstream})
	body := []byte(`{"model":"gpt-5.5","stream":true,"reasoning":{"effort":"low"},"input":"hello"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1, Name: "oauth-test", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
			Status: billingcore.StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-provider"},
		},
	}

	started := time.Now()
	_, err := svc.Forward(context.Background(), c, provider, body)

	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusGatewayTimeout, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "first_output_timeout")
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	// Leave two hundred milliseconds before the fixture guard timeout.
	require.Less(t, time.Since(started), 1300*time.Millisecond)
	require.Empty(t, rec.Body.String())
	select {
	case <-upstream.canceled:
	default:
		t.Fatal("response-header timeout did not cancel the upstream request context")
	}
}

// TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidInputItemIDs mixes
// valid and invalid IDs for several tool and message item types. It checks
// invalid IDs disappear while valid IDs, call IDs and tool arguments remain in
// captured passthrough input.
func TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidInputItemIDs(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	provider := newOpenAIImageGenerationControlTestProvider()
	provider.Record.Extra = map[string]any{"openai_passthrough": true}

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"input":[
			{"type":"message","id":"item_bad_message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},
			{"type":"function_call","id":"item_bad_call","call_id":"call_123","name":"exec_command","arguments":"{}"},
			{"type":"message","id":"msg_valid","role":"user","content":[{"type":"input_text","text":"continue"}]},
			{"type":"function_call","id":"fc_valid","call_id":"call_456","name":"apply_patch","arguments":"{}"},
			{"type":"custom_tool_call","id":"fc_wrong_custom","call_id":"call_custom_1","name":"apply_patch","input":"patch"},
			{"type":"custom_tool_call","id":"ctc_valid","call_id":"call_custom_2","name":"apply_patch","input":"patch"},
			{"type":"tool_search_call","id":"fc_wrong_search","call_id":"call_search_1","arguments":{"query":"docs"}},
			{"type":"tool_search_call","id":"tsc_valid","call_id":"call_search_2","arguments":{"query":"docs"}},
			{"type":"function_call_output","id":"item_output","call_id":"call_123","output":"done"},
			{"type":"web_search_call","id":"item_wrong_web"}
		]
	}`)

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)

	forwarded := upstream.lastBody
	require.False(t, gjson.GetBytes(forwarded, "input.0.id").Exists())
	require.Equal(t, "hello", gjson.GetBytes(forwarded, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(forwarded, "input.1.id").Exists())
	require.Equal(t, "call_123", gjson.GetBytes(forwarded, "input.1.call_id").String())
	require.Equal(t, "exec_command", gjson.GetBytes(forwarded, "input.1.name").String())
	require.Equal(t, "{}", gjson.GetBytes(forwarded, "input.1.arguments").String())
	require.Equal(t, "msg_valid", gjson.GetBytes(forwarded, "input.2.id").String())
	require.Equal(t, "fc_valid", gjson.GetBytes(forwarded, "input.3.id").String())
	require.False(t, gjson.GetBytes(forwarded, "input.4.id").Exists())
	require.Equal(t, "ctc_valid", gjson.GetBytes(forwarded, "input.5.id").String())
	require.False(t, gjson.GetBytes(forwarded, "input.6.id").Exists())
	require.Equal(t, "tsc_valid", gjson.GetBytes(forwarded, "input.7.id").String())
	require.Equal(t, "item_output", gjson.GetBytes(forwarded, "input.8.id").String())
	require.Equal(t, "call_123", gjson.GetBytes(forwarded, "input.8.call_id").String())
	require.False(t, gjson.GetBytes(forwarded, "input.9.id").Exists())
}

// TestOpenAIGatewayService_OAuthPassthrough_SanitizesNativeToolItemIDs sends
// custom and search tool items through OAuth and setup-token passthrough. It
// checks incorrect function prefixes disappear while valid custom/search
// prefixes reach the Codex endpoint.
func TestOpenAIGatewayService_OAuthPassthrough_SanitizesNativeToolItemIDs(t *testing.T) {
	for _, providerType := range []string{capability.ProviderTypeOAuth, capability.ProviderTypeSetupToken} {
		t.Run(providerType, func(t *testing.T) {
			upstreamSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			provider := newOpenAIImageGenerationControlTestProvider()
			provider.Record.Type = providerType
			provider.Record.Credentials = map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-provider",
			}
			provider.Record.Extra = map[string]any{"openai_passthrough": true}

			body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"instructions":"test",
		"input":[
			{"type":"custom_tool_call","id":"fc_wrong_custom","call_id":"call_custom_1","name":"apply_patch","input":"patch"},
			{"type":"custom_tool_call","id":"ctc_valid","call_id":"call_custom_2","name":"apply_patch","input":"patch"},
			{"type":"tool_search_call","id":"fc_wrong_search","call_id":"call_search_1","arguments":{"query":"docs"}},
			{"type":"tool_search_call","id":"tsc_valid","call_id":"call_search_2","arguments":{"query":"docs"}}
		]
	}`)

			result, err := svc.Forward(context.Background(), c, provider, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.id").Exists())
			require.Equal(t, "ctc_valid", gjson.GetBytes(upstream.lastBody, "input.1.id").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "input.2.id").Exists())
			require.Equal(t, "tsc_valid", gjson.GetBytes(upstream.lastBody, "input.3.id").String())
		})
	}
}

// TestOpenAIGatewayService_SetupTokenLegacy_SanitizesAndTransforms sends
// setup-token input with reasoning mode, a malformed custom-tool ID and an
// orphan result. It checks Codex preparation disables store, converts effort
// and removes the malformed ID and orphan item.
func TestOpenAIGatewayService_SetupTokenLegacy_SanitizesAndTransforms(t *testing.T) {
	upstreamSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	provider := newOpenAIImageGenerationControlTestProvider()
	provider.Record.Type = capability.ProviderTypeSetupToken
	provider.Record.Credentials = map[string]any{
		"access_token":       "setup-token",
		"chatgpt_account_id": "chatgpt-provider",
	}
	provider.Record.Extra = map[string]any{"openai_passthrough": false}
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"store":true,
		"reasoning":{"mode":"pro"},
		"instructions":"test",
		"input":[
			{"type":"custom_tool_call","id":"fc_wrong_custom","call_id":"call_custom_1","name":"apply_patch","input":"patch"},
			{"type":"function_call_output","call_id":"call_orphan","output":"orphan"}
		]
	}`)

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
	require.Equal(t, false, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning.mode").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.id").Exists())
	require.Len(t, gjson.GetBytes(upstream.lastBody, "input").Array(), 1)
}

// TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidReasoningItemIDs
// sends invalid and valid reasoning IDs with a valid message. It checks
// captured API-key passthrough input removes the invalid reasoning ID while
// retaining the valid reasoning and message IDs.
func TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidReasoningItemIDs(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}}
	service := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	provider := newOpenAIImageGenerationControlTestProvider()
	provider.Record.Extra = map[string]any{"openai_passthrough": true}
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"input":[
			{"type":"reasoning","id":"item_bad_reasoning","summary":[]},
			{"type":"reasoning","id":"rs_valid","summary":[]},
			{"type":"message","id":"msg_valid","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	result, err := service.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.id").Exists())
	require.Equal(t, "rs_valid", gjson.GetBytes(upstream.lastBody, "input.1.id").String())
	require.Equal(t, "msg_valid", gjson.GetBytes(upstream.lastBody, "input.2.id").String())
}

// TestShouldStripOpenAIResponsesInputItemID_Reasoning compares item types
// against valid and mismatched ID prefixes. It checks each table verdict,
// including the supported function-call ID on custom-tool output.
func TestShouldStripOpenAIResponsesInputItemID_Reasoning(t *testing.T) {
	testCases := []struct {
		name     string // case display name
		itemType string // input item type
		id       string // candidate item identifier
		want     bool   // expected removal verdict
	}{
		{"reasoning item_* id", "reasoning", "item_bad_reasoning", true},
		{"reasoning rs id", "reasoning", "rs_abc123", false},
		{"reasoning empty id", "reasoning", "", true},
		{"message msg id", "message", "msg_abc", false},
		{"message item id", "message", "item_x", true},
		{"function_call fc id", "function_call", "fc_abc", false},
		{"function_call ctc id", "function_call", "ctc_abc", true},
		{"function_call item id", "function_call", "item_x", true},
		{"custom tool ctc id", "custom_tool_call", "ctc_abc", false},
		{"custom tool fc id", "custom_tool_call", "fc_abc", true},
		{"tool search tsc id", "tool_search_call", "tsc_abc", false},
		{"tool search fc id", "tool_search_call", "fc_abc", true},
		{"web search ws id", "web_search_call", "ws_001", false},
		{"web search item id", "web_search_call", "item_001", true},
		{"custom output fc id", "custom_tool_call_output", "fc_001", false},
		{"custom output ctco id", "custom_tool_call_output", "ctco_001", true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openaicore.ShouldStripOpenAIResponsesInputItemID(tc.itemType, tc.id))
		})
	}
}

// TestSanitizeOpenAIResponsesInputItemIDs_AllocationGrowthIsLinear measures
// total allocated bytes while sanitizing twenty and two hundred malformed IDs.
// It checks ten times the item count stays below thirty times the smaller
// allocation sample.
func TestSanitizeOpenAIResponsesInputItemIDs_AllocationGrowthIsLinear(t *testing.T) {
	makeBody := func(itemCount int) []byte {
		items := make([]string, itemCount)
		for i := range items {
			items[i] = fmt.Sprintf(`{"type":"message","id":"item_%d","role":"user","content":[{"type":"input_text","text":"hello"}]}`, i)
		}
		return []byte(`{"model":"gpt-5.6-sol","input":[` + strings.Join(items, ",") + `]}`)
	}
	allocatedBytes := func(body []byte) uint64 {
		// Collect fixture garbage before each sample so TotalAlloc snapshots
		// surround the sanitizer call, excluding allocation for constructing the
		// input body.
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		sanitized, changed, err := gatewayprovider.SanitizeOpenAIResponsesInputItemIDs(body)
		runtime.ReadMemStats(&after)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotEmpty(t, sanitized)
		return after.TotalAlloc - before.TotalAlloc
	}

	smallAllocated := allocatedBytes(makeBody(20))
	largeAllocated := allocatedBytes(makeBody(200))
	// Allow thirtyfold allocation growth for the tenfold fixture size increase.
	require.Less(t, largeAllocated, smallAllocated*30,
		"10x more input items must not cause quadratic whole-body allocation growth")
}

// TestNormalizeOpenAIResponsesWebSocketCompatibilityBodyPreservesOpaqueReferences
// normalizes custom output beside an item reference and an unknown item type
// for two provider types. It checks only the malformed output ID disappears
// and a second normalization is unchanged.
func TestNormalizeOpenAIResponsesWebSocketCompatibilityBodyPreservesOpaqueReferences(t *testing.T) {
	body := []byte(`{"type":"response.create","input":[
		{"type":"custom_tool_call","id":"ctc_call","call_id":"call_custom","name":"apply_patch","input":"patch"},
		{"type":"custom_tool_call_output","id":"ctco_bad","call_id":"call_custom","output":"done"},
		{"type":"item_reference","id":"ctco_bad"},
		{"type":"future_item","id":"item_future","payload":"keep"}
	]}`)

	for _, providerType := range []string{capability.ProviderTypeAPIKey, capability.ProviderTypeOAuth} {
		t.Run(providerType, func(t *testing.T) {
			normalized, changed, err := gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
					Type: providerType,
				},
			}), false)

			require.NoError(t, err)
			require.True(t, changed)
			require.Len(t, gjson.GetBytes(normalized, "input").Array(), 4)
			require.Equal(t, "ctc_call", gjson.GetBytes(normalized, "input.0.id").String())
			require.Equal(t, "call_custom", gjson.GetBytes(normalized, "input.1.call_id").String())
			require.False(t, gjson.GetBytes(normalized, "input.1.id").Exists())
			require.Equal(t, "ctco_bad", gjson.GetBytes(normalized, "input.2.id").String())
			require.Equal(t, "item_future", gjson.GetBytes(normalized, "input.3.id").String())

			second, changedAgain, err := gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(normalized, gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
					Type: providerType,
				},
			}), false)
			require.NoError(t, err)
			require.False(t, changedAgain)
			require.JSONEq(t, string(normalized), string(second))
		})
	}
}

// TestForwardResponsesChatCompletionsFallbackKeepsFunctionArgumentsSingle
// splits a Chat tool call between identity and argument chunks. It checks
// translated delta accumulation and both final Responses argument
// representations contain the argument JSON once.
func TestForwardResponsesChatCompletionsFallbackKeepsFunctionArgumentsSingle(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"run a command","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		passthroughArgsSSEData(chatToolCallChunkJSON(true, "")),
		"",
		passthroughArgsSSEData(chatToolCallChunkJSON(false, `{"cmd":"echo hi"}`)),
		"",
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_fallback_tool_args"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	provider := passthroughArgsFallbackProvider()
	provider.Record.Extra = map[string]any{
		providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions),
	}
	svc := newResponsesFixture(responsesFixtureInputs{options: passthroughArgsTestConfig(), transport: upstream})

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)

	const wantArgs = `{"cmd":"echo hi"}`
	events := collectPassthroughArgsSSEDataPayloads(t, rec.Body.String())
	require.Equal(t, wantArgs, accumulateFunctionArgumentDeltas(events, "chatcmpl-tool-a"))
	require.Equal(t, wantArgs, gjson.Get(findPassthroughArgsSSEEvent(t, events, "response.function_call_arguments.done", "chatcmpl-tool-a"), "arguments").String())
	require.Equal(t, wantArgs, gjson.Get(findPassthroughArgsSSEEvent(t, events, "response.output_item.done", "chatcmpl-tool-a"), "item.arguments").String())
}

// chatToolCallChunkJSON builds one Chat tool-call delta for the fixed fixture
// response and tool slot. includeIdentity supplies the call ID, type and
// function name; later chunks can carry arguments alone. Empty arguments are
// encoded on the identity chunk, and later empty chunks omit that field.
// Keeping the same index while omitting repeated identity reproduces split
// tool delivery for the Responses converter. Callers supply argument text whose
// strconv.Quote representation is valid JSON, because this helper embeds that
// Go-quoted string directly.
func chatToolCallChunkJSON(includeIdentity bool, arguments string) string {
	identity := ""
	functionFields := make([]string, 0, 2)
	if includeIdentity {
		identity = `"id":"chatcmpl-tool-a","type":"function",`
		functionFields = append(functionFields, `"name":"exec_command"`)
	}
	if includeIdentity || arguments != "" {
		functionFields = append(functionFields, `"arguments":`+strconv.Quote(arguments))
	}
	return fmt.Sprintf(
		`{"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,%s"function":{%s}}]},"finish_reason":null}]}`,
		identity,
		strings.Join(functionFields, ","),
	)
}

// passthroughArgsTestConfig creates request options that allow the HTTP
// fixture upstream URL. URL policy validation is disabled and insecure HTTP is
// allowed for this synthetic host; callers receive fresh options for each
// service.
func passthroughArgsTestConfig() *responsesFixtureOptions {
	return &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{
		Enabled:           false,
		AllowInsecureHTTP: true,
	}}}
}

// passthroughArgsFallbackProvider creates a fresh API-key OpenAI provider for
// tool-argument conversion fixtures. It supplies the synthetic HTTP base URL
// and one execution slot; tests set routing extras on the returned provider.
func passthroughArgsFallbackProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 102,
			Name:        "passthrough-args-openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "http://upstream.example",
			},
		},
	}
}

// TestOpenAIGatewayService_APIKeyPassthrough_ImageIntentPreservesGateAndBilling
// exercises image tool input with denied and allowed groups in API-key
// passthrough. It checks pre-transport rejection or unchanged admitted body
// and returned image pricing dimensions.
func TestOpenAIGatewayService_APIKeyPassthrough_ImageIntentPreservesGateAndBilling(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"input":"draw"}`)

	t.Run("disabled group rejects before upstream", func(t *testing.T) {
		upstream := &auxiliaryHTTPRecorder{}
		svc := newOpenAIImageGenerationControlTestService(upstream)
		c, recorder := newOpenAIImageGenerationControlTestContext(false, "curl/8.0")
		provider := newOpenAIImageGenerationControlTestProvider()
		provider.Record.Extra = map[string]any{"openai_passthrough": true}

		result, err := svc.Forward(context.Background(), c, provider, body)

		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Equal(t, "permission_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
		require.Nil(t, upstream.lastReq)
	})

	t.Run("allowed group keeps image billing", func(t *testing.T) {
		upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-image","size":"2048x1152"}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		}}
		svc := newOpenAIImageGenerationControlTestService(upstream)
		c, _ := newOpenAIImageGenerationControlTestContext(true, "curl/8.0")
		provider := newOpenAIImageGenerationControlTestProvider()
		provider.Record.Extra = map[string]any{"openai_passthrough": true}

		result, err := svc.Forward(context.Background(), c, provider, body)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, upstream.lastReq)
		require.Equal(t, body, upstream.lastBody)
		require.Equal(t, 1, result.ImageCount)
		require.Equal(t, "gpt-image-2", result.BillingModel)
		require.Equal(t, "2K", result.ImageSize)
		require.Equal(t, "2048x1152", result.ImageInputSize)
	})
}

// TestForwardResponses_ForceChatCompletionsRoutesNonStreamingToChatCompletions
// forces buffered Responses input through Chat conversion with reasoning and
// priority tier. It checks Chat endpoint/profile, converted client text, usage
// and separate requested/observed tier and model attribution.
func TestForwardResponses_ForceChatCompletionsRoutesNonStreamingToChatCompletions(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","reasoning":{"effort":"max"},"stream":false,"service_tier":"priority"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_chat_json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_json","object":"chat.completion","model":"gpt-5.4","service_tier":"default","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`,
		)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackProvider(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "/v1/chat/completions", GetActualOpenAIUpstreamEndpoint(c))
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	require.Equal(t, "gpt-5.4", result.UpstreamResponseModel)
	require.False(t, result.Stream)
}

// TestForwardResponses_ForceChatCompletionsRoutesStreamingToChatCompletions
// forces streaming Responses input through split Chat text and usage chunks.
// It checks the Chat usage request option, translated text/terminal events and
// returned usage with no synthetic TTFT in this fixture.
func TestForwardResponses_ForceChatCompletionsRoutesStreamingToChatCompletions(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"he"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_resp_chat_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackProvider(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"he"`)
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.Contains(t, rec.Body.String(), `"input_tokens":4`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.True(t, result.Stream)
	require.Nil(t, result.Timings.TTFTMs())
}

// TestForwardResponses_ChatFallbackRejectsInvalidToolArgumentsAtOutputLimit
// truncates a Chat tool argument at the output-token limit. It checks an
// invalid-JSON error with retained usage and no translated final argument,
// item or done-marker output.
func TestForwardResponses_ChatFallbackRejectsInvalidToolArgumentsAtOutputLimit(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":"run the command","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_length_tool","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_length","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"ssh root@HOST"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_length_tool","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":6492,"total_tokens":6496}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_length_tool"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackProvider(), body)
	require.ErrorContains(t, err, "invalid JSON")
	require.NotNil(t, result)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 6492, result.Usage.OutputTokens)
	require.NotContains(t, rec.Body.String(), "response.function_call_arguments.done")
	require.NotContains(t, rec.Body.String(), "response.output_item.done")
	require.NotContains(t, rec.Body.String(), "data: [DONE]")
}

// TestForwardResponses_DeepSeekReasoningOnlyStreamProducesVisibleText receives
// DeepSeek reasoning followed by an empty content field at the length limit.
// It checks visible Responses text, incomplete status and a client done
// marker.
func TestForwardResponses_DeepSeekReasoningOnlyStreamProducesVisibleText(t *testing.T) {
	body := []byte(`{"model":"deepseek-reasoner","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"visible fallback"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_deepseek_reasoning_responses_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackProvider(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"visible fallback"`)
	require.Contains(t, rec.Body.String(), `"status":"incomplete"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

// TestForwardResponses_PreserveClientProtocolUsesResponsesEndpoint selects
// client-protocol preservation despite an earlier Chat endpoint hint. It
// checks native Responses input/endpoint, successful text and returned
// requested reasoning effort.
func TestForwardResponses_PreserveClientProtocolUsesResponsesEndpoint(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","reasoning":{"effort":"max"},"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_resp_native"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_native","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"status":"completed"}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
		)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
	provider := rawChatCompletionsTestProvider()
	provider.Record.Extra = map[string]any{
		providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModePreserveClientProtocol),
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "/v1/responses", GetActualOpenAIUpstreamEndpoint(c))
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

// forceChatResponsesFallbackProvider creates the ordinary raw Chat fixture
// provider and selects forced Chat routing. Callers may mutate the returned
// provider extras independently of other invocations because the base helper
// constructs a fresh provider.
func forceChatResponsesFallbackProvider() *gatewayprovider.ExecutionProvider {
	provider := rawChatCompletionsTestProvider()
	provider.Record.Extra = map[string]any{
		providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions),
	}
	return provider
}

// SetReasoningContent records reasoning text under itemID, allocating the
// capture map when absent. Existing text is overwritten, context and TTL are
// ignored, and tests provide serial access to this mutable fixture.
func (c *reasoningCacheStub) SetReasoningContent(_ context.Context, itemID, content string, _ time.Duration) error {
	if c.sets == nil {
		c.sets = make(map[string]string)
	}
	// Capture the plain reasoning summary written by history conversion so the
	// test can inspect it separately from encrypted text supplied by the read
	// fixture.
	c.sets[itemID] = content
	return nil
}

// GetReasoningContent reads the preloaded reasoning text for itemID and
// returns the cache not-found error when absent. Context is ignored and
// captured writes in sets do not populate this read map.
func (c *reasoningCacheStub) GetReasoningContent(_ context.Context, itemID string) (string, error) {
	if content, ok := c.getResp[itemID]; ok {
		return content, nil
	}
	return "", session.ErrReasoningContentNotFound
}

// TestForwardResponsesChatFallbackRestoresEncryptedReasoningFromCache converts
// tool history containing plain and encrypted reasoning into Chat messages. It
// checks the encrypted item uses cached text and the plain item is captured by
// the reasoning cache writer.
func TestForwardResponsesChatFallbackRestoresEncryptedReasoningFromCache(t *testing.T) {
	body := []byte(`{"model":"deepseek-reasoner","stream":false,"input":[
		{"type":"reasoning","id":"item_plain","summary":[{"type":"summary_text","text":"plain thinking"}]},
		{"type":"function_call","call_id":"call_0","name":"get_value","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_0","output":"ok"},
		{"type":"reasoning","id":"item_enc","summary":[],"encrypted_content":"opaque"},
		{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"ok"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
	]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_restore","object":"chat.completion","model":"deepseek-reasoner","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)),
	}}
	cache := &reasoningCacheStub{getResp: map[string]string{"item_enc": "cached thinking"}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream, cache: cache})

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackProvider(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "plain thinking", gjson.GetBytes(upstream.lastBody, "messages.0.reasoning_content").String())
	require.Equal(t, "cached thinking", gjson.GetBytes(upstream.lastBody, "messages.2.reasoning_content").String())
	require.Equal(t, "plain thinking", cache.sets["item_plain"])
}

// TestForwardResponsesReplaysUndeclaredNamespaceHistory sends compaction-style
// history through Chat, native Responses, and Anthropic provider routes. Each
// exchange reaches the transport with a paired call and returns a usable summary.
func TestForwardResponsesReplaysUndeclaredNamespaceHistory(t *testing.T) {
	for _, protocol := range []string{providercore.APIProtocolChatCompletions, providercore.APIProtocolResponses, providercore.APIProtocolAnthropic} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"deepseek-v4.1-flash","stream":%t,"input":[{"type":"function_call","call_id":"call_1","name":"js","namespace":"mcp__cua_proxy","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}],"tools":[{"type":"tool_search"}]}`, stream))
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				provider := rawChatCompletionsTestProvider()
				provider.Record.Platform = capability.PlatformDeepseek
				provider.Record.Credentials["api_protocol"] = protocol
				provider.Record.Credentials["base_url"] = "https://relay.example"

				contentType := "application/json"
				response := `{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`
				switch protocol {
				case providercore.APIProtocolChatCompletions:
					if stream {
						contentType = "text/event-stream"
						response = "data: " + `{"id":"chat_1","choices":[{"index":0,"delta":{"content":"summary"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}` + "\n\ndata: [DONE]\n\n"
					}
				case providercore.APIProtocolResponses:
					response = `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`
					if stream {
						contentType = "text/event-stream"
						response = "data: " + `{"type":"response.completed","response":` + response + "}\n\n"
					}
				case providercore.APIProtocolAnthropic:
					contentType = "text/event-stream"
					response = strings.Join([]string{
						`event: message_start`, `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"deepseek-v4.1-flash","content":[],"usage":{"input_tokens":2,"output_tokens":0}}}`, "",
						`event: content_block_start`, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, "",
						`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"summary"}}`, "",
						`event: content_block_stop`, `data: {"type":"content_block_stop","index":0}`, "",
						`event: message_delta`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, "",
						`event: message_stop`, `data: {"type":"message_stop"}`, "", "",
					}, "\n")
				}
				upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{contentType}},
					Body:       io.NopCloser(strings.NewReader(response)),
				}}
				svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
				result, err := svc.Forward(context.Background(), c, provider, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Contains(t, recorder.Body.String(), "summary")
				require.NotContains(t, recorder.Body.String(), "response.failed")
				switch protocol {
				case providercore.APIProtocolChatCompletions:
					require.Contains(t, upstream.lastReq.URL.Path, "/chat/completions")
					require.Equal(t, "mcp__cua_proxy__js", gjson.GetBytes(upstream.lastBody, "messages.0.tool_calls.0.function.name").String())
					require.Equal(t, "call_1", gjson.GetBytes(upstream.lastBody, "messages.1.tool_call_id").String())
				case providercore.APIProtocolResponses:
					require.Contains(t, upstream.lastReq.URL.Path, "/responses")
					require.Equal(t, "mcp__cua_proxy__js", gjson.GetBytes(upstream.lastBody, "input.0.name").String())
					require.Equal(t, "call_1", gjson.GetBytes(upstream.lastBody, "input.1.call_id").String())
				case providercore.APIProtocolAnthropic:
					require.Contains(t, upstream.lastReq.URL.Path, "/messages")
					require.Equal(t, "mcp__cua_proxy__js", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.name").String())
					require.Equal(t, "call_1", gjson.GetBytes(upstream.lastBody, "messages.1.content.0.tool_use_id").String())
				}
				require.Equal(t, int64(1), gjson.GetBytes(upstream.lastBody, "tools.#").Int())
			})
		}
	}
}

// TestDeepSeekResponsesForwardRestoresClientToolsStreaming lowers custom tools
// for a DeepSeek Responses exchange and receives function-call SSE. It checks
// the upstream tool shape and unversioned endpoint while downstream
// custom-call input is restored.
func TestDeepSeekResponsesForwardRestoresClientToolsStreaming(t *testing.T) {
	body := openAIClientToolsRequest(true)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"function_call","id":"i1","call_id":"c1","name":"exec","status":"in_progress"}}`,
		`data: {"type":"response.function_call_arguments.done","sequence_number":1,"item_id":"i1","call_id":"c1","name":"exec","arguments":"{\"input\":\"pwd\"}"}`,
		`data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"function_call","id":"i1","call_id":"c1","name":"exec","arguments":"{\"input\":\"pwd\"}","status":"completed"}}`,
		`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_ds_tools","status":"completed","output":[{"type":"function_call","id":"i1","call_id":"c1","name":"exec","arguments":"{\"input\":\"pwd\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
	}, "\n\n") + "\n\n"
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := openAIClientToolsTestService(upstream)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5661,
			Platform: capability.PlatformDeepseek,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":      "test-key",
				"api_protocol": providercore.APIProtocolResponses,
				"base_url":     "https://relay.example",
			},
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	assertOpenAIClientToolsLowered(t, upstream.lastBody)
	require.Equal(t, "/responses", upstream.lastReq.URL.Path)
	output := recorder.Body.String()
	require.Contains(t, output, `"type":"custom_tool_call"`)
	require.Contains(t, output, `"type":"response.custom_tool_call_input.done"`)
	require.Contains(t, output, `"input":"pwd"`)
}

// TestDeepSeekAdaptiveResponsesForwardRestoresClientToolsNonStreaming selects
// the configured adaptive DeepSeek Responses base and receives two lowered
// function calls. It checks the outgoing tool form and restored custom-call
// types and input text in buffered output.
func TestDeepSeekAdaptiveResponsesForwardRestoresClientToolsNonStreaming(t *testing.T) {
	body := openAIClientToolsRequest(false)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp_ds_adaptive_tools","status":"completed","output":[
			{"type":"function_call","id":"i1","call_id":"c1","name":"exec","arguments":"{\"input\":\"pwd\"}"},
			{"type":"function_call","id":"i2","call_id":"c2","name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}],
			"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := openAIClientToolsTestService(upstream)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5662,
			Platform: capability.PlatformDeepseek,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":      "test-key",
				"api_protocol": providercore.APIProtocolAdaptive,
				"api_base_urls": map[string]any{
					providercore.APIProtocolResponses: "https://relay.example",
				},
			},
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	assertOpenAIClientToolsLowered(t, upstream.lastBody)
	require.Equal(t, "/responses", upstream.lastReq.URL.Path)
	require.Equal(t, "custom_tool_call", gjson.Get(recorder.Body.String(), "output.0.type").String())
	require.Equal(t, "pwd", gjson.Get(recorder.Body.String(), "output.0.input").String())
	require.Equal(t, "custom_tool_call", gjson.Get(recorder.Body.String(), "output.1.type").String())
	require.Equal(t, "*** Begin Patch", gjson.Get(recorder.Body.String(), "output.1.input").String())
}

// TestDeepSeekResponsesCompactSkipsClientToolAdaptation sends custom tool
// declarations to the DeepSeek Compact path. It checks the captured
// declaration remains custom and the upstream endpoint includes the Compact
// suffix.
func TestDeepSeekResponsesCompactSkipsClientToolAdaptation(t *testing.T) {
	body := openAIClientToolsRequest(false)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_compact","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := openAIClientToolsTestService(upstream)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5663,
			Platform: capability.PlatformDeepseek,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key":      "test-key",
				"api_protocol": providercore.APIProtocolResponses,
				"base_url":     "https://relay.example",
			},
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.Equal(t, "custom", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "/responses/compact", upstream.lastReq.URL.Path)
}

// TestOpenAIResponsesEmptyCompletedFailsOver receives created and bare
// completed events in managed and passthrough streams. It checks a
// silent-refusal failover with upstream request ID and no client output.
func TestOpenAIResponsesEmptyCompletedFailsOver(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"rid-empty-completed"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_empty\",\"status\":\"in_progress\"}}\n\n" +
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_empty\",\"status\":\"completed\"}}\n\n",
				)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			provider := newOpenAIImageGenerationControlTestProvider()
			provider.Record.Extra = map[string]any{"openai_passthrough": passthrough}

			result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"continue"}`))

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
			require.True(t, forwardcore.IsOpenAISilentRefusalErrorBody(failoverErr.ResponseBody))
			require.Equal(t, "rid-empty-completed", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
			require.Empty(t, recorder.Body.String(), "空成功流不能写给客户端")
		})
	}
}

// TestOpenAIResponsesEmptyCompletedExemptions receives semantic text, terminal
// usage or a terminal output item in managed and passthrough streams. It
// checks each fixture succeeds and writes client output.
func TestOpenAIResponsesEmptyCompletedExemptions(t *testing.T) {
	tests := []struct {
		name string // case display name
		body string // upstream SSE fixture
	}{
		{
			name: "semantic output",
			body: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_output\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_output\",\"status\":\"completed\"}}\n\n",
		},
		{
			name: "terminal usage",
			body: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_usage\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_usage\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0,\"total_tokens\":3}}}\n\n",
		},
		{
			name: "terminal output item",
			body: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_item\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[]}]}}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, passthrough := range []bool{false, true} {
				upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
				}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				provider := newOpenAIImageGenerationControlTestProvider()
				provider.Record.Extra = map[string]any{"openai_passthrough": passthrough}

				result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"continue"}`))

				require.NoError(t, err, "passthrough=%v", passthrough)
				require.NotNil(t, result)
				require.NotEmpty(t, recorder.Body.String())
			}
		})
	}
}

// TestOpenAIResponsesCompletedEventIsEmpty classifies completed event payloads
// with output, usage, errors and malformed JSON. It checks accumulated usage
// also prevents the empty-terminal verdict in the corresponding case.
func TestOpenAIResponsesCompletedEventIsEmpty(t *testing.T) {
	tests := []struct {
		name string // case display name
		data string // terminal JSON fixture
		// usage supplies earlier accumulated counts; nil leaves emptiness dependent
		// on evidence in the terminal payload supplied by this case.
		usage *protocolopenai.ForwardUsage // earlier usage observation
		want  bool                         // expected empty verdict
	}{
		{name: "bare completed", data: `{"type":"response.completed"}`, want: true},
		{name: "empty output", data: `{"type":"response.completed","response":{"output":[]}}`, want: true},
		{name: "usage", data: `{"type":"response.completed","response":{"usage":{"input_tokens":1}}}`},
		{name: "error", data: `{"type":"response.completed","response":{"error":{"code":"x"}}}`},
		{name: "output item", data: `{"type":"response.completed","response":{"output":[{"type":"message"}]}}`},
		{name: "accumulated usage", data: `{"type":"response.completed"}`, usage: &protocolopenai.ForwardUsage{InputTokens: 1}},
		{name: "invalid json", data: `{"type":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, protocolopenai.OpenAIResponsesCompletedEventIsEmpty([]byte(tt.data), tt.usage))
		})
	}
}

// TestOpenAIGatewayService_Forward_LogsInstructionsRequiredDetails returns a
// missing-instructions error for a request carrying cache and token fields. It
// checks client error identity and warning metadata, including request
// headers/size with no logged body preview.
func TestOpenAIGatewayService_Forward_LogsInstructionsRequiredDetails(t *testing.T) {
	// Capture shared structured logs for this synchronous exchange, then restore
	// the hook on exit so later scenarios use their normal logging sink.
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?trace=1", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("OpenAI-Beta", "assistants=v2")

	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-upstream"},
			},
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Missing required parameter: 'instructions'","type":"invalid_request_error","param":"instructions","code":"missing_required_parameter"}}`)),
		},
	}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1001,
			Name:           "codex max套餐",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeAPIKey,
			Concurrency:    1,
			Credentials:    map[string]any{"api_key": "sk-test"},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}
	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"input":[{"type":"text","text":"hello"}],"prompt_cache_key":"pc-forward","access_token":"secret-token"}`)

	_, err := svc.Forward(context.Background(), c, provider, body)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "missing_required_parameter", gjson.Get(rec.Body.String(), "error.code").String())
	require.Equal(t, "instructions", gjson.Get(rec.Body.String(), "error.param").String())
	require.Contains(t, err.Error(), "upstream error: 400")

	require.True(t, logSink.ContainsMessageAtLevel("OpenAI upstream returned Instructions are required; request details logged for investigation", "warn"))
	require.True(t, logSink.ContainsFieldValue("request_user_agent", "codex_cli_rs/0.1.0"))
	require.True(t, logSink.ContainsFieldValue("request_model", "gpt-5.1-codex"))
	require.True(t, logSink.ContainsFieldValue("request_headers", "openai-beta"))
	require.True(t, logSink.ContainsFieldValue("request_body_size", ""))
	require.False(t, logSink.ContainsFieldValue("request_body_preview", ""))
}

// TestOpenAIGatewayService_Forward_TransientProcessingErrorTriggersFailover
// receives a transient processing message inside an HTTP 400 error. It checks
// the typed failover retains status/body and leaves the client writer
// uncommitted.
func TestOpenAIGatewayService_Forward_TransientProcessingErrorTriggersFailover(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-processing-400"},
			},
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_123 in your message.","type":"invalid_request_error"}}`)),
		},
	}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1001,
			Name:           "codex max套餐",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeAPIKey,
			Concurrency:    1,
			Credentials:    map[string]any{"api_key": "sk-test"},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}
	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"input":[{"type":"text","text":"hello"}]}`)

	_, err := svc.Forward(context.Background(), c, provider, body)
	require.Error(t, err)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "An error occurred while processing your request")
	require.False(t, c.Writer.Written(), "service 层应返回 failover 错误给上层换号，而不是直接向客户端写响应")
}

// TestOpenAIGatewayService_Forward_APIKeyMissingInstructionsKeepsLargeInputRaw
// forwards API-key input without instructions and with an integer beyond exact
// float precision. It checks JSON equivalence, absent injected instructions
// and retained raw integer text.
func TestOpenAIGatewayService_Forward_APIKeyMissingInstructionsKeepsLargeInputRaw(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}`,
			)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"reasoning":{"effort":"minimal"},"input":[{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	expectedBody := `{"model":"gpt-5","stream":false,"reasoning":{"effort":"minimal"},"input":[{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}]}`
	require.JSONEq(t, expectedBody, string(upstream.lastBody))
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.lastBody, "input.0.content.0.nonce").Raw)
}

// TestOpenAIGatewayService_Forward_AstraReasoningEffortUsesGroupMapping
// applies group effort mapping before mapping the client model to Astra. It
// checks both minimal and none become low in captured upstream input.
func TestOpenAIGatewayService_Forward_AstraReasoningEffortUsesGroupMapping(t *testing.T) {
	for _, effort := range []string{"minimal", "none"} {
		t.Run(effort, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
				},
			}
			cfg := &responsesFixtureOptions{}
			cfg.Request.URLPolicy.Enabled = false
			svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 3,
					Name:        "openai-apikey",
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{
						"api_key":  "sk-test",
						"base_url": "https://api.openai.com",
						"model_mapping": map[string]any{
							"client-model": "gpt-6-astra",
						},
					},
					Extra: map[string]any{"use_responses_api": true},
				},
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			body := []byte(`{"model":"client-model","stream":false,"reasoning":{"effort":"` + effort + `"},"input":"hi"}`)
			mappedBody, changed, err := requeststate.ApplyOpenAIReasoningEffortPolicy(body, "", []routing.ReasoningEffortMapping{{
				From:      effort,
				To:        "low",
				MatchType: "exact",
				Model:     "client-model",
			}}, "")
			require.NoError(t, err)
			require.True(t, changed)

			result, err := svc.Forward(context.Background(), c, provider, mappedBody)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
		})
	}
}

// TestOpenAIGatewayService_Forward_PassthroughPreservesAstraInputWithoutGroupMapping
// passes Astra effort values through API-key passthrough without a group rule.
// It checks none and minimal remain unchanged in the captured upstream body.
func TestOpenAIGatewayService_Forward_PassthroughPreservesAstraInputWithoutGroupMapping(t *testing.T) {
	for _, tt := range []struct {
		effort string // admitted reasoning effort
		want   string // expected upstream effort
	}{
		{effort: "none", want: "none"},
		{effort: "minimal", want: "minimal"},
	} {
		t.Run(tt.effort, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
				},
			}
			cfg := &responsesFixtureOptions{}
			cfg.Request.URLPolicy.Enabled = false
			svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 4,
					Name:        "openai-passthrough",
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{
						"api_key":  "sk-test",
						"base_url": "https://api.openai.com",
					},
					Extra: map[string]any{"openai_passthrough": true},
				},
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"`+tt.effort+`"},"input":"hi"}`))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, tt.want, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.NotEqual(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
		})
	}
}

// TestOpenAIGatewayService_Forward_DecodedMutationKeepsLaterFieldDeletes
// combines image-tool normalization with removal of an unsupported completion
// limit. It checks later deletions survive serialization while the image
// output-format field remains available.
func TestOpenAIGatewayService_Forward_DecodedMutationKeepsLaterFieldDeletes(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 2,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.4","stream":false,"max_completion_tokens":12,"tools":[{"type":"image_generation","format":"png"}],"input":[{"type":"message","content":"draw"}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools.0.format").Exists())
	require.Equal(t, "png", gjson.GetBytes(upstream.lastBody, "tools.0.output_format").String())
}

// TestOpenAIGatewayService_Forward_NormalizesMaxTokensAndStripsPromptCacheOptions
// sends legacy output limits and unsupported prompt-cache options through
// native forwarding. It checks limit renaming, option removal and precedence
// of an already supplied max_output_tokens value.
func TestOpenAIGatewayService_Forward_NormalizesMaxTokensAndStripsPromptCacheOptions(t *testing.T) {
	runForward := func(t *testing.T, body []byte) []byte {
		t.Helper()
		upstream := &auxiliaryHTTPRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		}
		cfg := &responsesFixtureOptions{}
		cfg.Request.URLPolicy.Enabled = false
		svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 4,
				Name:        "openai-apikey",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":  "sk-test",
					"base_url": "https://example.com",
				},
				Extra: map[string]any{},
			},
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

		result, err := svc.Forward(context.Background(), c, provider, body)
		require.NoError(t, err)
		require.NotNil(t, result)
		return upstream.lastBody
	}

	t.Run("max_tokens 归一化为 max_output_tokens 并移除 prompt_cache_options", func(t *testing.T) {
		out := runForward(t, []byte(`{"model":"gpt-5.4","stream":false,"max_tokens":256,"prompt_cache_options":{"enabled":true},"input":[{"type":"message","content":"hi"}]}`))
		require.Equal(t, int64(256), gjson.GetBytes(out, "max_output_tokens").Int())
		require.False(t, gjson.GetBytes(out, "max_tokens").Exists())
		require.False(t, gjson.GetBytes(out, "prompt_cache_options").Exists())
	})

	t.Run("同时存在时保留 max_output_tokens 丢弃 max_tokens", func(t *testing.T) {
		out := runForward(t, []byte(`{"model":"gpt-5.4","stream":false,"max_tokens":256,"max_output_tokens":512,"input":[{"type":"message","content":"hi"}]}`))
		require.Equal(t, int64(512), gjson.GetBytes(out, "max_output_tokens").Int())
		require.False(t, gjson.GetBytes(out, "max_tokens").Exists())
	})
}

// TestOpenAIGatewayService_Forward_MappedImageModelUsesImageGate maps a text
// alias to an image model for a group that denies images. It checks rejection
// before transport, then successful text-provider forwarding with the cached
// request-intent hint still false.
func TestOpenAIGatewayService_Forward_MappedImageModelUsesImageGate(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 3,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "sk-test",
				"base_url":      "https://example.com",
				"model_mapping": map[string]any{"draw-alias": "gpt-image-2"},
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: false}})
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"draw-alias","stream":false,"input":"draw"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusForbidden, rec.Code)
	cached, known := GetOpenAIImageIntentHint(c)
	require.True(t, known)
	require.False(t, cached)

	textProvider := *provider
	textProvider.Record.ID = 4
	textProvider.Record.Credentials = map[string]any{
		"api_key":  "sk-test",
		"base_url": "https://example.com",
	}
	result, err = svc.Forward(context.Background(), c, &textProvider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Len(t, upstream.bodies, 1)
	cached, known = GetOpenAIImageIntentHint(c)
	require.True(t, known)
	require.False(t, cached)
}

// TestOpenAIGatewayService_Forward_TextResponsesSetsBillingModelToMappedModel
// maps a text request to another upstream model. It checks client identity
// remains requested while outgoing body and returned billing/upstream model
// attribution use the mapping.
func TestOpenAIGatewayService_Forward_TextResponsesSetsBillingModelToMappedModel(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_text_mapped_billing"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_text_mapped","object":"response","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}`,
			)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 4,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "sk-test",
				"base_url":      "https://example.com",
				"model_mapping": map[string]any{"gpt-5.4": "gpt-5.5"},
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.4","stream":false,"input":"hello"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.5", result.BillingModel)
	require.Equal(t, "gpt-5.5", result.UpstreamModel)
	require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, 0, result.ImageCount)
}

// TestOpenAIGatewayService_Forward_TextResponsesWithoutMappingKeepsRequestedBillingModel
// forwards text input with no configured model mapping. It checks requested,
// billing and upstream model attribution all retain the admitted model after
// success.
func TestOpenAIGatewayService_Forward_TextResponsesWithoutMappingKeepsRequestedBillingModel(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_text_unmapped_billing"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_text_unmapped","object":"response","model":"gpt-5.4","status":"completed","usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 4,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","stream":false,"input":"hello"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.4", result.BillingModel)
	require.Equal(t, "gpt-5.4", result.UpstreamModel)
}

// TestOpenAIGatewayService_Forward_TextDataImageDoesNotForceMapMarshal places
// a literal image-data prefix inside ordinary text alongside a huge numeric
// exponent. It checks the captured upstream exponent retains its raw
// representation after successful forwarding.
func TestOpenAIGatewayService_Forward_TextDataImageDoesNotForceMapMarshal(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 4,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"input":[{"type":"message","content":[{"type":"input_text","text":"literal data:image/png;base64, only","nonce":1e1000000}]}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "1e1000000", gjson.GetBytes(upstream.lastBody, "input.0.content.0.nonce").Raw)
}

// TestOpenAIGatewayService_Forward_ImageToolBillingDoesNotForceFullDecode
// combines image generation metadata with an input number beyond float
// decoding range. It checks the raw number and returned image count, size
// class and billing-model attribution.
func TestOpenAIGatewayService_Forward_ImageToolBillingDoesNotForceFullDecode(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-image"}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"input":[{"type":"message","content":[{"type":"input_text","text":"draw","nonce":1e1000000}]}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "1e1000000", gjson.GetBytes(upstream.lastBody, "input.0.content.0.nonce").Raw)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "gpt-image-2", result.BillingModel)
}

// TestOpenAIGatewayService_Forward_ImageToolWithImageOnlyModelIsNormalized
// requests an image-only main model with an image generation tool. It checks
// successful forwarding uses the configured Responses main-model constant in
// captured input.
func TestOpenAIGatewayService_Forward_ImageToolWithImageOnlyModelIsNormalized(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 11,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-image-2","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2"}],"input":"draw"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, openaicore.ImagesResponsesMainModel, gjson.GetBytes(upstream.lastBody, "model").String())
}

// TestOpenAIGatewayService_Forward_HTTPRetryRecoveryDoesNotDecodeBeforeError
// rejects encrypted reasoning before a successful retry. It checks the first
// captured body retains ciphertext and exact integer text, while recovery
// removes ciphertext and keeps reasoning summary.
func TestOpenAIGatewayService_Forward_HTTPRetryRecoveryDoesNotDecodeBeforeError(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"bad encrypted content"}}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 10,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"input":[{"type":"reasoning","encrypted_content":"gAAA","summary":[{"type":"summary_text","text":"keep me"}]},{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "gAAA", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[0], "input.1.content.0.nonce").Raw)
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
	require.Equal(t, "summary_text", gjson.GetBytes(upstream.bodies[1], "input.0.summary.0.type").String())
}

// TestOpenAIGatewayService_Forward_HTTPRetryRecoveryDropsCompaction rejects an
// encrypted compaction item before a successful retry. It checks the first
// body contains compaction and the retry retains only the following message.
func TestOpenAIGatewayService_Forward_HTTPRetryRecoveryDropsCompaction(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"bad encrypted content"}}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 10,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"input":[{"id":"cmp_stale","type":"compaction","encrypted_content":"gAAA"},{"type":"message","content":[{"type":"input_text","text":"hi"}]}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "compaction", gjson.GetBytes(upstream.bodies[0], "input.0.type").String())
	require.Equal(t, "message", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.1").Exists())
}

// TestOpenAIGatewayService_Forward_CodexSparkRejectsEscapedInputImage spells
// an image item type with a JSON Unicode escape for Codex Spark. It checks
// HTTP 400, nil result and no outgoing transport request.
func TestOpenAIGatewayService_Forward_CodexSparkRejectsEscapedInputImage(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5,
			Name:        "openai-oauth",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token": "test-token",
				"base_url":     "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.3-codex-spark","stream":false,"input":[{"type":"input_` + "\\u0069" + `mage","file_id":"file_1"}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestOpenAIGatewayService_Forward_CodexBridgeInjectionSetsImageBilling
// enables CLI image-tool injection for an allowed group and receives an image
// result. It checks returned image count, size class and image billing-model
// attribution.
func TestOpenAIGatewayService_Forward_CodexBridgeInjectionSetsImageBilling(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-image","size":"1024x1024"}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	cfg.Request.ForceCLI = true
	cfg.ImageBridge = true
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: true}})
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"input":"draw if needed"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "gpt-image-2", result.BillingModel)
}

// TestOpenAIGatewayService_Forward_HTTPPreservesPreviousResponseIDForAPIKey
// sends empty-string and null continuation IDs through API-key HTTP
// forwarding. It checks both captured upstream bodies retain the
// previous_response_id field after successful exchange.
func TestOpenAIGatewayService_Forward_HTTPPreservesPreviousResponseIDForAPIKey(t *testing.T) {
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 8,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}

	for _, body := range [][]byte{
		[]byte(`{"model":"gpt-5","stream":false,"previous_response_id":"","input":"hi"}`),
		[]byte(`{"model":"gpt-5","stream":false,"previous_response_id":null,"input":"hi"}`),
	} {
		upstream := &auxiliaryHTTPRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		}
		svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

		result, err := svc.Forward(context.Background(), c, provider, body)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	}
}

// TestOpenAIGatewayService_Forward_CustomSparkAPIKeyPreservesImageTool uses a
// custom API-key Spark provider with an image-enabled group. It checks both
// image generation and ordinary function tools remain in captured upstream
// input.
func TestOpenAIGatewayService_Forward_CustomSparkAPIKeyPreservesImageTool(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 11,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	// Allow image generation on this custom API-key Spark case so captured tool
	// assertions exercise its credential-type handling rather than the group
	// permission rejection.
	c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: true}})
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.3-codex-spark","stream":false,"input":"hi","tools":[{"type":"function","name":"shell"},{"type":"image_generation","output_format":"png"}]}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="function")`).Exists())
}

// TestOpenAIGatewayService_Forward_ImageOnlyModelKeepsSupportedVerbosity
// requests an image-only model with low text verbosity. It checks upstream
// preparation selects the Responses main model and retains the supported
// verbosity field.
func TestOpenAIGatewayService_Forward_ImageOnlyModelKeepsSupportedVerbosity(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 6,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-image-2","stream":false,"text":{"verbosity":"low"},"input":"draw"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "text.verbosity").String())
	require.Equal(t, openaicore.ImagesResponsesMainModel, gjson.GetBytes(upstream.lastBody, "model").String())
}

// TestOpenAIGatewayServiceForwardPreservesGPT56MaxEffort forwards GPT-5.6
// input with maximum effort through an API-key provider. It checks captured
// reasoning effort and returned requested-effort attribution both retain max.
func TestOpenAIGatewayServiceForwardPreservesGPT56MaxEffort(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 7,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"reasoning":{"effort":"max"},"input":"hello"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

// TestOpenAIGatewayServiceForwardPreservesMappedGPT56MaxEffort maps a client
// alias to GPT-5.6 with maximum effort. It checks upstream model mapping
// retains max in captured input and result effort.
func TestOpenAIGatewayServiceForwardPreservesMappedGPT56MaxEffort(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9,
			Name:        "openai-apikey-mapped",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
				"model_mapping": map[string]any{
					"sol": "gpt-5.6-sol",
				},
			},
			Extra: map[string]any{"use_responses_api": true},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"sol","stream":false,"reasoning":{"effort":"max"},"input":"hello"}`)
	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

// TestOpenAIGatewayServiceForwardOAuthCompactDowngradesMaxEffort forwards an
// OAuth request through the legacy Compact path with maximum effort. It checks
// the Codex Compact URL and xhigh in captured input and result effort.
func TestOpenAIGatewayServiceForwardOAuthCompactDowngradesMaxEffort(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 8,
			Name:        "openai-oauth",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-acc",
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses/compact", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","instructions":"compact-test","input":"hello","reasoning":{"effort":"max"}}`)
	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, ChatgptCodexURL+"/compact", upstream.lastReq.URL.String())
	require.Equal(t, "xhigh", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "xhigh", *result.ReasoningEffort)
}

// TestOpenAIGatewayServiceForwardOAuthRemoteCompactV2PreservesResponsesWire
// requests remote compaction through ordinary OAuth Responses with a
// Compact-only model mapping configured. It checks the ordinary model/path,
// beta header and reasoning controls remain, and encrypted compaction reaches
// client output.
func TestOpenAIGatewayServiceForwardOAuthRemoteCompactV2PreservesResponsesWire(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"summary\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 10,
			Name:        "openai-oauth-responses",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-acc",
				"compact_model_mapping": map[string]any{
					"gpt-5.6-sol": "gpt-5.6-sol-openai-compact",
				},
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("x-codex-beta-features", "remote_compaction_v2")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"instructions":"response-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}],"reasoning":{"effort":"max","context":"all_turns"}}`)
	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, ChatgptCodexURL, upstream.lastReq.URL.String())
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "compaction_trigger", gjson.GetBytes(upstream.lastBody, "input.#(type==\"compaction_trigger\").type").String())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.Equal(t, "all_turns", gjson.GetBytes(upstream.lastBody, "reasoning.context").String())
	require.Equal(t, "remote_compaction_v2", upstream.lastReq.Header.Get("x-codex-beta-features"))
	require.Contains(t, rec.Body.String(), `"type":"compaction"`)
	require.Contains(t, rec.Body.String(), `"encrypted_content":"summary"`)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

// TestOpenAIGatewayServiceForwardAPIKeyRemoteCompactV2PreservesResponsesWire
// requests remote compaction through ordinary API-key Responses with a
// Compact-only model mapping configured. It checks the ordinary model/path,
// beta header and reasoning controls remain, and encrypted compaction reaches
// client output.
func TestOpenAIGatewayServiceForwardAPIKeyRemoteCompactV2PreservesResponsesWire(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"summary\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 11,
			Name:        "openai-apikey-responses",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com/v1",
				"compact_model_mapping": map[string]any{
					"gpt-5.6-sol": "gpt-5.6-sol-openai-compact",
				},
			},
			Extra:       map[string]any{"use_responses_api": true},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("x-codex-beta-features", "remote_compaction_v2")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"instructions":"response-test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}],"reasoning":{"effort":"max","context":"all_turns"}}`)
	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://example.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "compaction_trigger", gjson.GetBytes(upstream.lastBody, "input.#(type==\"compaction_trigger\").type").String())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.Equal(t, "all_turns", gjson.GetBytes(upstream.lastBody, "reasoning.context").String())
	require.Equal(t, "remote_compaction_v2", upstream.lastReq.Header.Get("x-codex-beta-features"))
	require.Contains(t, rec.Body.String(), `"type":"compaction"`)
	require.Contains(t, rec.Body.String(), `"encrypted_content":"summary"`)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

// TestOpenAIGatewayServiceForward_RejectsDisabledImageGenerationIntents
// requests images through a main model, hosted tool or tool choice in an
// image-disabled group. It checks permission errors and no outgoing transport
// request for each case.
func TestOpenAIGatewayServiceForward_RejectsDisabledImageGenerationIntents(t *testing.T) {
	tests := []struct {
		name string // case display name
		body []byte // image intent request
	}{
		{
			name: "image model",
			body: []byte(`{"model":"gpt-image-2","input":"draw"}`),
		},
		{
			name: "image tool",
			body: []byte(`{"model":"gpt-5.4","input":"draw","tools":[{"type":"image_generation"}]}`),
		},
		{
			name: "image tool choice",
			body: []byte(`{"model":"gpt-5.4","input":"draw","tool_choice":{"type":"image_generation"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(false, "unit-test-agent/1.0")
			provider := newOpenAIImageGenerationControlTestProvider()

			result, err := svc.Forward(context.Background(), c, provider, tt.body)

			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, "permission_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			require.Nil(t, upstream.lastReq, "disabled image request must not reach upstream")
		})
	}
}

// TestOpenAIGatewayServiceForward_DisabledGroupAllowsTextOnlyResponses sends
// ordinary text through a group that disables image generation. It checks
// successful transport/output, returned text usage and zero counted images.
func TestOpenAIGatewayServiceForward_DisabledGroupAllowsTextOnlyResponses(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_text","model":"gpt-5.4","usage":{"input_tokens":3,"output_tokens":2}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(false, "unit-test-agent/1.0")
	provider := newOpenAIImageGenerationControlTestProvider()

	result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","input":"write code","stream":false}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 0, result.ImageCount)
	require.NotNil(t, upstream.lastReq)
}

// TestOpenAIGatewayServiceForward_DisabledGroupAllowsPassiveImageNamespace
// declares a passive image namespace in managed and passthrough requests for
// an image-disabled group. It checks success, retained namespace and a true
// broad image-intent hint.
func TestOpenAIGatewayServiceForward_DisabledGroupAllowsPassiveImageNamespace(t *testing.T) {
	tests := []struct {
		name        string // case display name
		passthrough bool   // selected forwarding mode
	}{
		{name: "managed forwarding"},
		{name: "passthrough forwarding", passthrough: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_passive_namespace","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":2}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(false, "codex_cli_rs/0.144.1")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			provider := newOpenAIImageGenerationControlTestProvider()
			provider.Record.Extra = map[string]any{"openai_passthrough": tt.passthrough}
			body := []byte(`{
				"model":"gpt-5.5",
				"input":"write code",
				"stream":false,
				"tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],
				"tool_choice":"auto"
			}`)

			result, err := svc.Forward(context.Background(), c, provider, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, `tools.#(name=="image_gen").type`).String())
			cached, known := GetOpenAIImageIntentHint(c)
			require.True(t, known)
			require.True(t, cached, "宽泛意图仍应保留给转发和计费")
		})
	}
}

// TestOpenAIGatewayServiceForward_CodexImageInjectionRespectsGroupCapability
// varies group image permission, bridge enablement and Responses-lite
// selection. It checks hosted-tool, tool-choice and instruction injection
// follow the table, with the lite header forwarded.
func TestOpenAIGatewayServiceForward_CodexImageInjectionRespectsGroupCapability(t *testing.T) {
	tests := []struct {
		name string // case display name
		// allowImages and bridgeEnabled permit injection together in these cases;
		// responsesLite suppresses it despite both being enabled. wantInjected
		// applies to tool, choice and instruction assertions.
		allowImages   bool // group image permission
		bridgeEnabled bool // default bridge enablement
		responsesLite bool // lite header selection
		wantInjected  bool // expected bridge injection
	}{
		{name: "disabled group skips injection", allowImages: false, bridgeEnabled: true, wantInjected: false},
		{name: "enabled group skips injection by default", allowImages: true, bridgeEnabled: false, wantInjected: false},
		{name: "enabled group injects image tool when bridge enabled", allowImages: true, bridgeEnabled: true, wantInjected: true},
		{name: "responses lite skips hosted image bridge", allowImages: true, bridgeEnabled: true, responsesLite: true, wantInjected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_codex","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.ImageBridge.DefaultEnabled = tt.bridgeEnabled
			c, _ := newOpenAIImageGenerationControlTestContext(tt.allowImages, "codex_cli_rs/0.98.0")
			if tt.responsesLite {
				c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			}
			provider := newOpenAIImageGenerationControlTestProvider()

			result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","input":"write code","stream":false}`))

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			hasImageTool := gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists()
			require.Equal(t, tt.wantInjected, hasImageTool)
			toolChoice := gjson.GetBytes(upstream.lastBody, "tool_choice")
			require.Equal(t, tt.wantInjected, toolChoice.Exists())
			if tt.wantInjected {
				require.Equal(t, "auto", toolChoice.String())
			}
			expectedLiteHeader := ""
			if tt.responsesLite {
				expectedLiteHeader = "true"
			}
			require.Equal(t, expectedLiteHeader, upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
			instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
			require.Equal(t, tt.wantInjected, strings.Contains(instructions, "image_generation"))
		})
	}
}

// TestOpenAIGatewayServiceForward_ExplicitImageToolWorksWithBridgeDisabled
// supplies an image tool for an allowed group while automatic bridge injection
// is disabled. It checks output-format normalization and retained tool with no
// bridge instruction text.
func TestOpenAIGatewayServiceForward_ExplicitImageToolWorksWithBridgeDisabled(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_explicit_image","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	provider := newOpenAIImageGenerationControlTestProvider()
	body := []byte(`{"model":"gpt-5.4","input":"draw","stream":false,"tools":[{"type":"image_generation","format":"jpeg"}]}`)

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.Equal(t, "jpeg", gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation").output_format`).String())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation").format`).Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.NotContains(t, instructions, "image_generation")
}

// TestOpenAIGatewayServiceForward_ProviderPolicyStripsExplicitImageTool
// selects provider stripping for an explicit image tool and image tool choice.
// It checks both are removed while the ordinary function tool remains and
// bridge instructions stay absent.
func TestOpenAIGatewayServiceForward_ProviderPolicyStripsExplicitImageTool(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_stripped_image","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	provider := newOpenAIImageGenerationControlTestProvider()
	provider.Record.Extra = map[string]any{
		providercore.CodexImageGenerationExplicitToolPolicyKey: providercore.CodexImagePolicyStrip,
	}
	body := []byte(`{
		"model":"gpt-5.4",
		"input":"draw",
		"stream":false,
		"tools":[
			{"type":"function","name":"shell","parameters":{"type":"object"}},
			{"type":"image_generation","format":"jpeg"}
		],
		"tool_choice":{"type":"image_generation"}
	}`)

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="function")`).Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.NotContains(t, instructions, "image_generation")
}

// TestOpenAIGatewayServiceForward_ProviderPolicyStripsImageNamespaceTools
// declares image namespaces at the top level and inside additional_tools in
// both forwarding modes. It checks stripping keeps shell/code tools, user text
// and auto choice while the cached broad intent remains true.
func TestOpenAIGatewayServiceForward_ProviderPolicyStripsImageNamespaceTools(t *testing.T) {
	tests := []struct {
		name        string // case display name
		passthrough bool   // selected forwarding mode
	}{
		{name: "managed forwarding"},
		{name: "passthrough forwarding", passthrough: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_stripped_namespace","model":"gpt-5.5","usage":{"input_tokens":2,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, _ := newOpenAIImageGenerationControlTestContext(false, "codex_cli_rs/0.144.1")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			provider := newOpenAIImageGenerationControlTestProvider()
			provider.Record.Extra = map[string]any{
				providercore.CodexImageGenerationExplicitToolPolicyKey: providercore.CodexImagePolicyStrip,
				"openai_passthrough": tt.passthrough,
			}
			body := []byte(`{
				"model":"gpt-5.5",
				"stream":false,
				"tools":[
					{"type":"function","name":"shell","parameters":{"type":"object"}},
					{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]},
					{"type":"namespace","name":"code_tools","tools":[{"type":"function","name":"run"}]}
				],
				"input":[
					{"type":"message","role":"user","content":[{"type":"input_text","text":"write code"}]},
					{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}
				],
				"tool_choice":"auto"
			}`)

			result, err := svc.Forward(context.Background(), c, provider, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			var forwarded map[string]any
			require.NoError(t, json.Unmarshal(upstream.lastBody, &forwarded))
			require.False(t, openaicore.HasOpenAIImageGenerationTool(forwarded))
			require.Equal(t, "auto", forwarded["tool_choice"])
			require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(name=="shell")`).Exists())
			require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(name=="code_tools")`).Exists())
			require.Equal(t, "write code", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
			cached, known := GetOpenAIImageIntentHint(c)
			require.True(t, known)
			require.True(t, cached)
		})
	}
}

// TestOpenAIGatewayServiceForward_GroupProtocolEnablesCodexInjection enables
// the group Responses image policy on a Codex-shaped request. It checks hosted
// image-tool and image instruction injection after successful forwarding.
func TestOpenAIGatewayServiceForward_GroupProtocolEnablesCodexInjection(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_group_protocol","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	key, ok := c.MustGet("api_key").(*apikey.APIKey)
	require.True(t, ok)
	key.Group.ResponsesImagePolicy = "enabled"
	provider := newOpenAIImageGenerationControlTestProvider()

	result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","input":"write code","stream":false}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.Contains(t, instructions, "image_generation")
}

// TestOpenAIGatewayServiceForward_CodexBridgeDoesNotInjectHostedToolAlongsideImageGenNamespace
// enables the image bridge beside image namespaces at two request levels. It
// checks both namespaces remain and no hosted image tool is added.
func TestOpenAIGatewayServiceForward_CodexBridgeDoesNotInjectHostedToolAlongsideImageGenNamespace(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_namespace_image","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.ImageBridge.DefaultEnabled = true
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	provider := newOpenAIImageGenerationControlTestProvider()
	body := []byte(`{
		"model":"gpt-5.5",
		"stream":false,
		"tools":[
			{"type":"function","name":"shell","parameters":{"type":"object"}},
			{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}
		],
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"draw a cat"}]},
			{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}
		],
		"tool_choice":"auto"
	}`)

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, `tools.#(name=="image_gen").type`).String())
	require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools").tools.#(name=="image_gen").type`).String())
}

// TestOpenAIGatewayServiceForward_CodexBridgePreservesImageGenFunction enables
// the image bridge beside flat and nested image function declarations. It
// checks function recognition, no hosted-tool/choice injection and no bridge
// marker in captured instructions.
func TestOpenAIGatewayServiceForward_CodexBridgePreservesImageGenFunction(t *testing.T) {
	tests := []struct {
		name string // case display name
		tool string // image function declaration
	}{
		{
			name: "flat function",
			tool: `{"type":"function","name":"image_gen.imagegen","parameters":{"type":"object"}}`,
		},
		{
			name: "nested function",
			tool: `{"type":"function","function":{"name":"image_gen.imagegen","parameters":{"type":"object"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_function_image","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.ImageBridge.DefaultEnabled = true
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			provider := newOpenAIImageGenerationControlTestProvider()
			body := []byte(`{"model":"gpt-5.5","input":"draw a cat","stream":false,"tools":[` + tt.tool + `]}`)

			result, err := svc.Forward(context.Background(), c, provider, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)

			var forwarded map[string]any
			require.NoError(t, json.Unmarshal(upstream.lastBody, &forwarded))
			require.True(t, openaicore.HasCodexImageGenerationFunctionTool(forwarded))
			require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
			require.NotContains(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), openaicore.CodexImageGenerationBridgeMarker)
		})
	}
}

// TestOpenAIGatewayServiceForward_CodexBridgePreservesExistingToolChoice
// enables the image bridge with an existing hosted image tool choice. It
// checks successful forwarding retains the requested choice type in captured
// upstream input.
func TestOpenAIGatewayServiceForward_CodexBridgePreservesExistingToolChoice(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_codex_tool_choice","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.ImageBridge.DefaultEnabled = true
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	provider := newOpenAIImageGenerationControlTestProvider()

	result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","input":"draw","stream":false,"tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "image_generation", gjson.GetBytes(upstream.lastBody, "tool_choice.type").String())
}

// TestOpenAIGatewayServiceForward_CodexBridgeSkipsCompactRequests enables the
// image bridge on a legacy Compact request. It checks captured input omits
// hosted-tool/choice injection and instructions contain no image bridge text.
func TestOpenAIGatewayServiceForward_CodexBridgeSkipsCompactRequests(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_codex_compact","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.ImageBridge.DefaultEnabled = true
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses/compact", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
	provider := newOpenAIImageGenerationControlTestProvider()

	// Select the Compact path with the bridge enabled to expose any injected
	// image tool or choice in captured input; the assertions also inspect
	// instructions.
	result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.4","input":"summarize the conversation","stream":false}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	instructions := gjson.GetBytes(upstream.lastBody, "instructions").String()
	require.NotContains(t, instructions, "image_generation")
}

// Read returns no bytes and the configured error on every invocation. A nil
// configured error selects io.ErrUnexpectedEOF, allowing Compact tests to
// inject a response read failure after successful request establishment.
func (r passthroughErrReadCloser) Read(_ []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.ErrUnexpectedEOF
}

// Close accepts fixture response cleanup and returns nil; this stateless
// error reader records no closure observation.
func (r passthroughErrReadCloser) Close() error {
	return nil
}

// TestOpenAIGatewayService_ResponsesUnknownModelDoesNotFallbackToGPT54
// receives a model-not-found error for an unrecognized OAuth model. It checks
// the captured Codex request retains that model and forwarding returns an
// error response.
func TestOpenAIGatewayService_ResponsesUnknownModelDoesNotFallbackToGPT54(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	originalBody := []byte(`{"model":"gpt6","stream":false,"instructions":"local-test-instructions","input":[{"type":"text","text":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(originalBody))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_unknown_model"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"model not found"}}`)),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:        "acc",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-acc",
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
	require.Equal(t, "gpt6", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotEqual(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, rec.Code >= http.StatusBadRequest)
}

// TestOpenAIGatewayService_OAuthResponsesPromotesSystemMessageWithoutDuplication
// combines a system input message with existing OAuth instructions. It checks
// captured instructions concatenate both once and input retains only the user
// message before the fixture transport error.
func TestOpenAIGatewayService_OAuthResponsesPromotesSystemMessageWithoutDuplication(t *testing.T) {
	const systemPrompt = "Unique system prefix for Responses token accounting."
	const existingInstructions = "Existing instructions."
	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"` + existingInstructions + `","input":[{"role":"system","content":"` + systemPrompt + `"},{"role":"user","content":"hello"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{err: errors.New("stop after capture")}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 124,
			Name:        "openai-oauth",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-acc",
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)

	require.Error(t, err)
	require.Nil(t, result)
	require.NotEmpty(t, upstream.lastBody)
	require.Equal(t, systemPrompt+"\n\n"+existingInstructions, gjson.GetBytes(upstream.lastBody, "instructions").String())
	require.Equal(t, int64(1), gjson.GetBytes(upstream.lastBody, "input.#").Int())
	require.Equal(t, "user", gjson.GetBytes(upstream.lastBody, "input.0.role").String())
	require.Equal(t, 1, strings.Count(string(upstream.lastBody), systemPrompt))
}

// TestOpenAIGatewayService_NativeResponsesBodyModificationPreservesHTMLChars
// prepares native Responses input containing repeated HTML characters. It
// checks the captured body keeps literal text and the Responses endpoint
// before the fixture HTTP error.
func TestOpenAIGatewayService_NativeResponsesBodyModificationPreservesHTMLChars(t *testing.T) {
	payloadText := strings.Repeat(`<tag>&value</tag>`, 128)
	originalBody := []byte(fmt.Sprintf(`{"model":"gpt-5.5","stream":false,"max_output_tokens":100,"previous_response_id":"resp_prev","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}]}`, payloadText))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(originalBody))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_native_reencode"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"stop after capture"}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{
		Enabled:           false,
		AllowInsecureHTTP: true,
	}}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 456,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "http://upstream.example",
			},
			Extra: map[string]any{
				providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModePreserveClientProtocol),
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.lastReq.URL.String())
	require.Contains(t, string(upstream.lastBody), payloadText)
	require.NotContains(t, string(upstream.lastBody), `\\u003c`)
	require.NotContains(t, string(upstream.lastBody), `\\u003e`)
	require.NotContains(t, string(upstream.lastBody), `\\u0026`)
}

// TestOpenAIGatewayService_OAuthMessagesBridgeDoesNotInjectDefaultInstructions
// sends developer-marker input and a metadata session key through OAuth
// forwarding. It checks absent default instructions, removal of the body key
// and populated Session_Id with other Codex identity headers absent.
func TestOpenAIGatewayService_OAuthMessagesBridgeDoesNotInjectDefaultInstructions(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	originalBody := []byte(`{"model":"gpt-5.5","stream":true,"prompt_cache_key":"anthropic-metadata-session-1","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<tokenrouter-claude-code-todo-guard>"}]},{"type":"message","role":"user","content":"hello"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(originalBody))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_bridge"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"bridge stop"}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:        "acc",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-acc",
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "", gjson.GetBytes(upstream.lastBody, "instructions").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
	require.NotEmpty(t, upstream.lastReq.Header.Get("Session_Id"))
	require.Empty(t, upstream.lastReq.Header.Get("Conversation_Id"))
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Empty(t, upstream.lastReq.Header.Get("originator"))
}

// TestOpenAIGatewayService_OpenAIOAuthHTTPForwardsTLSProfile varies provider
// credential type and TLS fingerprint enablement. It checks the transport
// receives a TLS profile only for the enabled OAuth table case.
func TestOpenAIGatewayService_OpenAIOAuthHTTPForwardsTLSProfile(t *testing.T) {
	for _, tc := range []struct {
		name         string // case display name
		providerType string // fixture credential type
		// extra is retained by the constructed provider record and read during
		// forwarding; each case keeps that map stable until transport inspection.
		extra       map[string]any // provider TLS settings
		wantProfile bool           // expected profile presence
	}{
		{
			name:         "OpenAI OAuth 启用 TLS 时传入 profile",
			providerType: capability.ProviderTypeOAuth,
			extra:        map[string]any{"enable_tls_fingerprint": true},
			wantProfile:  true,
		},
		{
			name:         "OpenAI OAuth 关闭 TLS 时不传 profile",
			providerType: capability.ProviderTypeOAuth,
			extra:        map[string]any{"enable_tls_fingerprint": false},
			wantProfile:  false,
		},
		{
			name:         "OpenAI API Key 手写 extra 也不传 profile",
			providerType: capability.ProviderTypeAPIKey,
			extra:        map[string]any{"enable_tls_fingerprint": true},
			wantProfile:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := []byte(`{"model":"gpt-5.1","stream":false,"input":"hello"}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"stop"}}`)),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream, profiles: &egressadapter.TLSProfiles{}})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 321,
					Name:        "acc",
					Platform:    capability.PlatformOpenAI,
					Type:        tc.providerType,
					Concurrency: 1,
					Extra:       tc.extra,
					Status:      billingcore.StatusActive,
					Schedulable: true,
				},
			}
			if tc.providerType == capability.ProviderTypeAPIKey {
				provider.Record.Credentials = map[string]any{"api_key": "sk-test"}
			} else {
				provider.Record.Credentials = map[string]any{
					"access_token":       "oauth-token",
					"chatgpt_account_id": "chatgpt-acc",
				}
			}

			// Discard the scripted HTTP error because this table inspects transport
			// TLS-profile selection; the request capture remains available even when
			// forwarding reports fixture rejection.
			_, _ = svc.Forward(context.Background(), c, provider, body)
			if tc.wantProfile {
				require.NotNil(t, upstream.lastTLSProfile)
				return
			}
			require.Nil(t, upstream.lastTLSProfile)
		})
	}
}

// SetRateLimited appends the supplied reset deadline to the fixture call
// history. It ignores context and provider ID, reports success and requires
// tests to serialize writes with later inspection.
func (r *openAIPassthroughFailoverRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	// Capture every reset deadline in order so failover assertions can count
	// rate-limit updates as well as inspect the resulting wait duration.
	r.rateLimitCalls = append(r.rateLimitCalls, resetAt)
	return nil
}

// SetOverloaded appends the supplied overload deadline to the fixture call
// history. It ignores context and provider ID, reports success and requires
// tests to serialize writes with later inspection.
func (r *openAIPassthroughFailoverRepo) SetOverloaded(_ context.Context, _ int64, until time.Time) error {
	// Capture every overload deadline in order so failover assertions can count
	// cooldown updates separately from verifying the configured overload
	// duration.
	r.overloadCalls = append(r.overloadCalls, until)
	return nil
}

// TestOpenAIGatewayService_OAuthPassthrough_StreamKeepsToolNameAndBodyNormalized
// streams OAuth passthrough with inbound credentials and mixed request
// headers. It checks provider authentication/header selection, store/stream
// normalization, retained input and downstream apply_patch tool identity.
func TestOpenAIGatewayService_OAuthPassthrough_StreamKeepsToolNameAndBodyNormalized(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Authorization", "Bearer inbound-should-not-forward")
	c.Request.Header.Set("Cookie", "secret=1")
	c.Request.Header.Set("X-Api-Key", "sk-inbound")
	c.Request.Header.Set("X-Goog-Api-Key", "goog-inbound")
	c.Request.Header.Set("Accept-Encoding", "gzip")
	c.Request.Header.Set("Proxy-Authorization", "Basic abc")
	c.Request.Header.Set("X-Test", "keep")
	c.Request.Header.Set("x-codex-beta-features", "remote_compaction_v2")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"store":true,"instructions":"local-test-instructions","input":[{"type":"text","text":"hi"}]}`)

	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"tool_call","tool_calls":[{"function":{"name":"apply_patch"}}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream, credentials: &providercore.OpenAIExecutionCredentials{}})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	// Clear the supplied OpenAI token resolver before forwarding so the request
	// takes credentials from the current provider record and its captured
	// authentication can be asserted.
	svc.Requests.Credentials.OpenAI = nil

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)

	// OAuth passthrough sets store=false and stream=true.
	require.Equal(t, false, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, true, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "local-test-instructions", strings.TrimSpace(gjson.GetBytes(upstream.lastBody, "instructions").String()))
	// Other request fields retain their values.
	require.Equal(t, "gpt-5.2", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "input.0.text").String())

	// Provider authentication replaces inbound credentials and cookies.
	require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "codex_cli_rs/0.1.0", upstream.lastReq.Header.Get("User-Agent"))
	require.Empty(t, upstream.lastReq.Header.Get("Cookie"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Api-Key"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Goog-Api-Key"))
	require.Empty(t, upstream.lastReq.Header.Get("Accept-Encoding"))
	require.Empty(t, upstream.lastReq.Header.Get("Proxy-Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Test"))
	require.Equal(t, "remote_compaction_v2", upstream.lastReq.Header.Get("x-codex-beta-features"))

	// OAuth requests include the required host and account headers.
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))

	// Downstream SSE retains the upstream tool name.
	body := rec.Body.String()
	require.Contains(t, body, "apply_patch")
	require.NotContains(t, body, "\"name\":\"edit\"")
}

// TestOpenAIGatewayService_OAuthPassthrough_PreservesNamespaceRequest passes
// namespace declarations, tool choice and historical calls through OAuth
// passthrough. It checks their identities remain, a stale message-level
// namespace disappears and the nested namespace/output remain.
func TestOpenAIGatewayService_OAuthPassthrough_PreservesNamespaceRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
	originalBody := []byte(`{
		"model":"gpt-5.5","stream":true,"instructions":"local-test-instructions",
		"tools":[
			{"type":"function","name":"plain","parameters":{"type":"object"}},
			{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
		],
		"tool_choice":{"type":"function","name":"spawn_agent","namespace":"collaboration"},
		"input":[
			{"type":"function_call","call_id":"call_old","name":"spawn_agent","namespace":"collaboration","arguments":"{}"},
			{"type":"message","role":"user","namespace":"residual","content":[{"type":"input_text","text":"keep","namespace":"nested"}]}
		]
	}`)
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"spawn_agent","namespace":"collaboration","arguments":"{}"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 125, Name: "acc", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:       map[string]any{"openai_passthrough": true}, Status: billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, "tools.1.name").String())
	require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, "tool_choice.namespace").String())
	require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.1.namespace").Exists())
	require.Equal(t, "nested", gjson.GetBytes(upstream.lastBody, "input.1.content.0.namespace").String())
	require.NotContains(t, string(upstream.lastBody), "collaboration__spawn_agent")
	require.Contains(t, rec.Body.String(), `"namespace":"collaboration"`)
}

// TestOpenAIGatewayService_OAuthPassthrough_FlattenEnabledNamespaceRequestAndStreamResponse
// enables OAuth passthrough namespace flattening for declarations, choice and
// history. It checks one outgoing flattened exchange and restored tool
// name/namespace in client SSE.
func TestOpenAIGatewayService_OAuthPassthrough_FlattenEnabledNamespaceRequestAndStreamResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")

	originalBody := []byte(`{
		"model":"gpt-5.5",
		"stream":true,
		"instructions":"local-test-instructions",
		"tools":[
			{"type":"function","name":"plain","description":"keep","parameters":{"type":"object"}},
			{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","description":"spawn","parameters":{"type":"object"}}]}
		],
		"tool_choice":{"type":"function","name":"spawn_agent","namespace":"collaboration"},
		"input":[
			{"type":"function_call","call_id":"call_old","name":"spawn_agent","namespace":"collaboration","arguments":"{}"},
			{"type":"message","role":"user","namespace":"residual","content":[{"type":"input_text","text":"keep","namespace":"nested"}]}
		]
	}`)

	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"collaboration__spawn_agent","arguments":""}}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"collaboration__spawn_agent","arguments":"{}"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"collaboration__spawn_agent","arguments":"{}"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_namespace"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123, Name: "acc", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra: map[string]any{
				"openai_passthrough":                  true,
				"openai_responses_flatten_namespaces": true,
			},
			Status: billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, gjson.GetBytes(upstream.lastBody, "tools").Array(), 2)
	require.Equal(t, "plain", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
	require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(upstream.lastBody, "tools.1.name").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools.1.tools").Exists())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(upstream.lastBody, "tool_choice.name").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice.namespace").Exists())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(upstream.lastBody, "input.0.name").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.namespace").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.1.namespace").Exists())
	require.Equal(t, "nested", gjson.GetBytes(upstream.lastBody, "input.1.content.0.namespace").String())
	require.Len(t, upstream.bodies, 1)

	downstream := rec.Body.String()
	require.NotContains(t, downstream, "collaboration__spawn_agent")
	require.Contains(t, downstream, `"name":"spawn_agent"`)
	require.Contains(t, downstream, `"namespace":"collaboration"`)
}

// TestOpenAIGatewayService_NativeOAuth_FlattenEnabledNamespaceRequestAndStreamResponse
// enables namespace flattening on the managed OAuth Responses path. It checks
// flattened upstream declaration/history names and restored name/namespace in
// the client stream.
func TestOpenAIGatewayService_NativeOAuth_FlattenEnabledNamespaceRequestAndStreamResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
	body := []byte(`{
		"model":"gpt-5.5","stream":true,"instructions":"test",
		"tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}],
		"input":[{"type":"function_call","call_id":"call_old","name":"spawn_agent","namespace":"collaboration","arguments":"{}"}]
	}`)
	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"collaboration__spawn_agent","arguments":""}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"collaboration__spawn_agent","arguments":"{}"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_native_namespace"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 124, Name: "native", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:       map[string]any{"openai_responses_flatten_namespaces": true},
			Status:      billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(upstream.lastBody, "input.0.name").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.namespace").Exists())
	require.NotContains(t, rec.Body.String(), "collaboration__spawn_agent")
	require.Contains(t, rec.Body.String(), `"name":"spawn_agent"`)
	require.Contains(t, rec.Body.String(), `"namespace":"collaboration"`)
}

// TestOpenAIGatewayService_OAuthPassthrough_FlattenEnabledNamespaceCollisionReturnsBadRequest
// declares a top-level function that conflicts with a flattened namespace
// function. It checks pre-transport HTTP 400 with tools parameter attribution
// and the collision message.
func TestOpenAIGatewayService_OAuthPassthrough_FlattenEnabledNamespaceCollisionReturnsBadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
	body := []byte(`{
		"model":"gpt-5.5","stream":true,"instructions":"test",
		"tools":[
			{"type":"function","name":"collaboration__spawn_agent","parameters":{"type":"object"}},
			{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
		],"input":"hi"
	}`)
	upstream := &auxiliaryHTTPRecorder{}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123, Name: "acc", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra: map[string]any{
				"openai_passthrough":                  true,
				"openai_responses_flatten_namespaces": true,
			},
			Status: billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "tools", gjson.Get(rec.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "conflicts with a top-level tool")
}

// TestOpenAIGatewayService_OAuthPassthrough_CompactUsesJSONAndKeepsNonStreaming
// sends streaming/store flags to OAuth passthrough Compact and receives JSON.
// It checks those flags disappear, result is nonstreaming and the request uses
// JSON accept and Codex identity headers.
func TestOpenAIGatewayService_OAuthPassthrough_CompactUsesJSONAndKeepsNonStreaming(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Content-Type", "application/json")

	originalBody := []byte(`{"model":"gpt-5.1-codex","stream":true,"store":true,"instructions":"local-test-instructions","input":[{"type":"text","text":"compact me"}]}`)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-compact"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"cmp_123","usage":{"input_tokens":11,"output_tokens":22}}`)),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)

	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Exists())
	require.Equal(t, "gpt-5.1-codex", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "compact me", gjson.GetBytes(upstream.lastBody, "input.0.text").String())
	require.Equal(t, "local-test-instructions", strings.TrimSpace(gjson.GetBytes(upstream.lastBody, "instructions").String()))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, openaicore.CodexCLIVersion, upstream.lastReq.Header.Get("Version"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("Session_Id"))
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Contains(t, rec.Body.String(), `"id":"cmp_123"`)
}

// TestOpenAIGatewayService_OAuthPassthrough_UpstreamRequestIgnoresClientCancel
// cancels the client context before OAuth passthrough forwarding over HTTP. It
// checks a successful result and a captured upstream context with no
// cancellation error.
func TestOpenAIGatewayService_OAuthPassthrough_UpstreamRequestIgnoresClientCancel(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	reqCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil)).WithContext(reqCtx)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	// Cancel before calling Forward so the captured transport context
	// distinguishes HTTP upstream detachment from a request that happened to
	// finish before client cancellation.
	cancel()

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"store":true,"instructions":"local-test-instructions","input":[{"type":"text","text":"hi"}]}`)
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_passthrough_ctx"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":1}}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true, "openai_oauth_responses_websockets_v2_mode": "off"},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(reqCtx, c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.NoError(t, upstream.lastReq.Context().Err())
}

// TestOpenAIGatewayService_OAuthDefaultInstructions captures Responses and
// legacy Compact requests after provider mapping and passthrough preparation.
// It checks that the upstream model selects the default prompt and that
// supplied instructions survive both top-level and Responses Lite input.
func TestOpenAIGatewayService_OAuthDefaultInstructions(t *testing.T) {
	const liteDeveloperInput = `[{"type":"additional_tools","role":"developer","tools":[]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"Client base instructions."}]},{"type":"message","role":"user","content":"hi"}]`
	const liteToolsOnlyInput = `[{"type":"additional_tools","role":"developer","tools":[]},{"type":"message","role":"user","content":"hi"}]`
	for _, tc := range []struct {
		name         string // forwarding scenario name
		model        string // client model identifier
		mappedModel  string // configured provider target
		compactModel string // configured Compact override
		instructions string // optional client instructions
		passthrough  bool   // provider passthrough enabled
		compact      bool   // legacy Compact request
		liteInput    string // raw input for Lite requests
		omitPrompt   bool   // expected absent top-level prompt
	}{
		{name: "codex-passthrough", model: "gpt-5.1-codex-max", passthrough: true},
		{name: "codex-passthrough-compact", model: "gpt-5.1-codex-max", passthrough: true, compact: true},
		{name: "astra-passthrough", model: "gpt-6-astra", passthrough: true},
		{name: "sol-passthrough-mapped", model: "client-model", mappedModel: "gpt-6.1-sol", passthrough: true},
		{name: "terra-passthrough-compact", model: "gpt-6.1-terra", passthrough: true, compact: true},
		{name: "sol-transform-mapped", model: "client-model", mappedModel: "gpt-6.1-sol"},
		{name: "terra-transform-compact", model: "gpt-6.1-sol", compactModel: "gpt-6.1-terra", compact: true},
		{name: "mapped-with-client-instructions", model: "client-model", mappedModel: "gpt-6.1-sol", instructions: " Client instructions. "},
		{name: "passthrough-with-client-instructions", model: "gpt-6-astra", instructions: " Client instructions. ", passthrough: true},
		{name: "lite-passthrough-developer-input", model: "gpt-6-astra", passthrough: true, liteInput: liteDeveloperInput, omitPrompt: true},
		{name: "lite-managed-developer-input", model: "gpt-6-astra", liteInput: liteDeveloperInput, omitPrompt: true},
		{name: "lite-passthrough-tools-only", model: "gpt-6-astra", passthrough: true, liteInput: liteToolsOnlyInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			path := "/v1/responses"
			responseBody := strings.Join([]string{
				`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hi"}`,
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"", "data: [DONE]", "",
			}, "\n")
			responseContentType := "text/event-stream"
			if tc.compact {
				path = "/v1/responses/compact"
				responseBody = `{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				responseContentType = "application/json"
			}
			c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			body := map[string]any{"model": tc.model, "stream": !tc.compact, "store": true, "input": "hi"}
			if tc.liteInput != "" {
				c.Request.Header.Set(media.ResponsesLiteHeader, "true")
				body["input"] = json.RawMessage(tc.liteInput)
			}
			if tc.instructions != "" {
				body["instructions"] = tc.instructions
			}
			originalBody, err := json.Marshal(body)
			require.NoError(t, err)
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{responseContentType}, "x-request-id": []string{"rid"}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{compactModel: tc.compactModel, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 123, Name: "acc", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
					Extra:       map[string]any{"openai_passthrough": tc.passthrough, "openai_oauth_responses_websockets_v2_mode": "off"},
					Status:      billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
				},
			}
			wantModel := tc.model
			if tc.mappedModel != "" {
				provider.Record.Credentials["model_mapping"] = map[string]any{tc.model: tc.mappedModel}
				wantModel = tc.mappedModel
			}
			if tc.compactModel != "" {
				wantModel = tc.compactModel
			}

			result, err := svc.Forward(context.Background(), c, provider, originalBody)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			if tc.compact {
				require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Exists())
			} else {
				require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
			}
			require.Equal(t, wantModel, gjson.GetBytes(upstream.lastBody, "model").String())
			if tc.omitPrompt {
				require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
				require.Equal(t, "Client base instructions.", gjson.GetBytes(upstream.lastBody, `input.#(type=="message").content.0.text`).String())
				return
			}
			wantInstructions := tc.instructions
			if wantInstructions == "" {
				wantInstructions = openaicore.DefaultCodexSynthInstructions(wantModel)
			}
			require.Equal(t, wantInstructions, gjson.GetBytes(upstream.lastBody, "instructions").String())
		})
	}
}

// TestOpenAIGatewayService_OAuthPassthrough_DisabledUsesLegacyTransform
// disables passthrough for OAuth input that requests buffered stored output.
// It checks the captured body differs and carries the legacy store=false and
// stream=true controls.
func TestOpenAIGatewayService_OAuthPassthrough_DisabledUsesLegacyTransform(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

	// The OAuth transform forces store=false and stream=true.
	inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": false},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.NoError(t, err)

	// OAuth transformation rewrites the request body.
	require.NotEqual(t, inputBody, upstream.lastBody)
	require.Contains(t, string(upstream.lastBody), `"store":false`)
	require.Contains(t, string(upstream.lastBody), `"stream":true`)
}

// TestOpenAIGatewayService_OAuthLegacy_UpstreamRequestIgnoresClientCancel
// cancels the client context before managed OAuth HTTP forwarding. It checks a
// successful result and a captured upstream context with no cancellation
// error.
func TestOpenAIGatewayService_OAuthLegacy_UpstreamRequestIgnoresClientCancel(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	reqCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil)).WithContext(reqCtx)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	// Cancel before calling Forward so the captured transport context
	// distinguishes HTTP upstream detachment from a request that happened to
	// finish before client cancellation.
	cancel()

	originalBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_legacy_ctx"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": false, "openai_oauth_responses_websockets_v2_mode": "off"},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(reqCtx, c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.NoError(t, upstream.lastReq.Context().Err())
}

// TestOpenAIGatewayService_OAuthLegacy_CompositeCodexUAUsesCodexOriginator
// sends a browser-prefixed user agent containing a later Codex token. It
// checks outgoing identity uses the default Codex user agent and codex-tui
// originator.
func TestOpenAIGatewayService_OAuthLegacy_CompositeCodexUAUsesCodexOriginator(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	// Place the Codex token after a browser prefix to exercise
	// composite-user-agent recognition; paired outgoing identity is asserted
	// after the managed OAuth exchange.
	c.Request.Header.Set("User-Agent", "Mozilla/5.0 codex_cli_rs/0.1.0")

	inputBody := []byte(`{"model":"gpt-5.2","stream":true,"store":false,"input":[{"type":"text","text":"hi"}]}`)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": false},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	// The outgoing user agent and originator describe the same fixture client.
	require.Equal(t, gateway.DefaultOpenAICodexUserAgent, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", upstream.lastReq.Header.Get("originator"))
	require.NotEqual(t, "opencode", upstream.lastReq.Header.Get("originator"))
}

// TestOpenAIGatewayService_OAuthPassthrough_ResponseHeadersAllowXCodex returns
// quota headers alongside an OAuth passthrough usage stream. It checks primary
// and secondary used percentages reach the client response headers after
// successful forwarding.
func TestOpenAIGatewayService_OAuthPassthrough_ResponseHeadersAllowXCodex(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"input":[{"type":"text","text":"hi"}]}`)

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("x-request-id", "rid")
	headers.Set("x-codex-primary-used-percent", "12")
	headers.Set("x-codex-secondary-used-percent", "34")
	headers.Set("x-codex-primary-window-minutes", "300")
	headers.Set("x-codex-secondary-window-minutes", "10080")
	headers.Set("x-codex-primary-reset-after-seconds", "1")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     headers,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"h"}`,
			"",
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)

	require.Equal(t, "12", rec.Header().Get("x-codex-primary-used-percent"))
	require.Equal(t, "34", rec.Header().Get("x-codex-secondary-used-percent"))
}

// TestOpenAIGatewayService_OAuthPassthrough_UpstreamErrorIncludesPassthroughFlag
// returns an ordinary HTTP 400 through OAuth passthrough. It checks direct
// client status and a recorded http_error Ops event carrying passthrough
// attribution.
func TestOpenAIGatewayService_OAuthPassthrough_UpstreamErrorIncludesPassthroughFlag(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

	originalBody := []byte(`{"model":"gpt-5.2","stream":false,"input":[{"type":"text","text":"hi"}]}`)

	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad"}}`)),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.Error(t, err)
	require.True(t, c.Writer.Written(), "非 429/529 的 passthrough 错误应直接写回客户端")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Passthrough errors are recorded with passthrough=true.
	v, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	arr, ok := v.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, arr)
	require.True(t, arr[len(arr)-1].Passthrough)
	require.Equal(t, "http_error", arr[len(arr)-1].Kind)
}

// TestOpenAIGatewayService_APIKeyPassthrough_RebuildsUpstreamErrors returns
// structured and unstructured API-key errors with sensitive body/header
// fixtures. It checks status reconstruction, generic messages and allowed
// retry delay while dropping tested secrets and diagnostic headers.
func TestOpenAIGatewayService_APIKeyPassthrough_RebuildsUpstreamErrors(t *testing.T) {
	tests := []struct {
		name           string // case display name
		statusCode     int    // upstream HTTP status
		contentType    string // upstream media type
		responseBody   string // upstream error payload
		retryAfter     string // upstream retry hint
		wantStatus     int    // expected client status
		wantMessage    string // expected public message
		wantRetryAfter string // expected client retry hint
	}{
		{
			name:           "upstream forbidden is reported as gateway failure",
			statusCode:     http.StatusForbidden,
			contentType:    "text/html; charset=UTF-8",
			responseBody:   `<!DOCTYPE html><title>secret-upstream.example denied the request</title>`,
			retryAfter:     "17",
			wantStatus:     http.StatusBadGateway,
			wantMessage:    "Upstream access denied",
			wantRetryAfter: "17",
		},
		{
			name:         "upstream unauthorized is reported as gateway failure",
			statusCode:   http.StatusUnauthorized,
			contentType:  "application/json",
			responseBody: `{"error":{"message":"invalid secret-upstream.example token","type":"authentication_error","code":"invalid_api_key","param":"api_key"},"rate_limit":{"remaining":0}}`,
			wantStatus:   http.StatusBadGateway,
			wantMessage:  "Upstream authentication failed",
		},
		// Transient 5xx statuses (500/502/503/504/520-524) trigger API key provider failover.
		// APIKeyPassthrough_Transient5xxTriggersFailover covers those statuses;
		// use a non-transient 5xx here to exercise sanitized error reconstruction.
		{
			name:         "html 5xx",
			statusCode:   530,
			contentType:  "text/html; charset=UTF-8",
			responseBody: `<!DOCTYPE html><title>secret-upstream.example | 530: Origin DNS error</title>`,
			wantStatus:   530,
			wantMessage:  "Upstream service temporarily unavailable",
		},
		{
			name:         "structured 5xx",
			statusCode:   http.StatusNotImplemented,
			contentType:  "application/json",
			responseBody: `{"error":{"message":"secret-upstream.example internal failure"}}`,
			wantStatus:   http.StatusNotImplemented,
			wantMessage:  "Upstream service temporarily unavailable",
		},
		{
			name:         "unstructured 4xx",
			statusCode:   http.StatusBadRequest,
			contentType:  "text/plain",
			responseBody: `proxy secret-upstream.example rejected the request`,
			wantStatus:   http.StatusBadRequest,
			wantMessage:  "Upstream request failed",
		},
		{
			name:         "malicious valid json 4xx",
			statusCode:   http.StatusBadRequest,
			contentType:  "application/json",
			responseBody: `{"error":{"message":"secret-upstream.example invalid parameter","type":"invalid_request_error","code":"upstream_secret_code","param":"private_field","internal_token":"sk-upstream-secret"},"rate_limit":{"remaining":0,"reset":"internal-window"},"debug":{"admin":"root"},"redirect":"https://secret-upstream.example/admin"}`,
			retryAfter:   "not-a-valid-delay",
			wantStatus:   http.StatusBadRequest,
			wantMessage:  "Upstream request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: tt.statusCode,
				Header: http.Header{
					"Content-Type":                 []string{tt.contentType},
					"Location":                     []string{"https://secret-upstream.example/admin"},
					"Retry-After":                  []string{tt.retryAfter},
					"Server":                       []string{"secret-upstream-proxy"},
					"Set-Cookie":                   []string{"admin_token=secret"},
					"WWW-Authenticate":             []string{`Bearer realm="secret-upstream.example"`},
					"X-Admin-Debug":                []string{"internal-route=secret-upstream.example"},
					"X-Codex-Primary-Used-Percent": []string{"99"},
					"x-request-id":                 []string{"rid-sensitive-upstream"},
				},
				Body: io.NopCloser(strings.NewReader(tt.responseBody)),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 124,
					Name:        "sensitive-upstream",
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{
						"api_key":  "sk-test",
						"base_url": "https://secret-upstream.example",
					},
					Extra:       map[string]any{"openai_passthrough": true},
					Status:      billingcore.StatusActive,
					Schedulable: true,
				},
			}
			requestBody := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)

			_, err := svc.Forward(context.Background(), c, provider, requestBody)

			require.Error(t, err)
			require.Equal(t, tt.wantStatus, rec.Code)
			opsValue, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			opsEvents, ok := opsValue.([]*ops.OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.NotEmpty(t, opsEvents)
			require.Equal(t, tt.statusCode, opsEvents[len(opsEvents)-1].UpstreamStatusCode)
			require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			require.Equal(t, tt.wantRetryAfter, rec.Header().Get("Retry-After"))
			for _, key := range []string{
				"Location",
				"Server",
				"Set-Cookie",
				"WWW-Authenticate",
				"X-Admin-Debug",
				"X-Codex-Primary-Used-Percent",
				"X-Request-Id",
			} {
				require.Empty(t, rec.Header().Values(key), "sensitive upstream header %s must be dropped", key)
			}
			require.Equal(t, "upstream_error", gjson.Get(rec.Body.String(), "error.type").String())
			require.Equal(t, tt.wantMessage, gjson.Get(rec.Body.String(), "error.message").String())
			require.False(t, gjson.Get(rec.Body.String(), "error.code").Exists())
			require.False(t, gjson.Get(rec.Body.String(), "error.param").Exists())
			require.False(t, gjson.Get(rec.Body.String(), "rate_limit").Exists())
			require.NotContains(t, rec.Body.String(), "secret-upstream.example")
			require.NotContains(t, rec.Body.String(), "sk-upstream-secret")
			require.NotContains(t, err.Error(), "secret-upstream.example")
		})
	}
}

// TestOpenAIGatewayService_APIKeyPassthrough_CompactErrorBeforeKeepaliveIsSingleJSON
// starts Compact keepalive with an interval beyond the fixture error arrival.
// It checks one valid JSON error with no SSE framing or sensitive upstream
// text.
func TestOpenAIGatewayService_APIKeyPassthrough_CompactErrorBeforeKeepaliveIsSingleJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(nil))
	MarkOpenAICompactClientStream(c)
	// Place the first heartbeat beyond this fixture exchange so upstream
	// rejection can select ordinary JSON before keepalive commits client stream
	// output.
	stop := StartOpenAICompactSSEKeepalive(c, time.Hour)
	// Prevent future heartbeat writes when this scenario exits, including
	// assertion failures; the callback waits for an active write but does not
	// join worker exit.
	defer stop()

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"secret-upstream.example invalid request"}}`)),
	}}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 125, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://secret-upstream.example"},
			Extra:       map[string]any{"openai_passthrough": true}, Status: billingcore.StatusActive, Schedulable: true,
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.2","input":"hello"}`))

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.True(t, gjson.Valid(rec.Body.String()))
	require.Equal(t, "upstream_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.NotContains(t, rec.Body.String(), "event:")
	require.NotContains(t, rec.Body.String(), ": keepalive")
	require.NotContains(t, rec.Body.String(), "secret-upstream.example")
}

// TestOpenAIGatewayService_APIKeyPassthrough_CompactErrorAfterKeepaliveIsFailedSSE
// waits for Compact keepalive before returning an API-key error. It checks the
// committed stream carries one response.failed event after keepalive removal,
// with generic error text.
func TestOpenAIGatewayService_APIKeyPassthrough_CompactErrorAfterKeepaliveIsFailedSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(nil))
	MarkOpenAICompactClientStream(c)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	// Prevent future heartbeat writes when this scenario exits, including
	// assertion failures; the callback waits for an active write but does not
	// join worker exit.
	defer stop()
	// Allow twenty short heartbeat intervals before forwarding the error. This
	// helper sleeps; the SSE framing assertions below establish the observed
	// committed stream.
	waitForKeepaliveBeats()

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"secret-upstream.example invalid request"}}`)),
	}}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 126, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://secret-upstream.example"},
			Extra:       map[string]any{"openai_passthrough": true}, Status: billingcore.StatusActive, Schedulable: true,
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.2","input":"hello"}`))

	require.Error(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Result().Header.Get("Content-Type"), "text/event-stream")
	events := httptestkit.ParseCompactSSE(t, stripKeepaliveComments(rec.Body.String()))
	require.Len(t, events, 1)
	require.Equal(t, "response.failed", events[0][0])
	require.Equal(t, "failed", gjson.Get(events[0][1], "response.status").String())
	require.Equal(t, "upstream_error", gjson.Get(events[0][1], "response.error.code").String())
	require.Equal(t, "Upstream request failed", gjson.Get(events[0][1], "response.error.message").String())
	require.NotContains(t, rec.Body.String(), "secret-upstream.example")
}

// TestOpenAIGatewayService_OpenAIPassthrough_429And529TriggerFailover returns
// rate-limit and overload statuses for OAuth and API-key passthrough. It
// checks typed failover, untouched client writer, Ops attribution and the
// corresponding repository reset or overload deadline.
func TestOpenAIGatewayService_OpenAIPassthrough_429And529TriggerFailover(t *testing.T) {
	originalBody := []byte(`{"model":"gpt-5.2","stream":false,"instructions":"local-test-instructions","input":[{"type":"text","text":"hi"}]}`)

	newProvider := func(providerType string) *gatewayprovider.ExecutionProvider {
		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 123,
				Name:           "acc",
				Platform:       capability.PlatformOpenAI,
				Type:           providerType,
				Concurrency:    1,
				Extra:          map[string]any{"openai_passthrough": true},
				Status:         billingcore.StatusActive,
				Schedulable:    true,
				RateMultiplier: new(float64(1)),
			},
		}
		switch providerType {
		case capability.ProviderTypeOAuth:
			provider.Record.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"}
		case capability.ProviderTypeAPIKey:
			provider.Record.Credentials = map[string]any{"api_key": "sk-test"}
		}
		return provider
	}

	testCases := []struct {
		name         string // case display name
		providerType string // fixture credential type
		statusCode   int    // upstream failure status
		body         string // upstream error fixture
		// assertRepo runs after Forward returns and compares serial repository calls
		// with the timestamp captured immediately before forwarding for this case.
		// post-forward health assertions
		assertRepo func(t *testing.T, repo *openAIPassthroughFailoverRepo, start time.Time)
	}{
		{
			name:         "oauth_429_rate_limit",
			providerType: capability.ProviderTypeOAuth,
			statusCode:   http.StatusTooManyRequests,
			body: func() string {
				// Place reset one week ahead to exceed the asserted one-day wait.
				resetAt := time.Now().Add(7 * 24 * time.Hour).Unix()
				return fmt.Sprintf(`{"error":{"message":"The usage limit has been reached","type":"usage_limit_reached","resets_at":%d}}`, resetAt)
			}(),
			assertRepo: func(t *testing.T, repo *openAIPassthroughFailoverRepo, _ time.Time) {
				require.Len(t, repo.rateLimitCalls, 1)
				require.Empty(t, repo.overloadCalls)
				require.True(t, time.Until(repo.rateLimitCalls[0]) > 24*time.Hour)
			},
		},
		{
			name:         "oauth_529_overload",
			providerType: capability.ProviderTypeOAuth,
			statusCode:   529,
			body:         `{"error":{"message":"server overloaded","type":"server_error"}}`,
			assertRepo: func(t *testing.T, repo *openAIPassthroughFailoverRepo, start time.Time) {
				require.Empty(t, repo.rateLimitCalls)
				require.Len(t, repo.overloadCalls, 1)
				// Allow five seconds between call-start and health timestamp preparation.
				require.WithinDuration(t, start.Add(10*time.Minute), repo.overloadCalls[0], 5*time.Second)
			},
		},
		{
			name:         "apikey_429_rate_limit",
			providerType: capability.ProviderTypeAPIKey,
			statusCode:   http.StatusTooManyRequests,
			body: func() string {
				// Place reset one week ahead to exceed the asserted one-day wait.
				resetAt := time.Now().Add(7 * 24 * time.Hour).Unix()
				return fmt.Sprintf(`{"error":{"message":"The usage limit has been reached","type":"usage_limit_reached","resets_at":%d}}`, resetAt)
			}(),
			assertRepo: func(t *testing.T, repo *openAIPassthroughFailoverRepo, _ time.Time) {
				require.Len(t, repo.rateLimitCalls, 1)
				require.Empty(t, repo.overloadCalls)
				require.True(t, time.Until(repo.rateLimitCalls[0]) > 24*time.Hour)
			},
		},
		{
			name:         "apikey_529_overload",
			providerType: capability.ProviderTypeAPIKey,
			statusCode:   529,
			body:         `{"error":{"message":"server overloaded","type":"server_error"}}`,
			assertRepo: func(t *testing.T, repo *openAIPassthroughFailoverRepo, start time.Time) {
				require.Empty(t, repo.rateLimitCalls)
				require.Len(t, repo.overloadCalls, 1)
				// Allow five seconds between call-start and health timestamp preparation.
				require.WithinDuration(t, start.Add(10*time.Minute), repo.overloadCalls[0], 5*time.Second)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

			resp := &http.Response{
				StatusCode: tc.statusCode,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"x-request-id": []string{"rid-failover"},
				},
				Body: io.NopCloser(strings.NewReader(tc.body)),
			}
			upstream := &auxiliaryHTTPRecorder{resp: resp}
			repo := &openAIPassthroughFailoverRepo{}
			rateSvc := newHTTPHealthFixture(repo,
				&responsesFixtureOptions{Health: providercore.HealthOptions{OverloadMinutes: 10}}, nil, providercore.HealthOptions{}, nil)

			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream, health: rateSvc})

			provider := newProvider(tc.providerType)
			start := time.Now()
			_, err := svc.Forward(context.Background(), c, provider, originalBody)
			require.Error(t, err)

			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, tc.statusCode, failoverErr.StatusCode)
			require.False(t, c.Writer.Written(), "429/529 passthrough 应返回 failover 错误给上层换号，而不是直接向客户端写响应")

			v, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			arr, ok := v.([]*ops.OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.NotEmpty(t, arr)
			require.True(t, arr[len(arr)-1].Passthrough)
			require.Equal(t, "failover", arr[len(arr)-1].Kind)
			require.Equal(t, tc.statusCode, arr[len(arr)-1].UpstreamStatusCode)

			tc.assertRepo(t, repo, start)
		})
	}
}

// TestOpenAIGatewayService_APIKeyPassthrough_Transient5xxTriggersFailover
// returns the listed transient server statuses through API-key passthrough. It
// checks typed failover, retained body/request ID, closed failed body,
// unchanged request bytes and untouched client output.
func TestOpenAIGatewayService_APIKeyPassthrough_Transient5xxTriggersFailover(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)

	for _, statusCode := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		520, 521, 522, 523, 524,
	} {
		t.Run(fmt.Sprintf("status_%d", statusCode), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

			upstreamBody := fmt.Sprintf(`{"error":{"message":"temporary upstream failure","status":%d}}`, statusCode)
			body := &gatewaytestkit.CloseTrackingReader{Reader: strings.NewReader(upstreamBody)}
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: statusCode,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"rid-api-key-5xx"},
				},
				Body: body,
			}}
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 124,
					Name:        "api-key-transient-5xx",
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{
						"api_key":  "sk-test",
						"base_url": "https://api.example.test",
					},
					Extra:       map[string]any{"openai_passthrough": true},
					Status:      billingcore.StatusActive,
					Schedulable: true,
				},
			}

			result, err := svc.Forward(context.Background(), c, provider, requestBody)

			require.Nil(t, result, "failed attempts must not report usage or success metadata")
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, statusCode, failoverErr.StatusCode)
			require.JSONEq(t, upstreamBody, string(failoverErr.ResponseBody))
			require.Equal(t, "rid-api-key-5xx", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
			require.False(t, c.Writer.Written(), "failover must happen before downstream output is committed")
			require.True(t, body.Closed, "the failed upstream response body must be closed")
			require.Equal(t, requestBody, upstream.lastBody, "the request body remains available for the outer provider retry")

			value, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events, ok := value.([]*ops.OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.NotEmpty(t, events)
			require.Equal(t, "failover", events[len(events)-1].Kind)
			require.Equal(t, provider.Record.ID, events[len(events)-1].ProviderID)
		})
	}
}

// TestOpenAIGatewayService_APIKeyPassthrough_ContextWindow502DoesNotFailover
// returns context-window evidence with HTTP 502 through API-key passthrough.
// It checks direct client error text/status, closed body and an error that is
// not provider failover.
func TestOpenAIGatewayService_APIKeyPassthrough_ContextWindow502DoesNotFailover(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	const upstreamBody = `{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"upstream_error"}}`
	body := &gatewaytestkit.CloseTrackingReader{Reader: strings.NewReader(upstreamBody)}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 127, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
			Extra:       map[string]any{"openai_passthrough": true}, Status: billingcore.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.2","input":"hello"}`))

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "context-window errors are deterministic request failures")
	require.True(t, c.Writer.Written())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "exceeds the context window")
	require.True(t, body.Closed)
}

// TestOpenAIGatewayService_APIKeyPassthrough_PoolModeConfigured5xxRetriesSameProvider
// configures pool-mode retry for HTTP 502 on API-key passthrough. It checks
// typed failover advertises retry on the same provider before any client
// write.
func TestOpenAIGatewayService_APIKeyPassthrough_PoolModeConfigured5xxRetriesSameProvider(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary upstream failure"}}`)),
	}}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 128, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{
				"api_key":                      "sk-test",
				"base_url":                     "https://api.example.test",
				"pool_mode":                    true,
				"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
			},
			Extra: map[string]any{"openai_passthrough": true}, Status: billingcore.StatusActive, Schedulable: true,
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, []byte(`{"model":"gpt-5.2","input":"hello"}`))

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameProvider)
	require.False(t, c.Writer.Written())
}

// TestOpenAIGatewayService_OpenAIPassthrough_CompactNetworkErrorsTriggerFailover
// compares Compact request establishment failure with response-body read
// failure. It checks establishment yields typed failover while the read
// failure retains io.ErrUnexpectedEOF, with neither case writing client
// output.
func TestOpenAIGatewayService_OpenAIPassthrough_CompactNetworkErrorsTriggerFailover(t *testing.T) {
	tests := []struct {
		name string // case display name
		// resp supplies established-response reading and err overrides response
		// selection with request failure in the capture transport. expectFailover
		// distinguishes those phases in the error assertions.
		resp           *http.Response // configured upstream response
		err            error          // injected establishment error
		expectFailover bool           // expected failover classification
	}{
		{
			name:           "request_error",
			err:            errors.New("stream disconnected before completion"),
			expectFailover: true,
		},
		{
			name: "read_error",
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-compact"}},
				Body:       passthroughErrReadCloser{err: io.ErrUnexpectedEOF},
			},
			expectFailover: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

			upstream := &auxiliaryHTTPRecorder{resp: tt.resp, err: tt.err}
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 123,
					Name:           "acc",
					Platform:       capability.PlatformOpenAI,
					Type:           capability.ProviderTypeOAuth,
					Concurrency:    1,
					Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
					Extra:          map[string]any{"openai_passthrough": true},
					Status:         billingcore.StatusActive,
					Schedulable:    true,
					RateMultiplier: new(float64(1)),
				},
			}
			body := []byte(`{"model":"gpt-5.5","instructions":"local-test-instructions","input":[{"type":"text","text":"compact me"}]}`)

			_, err := svc.Forward(context.Background(), c, provider, body)
			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			if tt.expectFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
				require.False(t, c.Writer.Written(), "compact 网络错误应交给外层 failover，而不是直接写回客户端")
			} else {
				require.False(t, errors.As(err, &failoverErr))
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				require.False(t, c.Writer.Written())
			}
		})
	}
}

// TestOpenAIGatewayService_OAuthPassthrough_NonCodexUAFallbackToCodexUA sends
// a curl user agent through OAuth passthrough. It checks store/stream
// normalization and selection of the Codex CLI user agent for the captured
// request.
func TestOpenAIGatewayService_OAuthPassthrough_NonCodexUAFallbackToCodexUA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	// curl supplies the non-Codex client identity for this request.
	c.Request.Header.Set("User-Agent", "curl/8.0")

	inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.NoError(t, err)
	require.Equal(t, false, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, true, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, openaicore.CodexCLIUserAgent, upstream.lastReq.Header.Get("User-Agent"))
}

// TestOpenAIGatewayService_OAuthPassthrough_BrowserUAUsesConfiguredCodexUA
// sends a browser user agent with a configured Codex replacement identity. It
// checks the captured user agent uses that setting and originator is paired as
// codex-tui.
func TestOpenAIGatewayService_OAuthPassthrough_BrowserUAUsesConfiguredCodexUA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")

	inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	settingSvc := newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{
		gateway.SettingKeyOpenAICodexUserAgent: "codex-tui/9.9.9 test-terminal",
	}}, &responsesFixtureOptions{})

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream, readers: settingSvc})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.NoError(t, err)
	require.Equal(t, "codex-tui/9.9.9 test-terminal", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", upstream.lastReq.Header.Get("originator"))
}

// TestOpenAIGatewayService_OAuthPassthrough_CodexTuiIdentityPreservedAndPaired
// sends an official TUI user agent with mismatched inbound originator. It
// checks the user agent remains and the captured originator becomes codex-tui.
func TestOpenAIGatewayService_OAuthPassthrough_CodexTuiIdentityPreservedAndPaired(t *testing.T) {
	const tuiUA = "codex-tui/0.140.2 (Mac OS X 14.0; arm64) iTerm (codex-tui; 0.140.2)"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", tuiUA)
	c.Request.Header.Set("originator", "codex_cli_rs")

	inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, tuiUA, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", upstream.lastReq.Header.Get("originator"))
}

// TestOpenAIGatewayService_OAuthPassthrough_TLSRouterOfficialUAIsPreservedAndPaired
// matches a custom client to a TLS router rule supplying an official TUI
// identity. It checks the routed user agent and derived codex-tui originator
// in captured input.
func TestOpenAIGatewayService_OAuthPassthrough_TLSRouterOfficialUAIsPreservedAndPaired(t *testing.T) {
	const routedUA = "codex-tui/9.9.0 (Linux; x86_64) xterm (codex-tui; 9.9.0)"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "custom-client/1.0")

	inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	routerSvc := newTLSFingerprintRouterTestService(&egress.TLSFingerprintRouter{
		ID:      77,
		Name:    "客户端路由",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Name:                    "custom",
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "custom-client/1.0",
			TLSFingerprintProfileID: 0,
			UpstreamUserAgent:       routedUA,
			UpstreamOriginator:      "custom-originator",
		}},
	})

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream, routers: routerSvc})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true, "tls_fingerprint_router_id": int64(77)},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.NoError(t, err)
	require.Equal(t, routedUA, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", upstream.lastReq.Header.Get("originator"))
}

// TestOpenAIGatewayService_CodexCLIOnly_RejectsNonCodexClient enables provider
// client restrictions for a curl request. It checks forwarding returns HTTP
// 403 with the official-client message before needing an upstream fixture.
func TestOpenAIGatewayService_CodexCLIOnly_RejectsNonCodexClient(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "curl/8.0")

	inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true, "codex_cli_only": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, inputBody)
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Codex official clients")
}

// TestOpenAIGatewayService_CodexCLIOnly_AllowOfficialClientFamilies enables
// provider client restrictions across CLI, editor, app and desktop-originator
// fixtures. It checks each case forwards successfully and reaches the
// transport recorder.
func TestOpenAIGatewayService_CodexCLIOnly_AllowOfficialClientFamilies(t *testing.T) {
	tests := []struct {
		name       string // case display name
		ua         string // inbound user agent
		originator string // inbound client originator
	}{
		{name: "codex_cli_rs", ua: "codex_cli_rs/0.99.0", originator: ""},
		{name: "codex_vscode", ua: "codex_vscode/1.0.0", originator: ""},
		{name: "codex_app", ua: "codex_app/2.1.0", originator: ""},
		{name: "originator_codex_chatgpt_desktop", ua: "curl/8.0", originator: "codex_chatgpt_desktop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", tt.ua)
			if tt.originator != "" {
				c.Request.Header.Set("originator", tt.originator)
			}

			inputBody := []byte(`{"model":"gpt-5.2","stream":false,"store":true,"input":[{"type":"text","text":"hi"}]}`)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
				Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
			}
			upstream := &auxiliaryHTTPRecorder{resp: resp}

			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 123,
					Name:           "acc",
					Platform:       capability.PlatformOpenAI,
					Type:           capability.ProviderTypeOAuth,
					Concurrency:    1,
					Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
					Extra:          map[string]any{"openai_passthrough": true, "codex_cli_only": true},
					Status:         billingcore.StatusActive,
					Schedulable:    true,
					RateMultiplier: new(float64(1)),
				},
			}

			_, err := svc.Forward(context.Background(), c, provider, inputBody)
			require.NoError(t, err)
			require.NotNil(t, upstream.lastReq)
		})
	}
}

// TestOpenAIGatewayService_OAuthPassthrough_StreamingSetsTTFT streams
// one text delta through OAuth passthrough with the fast tier alias. It checks
// absent TTFT from this uninstrumented fixture and normalized priority result attribution.
func TestOpenAIGatewayService_OAuthPassthrough_StreamingSetsTTFT(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"service_tier":"fast","input":[{"type":"text","text":"hi"}]}`)

	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"h"}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	start := time.Now()
	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	// Timing measurements are nonnegative.
	require.GreaterOrEqual(t, time.Since(start), time.Duration(0))
	require.Nil(t, result.Timings.TTFTMs())
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
}

// TestOpenAIGatewayService_OAuthPassthrough_StreamClientDisconnectStillCollectsUsage
// makes later client writes fail during OAuth passthrough output. It checks
// forwarding still returns terminal input/output/cache usage while TTFT remains
// absent from this uninstrumented fixture.
func TestOpenAIGatewayService_OAuthPassthrough_StreamClientDisconnectStillCollectsUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	// Permit one delegated client Write before later Write calls fail, allowing
	// the scanner to encounter terminal usage after downstream delivery becomes
	// unavailable.
	c.Writer = &httptestkit.FailingWriter{ResponseWriter: c.Writer, FailAfter: 1}

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"input":[{"type":"text","text":"hi"}]}`)

	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"h"}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 123,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Nil(t, result.Timings.TTFTMs())
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
}

// TestOpenAIGatewayService_APIKeyPassthrough_PreservesBodyAndUsesResponsesEndpoint
// passes an API-key body with flex tier and output limit through Responses. It
// checks identical captured bytes, endpoint/authentication/header selection
// and returned requested-tier attribution.
func TestOpenAIGatewayService_APIKeyPassthrough_PreservesBodyAndUsesResponsesEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "curl/8.0")
	c.Request.Header.Set("X-Test", "keep")
	c.Request.Header.Set("x-codex-beta-features", "remote_compaction_v2")

	originalBody := []byte(`{"model":"gpt-5.2","stream":false,"service_tier":"flex","max_output_tokens":128,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader(`{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`)),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}

	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 456,
			Name:           "apikey-acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeAPIKey,
			Concurrency:    1,
			Credentials:    map[string]any{"api_key": "sk-api-key", "base_url": "https://api.openai.com"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "flex", *result.ServiceTier)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, originalBody, upstream.lastBody)
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
	require.Equal(t, "https://api.openai.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "curl/8.0", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "remote_compaction_v2", upstream.lastReq.Header.Get("x-codex-beta-features"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Test"))
}

// TestOpenAIGatewayService_OAuthPassthrough_WarnOnTimeoutHeadersForStream
// includes a client timeout header in streaming OAuth passthrough input. It
// checks the structured warning records the timeout header and value after
// successful forwarding.
func TestOpenAIGatewayService_OAuthPassthrough_WarnOnTimeoutHeadersForStream(t *testing.T) {
	// Capture shared structured logs for this synchronous exchange, then restore
	// the hook on exit so later scenarios use their normal logging sink.
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("x-stainless-timeout", "10000")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-timeout"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 321,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.True(t, logSink.ContainsMessage("detected timeout headers and will filter them as configured to reduce early stream termination"))
	require.True(t, logSink.ContainsFieldValue("timeout_headers", "x-stainless-timeout=10000"))
}

// TestOpenAIGatewayService_OAuthPassthrough_InfoWhenStreamEndsWithoutDone ends
// upstream SSE after text with no terminal event or done marker. It checks
// incomplete-usage error and an informational truncation log carrying the
// upstream request ID.
func TestOpenAIGatewayService_OAuthPassthrough_InfoWhenStreamEndsWithoutDone(t *testing.T) {
	// Capture shared structured logs for this synchronous exchange, then restore
	// the hook on exit so later scenarios use their normal logging sink.
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"input":[{"type":"text","text":"hi"}]}`)
	// End the fixture after visible text with neither a terminal event nor a done
	// marker, separating emitted text from the missing usage-completion evidence.
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-truncate"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"h\"}\n\n")),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 654,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.EqualError(t, err, "stream usage incomplete: missing terminal event")
	require.True(t, logSink.ContainsMessage("upstream stream ended without [DONE], possible interrupted stream"))
	require.True(t, logSink.ContainsMessageAtLevel("upstream stream ended without [DONE], possible interrupted stream", "info"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_id", "rid-truncate"))
}

// TestOpenAIGatewayService_OAuthPassthrough_DefaultFiltersTimeoutHeaders sends
// timeout and arbitrary headers through default OAuth passthrough options. It
// checks both tested headers are absent from the captured upstream request.
func TestOpenAIGatewayService_OAuthPassthrough_DefaultFiltersTimeoutHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("x-stainless-timeout", "120000")
	c.Request.Header.Set("X-Test", "keep")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-filter-default"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 111,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Empty(t, upstream.lastReq.Header.Get("x-stainless-timeout"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Test"))
}

// TestOpenAIGatewayService_OAuthPassthrough_AllowTimeoutHeadersWhenConfigured
// enables timeout-header forwarding beside an arbitrary inbound header. It
// checks the timeout value reaches the upstream while the arbitrary header
// remains absent.
func TestOpenAIGatewayService_OAuthPassthrough_AllowTimeoutHeadersWhenConfigured(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("x-stainless-timeout", "120000")
	c.Request.Header.Set("X-Test", "keep")

	originalBody := []byte(`{"model":"gpt-5.2","stream":true,"input":[{"type":"text","text":"hi"}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-filter-allow"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}
	upstream := &auxiliaryHTTPRecorder{resp: resp}
	svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false, AllowTimeoutHeaders: true}}, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 222,
			Name:           "acc",
			Platform:       capability.PlatformOpenAI,
			Type:           capability.ProviderTypeOAuth,
			Concurrency:    1,
			Credentials:    map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
			Extra:          map[string]any{"openai_passthrough": true},
			Status:         billingcore.StatusActive,
			Schedulable:    true,
			RateMultiplier: new(float64(1)),
		},
	}

	_, err := svc.Forward(context.Background(), c, provider, originalBody)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "120000", upstream.lastReq.Header.Get("x-stainless-timeout"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Test"))
}

// TestOpenAIGatewayServiceForwardOAuthDerivesEffortFromSuffixModel sends an
// OAuth model spelling that includes an effort suffix. It checks captured
// model spelling remains intact and returned requested effort is nil.
func TestOpenAIGatewayServiceForwardOAuthDerivesEffortFromSuffixModel(t *testing.T) {
	upstream := &auxiliaryHTTPRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &responsesFixtureOptions{}
	cfg.Request.URLPolicy.Enabled = false
	svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 11,
			Name:        "openai-oauth-suffix",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-acc",
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.3-codex-xhigh","instructions":"suffix-test","input":"hello","stream":false}`)
	result, err := svc.Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.3-codex-xhigh", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Nil(t, result.ReasoningEffort)
}

// TestOpenAIRequestBodyLimitFailover_HTTP413SwitchesProvidersBeforeWrite
// returns provider body-limit evidence for managed and API-key passthrough
// attempts with pool retries configured. It checks next-provider retry
// metadata, generic client message, closed body and no committed client
// output.
func TestOpenAIRequestBodyLimitFailover_HTTP413SwitchesProvidersBeforeWrite(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)

	for _, passthrough := range []bool{false, true} {
		name := "native_responses"
		if passthrough {
			name = "api_key_passthrough"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

			const upstreamBody = `{"error":{"message":"request body exceeds this provider's 16MB proxy limit; secret=must-not-leak","type":"invalid_request_error"}}`
			body := &gatewaytestkit.CloseTrackingReader{Reader: strings.NewReader(upstreamBody)}
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusRequestEntityTooLarge,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"rid-body-limit"},
				},
				Body: body,
			}}
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 161,
					Name:        name,
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{
						"api_key":   "sk-test",
						"base_url":  "https://api.example.test",
						"pool_mode": true,
						"pool_mode_retry_status_codes": []any{
							float64(http.StatusRequestEntityTooLarge),
						},
					},
					Extra: map[string]any{
						"openai_passthrough": passthrough,
					},
					Status:      billingcore.StatusActive,
					Schedulable: true,
				},
			}

			result, err := svc.Forward(context.Background(), c, provider, requestBody)

			require.Nil(t, result)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusRequestEntityTooLarge, failoverErr.StatusCode)
			require.Equal(t, forwardcore.GatewayFailureScopeProvider, failoverErr.Scope)
			require.Equal(t, forwardcore.GatewayFailureReason("openai_request_body_too_large"), failoverErr.Reason)
			require.Equal(t, forwardcore.NextProviderRetry, failoverErr.NextProviderAction)
			require.Equal(t, http.StatusRequestEntityTooLarge, failoverErr.ClientStatusCode)
			require.Equal(t, "Request payload is too large", failoverErr.ClientMessage)
			require.False(t, failoverErr.RetryableOnSameProvider, "a body limit requires another provider, not another attempt on the same provider")
			require.False(t, c.Writer.Written(), "provider failover must happen before downstream output is committed")
			require.Empty(t, rec.Body.String())
			require.True(t, body.Closed)
			if passthrough {
				require.Equal(t, requestBody, upstream.lastBody)
			} else {
				require.Equal(t, "gpt-5.2", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "input").String())
			}
		})
	}
}

// TestOpenAIRequestBodyLimitFailover_ContextWindow413DoesNotSwitchProviders
// returns context-window evidence inside HTTP 413 in managed and passthrough
// attempts. It checks direct client error text and closed body with no
// provider-failover error.
func TestOpenAIRequestBodyLimitFailover_ContextWindow413DoesNotSwitchProviders(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)

	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough_%t", passthrough), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

			const upstreamBody = `{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"invalid_request_error"}}`
			body := &gatewaytestkit.CloseTrackingReader{Reader: strings.NewReader(upstreamBody)}
			svc := newResponsesFixture(responsesFixtureInputs{options: &responsesFixtureOptions{Request: OpenAIRequestOptions{ForceCLI: false}}, transport: &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusRequestEntityTooLarge,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       body,
			}}})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 162, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
					Extra: map[string]any{
						"openai_passthrough": passthrough,
					},
					Status: billingcore.StatusActive, Schedulable: true,
				},
			}

			result, err := svc.Forward(context.Background(), c, provider, requestBody)

			require.Nil(t, result)
			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr), "context-window failures are deterministic request errors")
			require.True(t, c.Writer.Written())
			require.Contains(t, rec.Body.String(), "exceeds the context window")
			require.True(t, body.Closed)
		})
	}
}

// TestOpenAIGatewayService_OAuthDropsOrphanAfterDroppingPreviousResponse sends
// an orphan tool result beside an OAuth continuation ID. It checks one
// outgoing attempt removes both the previous-response field and the orphan
// input.
func TestOpenAIGatewayService_OAuthDropsOrphanAfterDroppingPreviousResponse(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"previous_response_id":"resp_missing","input":[{"type":"function_call_output","call_id":"call_missing","output":"keep this result"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIOAuthNamespaceTestProvider(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.False(t, gjson.GetBytes(upstream.bodies[0], "previous_response_id").Exists())
	require.Empty(t, gjson.GetBytes(upstream.bodies[0], "input").Array())
}

// TestOpenAIGatewayService_PreservesOversizedToolOutputForUpstream builds tool
// output above the large-text fixture length with a final multibyte character.
// It checks the one captured upstream attempt retains the entire output
// string.
func TestOpenAIGatewayService_PreservesOversizedToolOutputForUpstream(t *testing.T) {
	// Append a multibyte character after the ten-million-character ASCII prefix
	// so exact output comparison exposes clipping or damage to the fixture tail.
	oversized := strings.Repeat("x", openAIResponsesInputTextMaxChars) + "中"
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"` + oversized + `"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestProvider(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, oversized, gjson.GetBytes(upstream.bodies[0], "input.1.output").String())
}

// TestOpenAIGatewayServiceForward_NormalizesResponsesLiteToolsForOAuth sends
// mixed tool declarations and reasoning controls through managed and
// passthrough OAuth lite paths. It checks namespace relocation, disabled
// parallel calls, expanded reasoning context and parameter-specific rejection
// of malformed field shapes.
func TestOpenAIGatewayServiceForward_NormalizesResponsesLiteToolsForOAuth(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
			c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_lite\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
						"data: [DONE]\n\n",
				)),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 501, Name: "responses-lite", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
					Concurrency: 1, Status: billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
					Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-provider"},
					Extra:       map[string]any{"openai_passthrough": passthrough},
				},
			}
			body := []byte(`{
				"model":"gpt-5.6-terra","stream":true,"instructions":"test",
				"parallel_tool_calls":true,
				"reasoning":{"effort":"high","context":"current_turn"},
				"tools":[
					{"type":"function","name":"shell","parameters":{"type":"object"}},
					{"type":"custom","name":"exec"},
					{"type":"tool_search"},
					{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
				],
				"input":[{"type":"message","role":"user","content":"hello"}],
				"tool_choice":{"type":"namespace","name":"collaboration"}
			}`)

			result, err := svc.Forward(context.Background(), c, provider, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "true", upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
			require.True(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Bool())
			require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.Equal(t, "all_turns", gjson.GetBytes(upstream.lastBody, "reasoning.context").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="namespace")`).Exists())
			require.Equal(t, "shell", gjson.GetBytes(upstream.lastBody, `tools.#(type=="function").name`).String())
			require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, `tools.#(type=="custom").name`).String())
			require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="tool_search")`).Exists())
			require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools").tools.0.name`).String())
			require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, "tool_choice.type").String())
			require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, "tool_choice.name").String())

			badRec := httptest.NewRecorder()
			badCtx, _ := gin.CreateTestContext(badRec)
			badCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			badCtx.Request.Header.Set(media.ResponsesLiteHeader, "true")
			badUpstream := &auxiliaryHTTPRecorder{}
			svc.Requests.Transport = badUpstream

			result, err = svc.Forward(context.Background(), badCtx, provider, []byte(`{"model":"gpt-5.6-terra","tools":[{"type":"function","name":"shell"}],"parallel_tool_calls":"false"}`))

			require.ErrorContains(t, err, "parallel_tool_calls to be a boolean")
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, badRec.Code)
			require.Equal(t, "invalid_request_error", gjson.Get(badRec.Body.String(), "error.type").String())
			require.Equal(t, "parallel_tool_calls", gjson.Get(badRec.Body.String(), "error.param").String())
			require.Contains(t, gjson.Get(badRec.Body.String(), "error.message").String(), "parallel_tool_calls to be a boolean")
			require.Nil(t, badUpstream.lastReq)

			for _, malformed := range []struct {
				body      string // malformed lite input
				wantParam string // expected rejected parameter
			}{
				{body: `{"model":"gpt-5.6-terra","tools":{}}`, wantParam: "tools"},
				{body: `{"model":"gpt-5.6-terra","reasoning":[]}`, wantParam: "reasoning"},
			} {
				rec := httptest.NewRecorder()
				requestCtx, _ := gin.CreateTestContext(rec)
				requestCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
				requestCtx.Request.Header.Set(media.ResponsesLiteHeader, "true")

				result, err = svc.Forward(context.Background(), requestCtx, provider, []byte(malformed.body))

				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, malformed.wantParam, gjson.Get(rec.Body.String(), "error.param").String())
			}
		})
	}
}

// TestOpenAIGatewayServiceForward_DisablesResponsesLiteParallelToolCallsForAPIKey
// sends parallel calls and current-turn reasoning through API-key lite paths
// in both forwarding modes. It checks parallel calls become false while
// reasoning context remains current_turn.
func TestOpenAIGatewayServiceForward_DisablesResponsesLiteParallelToolCallsForAPIKey(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_lite_apikey\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
						"data: [DONE]\n\n",
				)),
			}}
			cfg := &responsesFixtureOptions{}
			cfg.Request.URLPolicy.Enabled = false
			svc := newResponsesFixture(responsesFixtureInputs{options: cfg, transport: upstream})
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 502, Name: "responses-lite-apikey", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
					Concurrency: 1, Status: billingcore.StatusActive, Schedulable: true, RateMultiplier: new(float64(1)),
					Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
					Extra: map[string]any{
						"openai_passthrough": passthrough,
					},
				},
			}
			body := []byte(`{
				"model":"gpt-5.6-terra","stream":true,
				"parallel_tool_calls":true,
				"reasoning":{"context":"current_turn"},
				"input":"hello"
			}`)

			result, err := svc.Forward(context.Background(), c, provider, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "true", upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
			require.True(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Bool())
			require.Equal(t, "current_turn", gjson.GetBytes(upstream.lastBody, "reasoning.context").String())
		})
	}
}

// TestOpenAIGatewayService_OAuthPreservesCodexNamespaceTools forwards declared
// namespace tools and historical calls through ordinary OAuth execution. It
// checks retained namespace identity, removal of stale message namespace and
// an empty restoration map.
func TestOpenAIGatewayService_OAuthPreservesCodexNamespaceTools(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIOAuthNamespaceTestProvider(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	forwarded := upstream.bodies[0]
	namespaceTool := gjson.GetBytes(forwarded, `tools.#(type=="namespace")`)
	require.True(t, namespaceTool.Exists())
	require.Equal(t, "collaboration", namespaceTool.Get("name").String())
	require.Equal(t, "spawn_agent", namespaceTool.Get("tools.0.name").String())
	require.NotContains(t, string(forwarded), "collaboration__spawn_agent")
	require.Equal(t, "collaboration", gjson.GetBytes(forwarded, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(forwarded, "input.1.namespace").Exists())
	require.Empty(t, OpenAIResponsesNamespaceNames(c))
}

// TestOpenAIGatewayService_APIKeyPreservesDeclaredNamespaceToolCalls forwards
// namespace declarations and historical function calls through an API-key
// provider. It checks declaration/call namespaces remain while stale namespace
// on the ordinary message disappears.
func TestOpenAIGatewayService_APIKeyPreservesDeclaredNamespaceToolCalls(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIRejectedFieldTestProvider(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	forwarded := upstream.bodies[0]

	require.True(t, gjson.GetBytes(forwarded, `tools.#(type=="namespace")`).Exists())
	require.Equal(t, "collaboration", gjson.GetBytes(forwarded, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(forwarded, "input.1.namespace").Exists())
}

// TestOpenAIGatewayService_OAuthCompactKeepsFlattening sends declared
// namespace history through the OAuth Compact path. It checks the historical
// name is flattened and input namespace and namespace declaration disappear.
func TestOpenAIGatewayService_OAuthCompactKeepsFlattening(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	c.Request.URL.Path = "/v1/responses/compact"

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIOAuthNamespaceTestProvider(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	forwarded := upstream.bodies[0]
	require.False(t, gjson.GetBytes(forwarded, "input.0.namespace").Exists())
	require.False(t, gjson.GetBytes(forwarded, `tools.#(type=="namespace")`).Exists())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(forwarded, "input.0.name").String())
}

// TestOpenAIGatewayService_OAuthFlattenFlagRestoresLegacyBehavior enables
// namespace compatibility on an ordinary OAuth request. It checks flattened
// declarations/history and the Gin restoration map linking the flattened name
// to namespace and function identity.
func TestOpenAIGatewayService_OAuthFlattenFlagRestoresLegacyBehavior(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	provider := newOpenAIOAuthNamespaceTestProvider()
	provider.Record.Extra = map[string]any{"openai_responses_flatten_namespaces": true}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), c, provider, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	forwarded := upstream.bodies[0]
	require.False(t, gjson.GetBytes(forwarded, `tools.#(type=="namespace")`).Exists())
	require.True(t, gjson.GetBytes(forwarded, `tools.#(name=="collaboration__spawn_agent")`).Exists())
	require.False(t, gjson.GetBytes(forwarded, "input.0.namespace").Exists())
	require.Equal(t, bridge.ResponsesNamespaceName{
		Namespace: "collaboration",
		Name:      "spawn_agent",
	}, OpenAIResponsesNamespaceNames(c)["collaboration__spawn_agent"])
}

// TestOpenAIGatewayService_ForwardClearsStaleNamespaceNames preloads a stale
// restoration mapping before an ordinary OAuth forward. It checks successful
// forwarding clears that Gin mapping for this attempt.
func TestOpenAIGatewayService_ForwardClearsStaleNamespaceNames(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	SetOpenAIResponsesNamespaceNames(c, map[string]bridge.ResponsesNamespaceName{
		"stale__tool": {Namespace: "stale", Name: "tool"},
	})

	_, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIOAuthNamespaceTestProvider(), body,
	)

	require.NoError(t, err)
	require.Empty(t, OpenAIResponsesNamespaceNames(c))
}

// TestOpenAIGatewayService_APIKeyRetriesExplicitlyRejectedTopLevelTruncation
// receives an exact unsupported-truncation rejection before success. It checks
// two attempts, removal of the top-level field on retry and retained input
// text.
func TestOpenAIGatewayService_APIKeyRetriesExplicitlyRejectedTopLevelTruncation(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"truncation":"auto","input":"keep"}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: 'truncation'.","param":"truncation"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestProvider(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "auto", gjson.GetBytes(upstream.bodies[0], "truncation").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "truncation").Exists())
	require.Equal(t, "keep", gjson.GetBytes(upstream.bodies[1], "input").String())
}

// TestOpenAIGatewayService_OAuthRetriesExactRejectedStatus receives an exact
// indexed input-status rejection before a successful OAuth stream. It checks
// two attempts remove the identified status only on retry.
func TestOpenAIGatewayService_OAuthRetriesExactRejectedStatus(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":true,"instructions":"test","input":[{"type":"message","role":"user","status":"completed","content":"hello"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].status'.","param":"input[0].status"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"),
	}}
	upstream.responses[1].Header.Set("Content-Type", "text/event-stream")

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIOAuthNamespaceTestProvider(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "completed", gjson.GetBytes(upstream.bodies[0], "input.0.status").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.status").Exists())
}

// TestOpenAIGatewayService_APIKeyRetriesExactRejectedNullMessageContent
// receives an invalid-type rejection for null assistant content before
// success. It checks retry content becomes a string and the following user
// text remains.
func TestOpenAIGatewayService_APIKeyRetriesExactRejectedNullMessageContent(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"message","role":"assistant","content":null},{"type":"message","role":"user","content":"continue"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"invalid_type","message":"Invalid type for 'input[0].content': expected one of a string or a list of input items, but got null instead.","param":"input[0].content"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestProvider(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, gjson.Null, gjson.GetBytes(upstream.bodies[0], "input.0.content").Type)
	require.Equal(t, gjson.String, gjson.GetBytes(upstream.bodies[1], "input.0.content").Type)
	require.Equal(t, "continue", gjson.GetBytes(upstream.bodies[1], "input.1.content").String())
}

// TestOpenAIGatewayService_APIKeyKeepsIndexedNamespacesBeforeFirstForward
// sends function and custom-tool identities without current declarations. It
// checks the first API-key attempt retains namespaces belonging to historical
// tools or an upstream conversation.
func TestOpenAIGatewayService_APIKeyKeepsIndexedNamespacesBeforeFirstForward(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"function_call","name":"first","namespace":"remove-first","arguments":"{}"},{"type":"custom_tool_call","name":"second","namespace":"remove-second","input":"{}"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestProvider(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, "remove-first", gjson.GetBytes(upstream.bodies[0], "input.0.namespace").String())
	require.Equal(t, "remove-second", gjson.GetBytes(upstream.bodies[0], "input.1.namespace").String())
}

// TestOpenAIGatewayService_APIKeyPreservesHistoricalNamespaces exercises
// declaration placement and omitted tools on native Responses requests. Every
// first upstream attempt retains the caller's tool identity and correlation.
func TestOpenAIGatewayService_APIKeyPreservesHistoricalNamespaces(t *testing.T) {
	declarations := map[string]string{
		"omitted":    ``,
		"static":     `,"tools":[{"type":"namespace","name":"browser","tools":[{"type":"function","name":"js"}]}]`,
		"discovered": `,"tools":[{"type":"tool_search"}]`,
	}
	for name, declaration := range declarations {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","stream":false` + declaration + `,"input":[` +
				`{"type":"tool_search_output","call_id":"search_1","tools":[{"type":"namespace","name":"browser","tools":[{"type":"function","name":"js"}]}]},` +
				`{"type":"function_call","call_id":"call_1","name":"js","namespace":"browser","arguments":"{}"},` +
				`{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
			upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
			}}
			result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
				context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestProvider(), body,
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, "browser", gjson.GetBytes(upstream.bodies[0], "input.1.namespace").String())
			require.Equal(t, "call_1", gjson.GetBytes(upstream.bodies[0], "input.1.call_id").String())
		})
	}
}

// TestOpenAIGatewayService_APIKeyPreservesAllCallNamespaceKinds sends the
// recognized call shapes beside a message carrying a stale namespace. Native
// forwarding keeps every call identity and cleans only the message field.
func TestOpenAIGatewayService_APIKeyPreservesAllCallNamespaceKinds(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"input":[{"type":"function_call","name":"js","namespace":"browser","call_id":"call_1","arguments":"{}"},{"type":"custom_tool_call","name":"exec","namespace":"terminal","call_id":"call_2","input":"pwd"},{"type":"tool_call","name":"inspect","namespace":"tools"},{"type":"mcp_tool_call","name":"list","namespace":"mcp"},{"type":"message","role":"user","namespace":"stale","content":"continue"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	_, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestProvider(), body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 1)
	for index, namespace := range []string{"browser", "terminal", "tools", "mcp"} {
		require.Equal(t, namespace, gjson.GetBytes(upstream.bodies[0], fmt.Sprintf("input.%d.namespace", index)).String())
	}
	require.False(t, gjson.GetBytes(upstream.bodies[0], "input.4.namespace").Exists())
}

// TestOpenAIGatewayService_APIKeyPassthroughRestoresDiscoveredFunction carries
// a plain discovered function through request lowering and streamed response
// restoration. Added, done, and terminal items keep its client namespace and
// the same call ID while the upstream declaration and history stay bare.
func TestOpenAIGatewayService_APIKeyPassthroughRestoresDiscoveredFunction(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"tools":[{"type":"tool_search","execution":"client"}],"input":[{"type":"tool_search_call","call_id":"search_1","arguments":{"query":"js"},"execution":"client"},{"type":"tool_search_output","call_id":"search_1","tools":[{"type":"function","name":"js","parameters":{"type":"object"}}]},{"type":"function_call","name":"js","namespace":"js","call_id":"call_1","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	sse := "data: {\"type\":\"response.output_item.added\",\"sequence_number\":0,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_2\",\"name\":\"js\",\"arguments\":\"{}\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_2\",\"name\":\"js\",\"arguments\":\"{}\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_2\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_2\",\"name\":\"js\",\"arguments\":\"{}\",\"status\":\"completed\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}}
	provider := newOpenAIRejectedFieldTestProvider()
	provider.Record.Extra["openai_passthrough"] = true
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "curl/8.0")
	_, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), c, provider, body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, "js", gjson.GetBytes(upstream.bodies[0], "tools.1.name").String())
	require.Equal(t, "js", gjson.GetBytes(upstream.bodies[0], "input.2.name").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "input.2.namespace").Exists())
	restored := 0
	for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		item := gjson.Get(payload, "item")
		if gjson.Get(payload, "type").String() == "response.completed" {
			item = gjson.Get(payload, "response.output.0")
		}
		if !item.Exists() {
			continue
		}
		require.Equal(t, "js", item.Get("name").String())
		require.Equal(t, "js", item.Get("namespace").String())
		require.Equal(t, "call_2", item.Get("call_id").String())
		restored++
	}
	require.Equal(t, 3, restored)
}

// TestOpenAIGatewayServiceProactivelyStripsCrossProviderReasoningContent
// includes reasoning content and summary after ordinary text/tool history. It
// checks the first attempt removes reasoning content while retaining the
// reasoning item and summary text.
func TestOpenAIGatewayServiceProactivelyStripsCrossProviderReasoningContent(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"store":true,"input":[` +
		`{"type":"message","role":"user","content":"one"},` +
		`{"type":"message","role":"assistant","content":"two"},` +
		`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"ok"},` +
		`{"type":"message","role":"user","content":"five"},` +
		`{"type":"reasoning","summary":[{"type":"summary_text","text":"keep"}],"content":[{"type":"reasoning_text","text":"remove"}]}` +
		`]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestProvider(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1, "reasoning content should be normalized before the first upstream request")
	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[0], "input.5.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "input.5.content").Exists())
	require.Equal(t, "keep", gjson.GetBytes(upstream.bodies[0], "input.5.summary.0.text").String())
}

// TestOpenAIGatewayService_OpenAIHTTPStripsInputNamespacesBeforeFirstForward
// varies OAuth/API-key providers and ordinary/Compact paths with a stale
// message namespace. It checks one attempt removes that top-level item field
// and retains nested content namespace.
func TestOpenAIGatewayService_OpenAIHTTPStripsInputNamespacesBeforeFirstForward(t *testing.T) {
	providers := []struct {
		name     string                             // provider case name
		provider *gatewayprovider.ExecutionProvider // retained fixture provider
	}{
		{name: "oauth", provider: newOpenAIOAuthNamespaceTestProvider()},
		{name: "apikey", provider: newOpenAIRejectedFieldTestProvider()},
	}
	for _, tt := range providers {
		for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
			t.Run(tt.name+path, func(t *testing.T) {
				body := []byte(`{"model":"gpt-5.5","stream":false,"instructions":"test","input":[{"type":"message","role":"user","namespace":"remove","content":[{"type":"input_text","text":"hello","namespace":"nested-keep"}]}]}`)
				upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_namespace_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
				}}
				c := newOpenAIRejectedFieldTestContext(body)
				c.Request.URL.Path = path

				result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
					context.Background(),
					c,
					tt.provider,
					body,
				)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 1, "namespace must be removed before the first upstream request")
				require.False(t, gjson.GetBytes(upstream.bodies[0], "input.0.namespace").Exists())
				require.Equal(t, "nested-keep", gjson.GetBytes(upstream.bodies[0], "input.0.content.0.namespace").String())
			})
		}
	}
}

// TestOpenAIGatewayService_RetriesExplicitMaxOutputTokensRejection rejects the
// top-level output limit beside a nested field with the same name. It checks
// retry removes the rejected limit and retains the nested content value.
func TestOpenAIGatewayService_RetriesExplicitMaxOutputTokensRejection(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":4096,"input":[{"type":"message","role":"user","content":{"max_output_tokens":"keep"}}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens","type":"invalid_request_error"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestProvider(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, int64(4096), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
	require.Equal(t, "keep", gjson.GetBytes(upstream.bodies[1], "input.0.content.max_output_tokens").String())
}

// TestOpenAIGatewayService_KeepsNamespacesDuringRejectedFieldRetry
// combines historical tool namespaces with an explicitly rejected output
// limit. Both attempts retain call identities while the retry removes only
// the rejected output parameter.
func TestOpenAIGatewayService_KeepsNamespacesDuringRejectedFieldRetry(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":2048,"input":[{"type":"function_call","name":"first","namespace":"remove-first","arguments":"{}"},{"type":"custom_tool_call","name":"second","namespace":"remove-second","input":"{}"}]}`)
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestProvider(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	for _, forwardedBody := range upstream.bodies {
		require.Equal(t, "remove-first", gjson.GetBytes(forwardedBody, "input.0.namespace").String())
		require.Equal(t, "remove-second", gjson.GetBytes(forwardedBody, "input.1.namespace").String())
	}
	require.Equal(t, int64(2048), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
}

// newOpenAIRejectedFieldTestService builds a Responses executor over the
// supplied capture transport using fixture defaults. The recorder and its
// scripted response bodies remain shared, so callers coordinate their mutation
// and consumption with forwarding.
func newOpenAIRejectedFieldTestService(upstream *auxiliaryHTTPRecorder) *OpenAIResponsesExecutor {
	return newResponsesFixture(responsesFixtureInputs{transport: upstream})
}

// newOpenAIRejectedFieldTestContext creates a Gin recorder context for a JSON
// Responses request with curl identity. The request reader retains the
// supplied byte slice; callers keep those bytes stable while forwarding reads
// them.
func newOpenAIRejectedFieldTestContext(body []byte) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "curl/8.0")
	return c
}

// newOpenAIRejectedFieldTestProvider creates an active API-key fixture
// provider for native Responses field normalization. It selects
// client-protocol preservation and the synthetic HTTPS compatibility host;
// each call returns fresh credentials and extras.
func newOpenAIRejectedFieldTestProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5107,
			Name:        "responses-compatible",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://compat.example",
			},
			Extra: map[string]any{
				providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModePreserveClientProtocol),
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}
}

// newOpenAIOAuthNamespaceTestProvider creates an active OAuth fixture provider
// with the namespace test identity. It supplies access and account credentials
// and leaves namespace compatibility extras unset for callers to select per
// scenario.
func newOpenAIOAuthNamespaceTestProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5108,
			Name:        "openai-oauth-namespace",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-provider",
			},
			Status:      billingcore.StatusActive,
			Schedulable: true,
		},
	}
}

// newOpenAIRejectedFieldTestResponse builds a JSON upstream response using the
// supplied status and body text. The body is a string reader wrapped for
// closure; callers can replace headers before the scripted transport consumes
// it.
func newOpenAIRejectedFieldTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// Do sends a built upstream request to the fixture HTTP server while retaining
// its path and query. It requires initialized client and endpoint references
// and a valid request.
//
// The request clone keeps the supplied context and shares the body reader, so
// the HTTP client consumes that same body. A separate URL value receives the
// loopback scheme and host, and the request Host is updated to match. The
// supplied upstream request URL remains available for inspection; proxy and
// provider-slot parameters are ignored by this transport. Callers close any
// returned response body after reading it.
func (p mappingHTTPTransport) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	clone := request.Clone(request.Context())
	// Redirect the copied URL to loopback while retaining path and query, so
	// consuming its shared body leaves the supplied upstream URL inspectable.
	target := *clone.URL
	target.Scheme = p.endpoint.Scheme
	target.Host = p.endpoint.Host
	clone.URL = &target
	clone.Host = p.endpoint.Host
	return p.client.Do(clone)
}

// DoWithTLS delegates loopback request redirection to Do and ignores the
// supplied TLS profile. The fixture client performs the exchange under the
// request context, sharing the request body reader as described by Do.
func (p mappingHTTPTransport) DoWithTLS(request *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return p.Do(request, proxy, id, concurrency)
}

// TestOpenAIPassthroughHTTPAppliesExplicitModelMappingOnce exchanges real HTTP
// fixtures for both provider types and stream choices with chained mapping
// entries configured. It checks one outgoing mapping, restored client model,
// retained extension and unchanged admitted bytes.
func TestOpenAIPassthroughHTTPAppliesExplicitModelMappingOnce(t *testing.T) {
	for _, providerType := range []string{capability.ProviderTypeAPIKey, capability.ProviderTypeOAuth} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", providerType, stream), func(t *testing.T) {
				// Buffer the expected capture so the server can publish bytes before
				// writing its response; this scenario reads the capture after Forward
				// completes.
				observed := make(chan []byte, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					observed <- body
					response := `{"id":"resp_mapping","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`
					w.Header().Set("x-request-id", "mapping-request")
					if gjson.GetBytes(body, "stream").Bool() {
						w.Header().Set("Content-Type", "text/event-stream")
						// Write the scripted SSE terminal and discard callback write errors;
						// Forward error and output assertions supply the observable outcome of
						// this exchange.
						fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\ndata: [DONE]\n\n", response)
					} else {
						w.Header().Set("Content-Type", "application/json")
						// Write the scripted JSON terminal and discard callback write errors;
						// Forward error and output assertions supply the observable outcome of
						// this exchange.
						_, _ = io.WriteString(w, response)
					}
				}))
				// Keep the server available through forwarding and capture inspection,
				// then close its listener and connections when this provider/stream
				// scenario finishes.
				defer server.Close()
				target, err := url.Parse(server.URL)
				require.NoError(t, err)
				body := []byte(fmt.Sprintf(`{"model":"public-model","stream":%v,"instructions":"test instructions","input":[{"role":"user","content":"hi"}],"custom_extension":{"keep":"exact"}}`, stream))
				original := bytes.Clone(body)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
				service := newResponsesFixture(responsesFixtureInputs{transport: mappingHTTPTransport{client: server.Client(), endpoint: target}, credentials: &providercore.OpenAIExecutionCredentials{}})
				value := gatewayprovider.NewExecutionProvider(&providercore.Record{ID: 981, Platform: capability.PlatformOpenAI, Type: providerType, Concurrency: 1, Status: providercore.StatusActive, Schedulable: true, Extra: map[string]any{"openai_passthrough": true}, Credentials: map[string]any{
					"api_key": "test-key", "base_url": "https://api.openai.com", "access_token": "test-oauth", "chatgpt_account_id": "test-provider",
					"model_mapping":   map[string]any{"public-model": "gpt-5.4", "gpt-5.4": "must-not-map-again"},
					"model_whitelist": []string{"gpt-5.4"},
				}})
				// Use a five-second forwarding context and cancel it on exit to release
				// the deadline timer; capture inspection follows successful Forward
				// completion.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				result, err := service.Forward(ctx, c, value, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				sent := <-observed
				require.Equal(t, "gpt-5.4", gjson.GetBytes(sent, "model").String())
				require.Equal(t, "exact", gjson.GetBytes(sent, "custom_extension.keep").String())
				require.Equal(t, "public-model", result.Model)
				require.Equal(t, "gpt-5.4", result.UpstreamModel)
				require.Contains(t, recorder.Body.String(), `"model":"public-model"`)
				require.Equal(t, original, body)
			})
		}
	}
}

// List returns the retained router slice and pointers for synchronous fixture
// startup. Nil values represent an empty router list; callers keep the
// returned data stable during reload because this store neither copies nor
// locks it.
func (s *tlsRouterTestStore) List(context.Context) ([]*egress.TLSFingerprintRouter, error) {
	return s.values, nil
}

// newTLSFingerprintRouterTestService constructs a router service over the
// supplied router pointers and warms it synchronously. The fixture uses a nil
// distributed cache, so startup loads the List values with no subscription to
// stop.
func newTLSFingerprintRouterTestService(routers ...*egress.TLSFingerprintRouter) *egress.TLSFingerprintRouterService {
	service := egress.NewTLSFingerprintRouterService(&tlsRouterTestStore{values: routers}, nil)
	service.Start()
	return service
}

// TestSecondImageToolErrorPresentation gives a valid first tool and an unknown second tool.
// Managed and passthrough forwarding must reject locally with a structured capability
// error before invoking transport, including when outer error presentation runs.
func TestSecondImageToolErrorPresentation(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2"},{"type":"image_generation","model":"unknown-second-tool"}],"input":"draw"}`)
			upstream := &auxiliaryHTTPRecorder{}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, w := newOpenAIImageGenerationControlTestContext(true, "curl/8.0")
			provider := newOpenAIImageGenerationControlTestProvider()
			provider.Record.Extra = map[string]any{"openai_passthrough": passthrough}
			result, err := svc.Forward(c.Request.Context(), c, provider, body)
			require.ErrorContains(t, err, "MODEL_CAPABILITY_UNKNOWN")
			require.Nil(t, result)
			require.Nil(t, upstream.lastReq)
			firstError := w.Body.String()
			require.True(t, WriteOpenAIModelAttributeError(c, err, false))
			require.Equal(t, firstError, w.Body.String())
			DefaultOpenAIErrorOutput().EnsureResponse(c, false, err)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Equal(t, "MODEL_CAPABILITY_UNKNOWN", gjson.GetBytes(w.Body.Bytes(), "error.code").String())
		})
	}
}

// TestOpenAIResponsesOAuthImageDriverOverride prepares an image-only Responses
// request using a provider override. An explicit text model keeps its identity,
// while automatic image adaptation selects the provider's configured driver
// and the corresponding default instructions.
func TestOpenAIResponsesOAuthImageDriverOverride(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gpt-6-luna"} {
		upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_image\",\"status\":\"completed\",\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\"}]}}\n\n")),
		}}
		svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
		provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
			ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
			Credentials: map[string]any{"access_token": "test"},
			Extra:       map[string]any{providercore.CodexImageResponsesModelKey: "gpt-6.1-sol"},
		}}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: true}})
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
		body := []byte(fmt.Sprintf(`{"model":%q,"stream":false,"input":"draw a cat","tools":[{"type":"image_generation","model":"gpt-image-2"}]}`, model))
		_, err := svc.Forward(context.Background(), c, provider, body)
		require.NoError(t, err)
		expected := model
		if model == "gpt-image-2" {
			expected = "gpt-6.1-sol"
		}
		require.Equal(t, expected, gjson.GetBytes(upstream.lastBody, "model").String())
		require.Equal(t, openaicore.DefaultCodexSynthInstructions(expected), gjson.GetBytes(upstream.lastBody, "instructions").String())
		require.Equal(t, "gpt-image-2", gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
	}
}

// TestOpenAIResponsesImageDriverRejection varies requested streaming, committed
// output, and administrator error rules. Before commitment it returns JSON with
// the selected status; committed streams receive one terminal with the selected
// message, while the returned driver error retains its upstream classification.
func TestOpenAIResponsesImageDriverRejection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, committed := range []bool{false, true} {
			if committed && !stream {
				continue
			}
			for _, ruleMode := range []string{"none", "custom", "passthrough"} {
				t.Run(fmt.Sprintf("stream=%t/committed=%t/rule=%s", stream, committed, ruleMode), func(t *testing.T) {
					upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{},
						Body:       io.NopCloser(strings.NewReader(`{"detail":"The 'gpt-6-luna' model is not supported when using Codex with a ChatGPT account. See https://example.com/?access_token=driver-review-secret"}`)),
					}}
					svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
					provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
						ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
						Credentials: map[string]any{"access_token": "test"},
					}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: true}})
					SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
					expectedStatus := http.StatusBadRequest
					expectedMessage := "gpt-6-luna"
					if ruleMode != "none" {
						expectedStatus = http.StatusServiceUnavailable
						expectedMessage = "Image service temporarily unavailable"
						rule := gatewaytestkit.NonFailoverRule(http.StatusBadRequest, "not supported when using Codex", expectedStatus, expectedMessage)
						if ruleMode == "passthrough" {
							rule.PassthroughBody = true
							expectedMessage = "gpt-6-luna"
						}
						rule.SkipMonitoring = true
						BindErrorPassthroughService(c, gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule}))
					}
					if committed {
						c.Header("Content-Type", "text/event-stream")
						_, err := c.Writer.WriteString(": queued\n\n")
						require.NoError(t, err)
						c.Writer.Flush()
					}
					body := []byte(fmt.Sprintf(`{"model":"gpt-image-2","stream":%t,"input":"draw a cat"}`, stream))
					_, err := svc.Forward(context.Background(), c, provider, body)
					var driverErr *openaicore.OpenAIImagesUpstreamError
					require.ErrorAs(t, err, &driverErr)
					require.Equal(t, "image_main_model_unsupported", driverErr.Code)
					require.Equal(t, http.StatusBadRequest, driverErr.StatusCode)
					require.Equal(t, ruleMode != "none", c.GetBool(OpsSkipPassthroughKey))
					require.Contains(t, rec.Body.String(), expectedMessage)
					require.NotContains(t, rec.Body.String(), "driver-review-secret")
					if committed {
						require.Equal(t, http.StatusOK, rec.Code)
						require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
						require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
					} else {
						require.Equal(t, expectedStatus, rec.Code)
						require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
						require.Equal(t, "image_main_model_unsupported", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
					}
					switch ruleMode {
					case "custom":
						require.NotContains(t, rec.Body.String(), "gpt-6-luna")
					case "none":
						require.Contains(t, rec.Body.String(), "gpt-image-2")
					}
					before := rec.Body.String()
					require.False(t, DefaultOpenAIErrorOutput().EnsureResponse(c, stream, err))
					require.Equal(t, before, rec.Body.String())
				})
			}
		}
	}
}
