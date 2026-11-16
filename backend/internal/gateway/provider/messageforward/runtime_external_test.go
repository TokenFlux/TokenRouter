package messageforward_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// gatewayTTLSettingRepo supplies mutable setting strings for external
// forwarding tests.
type gatewayTTLSettingRepo struct {
	// Repository embeds unused settings methods; external runtime fixtures
	// implement the requested reads and expose unexpected repository calls
	// through nil dispatch.
	settings.Repository // unused settings repository methods
	// data retains the test map; changing its entries takes effect after
	// the corresponding runtime settings reader is invalidated by the
	// caller.
	data map[string]string // mutable setting fixture map
}

// gatewayForwardErrorPolicyRepoStub records health persistence operations from
// external Messages attempts.
type gatewayForwardErrorPolicyRepoStub struct {
	// ExecutionProviderStore embeds unused provider methods; partial-stream
	// fixtures record the exercised health writes and expose unexpected
	// repository calls through nil dispatch.
	gatewayprovider.ExecutionProviderStore // unused provider repository methods

	tempCalls     int // provider temporary exclusion count
	overloadCalls int // global overload observation count
	// recorded model cooldown writes
	modelRateLimitCalls []gatewayForwardModelRateLimitCall
}

// gatewayForwardModelRateLimitCall identifies one recorded provider and model
// cooldown operation.
type gatewayForwardModelRateLimitCall struct {
	providerID int64  // observed execution provider identifier
	scope      string // observed model cooldown scope
}

// anthropicHTTPUpstreamRecorder captures wire requests before returning
// configured exchange results.
type anthropicHTTPUpstreamRecorder struct {
	// lastReq retains the exchanged request and lastBody captures its read
	// bytes. resp is handed to the executor for response cleanup when err
	// is nil; a configured err returns no response while leaving request
	// evidence recorded.
	lastReq  *http.Request  // last borrowed HTTP request
	lastBody []byte         // last captured request bytes
	resp     *http.Response // configured upstream response pointer
	err      error          // configured upstream exchange error
}

// TestGatewayService_AnthropicAPIKeyPassthrough_ForwardStreamPreservesBodyAndAuthReplacement
// forwards a mapped native API key stream with inbound credentials and
// cache-token aliases. Provider authentication replaces client secrets, headers
// are filtered, and client cached_tokens spelling remains intact.
func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardStreamPreservesBodyAndAuthReplacement(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/1.0.0")
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("X-Goog-Api-Key", "inbound-goog-key")
	c.Request.Header.Set("Cookie", "secret=1")
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	body := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"system":[{"type":"text","text":"x-anthropic-billing-header keep"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed := &requeststate.ParsedRequest{
		Body:   requeststate.NewRequestBodyRef(body),
		Model:  "claude-3-7-sonnet-20250219",
		Stream: true,
	}

	upstreamSSE := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":9,"cached_tokens":7}}}`,
		"",
		`data: {"type":"message_delta","usage":{"output_tokens":3}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"x-request-id": []string{"rid-anthropic-pass"},
				"Set-Cookie":   []string{"secret=upstream"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamSSE)),
		},
	}

	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture(), Deferred: &providercore.DeferredService{}}, compileResponseHeaderFilter(cfg),
	)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 101,
			Name:        "anthropic-apikey-pass",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "upstream-anthropic-key",
				"base_url":      "https://api.anthropic.com",
				"model_mapping": map[string]any{"claude-3-7-sonnet-20250219": "claude-3-haiku-20240307"},
			},
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)

	require.Equal(t, "claude-3-haiku-20240307", gjson.GetBytes(upstream.lastBody, "model").String(), "透传模式应应用提供商级模型映射")

	require.Equal(t, "upstream-anthropic-key", claude.GetHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "authorization"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "x-goog-api-key"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "cookie"))
	require.Equal(t, "2023-06-01", claude.GetHeaderRaw(upstream.lastReq.Header, "anthropic-version"))
	require.Equal(t, "interleaved-thinking-2025-05-14", claude.GetHeaderRaw(upstream.lastReq.Header, "anthropic-beta"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "x-stainless-lang"), "API Key 透传不应注入 OAuth 指纹头")

	require.Contains(t, rec.Body.String(), `"cached_tokens":7`)
	require.NotContains(t, rec.Body.String(), `"cache_read_input_tokens":7`, "透传输出不应被网关改写")
	require.Equal(t, 7, result.Usage.CacheReadInputTokens, "计费 usage 解析应保留 cached_tokens 兼容")
	require.Empty(t, rec.Header().Get("Set-Cookie"), "响应头应经过安全过滤")
}

// TestGatewayService_AnthropicAPIKeyPassthrough_ForwardCountTokensPreservesBody
// forwards native counting with provider mapping and inbound secrets. The wire
// model and authentication use the provider values, counting JSON is returned,
// and response cookies are filtered.
func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardCountTokensPreservesBody(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Cookie", "secret=1")

	body := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"thinking":{"type":"enabled"}}`)
	parsed := &requeststate.ParsedRequest{
		Body:  requeststate.NewRequestBodyRef(body),
		Model: "claude-3-5-sonnet-latest",
	}

	upstreamRespBody := `{"input_tokens":42}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-count"},
				"Set-Cookie":   []string{"secret=upstream"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamRespBody)),
		},
	}

	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture()}, compileResponseHeaderFilter(cfg),
	)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 102,
			Name:        "anthropic-apikey-pass-count",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "upstream-anthropic-key",
				"base_url":      "https://api.anthropic.com",
				"model_mapping": map[string]any{"claude-3-5-sonnet-latest": "claude-3-opus-20240229"},
			},
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)
	require.NoError(t, err)

	require.Equal(t, "claude-3-opus-20240229", gjson.GetBytes(upstream.lastBody, "model").String(), "count_tokens 透传模式应应用提供商级模型映射")
	require.Equal(t, "upstream-anthropic-key", claude.GetHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "authorization"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "cookie"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, upstreamRespBody, rec.Body.String())
	require.Empty(t, rec.Header().Get("Set-Cookie"))
}

// TestGatewayService_AnthropicAPIKeyPassthrough_ModelMappingEdgeCases compares
// absent, empty, unmatched, exact, and wildcard mapping on generation and
// counting. Every exercised request carries the selected expected upstream
// model after preparation.
func TestGatewayService_AnthropicAPIKeyPassthrough_ModelMappingEdgeCases(t *testing.T) {
	tests := []struct {
		name          string         // table scenario name
		model         string         // client request model identifier
		modelMapping  map[string]any // provider model mapping values
		expectedModel string         // expected upstream model identifier
		endpoint      string         // selected HTTP fixture endpoint
	}{
		{
			name:          "Forward: 无映射配置时不改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  nil,
			expectedModel: "claude-sonnet-4-20250514",
			endpoint:      "messages",
		},
		{
			name:          "Forward: 空映射配置时不改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{},
			expectedModel: "claude-sonnet-4-20250514",
			endpoint:      "messages",
		},
		{
			name:          "Forward: 模型不在映射表中时不改写",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{"claude-3-haiku-20240307": "claude-3-opus-20240229"},
			expectedModel: "claude-sonnet-4-20250514",
			endpoint:      "messages",
		},
		{
			name:          "Forward: 精确匹配映射应改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{"claude-sonnet-4-20250514": "claude-sonnet-4-5-20241022"},
			expectedModel: "claude-sonnet-4-5-20241022",
			endpoint:      "messages",
		},
		{
			name:          "Forward: 通配符映射应改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{"claude-sonnet-4-*": "claude-sonnet-4-5-20241022"},
			expectedModel: "claude-sonnet-4-5-20241022",
			endpoint:      "messages",
		},
		{
			name:          "CountTokens: 无映射配置时不改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  nil,
			expectedModel: "claude-sonnet-4-20250514",
			endpoint:      "count_tokens",
		},
		{
			name:          "CountTokens: 模型不在映射表中时不改写",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{"claude-3-haiku-20240307": "claude-3-opus-20240229"},
			expectedModel: "claude-sonnet-4-20250514",
			endpoint:      "count_tokens",
		},
		{
			name:          "CountTokens: 精确匹配映射应改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{"claude-sonnet-4-20250514": "claude-sonnet-4-5-20241022"},
			expectedModel: "claude-sonnet-4-5-20241022",
			endpoint:      "count_tokens",
		},
		{
			name:          "CountTokens: 通配符映射应改写模型",
			model:         "claude-sonnet-4-20250514",
			modelMapping:  map[string]any{"claude-sonnet-4-*": "claude-sonnet-4-5-20241022"},
			expectedModel: "claude-sonnet-4-5-20241022",
			endpoint:      "count_tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			body := []byte(`{"model":"` + tt.model + `","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
			parsed := &requeststate.ParsedRequest{
				Body:  requeststate.NewRequestBodyRef(body),
				Model: tt.model,
			}

			credentials := map[string]any{
				"api_key":  "upstream-key",
				"base_url": "https://api.anthropic.com",
			}
			if tt.modelMapping != nil {
				credentials["model_mapping"] = tt.modelMapping
			}

			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 300,
					Name:        "edge-case-test",
					Platform:    capability.PlatformAnthropic,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: credentials,
					Extra:       map[string]any{"anthropic_passthrough": true},
					Status:      billing.StatusActive,
					Schedulable: true,
				},
			}

			if tt.endpoint == "messages" {
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				parsed.Stream = false

				upstreamJSON := `{"id":"msg_1","type":"message","usage":{"input_tokens":5,"output_tokens":3}}`
				upstream := &anthropicHTTPUpstreamRecorder{
					resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(upstreamJSON)),
					},
				}
				svc := newHTTPRuntimeFixture(
					&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture()}, nil,
				)

				result, err := svc.Forward(context.Background(), c, provider, parsed)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tt.expectedModel, gjson.GetBytes(upstream.lastBody, "model").String(),
					"Forward 上游请求体中的模型应为: %s", tt.expectedModel)
			} else {
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

				upstreamRespBody := `{"input_tokens":42}`
				upstream := &anthropicHTTPUpstreamRecorder{
					resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(upstreamRespBody)),
					},
				}
				svc := newHTTPRuntimeFixture(
					&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture()}, nil,
				)

				err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)
				require.NoError(t, err)
				require.Equal(t, tt.expectedModel, gjson.GetBytes(upstream.lastBody, "model").String(),
					"CountTokens 上游请求体中的模型应为: %s", tt.expectedModel)
			}
		})
	}
}

// TestGatewayService_AnthropicAPIKeyPassthrough_ModelMappingPreservesOtherFields
// maps native counting while supplying structured system, thinking, and message
// input. The selected model changes, input fields remain present, and
// generation max_tokens is removed.
func TestGatewayService_AnthropicAPIKeyPassthrough_ModelMappingPreservesOtherFields(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	// Exercise model rewriting with structured system, thinking, and
	// message fields.
	body := []byte(`{"model":"claude-sonnet-4-20250514","system":[{"type":"text","text":"You are a helpful assistant."}],"messages":[{"role":"user","content":[{"type":"text","text":"hello world"}]}],"thinking":{"type":"enabled","budget_tokens":5000},"max_tokens":1024}`)
	parsed := &requeststate.ParsedRequest{
		Body:  requeststate.NewRequestBodyRef(body),
		Model: "claude-sonnet-4-20250514",
	}

	upstreamRespBody := `{"input_tokens":42}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(upstreamRespBody)),
		},
	}

	svc := newHTTPRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture()}, nil,
	)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 301,
			Name:        "preserve-fields-test",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "upstream-key",
				"base_url":      "https://api.anthropic.com",
				"model_mapping": map[string]any{"claude-sonnet-4-20250514": "claude-sonnet-4-5-20241022"},
			},
			Extra:       map[string]any{"anthropic_passthrough": true},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)
	require.NoError(t, err)

	sentBody := upstream.lastBody
	require.Equal(t, "claude-sonnet-4-5-20241022", gjson.GetBytes(sentBody, "model").String(), "model 应被映射")
	require.Equal(t, "You are a helpful assistant.", gjson.GetBytes(sentBody, "system.0.text").String(), "system 字段不应被修改")
	require.Equal(t, "hello world", gjson.GetBytes(sentBody, "messages.0.content.0.text").String(), "messages 字段不应被修改")
	require.Equal(t, "enabled", gjson.GetBytes(sentBody, "thinking.type").String(), "thinking 字段不应被修改")
	require.Equal(t, int64(5000), gjson.GetBytes(sentBody, "thinking.budget_tokens").Int(), "thinking.budget_tokens 不应被修改")
	require.False(t, gjson.GetBytes(sentBody, "max_tokens").Exists(), "count_tokens 不应携带 max_tokens")
}

// TestGatewayService_AnthropicAPIKeyPassthrough_CountTokensFiltersGenerationFields
// supplies native counting with sampling, streaming, stopping, and
// generation-limit controls. Those generation controls disappear while model,
// system, messages, tools, and thinking remain available in the wire body.
func TestGatewayService_AnthropicAPIKeyPassthrough_CountTokensFiltersGenerationFields(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	body := []byte(`{"model":"claude-sonnet-4-20250514","system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"tool","input_schema":{"type":"object"}}],"temperature":0.7,"top_p":0.9,"top_k":40,"stream":true,"stop_sequences":["END"],"stop":"END","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":5000}}`)
	parsed := &requeststate.ParsedRequest{
		Body:  requeststate.NewRequestBodyRef(body),
		Model: "claude-sonnet-4-20250514",
	}

	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"input_tokens":42}`)),
		},
	}

	svc := newHTTPRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture()}, nil,
	)
	provider := newAnthropicAPIKeyProviderForTest()

	err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)
	require.NoError(t, err)

	sentBody := upstream.lastBody
	for _, path := range []string{"temperature", "top_p", "top_k", "stream", "stop_sequences", "stop"} {
		require.False(t, gjson.GetBytes(sentBody, path).Exists(), "%s 不应发送到 count_tokens 上游", path)
	}
	require.Equal(t, "claude-sonnet-4-20250514", gjson.GetBytes(sentBody, "model").String(), "model 应保留")
	require.Equal(t, "sys", gjson.GetBytes(sentBody, "system.0.text").String(), "system 应保留")
	require.Equal(t, "hello", gjson.GetBytes(sentBody, "messages.0.content").String(), "messages 应保留")
	require.Equal(t, "tool", gjson.GetBytes(sentBody, "tools.0.name").String(), "tools 应保留")
	require.False(t, gjson.GetBytes(sentBody, "max_tokens").Exists(),
		"count_tokens 请求不得携带生成参数 max_tokens")
	require.Equal(t, "enabled", gjson.GetBytes(sentBody, "thinking.type").String(), "thinking 应保留")
}

// TestGatewayService_AnthropicAPIKeyPassthrough_EmptyModelSkipsMapping supplies
// counting without a model against a wildcard mapping. The upstream body stays
// byte-identical and the counting call succeeds with the configured response.
func TestGatewayService_AnthropicAPIKeyPassthrough_EmptyModelSkipsMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	parsed := &requeststate.ParsedRequest{
		Body:  requeststate.NewRequestBodyRef(body),
		Model: "",
	}

	upstreamRespBody := `{"input_tokens":10}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(upstreamRespBody)),
		},
	}

	svc := newHTTPRuntimeFixture(
		&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture()}, nil,
	)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 302,
			Name:        "empty-model-test",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":       "upstream-key",
				"base_url":      "https://api.anthropic.com",
				"model_mapping": map[string]any{"*": "claude-3-opus-20240229"},
			},
			Extra:       map[string]any{"anthropic_passthrough": true},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	// An absent model skips mapping and keeps the body unchanged.
	require.Equal(t, body, upstream.lastBody, "空模型名时请求体不应被修改")
}

// TestGatewayService_AnthropicAPIKeyPassthrough_CountTokens404PassthroughNotError
// compares missing counting-route 404 with generic missing resources, invalid
// input, and server errors. The supported compatibility case returns 404
// successfully, while other failures retain their selected error paths.
func TestGatewayService_AnthropicAPIKeyPassthrough_CountTokens404PassthroughNotError(t *testing.T) {
	tests := []struct {
		name            string // table scenario name
		statusCode      int    // upstream HTTP status
		respBody        string // upstream response fixture bytes
		wantPassthrough bool   // expected unsupported-count route result
	}{
		{
			name:            "404 endpoint not found passes through as 404",
			statusCode:      http.StatusNotFound,
			respBody:        `{"error":{"message":"Not found: /v1/messages/count_tokens","type":"not_found_error"}}`,
			wantPassthrough: true,
		},
		{
			name:            "404 generic not found does not passthrough",
			statusCode:      http.StatusNotFound,
			respBody:        `{"error":{"message":"resource not found","type":"not_found_error"}}`,
			wantPassthrough: false,
		},
		{
			name:            "400 Invalid URL does not passthrough",
			statusCode:      http.StatusBadRequest,
			respBody:        `{"error":{"message":"Invalid URL (POST /v1/messages/count_tokens)","type":"invalid_request_error"}}`,
			wantPassthrough: false,
		},
		{
			name:            "400 model error does not passthrough",
			statusCode:      http.StatusBadRequest,
			respBody:        `{"error":{"message":"model not found: claude-unknown","type":"invalid_request_error"}}`,
			wantPassthrough: false,
		},
		{
			name:            "500 internal error does not passthrough",
			statusCode:      http.StatusInternalServerError,
			respBody:        `{"error":{"message":"internal error","type":"api_error"}}`,
			wantPassthrough: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

			body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`)
			parsed := &requeststate.ParsedRequest{Body: requeststate.NewRequestBodyRef(body), Model: "claude-sonnet-4-5"}

			upstream := &anthropicHTTPUpstreamRecorder{
				resp: &http.Response{
					StatusCode: tt.statusCode,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.respBody)),
				},
			}

			svc := newHTTPRuntimeFixture(
				&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}, messageforward.Dependencies{Transport: upstream, Health: nil}, nil,
			)

			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 200,
					Name:        "proxy-acc",
					Platform:    capability.PlatformAnthropic,
					Type:        capability.ProviderTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{
						"api_key":  "sk-proxy",
						"base_url": "https://proxy.example.com",
					},
					Extra:       map[string]any{"anthropic_passthrough": true},
					Status:      billing.StatusActive,
					Schedulable: true,
				},
			}

			err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)

			if tt.wantPassthrough {
				// An unsupported counting endpoint returns an
				// Anthropic 404 response with no Go error.
				require.NoError(t, err)
				require.Equal(t, http.StatusNotFound, rec.Code)
				var errResp map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
				require.Equal(t, "error", errResp["type"])
				errObj, ok := errResp["error"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, "not_found_error", errObj["type"])
			} else if tt.statusCode >= http.StatusInternalServerError {
				// Before output starts, upstream server errors
				// let the handler select another provider.
				var failoverErr *forwardcore.UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, tt.statusCode, failoverErr.StatusCode)
				require.False(t, c.Writer.Written())
			} else {
				require.Error(t, err)
				require.Equal(t, tt.statusCode, rec.Code)
			}
		})
	}
}

// TestGatewayService_QoderCountTokensUnsupported invokes counting on a Qoder
// target with a transport recorder available. The client receives a
// not_found_error response and the recorder confirms no upstream request was
// made.
func TestGatewayService_QoderCountTokensUnsupported(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hi"}]}`)
	parsed := &requeststate.ParsedRequest{Body: requeststate.NewRequestBodyRef(body), Model: "deepseek-v4-pro"}
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"input_tokens":999}`)),
		},
	}
	svc := newHTTPRuntimeFixture(nil, messageforward.Dependencies{Transport: upstream}, nil)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 301,
			Name:        "qoder",
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Concurrency: 1,
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	err := svc.ForwardCountTokens(context.Background(), c, provider, parsed)

	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Nil(t, upstream.lastReq)
	var errResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	require.Equal(t, "error", errResp["type"])
	errObj, ok := errResp["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "not_found_error", errObj["type"])
}

// TestGatewayService_AnthropicOAuth_AppliesProviderMappingBeforeNormalization
// forwards a third-party client alias through an OAuth target mapping. Both
// completion UpstreamModel and captured request model identify the mapped
// provider target after preparation.
func TestGatewayService_AnthropicOAuth_AppliesProviderMappingBeforeNormalization(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "third-party-client/1.0")

	body := []byte(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)

	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_oauth_mapping","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture(), Deferred: &providercore.DeferredService{}}, compileResponseHeaderFilter(cfg),
	)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 303,
			Name:        "anthropic-oauth-mapping",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "oauth-token",
				"model_mapping": map[string]any{"client-alias": "claude-sonnet-4-5"},
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4-5", result.UpstreamModel)
	require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(upstream.lastBody, "model").String())
}

// TestGatewayService_AnthropicOAuthMimic_RewritesSystemWithBillingBlock
// forwards third-party OAuth system strings and arrays, including Haiku cache
// annotations. The prepared identity blocks and headers match mimic defaults,
// and client instructions move into message history.
func TestGatewayService_AnthropicOAuthMimic_RewritesSystemWithBillingBlock(t *testing.T) {
	tests := []struct {
		name string // table scenario name
		// inbound protocol fixture bytes
		body string
		// expected client model identifier
		wantModel                  string
		wantOriginalSystem         string // expected moved system text
		wantOriginalSystemCacheTTL string // expected moved cache TTL
		// expected metadata identity text
		wantMetadataUserID string
	}{
		{
			name:               "sonnet system array",
			body:               `{"model":"claude-3-5-sonnet-latest","system":[{"type":"text","text":"x-anthropic-billing-header keep"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			wantModel:          "claude-3-5-sonnet-latest",
			wantOriginalSystem: "x-anthropic-billing-header keep",
		},
		{
			name:               "sonnet system string",
			body:               `{"model":"claude-3-5-sonnet-latest","system":"x-anthropic-billing-header keep","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			wantModel:          "claude-3-5-sonnet-latest",
			wantOriginalSystem: "x-anthropic-billing-header keep",
		},
		{
			name:                       "haiku full mimicry",
			body:                       `{"model":"claude-haiku-4-5","metadata":{"user_id":"pi-session-metadata"},"system":[{"type":"text","text":"Pi project instructions","cache_control":{"type":"ephemeral","ttl":"1h"}}],"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			wantModel:                  "claude-haiku-4-5",
			wantOriginalSystem:         "Pi project instructions",
			wantOriginalSystemCacheTTL: "1h",
			wantMetadataUserID:         "pi-session-metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Request.Header.Set("User-Agent", "pi/0.51.0")
			c.Request.Header.Set("Anthropic-Beta", "client-only-beta")

			parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef([]byte(tt.body)), capability.PlatformAnthropic)
			require.NoError(t, err)

			upstream := &anthropicHTTPUpstreamRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json"},
						"x-request-id": []string{"rid-oauth-mimic"},
					},
					Body: io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":7}}`)),
				},
			}

			cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
			svc := newHTTPRuntimeFixture(
				cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture(), Deferred: &providercore.DeferredService{}}, compileResponseHeaderFilter(cfg),
			)

			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 301,
					Name:        "anthropic-oauth-mimic",
					Platform:    capability.PlatformAnthropic,
					Type:        capability.ProviderTypeOAuth,
					Concurrency: 1,
					Credentials: map[string]any{
						"access_token": "oauth-token",
					},
					Status:      billing.StatusActive,
					Schedulable: true,
				},
			}

			result, err := svc.Forward(context.Background(), c, provider, parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "Bearer oauth-token", claude.GetHeaderRaw(upstream.lastReq.Header, "authorization"))
			finalBeta := claude.GetHeaderRaw(upstream.lastReq.Header, "anthropic-beta")
			for _, beta := range claude.FullClaudeCodeMimicryBetas() {
				require.Truef(t, claude.AnthropicBetaTokensContains(finalBeta, beta), "missing mimic beta %s", beta)
			}
			require.False(t, claude.AnthropicBetaTokensContains(finalBeta, "client-only-beta"))
			for key, value := range claude.DefaultHeaders {
				require.Equal(t, value, claude.GetHeaderRaw(upstream.lastReq.Header, key), "mimic fingerprint header %s", key)
			}
			require.NotEmpty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "x-client-request-id"))

			require.Equal(t, tt.wantModel, gjson.GetBytes(upstream.lastBody, "model").String())
			system := gjson.GetBytes(upstream.lastBody, "system")
			require.True(t, system.Exists())
			require.True(t, system.IsArray(), "system should be an array")
			arr := system.Array()
			require.Len(t, arr, 3, "system array should have billing block + cc prompt block + expansion block")

			billingText := arr[0].Get("text").String()
			require.Contains(t, billingText, "x-anthropic-billing-header:")
			require.Contains(t, billingText, "cc_version="+claude.CLICurrentVersion+".")
			require.Contains(t, billingText, "cc_entrypoint=cli;")

			require.Equal(t, claude.ClaudeCodeSystemPrompt, arr[1].Get("text").String())
			require.False(t, arr[1].Get("cache_control").Exists(), "身份前缀 block 不应带 cache_control")

			require.Equal(t, claude.ClaudeCodeSystemPromptExpansion, arr[2].Get("text").String())
			require.Equal(t, "ephemeral", arr[2].Get("cache_control.type").String())

			// The client's system prompt moves into the messages
			// array.
			messages := gjson.GetBytes(upstream.lastBody, "messages")
			require.True(t, messages.IsArray())
			firstMsg := messages.Array()[0]
			require.Equal(t, "user", firstMsg.Get("role").String())
			require.Contains(t, firstMsg.Get("content.0.text").String(), tt.wantOriginalSystem)
			if tt.wantOriginalSystemCacheTTL != "" {
				require.Equal(t, "ephemeral", firstMsg.Get("content.0.cache_control.type").String())
				require.Equal(t, tt.wantOriginalSystemCacheTTL, firstMsg.Get("content.0.cache_control.ttl").String())
			} else {
				require.False(t, firstMsg.Get("content.0.cache_control").Exists())
			}

			if tt.wantMetadataUserID != "" {
				require.Equal(t, tt.wantMetadataUserID, gjson.GetBytes(upstream.lastBody, "metadata.user_id").String())
				require.True(t, gjson.GetBytes(upstream.lastBody, "context_management").Exists())
			}
		})
	}
}

// TestGatewayService_AnthropicOAuthRealClaudeCodeHaiku_PreservesClientHeadersAndBody
// forwards an identified Claude Code Haiku request through OAuth. Client
// identity headers, beta set, system/messages, and metadata remain observable
// while mimic billing and request-ID injection stay absent.
func TestGatewayService_AnthropicOAuthRealClaudeCodeHaiku_PreservesClientHeadersAndBody(t *testing.T) {
	metadataUserID := claude.FormatMetadataUserID(
		"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"550e8400-e29b-41d4-a716-446655440000",
		"123e4567-e89b-42d3-a456-426614174000",
		claude.CLICurrentVersion,
	)
	body := []byte(`{"model":"claude-haiku-4-5","metadata":{"user_id":` + strconvQuote(metadataUserID) + `},"system":[{"type":"text","text":"Client-owned Claude Code system","cache_control":{"type":"ephemeral"}}],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/"+claude.CLICurrentVersion+" (external, cli)")
	c.Request.Header.Set("X-Stainless-Package-Version", "real-client-package")
	clientBeta := strings.Join([]string{
		claude.BetaClaudeCode,
		claude.BetaOAuth,
		claude.BetaInterleavedThinking,
		claude.BetaContextManagement,
	}, ",")
	c.Request.Header.Set("Anthropic-Beta", clientBeta)

	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_real_cc","type":"message","role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":7}}`)),
	}}
	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture(), Deferred: &providercore.DeferredService{}}, compileResponseHeaderFilter(cfg),
	)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 302, Name: "anthropic-real-cc", Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token"}, Status: billing.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, c.Request.Header.Get("User-Agent"), claude.GetHeaderRaw(upstream.lastReq.Header, "User-Agent"))
	require.Equal(t, "real-client-package", claude.GetHeaderRaw(upstream.lastReq.Header, "X-Stainless-Package-Version"))
	require.Equal(t, clientBeta, claude.GetHeaderRaw(upstream.lastReq.Header, "anthropic-beta"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "x-client-request-id"), "真实 CC 不应被强制写入 mimic request id")
	require.Equal(t, gjson.GetBytes(body, "system").Raw, gjson.GetBytes(upstream.lastBody, "system").Raw)
	require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw)
	require.Equal(t, metadataUserID, gjson.GetBytes(upstream.lastBody, "metadata.user_id").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "context_management").Exists())
	require.NotContains(t, string(upstream.lastBody), "x-anthropic-billing-header:")
}

// TestGatewayService_AnthropicOAuthProxiedClaudeCode_PreservesSystemCachePrefix
// forwards a proxied Claude Code body with a generic Go user agent. The
// existing system cache prefix, messages, metadata, and client headers remain
// visible in prepared wire state.
func TestGatewayService_AnthropicOAuthProxiedClaudeCode_PreservesSystemCachePrefix(t *testing.T) {
	metadataUserID := claude.FormatMetadataUserID(
		"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"550e8400-e29b-41d4-a716-446655440000",
		"123e4567-e89b-42d3-a456-426614174000",
		claude.CLICurrentVersion,
	)
	body := []byte(`{"model":"claude-sonnet-4-5","metadata":{"user_id":` + strconvQuote(metadataUserID) + `},"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.abc; cc_entrypoint=cli;"},{"type":"text","text":"Client-owned project instructions","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "Go-http-client/2.0")
	clientBeta := strings.Join([]string{claude.BetaClaudeCode, claude.BetaOAuth, claude.BetaPromptCachingScope}, ",")
	c.Request.Header.Set("Anthropic-Beta", clientBeta)

	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_proxied_cc","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":7}}`)),
	}}
	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture(), Deferred: &providercore.DeferredService{}}, compileResponseHeaderFilter(cfg),
	)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 304, Name: "anthropic-proxied-cc", Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-token"}, Status: billing.StatusActive, Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "Go-http-client/2.0", claude.GetHeaderRaw(upstream.lastReq.Header, "User-Agent"))
	require.Equal(t, clientBeta, claude.GetHeaderRaw(upstream.lastReq.Header, "anthropic-beta"))
	require.Empty(t, claude.GetHeaderRaw(upstream.lastReq.Header, "x-client-request-id"), "代理的真实 CC 不应被强制写入 mimic request id")
	require.Equal(t, gjson.GetBytes(body, "system").Raw, gjson.GetBytes(upstream.lastBody, "system").Raw)
	require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw)
	require.Equal(t, metadataUserID, gjson.GetBytes(upstream.lastBody, "metadata.user_id").String())
}

// TestGatewayService_AnthropicOAuth_SystemPromptInjectionCanBeDisabled disables
// OAuth system injection before forwarding a third-party request. The captured
// system string stays client-provided and contains neither billing attribution
// nor injected system instructions.
func TestGatewayService_AnthropicOAuth_SystemPromptInjectionCanBeDisabled(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","system":"Original system prompt","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)

	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-oauth-no-system-injection"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet-20241022","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":7}}`)),
		},
	}

	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	settingService := newRuntimeSettingsFixture(&gatewayTTLSettingRepo{data: map[string]string{
		gateway.SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
	}})
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream, Health: newPartialHealthFixture(), Settings: settingService, Deferred: &providercore.DeferredService{}}, compileResponseHeaderFilter(cfg),
	)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 302,
			Name:        "anthropic-oauth-no-system-injection",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token": "oauth-token",
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	result, err := svc.Forward(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)

	system := gjson.GetBytes(upstream.lastBody, "system")
	require.True(t, system.Exists())
	require.Equal(t, "Original system prompt", system.String())
	require.NotContains(t, string(upstream.lastBody), "x-anthropic-billing-header:")
	require.NotContains(t, string(upstream.lastBody), "[System Instructions]")
}

// newHTTPRuntimeFixture constructs HTTP forwarding using the supplied
// dependencies, options, and header filter. It replaces Search with the default
// test emulator and uses a 128 MiB read cap when options are absent.
func newHTTPRuntimeFixture(options *messageforward.Options, deps messageforward.Dependencies, filter *egress.CompiledHeaderFilter) *gatewayhttp.MessagesExecutor {
	value := messageforward.Options{ResponseReadLimit: 128 * 1024 * 1024}
	if options != nil {
		value = *options
	}
	deps.Search = gatewayprovider.NewSearchTools(nil, nil)
	return gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(deps, value), filter)
}

// compileResponseHeaderFilter returns the default compiled response policy when
// options exist. Nil options return nil, allowing external fixtures to exercise
// native and converted default filtering separately.
func compileResponseHeaderFilter(options *messageforward.Options) *egress.CompiledHeaderFilter {
	if options == nil {
		return nil
	}
	return egress.CompileHeaderFilter(egress.ResponseHeaderOptions{})
}

// GetValue returns the configured fixture string or ErrSettingNotFound for
// missing input. The data map remains borrowed so tests can change settings
// before invalidating the corresponding runtime reader.
func (r *gatewayTTLSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.data[key]; ok {
		return value, nil
	}
	return "", settings.ErrSettingNotFound
}

// GetMultiple returns a fresh map of configured strings for requested keys.
// Missing keys are omitted, allowing runtime settings to apply defaults after
// the fixture values are changed.
func (r *gatewayTTLSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.data[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// newRuntimeSettingsFixture constructs runtime settings through a settings
// service wrapper with Anthropic beta defaults. The caller retains repository
// fixture state and invalidates readers after changing their values.
func newRuntimeSettingsFixture(repo settings.Repository) *gateway.RuntimeSettings {
	return gateway.NewRuntimeSettings(settings.New(repo), settings.ErrSettingNotFound, func() *gateway.BetaPolicySettings {
		return gatewayprovider.GatewayBetaPolicy(claude.DefaultBetaPolicySettings())
	})
}

// strconvQuote escapes backslashes and quotes for fixture JSON string literals.
func strconvQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

// TestGatewayServiceForwardCountTokensAppliesOAuthProviderMappingBeforeNormalization
// maps an OAuth counting alias before normalization. The parsed request model
// and captured upstream body both carry the provider target selected for the
// count request.
func TestGatewayServiceForwardCountTokensAppliesOAuthProviderMappingBeforeNormalization(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("User-Agent", "third-party-client/1.0")

	body := []byte(`{"model":"group-model","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)

	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"input_tokens":7}`)),
	}}
	cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
	svc := newHTTPRuntimeFixture(
		cfg, messageforward.Dependencies{Transport: upstream}, compileResponseHeaderFilter(cfg),
	)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 501,
			Name:        "oauth-count-token-mapping",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "oauth-token",
				"model_mapping": map[string]any{"group-model": "claude-sonnet-4-5"},
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}

	err = svc.ForwardCountTokens(context.Background(), c, provider, parsed)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
	require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(upstream.lastBody, "model").String())
}

// TestGatewayServiceAnthropicCompatibilityForwardersUseFinalOAuthModel compares
// Chat and Responses OAuth conversion against a mapped alias and upstream
// error. Both paths capture the final provider model in the request sent
// upstream.
func TestGatewayServiceAnthropicCompatibilityForwardersUseFinalOAuthModel(t *testing.T) {
	tests := []struct {
		name string // table scenario name
		path string // HTTP fixture request path
		body []byte // inbound protocol fixture bytes
		// selected protocol fixture invocation
		call func(*gatewayhttp.MessagesExecutor, context.Context, *gin.Context, *gatewayprovider.ExecutionProvider, []byte) error
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"group-model","messages":[{"role":"user","content":"hello"}],"stream":false}`),
			call: func(svc *gatewayhttp.MessagesExecutor, ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body []byte) error {
				_, err := svc.ForwardAsChatCompletions(ctx, c, provider, body, nil)
				return err
			},
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"group-model","input":"hello","stream":false}`),
			call: func(svc *gatewayhttp.MessagesExecutor, ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body []byte) error {
				_, err := svc.ForwardAsResponses(ctx, c, provider, body, nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			c.Request.Header.Set("User-Agent", "third-party-client/1.0")

			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"test error"}}`)),
			}}
			cfg := &messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize}
			svc := newHTTPRuntimeFixture(
				cfg, messageforward.Dependencies{Transport: upstream}, compileResponseHeaderFilter(cfg),
			)
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 502,
					Name:        "oauth-compat-mapping",
					Platform:    capability.PlatformAnthropic,
					Type:        capability.ProviderTypeOAuth,
					Concurrency: 1,
					Credentials: map[string]any{
						"access_token":  "oauth-token",
						"model_mapping": map[string]any{"group-model": "claude-sonnet-4-5"},
					},
					Status:      billing.StatusActive,
					Schedulable: true,
				},
			}

			err := tt.call(svc, context.Background(), c, provider, tt.body)
			require.Error(t, err)
			require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(upstream.lastBody, "model").String())
		})
	}
}

// SetTempUnschedulable records the provider-wide exclusion count for external
// health assertions.
func (r *gatewayForwardErrorPolicyRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

// SetModelRateLimit saves provider identity and model scope for external health
// assertions.
func (r *gatewayForwardErrorPolicyRepoStub) SetModelRateLimit(_ context.Context, id int64, scope string, _ time.Time, _ ...string) error {
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, gatewayForwardModelRateLimitCall{providerID: id, scope: scope})
	return nil
}

// SetOverloaded records the global overload observation count for stream-error
// assertions.
func (r *gatewayForwardErrorPolicyRepoStub) SetOverloaded(context.Context, int64, time.Time) error {
	r.overloadCalls++
	return nil
}

// newForwardPartialUsageServiceForTest creates forwarding with the supplied
// partial-stream transport recorder.
func newForwardPartialUsageServiceForTest(upstream *anthropicHTTPUpstreamRecorder) *messageforward.Runtime {
	return newPartialRuntime(upstream, nil)
}

// newPartialRuntime builds ordinary Messages forwarding with default test
// search, health, and response limits. The optional provider store receives
// health writes, and the supplied transport is retained through the attempt.
func newPartialRuntime(upstream *anthropicHTTPUpstreamRecorder, store gatewayprovider.ExecutionProviderStore) *messageforward.Runtime {
	return messageforward.NewRuntime(messageforward.Dependencies{
		Transport: upstream,
		Search:    gatewayprovider.NewSearchTools(nil, nil),
		Health:    gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store}),
		Deferred:  &providercore.DeferredService{},
	}, messageforward.Options{Configured: true, MaxLineSize: 500 * 1024 * 1024, ResponseReadLimit: 128 * 1024 * 1024, PreserveContentType: true})
}

// newAnthropicOAuthProviderForPartialUsageTest creates a schedulable OAuth
// target with fixed test credentials.
func newAnthropicOAuthProviderForPartialUsageTest() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 501,
			Name:        "anthropic-oauth-partial-usage",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token": "oauth-token",
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
}

// TestGatewayService_Forward_StreamMissingTerminalPreservesPartialUsage
// disconnects an ordinary Fast stream after usage and content before stop. The
// partial result retains input/cache/output usage, speed, request ID, stream
// mode beside the error. The uninstrumented fixture supplies no TTFT value.
func TestGatewayService_Forward_StreamMissingTerminalPreservesPartialUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("anthropic-beta", claude.BetaFastMode)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"speed":"fast","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)

	// Simulate an aggregator that reports usage in start/delta events and
	// disconnects before stop.
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet-latest","content":[],"usage":{"input_tokens":11,"cache_read_input_tokens":7}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":null},"usage":{"output_tokens":5}}`,
		"",
		"",
	}, "\n")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-partial"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)

	ctx := requeststate.SetClaudeCodeClient(context.Background(), true)
	result, err := svc.Execute(ctx, gatewayhttp.NewMessageForwardBoundary(c, nil), newAnthropicOAuthProviderForPartialUsageTest(), parsed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result, "流中断但已观测到 usage 时必须返回部分结果")
	require.True(t, result.Stream)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.CacheReadInputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, "fast", result.Usage.Speed, "部分结果必须保留 fork 的 Fast 计费语义")
	require.Equal(t, "rid-partial", result.RequestID)
	require.Nil(t, result.Timings.TTFTMs())
}

// TestGatewayService_Forward_StreamReadErrorAfterOutputPreservesPartialUsage
// returns unexpected EOF after a complete usage event reaches output. The
// resulting stream error carries observed input and cache-creation counts for
// later completion processing.
func TestGatewayService_Forward_StreamReadErrorAfterOutputPreservesPartialUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			payload: []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9,\"cache_creation_input_tokens\":4}}}\n\n"),
			err:     io.ErrUnexpectedEOF,
		},
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)

	result, err := svc.Execute(context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), newAnthropicOAuthProviderForPartialUsageTest(), parsed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream read error")
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
}

// TestGatewayService_Forward_StreamErrorWithoutUsageReturnsNilResult ends a
// ping-only stream without terminal completion. The missing-terminal error
// returns no completion result when those frames provide no usage observation
// for the interrupted attempt.
func TestGatewayService_Forward_StreamErrorWithoutUsageReturnsNilResult(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: ping\ndata: {\"type\": \"ping\"}\n\n")),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)

	result, err := svc.Execute(context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), newAnthropicOAuthProviderForPartialUsageTest(), parsed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.Nil(t, result, "无已观测 usage 时不应生成零用量记录")
}

// TestGatewayService_Forward_FailoverErrorKeepsNilResult fails the upstream
// stream before output and usage observation. The typed failover error returns
// no result, keeping that unsuccessful attempt out of partial completion
// processing.
func TestGatewayService_Forward_FailoverErrorKeepsNilResult(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			err: errors.New("connection reset by peer"),
		},
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)

	result, err := svc.Execute(context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), newAnthropicOAuthProviderForPartialUsageTest(), parsed)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result, "failover 错误必须保持结果为 nil，防止重试成功后双重计费")
}

// TestGatewayService_AnthropicAPIKeyPassthrough_ForwardStreamMissingTerminalPreservesPartialUsage
// ends a native API key stream after usage without a terminal event. The
// returned partial result retains input/cache/output buckets, original model,
// and stream mode with the error.
func TestGatewayService_AnthropicAPIKeyPassthrough_ForwardStreamMissingTerminalPreservesPartialUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed := &requeststate.ParsedRequest{Body: requeststate.NewRequestBodyRef(body), Model: "claude-3-7-sonnet-20250219", Stream: true}
	upstreamSSE := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":9,"cache_read_input_tokens":2}}}`,
		"",
		`data: {"type":"message_delta","usage":{"output_tokens":3}}`,
		"",
		"",
	}, "\n")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-pass-partial"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)

	result, err := svc.Execute(context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), newAnthropicAPIKeyProviderForTest(), parsed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, "claude-3-7-sonnet-20250219", result.Model)
}

// TestGatewayService_Forward_PreOutputSSEOverloadedErrorUsesSemantic529
// receives an overloaded SSE error before client output despite HTTP status
// 200. Failover reports semantic 529, records global overload once, and keeps
// model-specific writes and output absent.
func TestGatewayService_Forward_PreOutputSSEOverloadedErrorUsesSemantic529(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)
	const errorJSON = `{"type":"error","error":{"details":null,"type":"overloaded_error","message":"Overloaded"},"request_id":"req_01"}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: error\ndata: " + errorJSON + "\n\n")),
	}}
	repo := &gatewayForwardErrorPolicyRepoStub{}
	svc := newPartialRuntime(upstream, repo)
	provider := newAnthropicOAuthProviderForPartialUsageTest()
	provider.Record.Credentials["temp_unschedulable_enabled"] = true
	provider.Record.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
		"error_code": float64(529), "keywords": []any{"Overloaded"}, "duration_minutes": float64(10),
	}}
	result, err := svc.Execute(context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), provider, parsed)
	require.Error(t, err)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, 529, failoverErr.StatusCode)
	require.JSONEq(t, errorJSON, string(failoverErr.ResponseBody))
	require.Equal(t, 1, repo.overloadCalls, "synthetic 529 must apply global overload cooldown")
	require.Empty(t, repo.modelRateLimitCalls, "global 529 cooldown must take precedence over custom model rules")
	require.Empty(t, rec.Body.String(), "pre-output overload must remain eligible for provider failover")
}

// TestGatewayService_Forward_PostOutputSSEOverloadedErrorKeepsExistingStatus
// receives an overloaded SSE error after message_start reaches output. The
// returned typed event retains raw error JSON, and the recorder contains the
// earlier lifecycle bytes.
func TestGatewayService_Forward_PostOutputSSEOverloadedErrorKeepsExistingStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)
	const errorJSON = `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	fixture := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n" + "event: error\ndata: " + errorJSON + "\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(fixture)),
	}}
	repo := &gatewayForwardErrorPolicyRepoStub{}
	svc := newPartialRuntime(upstream, repo)
	result, err := svc.Execute(context.Background(), gatewayhttp.NewMessageForwardBoundary(c, nil), newAnthropicOAuthProviderForPartialUsageTest(), parsed)
	require.Error(t, err)
	require.Nil(t, result)
	var sseErr *claude.StreamErrorEventError
	require.ErrorAs(t, err, &sseErr)
	require.JSONEq(t, errorJSON, sseErr.RawData)
	require.Contains(t, rec.Body.String(), "message_start")
}

// newAnthropicAPIKeyProviderForTest creates a schedulable Anthropic API key
// passthrough test target.
func newAnthropicAPIKeyProviderForTest() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 201,
			Name:        "anthropic-apikey-pass-test",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "upstream-anthropic-key",
				"base_url": "https://api.anthropic.com",
			},
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
			Status:      billing.StatusActive,
			Schedulable: true,
		},
	}
}

// Do captures the request and rebuilds its body from available fixture bytes. A
// configured error returns no response; otherwise the shared configured
// response is handed to the executor.
func (u *anthropicHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	u.lastReq = req
	if req != nil && req.Body != nil {
		// Capture available body evidence and rebuild the reader so
		// wire assertions can consume the prepared request again after
		// transport-stub inspection.
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	if u.err != nil {
		return nil, u.err
	}
	return u.resp, nil
}

// DoWithTLS delegates recording to Do while ignoring the fixture TLS profile.
func (u *anthropicHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// TestMaxReasoningPricing_AnthropicForwardReportsOutboundEffort compares xhigh
// and max across Chat and Responses requests. Recorded effort and prepared
// Anthropic output_config.effort retain their separate requested values in
// every exercised case.
func TestMaxReasoningPricing_AnthropicForwardReportsOutboundEffort(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		for _, effort := range []string{"xhigh", "max"} {
			t.Run(protocol+"/"+effort, func(t *testing.T) {
				stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":100}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
				svc := newHTTPRuntimeFixture(&messageforward.Options{Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728}, messageforward.Dependencies{Transport: upstream}, nil)
				provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.anthropic.com"}}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				var result *forwardcore.MessagesResult
				var err error
				if protocol == "responses" {
					result, err = svc.ForwardAsResponses(c.Request.Context(), c, provider, []byte(`{"model":"claude-fable-5-1","input":"hi","reasoning":{"effort":"`+effort+`"}}`), nil)
				} else {
					result, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, provider, []byte(`{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"`+effort+`"}`), nil)
				}
				require.NoError(t, err)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, effort, *result.ReasoningEffort)
				requestBody, err := io.ReadAll(upstream.lastReq.Body)
				require.NoError(t, err)
				require.Equal(t, effort, gjson.GetBytes(requestBody, "output_config.effort").String())
			})
		}
	}
}

// TestAnthropicConversionPreservesResponseModel crosses Chat/Responses,
// stream/buffered modes, and present/absent upstream declarations with a client
// alias. Result and completion capture retain the upstream declaration while
// client output uses the alias.
func TestAnthropicConversionPreservesResponseModel(t *testing.T) {
	const clientModel = "client-alias"
	const sentModel = "claude-fable-5-1"
	for _, wire := range []string{"responses", "chat"} {
		for _, stream := range []bool{false, true} {
			for _, declaration := range []struct {
				name string // table scenario name
				// value supplies the upstream model
				// declaration; nil omits the field so the
				// result retains its absent-observation
				// representation in this table case.
				value any // upstream declaration fixture value
				// expected upstream declaration text
				want string
			}{
				{name: "different", value: " claude-runtime-version ", want: "claude-runtime-version"},
				{name: "same", value: sentModel, want: sentModel},
				{name: "missing"},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", wire, stream, declaration.name), func(t *testing.T) {
					message := map[string]any{
						"id": "msg_1", "type": "message", "role": "assistant",
						"content": []any{}, "usage": map[string]any{"input_tokens": 100},
					}
					if declaration.value != nil {
						message["model"] = declaration.value
					}
					start, err := json.Marshal(map[string]any{"type": "message_start", "message": message})
					require.NoError(t, err)
					upstreamBody := "event: message_start\ndata: " + string(start) + "\n\n" +
						"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
						"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					transport := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(upstreamBody)),
					}}
					executor := newHTTPRuntimeFixture(&messageforward.Options{
						Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728,
					}, messageforward.Dependencies{Transport: transport}, nil)
					target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
						LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey,
						Credentials: map[string]any{
							"api_key": "test-key", "base_url": "https://api.anthropic.com",
							"model_mapping": map[string]any{clientModel: sentModel},
						},
					}}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+wire, nil)
					request := map[string]any{"model": clientModel, "stream": stream}
					if wire == "responses" {
						request["input"] = "hello"
					} else {
						request["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
					}
					body, err := json.Marshal(request)
					require.NoError(t, err)
					var result *forwardcore.MessagesResult
					if wire == "responses" {
						result, err = executor.ForwardAsResponses(c.Request.Context(), c, target, body, nil)
					} else {
						result, err = executor.ForwardAsChatCompletions(c.Request.Context(), c, target, body, nil)
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, clientModel, result.Model)
					require.Equal(t, sentModel, result.UpstreamModel)
					require.Equal(t, sentModel, gjson.GetBytes(transport.lastBody, "model").String())
					require.Equal(t, declaration.want, result.UpstreamResponseModel)
					require.Equal(t, 100, result.Usage.InputTokens)
					require.Equal(t, 1, result.Usage.OutputTokens)
					captured := gatewayprovider.ProjectMessagesCompletionResult(result, &target.Record)
					require.Equal(t, declaration.want, captured.UpstreamResponseModel)

					// The client sees its alias; the
					// upstream declaration is saved in the
					// completion result.
					if !stream {
						require.Equal(t, clientModel, gjson.Get(recorder.Body.String(), "model").String())
					} else {
						models := 0
						openai.ForEachOpenAISSEFrame(recorder.Body.String(), func(_ string, payload []byte) {
							for _, path := range []string{"model", "response.model"} {
								if model := gjson.GetBytes(payload, path); model.Exists() {
									models++
									require.Equal(t, clientModel, model.String())
								}
							}
						})
						require.Positive(t, models)
					}
				})
			}
		}
	}
}

// TestGatewayService_ForceClaude1hCache_ConversionRewritesOAuthMimicOnly
// forwards converted Responses and Chat requests through production runtime
// helpers with global 1h cache injection disabled. OAuth conversions rewrite
// mimic-injected ephemeral breakpoints to 1h when Key caching is forced, while
// non-OAuth API key conversions emit no synthesized cache_control fields.
func TestGatewayService_ForceClaude1hCache_ConversionRewritesOAuthMimicOnly(t *testing.T) {
	upstreamSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":25}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	protocols := []struct {
		name string // converted OpenAI protocol name
		path string // inbound gateway route path
		body []byte // OpenAI request payload with instructions and one tool
	}{
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"claude-sonnet-4-6","instructions":"be helpful","input":"hello","tools":[{"type":"function","name":"lookup","description":"lookup item","parameters":{"type":"object","properties":{}}}]}`),
		},
		{
			name: "chat",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"system","content":"be helpful"},{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","description":"lookup item","parameters":{"type":"object","properties":{}}}}]}`),
		},
	}

	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			runConversion := func(target *gatewayprovider.ExecutionProvider, force1h bool) []byte {
				transport := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
				}}
				settingService := newRuntimeSettingsFixture(&gatewayTTLSettingRepo{data: map[string]string{
					gateway.SettingKeyEnableAnthropicCacheTTL1hInjection: "false",
				}})
				executor := newHTTPRuntimeFixture(&messageforward.Options{
					Configured: true, PreserveContentType: true, ResponseReadLimit: 134217728, MaxLineSize: defaultMaxLineSize,
				}, messageforward.Dependencies{
					Transport: transport,
					Settings:  settingService,
					Deferred:  &providercore.DeferredService{},
				}, nil)

				ctx := gatewayprovider.WithAPIKeyForceClaude1hCache(context.Background(), force1h)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, proto.path, nil)
				c.Request.Header.Set("User-Agent", "openai-node/4.0.0")

				var err error
				if proto.name == "responses" {
					_, err = executor.ForwardAsResponses(ctx, c, target, proto.body, nil)
				} else {
					_, err = executor.ForwardAsChatCompletions(ctx, c, target, proto.body, nil)
				}
				require.NoError(t, err)
				return transport.lastBody
			}

			oauthTarget := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
				LoadLocation: time.LoadLocation,
				ID:           601,
				Name:         "anthropic-oauth-conversion",
				Platform:     capability.PlatformAnthropic,
				Type:         capability.ProviderTypeOAuth,
				Concurrency:  1,
				Credentials:  map[string]any{"access_token": "oauth-token"},
				Status:       billing.StatusActive,
				Schedulable:  true,
			}}

			oauthDefaultBody := runConversion(oauthTarget, false)
			require.Equal(t, "ephemeral", gjson.GetBytes(oauthDefaultBody, "system.2.cache_control.type").String())
			require.Equal(t, "5m", gjson.GetBytes(oauthDefaultBody, "system.2.cache_control.ttl").String())
			require.Equal(t, "ephemeral", gjson.GetBytes(oauthDefaultBody, "tools.0.cache_control.type").String())
			require.Equal(t, "5m", gjson.GetBytes(oauthDefaultBody, "tools.0.cache_control.ttl").String())

			oauthForcedBody := runConversion(oauthTarget, true)
			require.Equal(t, "ephemeral", gjson.GetBytes(oauthForcedBody, "system.2.cache_control.type").String())
			require.Equal(t, "1h", gjson.GetBytes(oauthForcedBody, "system.2.cache_control.ttl").String())
			require.Equal(t, "ephemeral", gjson.GetBytes(oauthForcedBody, "tools.0.cache_control.type").String())
			require.Equal(t, "1h", gjson.GetBytes(oauthForcedBody, "tools.0.cache_control.ttl").String())

			apiKeyTarget := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
				LoadLocation: time.LoadLocation,
				ID:           602,
				Name:         "anthropic-apikey-conversion",
				Platform:     capability.PlatformAnthropic,
				Type:         capability.ProviderTypeAPIKey,
				Concurrency:  1,
				Credentials:  map[string]any{"api_key": "sk-ant-test", "base_url": "https://api.anthropic.com"},
				Status:       billing.StatusActive,
				Schedulable:  true,
			}}

			apiKeyForcedBody := runConversion(apiKeyTarget, true)
			require.NotContains(t, string(apiKeyForcedBody), "cache_control")
		})
	}
}
