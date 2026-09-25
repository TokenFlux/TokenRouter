//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	sessiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/session/testkit"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	grokRateLimitRepeatCooldown = 10 * time.Minute
)

func TestForwardGrokResponsesCodexAdditionalToolsUsesMixedCacheIntent(t *testing.T) {

	body := []byte(`{
		"model":"grok",
		"stream":false,
		"prompt_cache_key":"codex-session",
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"function","name":"lookup","description":"look up a key","parameters":{"type":"object"}},
				{"type":"function","name":"web_search","description":"search","parameters":{"type":"object"}},
				{"type":"custom","name":"apply_patch"},
				{"type":"namespace","name":"collaboration"}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("X-Sub2API-Grok-Client-Tool-Cache", "prefer-cache")
	c.Set("api_key", &apikey.APIKey{ID: 4501})

	account := gatewaytestkit.HealthyGrokOAuthAccount(4501, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_codex_lite","object":"response","model":"grok-4.5","status":"completed",
			"output":[],"usage":{"input_tokens":10,"output_tokens":1}
		}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_codex_lite", result.ResponseID)
	require.False(t, gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools")`).Exists())
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 4)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "function", tools[2].Get("type").String())
	require.Equal(t, "apply_patch", tools[2].Get("name").String())
	require.Equal(t, "x_search", tools[3].Get("type").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="custom")`).Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="namespace")`).Exists())
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.Empty(t, upstream.lastReq.Header.Get("X-Sub2API-Grok-Client-Tool-Cache"))
}

func TestForwardGrokResponsesClaudeDesktopClientToolsUseCacheRoute(t *testing.T) {

	firstBody := []byte(`{
		"model":"grok","stream":false,"instructions":"You are Claude Desktop.",
		"tools":[
			{"type":"function","name":"Read","parameters":{"type":"object"}},
			{"type":"function","name":"Edit","parameters":{"type":"object"}},
			{"type":"function","name":"WebSearch","parameters":{"type":"object"}},
			{"type":"function","name":"mcp__workspace__bash","parameters":{"type":"object"}}
		],
		"input":[{"role":"user","content":[{"type":"input_text","text":"first turn"}]}]
	}`)
	secondBody := []byte(`{
		"model":"grok","stream":false,"instructions":"You are Claude Desktop.",
		"tools":[
			{"type":"function","name":"Read","parameters":{"type":"object"}},
			{"type":"function","name":"Edit","parameters":{"type":"object"}},
			{"type":"function","name":"WebSearch","parameters":{"type":"object"}},
			{"type":"function","name":"mcp__workspace__bash","parameters":{"type":"object"}}
		],
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"first turn"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"first answer"}]},
			{"role":"user","content":[{"type":"input_text","text":"second turn"}]}
		]
	}`)

	account := gatewaytestkit.HealthyGrokOAuthAccount(4504, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"resp_claude_desktop_1","object":"response","model":"grok-4.5","status":"completed",
				"output":[],"usage":{"input_tokens":30000,"output_tokens":10,"input_tokens_details":{"cached_tokens":0}}
			}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"resp_claude_desktop_2","object":"response","model":"grok-4.5","status":"completed",
				"output":[],"usage":{"input_tokens":30100,"output_tokens":12,"input_tokens_details":{"cached_tokens":28672}}
			}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	newContext := func(body []byte) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("User-Agent", "claude-cli/2.1.215 (external, claude-desktop-3p, agent-sdk/0.3.215)")
		c.Request.Header.Set("X-App", "cli")
		c.Request.Header.Set("anthropic-client-platform", "desktop_app")
		c.Request.Header.Set("X-Claude-Code-Session-Id", "claude-desktop-session")
		c.Set("api_key", &apikey.APIKey{ID: 4504})
		return c
	}

	first, err := svc.Grok.ForwardResponses(context.Background(), newContext(firstBody), account, firstBody, "grok", false, time.Now())
	require.NoError(t, err)
	second, err := svc.Grok.ForwardResponses(context.Background(), newContext(secondBody), account, secondBody, "grok", false, time.Now())
	require.NoError(t, err)

	require.Equal(t, 0, first.Usage.CacheReadInputTokens)
	require.Equal(t, 28672, second.Usage.CacheReadInputTokens)
	require.Len(t, upstream.bodies, 2)
	require.Len(t, upstream.requests, 2)
	for i := range upstream.bodies {
		tools := gjson.GetBytes(upstream.bodies[i], "tools").Array()
		require.Len(t, tools, 6)
		require.Equal(t, "Read", tools[0].Get("name").String())
		require.Equal(t, "Edit", tools[1].Get("name").String())
		require.Equal(t, "WebSearch", tools[2].Get("name").String())
		require.Equal(t, "mcp__workspace__bash", tools[3].Get("name").String())
		require.Equal(t, "web_search", tools[4].Get("type").String())
		require.Equal(t, "x_search", tools[5].Get("type").String())
		require.False(t, gjson.GetBytes(upstream.bodies[i], "tool_choice").Exists())
		require.Empty(t, upstream.requests[i].Header.Get("X-App"))
		require.Empty(t, upstream.requests[i].Header.Get("anthropic-client-platform"))
		require.Empty(t, upstream.requests[i].Header.Get("X-Claude-Code-Session-Id"))
	}
	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.Equal(t, firstIdentity, upstream.requests[0].Header.Get(GrokConversationIDHeader))
	require.Equal(t, secondIdentity, upstream.requests[1].Header.Get(GrokConversationIDHeader))
}

func TestCodexUnsupportedAdditionalToolsDoNotBecomeToolFreeCacheIntent(t *testing.T) {
	body := []byte(`{
		"model":"grok","tool_choice":"auto",
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"custom","name":"apply_patch"},
				{"type":"namespace","name":"collaboration"}
			]},
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())

	mixedCacheIntent := patched
	patched, err = grok.ApplyGrokResponsesCacheIdentity(patched, body, "isolated-id", true)
	require.NoError(t, err)
	account := gatewaytestkit.HealthyGrokOAuthAccount(4502, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	patched, err = ApplyGrokFreeRequestToolCacheRoute(nil, patched, mixedCacheIntent, account, "isolated-id")

	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.Equal(t, "isolated-id", gjson.GetBytes(patched, "prompt_cache_key").String())
}

func TestForwardGrokResponsesCompactRoundTrip(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"compact this"}]}],"metadata":{"large_id":9007199254740993},"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 71031,
		Name:        "grok-compact-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_grok_compact",
			"object":"response",
			"status":"completed",
			"model":"grok-4.5",
			"output":[
				{"type":"reasoning","summary":[],"encrypted_content":"compact-state"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"compact summary"}]}
			],
			"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}
		}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "resp_grok_compact", result.ResponseID)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, "grok", result.BillingModel)
	require.Equal(t, grok.DefaultResponsesModel, result.UpstreamModel)
	require.Equal(t, grok.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(upstream.lastBody, "include.0").String())
	require.Equal(t, "compact this", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.1.content.0.text").String(), "Primary Request and Intent")
	// Grok Responses 不支持 OpenAI metadata，兼容层在出站前明确剥离该字段。
	require.False(t, gjson.GetBytes(upstream.lastBody, "metadata").Exists())
	require.Equal(t, "compaction", gjson.Get(recorder.Body.String(), "output.0.type").String())
	require.Equal(t, "compact-state", gjson.Get(recorder.Body.String(), "output.0.encrypted_content").String())
	require.Equal(t, "compact summary", gjson.Get(recorder.Body.String(), "output.0.summary.0.text").String())
}

func TestForwardGrokMediaImagesGenerationNormalizesImagineAlias(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine","prompt":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 61,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-image-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.test/cat.png"}]}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, http.MethodPost, upstream.lastReq.Method)
	require.Equal(t, "Bearer api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, grok.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.JSONEq(t, `{"model":"grok-imagine-image-quality","prompt":"draw a cat"}`, string(upstream.lastBody))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"data":[{"url":"https://images.test/cat.png"}]}`, recorder.Body.String())
	require.Equal(t, "xai-image-req", result.RequestID)
	require.Equal(t, "grok-imagine-image-quality", result.Model)
	require.Equal(t, "grok-imagine-image-quality", result.BillingModel)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, pricing.ImageBillingSize2K, result.ImageSize)
}

func TestForwardGrokMediaImagesGenerationRejectsEmptySuccessfulResponse(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-image","prompt":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.JSONEq(t, `{"data":[]}`, string(failoverErr.ResponseBody))
	require.Empty(t, recorder.Body.String())
}

func TestForwardGrokMediaAppliesAccountModelMappingAfterEndpointNormalization(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	tests := []struct {
		name             string
		endpoint         grok.GrokMediaEndpoint
		path             string
		body             string
		modelMapping     map[string]any
		wantRequestModel string
		wantBillingModel string
		wantUpstream     string
		wantBody         string
		responseBody     string
	}{
		{
			name:             "image generation maps normalized image alias",
			endpoint:         grok.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw a cat"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "vendor-image-model"},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "vendor-image-model",
			wantUpstream:     "vendor-image-model",
			wantBody:         `{"model":"vendor-image-model","prompt":"draw a cat"}`,
			responseBody:     `{"data":[{"url":"https://images.test/mapped.png"}]}`,
		},
		{
			name:             "video generation maps explicit text-only model",
			endpoint:         grok.GrokMediaEndpointVideosGenerations,
			path:             "/v1/videos/generations",
			body:             `{"model":"grok-imagine-video-1.5","prompt":"waves"}`,
			modelMapping:     map[string]any{"grok-imagine-video-1.5": "grok-image-video"},
			wantRequestModel: "grok-imagine-video-1.5",
			wantBillingModel: "grok-image-video",
			wantUpstream:     "grok-image-video",
			wantBody:         `{"model":"grok-image-video","prompt":"waves"}`,
			responseBody:     `{"request_id":"video-request-mapped"}`,
		},
		{
			name:             "image-to-video preserves then maps the requested model",
			endpoint:         grok.GrokMediaEndpointVideosGenerations,
			path:             "/v1/videos/generations",
			body:             `{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"url":"https://example.com/input.png"}}`,
			modelMapping:     map[string]any{"grok-imagine-video-1.5": "vendor-image-video"},
			wantRequestModel: "grok-imagine-video-1.5",
			wantBillingModel: "vendor-image-video",
			wantUpstream:     "vendor-image-video",
			wantBody:         `{"model":"vendor-image-video","prompt":"animate","image":{"url":"https://example.com/input.png"}}`,
			responseBody:     `{"request_id":"image-video-request-mapped"}`,
		},
		{
			name:             "mapping and image sanitization compose",
			endpoint:         grok.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw","size":"1024x1024"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "vendor-image-model"},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "vendor-image-model",
			wantUpstream:     "vendor-image-model",
			wantBody:         `{"model":"vendor-image-model","prompt":"draw","resolution":"1k","aspect_ratio":"1:1"}`,
			responseBody:     `{"data":[{"url":"https://images.test/mapped.png"}]}`,
		},
		{
			name:             "whitespace mapping target safely preserves normalized model",
			endpoint:         grok.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "   "},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "grok-imagine-image-quality",
			wantUpstream:     "grok-imagine-image-quality",
			wantBody:         `{"model":"grok-imagine-image-quality","prompt":"draw"}`,
			responseBody:     `{"data":[{"url":"https://images.test/mapped.png"}]}`,
		},
		{
			name:             "account mapping target continues through builtin normalization",
			endpoint:         grok.GrokMediaEndpointImagesGenerations,
			path:             "/v1/images/generations",
			body:             `{"model":"grok-imagine","prompt":"draw"}`,
			modelMapping:     map[string]any{"grok-imagine-image-quality": "grok-build"},
			wantRequestModel: "grok-imagine-image-quality",
			wantBillingModel: "grok-build",
			wantUpstream:     "grok-build-0.1",
			wantBody:         `{"model":"grok-build-0.1","prompt":"draw"}`,
			responseBody:     `{"data":[{"url":"https://images.test/normalized.png"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66,
				Name:        "grok-mapped",
				Platform:    capability.PlatformGrok,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":       "api-key",
					"base_url":      "https://xai.test/v1",
					"model_mapping": tt.modelMapping,
				}},
			}
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

			result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, tt.endpoint, "", []byte(tt.body), "application/json")

			require.NoError(t, err)
			require.JSONEq(t, tt.wantBody, string(upstream.lastBody))
			require.Equal(t, tt.wantRequestModel, result.Model)
			require.Equal(t, tt.wantBillingModel, result.BillingModel)
			require.Equal(t, tt.wantUpstream, result.UpstreamModel)
		})
	}
}

func TestForwardGrokMediaImagesGenerationStripsUnsupportedSize(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-image","prompt":"draw a cat","size":"1024x1024"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 65,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "api-key",
			"base_url":      "https://xai.test/v1",
			"model_mapping": map[string]any{"grok-imagine-edit": "vendor-image-edit"},
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.test/cat.png"}]}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"grok-imagine-image","prompt":"draw a cat","resolution":"1k","aspect_ratio":"1:1"}`, string(upstream.lastBody))
	require.False(t, gjson.GetBytes(upstream.lastBody, "size").Exists())
	require.Equal(t, pricing.ImageBillingSize1K, result.ImageSize)
	require.Equal(t, "1024x1024", result.ImageInputSize)
}

func TestForwardGrokMediaImagesEditMultipartConvertsToJSON(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("model", "grok-imagine-edit"))
	require.NoError(t, writer.WriteField("prompt", "edit this private image"))
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="input.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(buf.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 62,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "api-key",
			"base_url":      "https://xai.test/v1",
			"model_mapping": map[string]any{"grok-imagine-edit": "vendor-image-edit"},
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://images.test/edited.png"}]}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointImagesEdits, "", buf.Bytes(), writer.FormDataContentType())
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/images/edits", upstream.lastReq.URL.String())
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.True(t, json.Valid(upstream.lastBody))
	require.Equal(t, "vendor-image-edit", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "edit this private image", gjson.GetBytes(upstream.lastBody, "prompt").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "image.url").String(), "data:image/png;base64,"))
	require.False(t, gjson.GetBytes(upstream.lastBody, "image.image_url").Exists())
	require.Equal(t, "vendor-image-edit", result.BillingModel)
	require.Equal(t, "vendor-image-edit", result.UpstreamModel)
}

func TestForwardGrokMediaVideoGenerationReturnsUsageAndResponseID(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"waves","resolution":"720p","duration":10}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-video-generate-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"request_id":"video-request-123","usage":{"prompt_tokens":3,"completion_tokens":4}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/videos/generations", upstream.lastReq.URL.String())
	require.JSONEq(t, `{"model":"grok-imagine-video-1.5","prompt":"waves","resolution":"720p","duration":10}`, string(upstream.lastBody))
	require.Equal(t, "video-request-123", result.ResponseID)
	require.Equal(t, "grok-imagine-video-1.5", result.BillingModel)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	// 创建阶段只受理任务，在状态返回 video.url 前 VideoCount 保持为零。
	require.Equal(t, 0, result.ImageCount)
	require.Empty(t, result.ImageSize)
	require.Equal(t, 0, result.VideoCount)
	require.Equal(t, pricing.VideoBillingResolution720P, result.VideoResolution)
	require.Equal(t, 10, result.VideoDurationSeconds)
}

func TestForwardGrokMediaVideoGenerationReturnsTaskIDAsResponseID(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video","prompt":"waves"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"video-task-123"}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "video-task-123", result.ResponseID)
}

func TestForwardGrokMediaVideoGenerationPreservesImageToVideoModel(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"image_url":"data:image/png;base64,aW1n"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 63,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"request_id":"video-request-456"}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/videos/generations", upstream.lastReq.URL.String())
	require.JSONEq(t, `{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"url":"data:image/png;base64,aW1n"}}`, string(upstream.lastBody))
	require.Equal(t, "video-request-456", result.ResponseID)
	require.Equal(t, "grok-imagine-video-1.5", result.BillingModel)
	// 未指定 duration 时按上游默认 8 秒计费。
	require.Equal(t, pricing.VideoBillingDefaultDurationSeconds, result.VideoDurationSeconds)
}

func TestForwardGrokMediaOAuthImageToVideoUsesOfficialAPIForLargeBody(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	imageData := strings.Repeat("A", 2*1024*1024)
	body := []byte(`{"model":"grok-imagine-video-1.5","prompt":"animate","image":{"image_url":"data:image/png;base64,` + imageData + `"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 66,
		Name:        "grok-oauth",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "oauth-access-token",
			"refresh_token": "oauth-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"base_url":      grok.DefaultCLIBaseURL,
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"request_id":"video-request-oauth"}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(nil, nil), transport: upstream})

	_, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointVideosGenerations, "", body, "application/json")
	require.NoError(t, err)
	require.Equal(t, grok.DefaultBaseURL+"/videos/generations", upstream.lastReq.URL.String())
	require.Empty(t, upstream.lastReq.Header.Get("X-XAI-Token-Auth"))
	require.Empty(t, upstream.lastReq.Header.Get("x-grok-client-version"))
	require.Equal(t, "data:image/png;base64,"+imageData, gjson.GetBytes(upstream.lastBody, "image.url").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "image.image_url").Exists())
}

func TestForwardGrokMediaVideoStatusUsesGETWithoutBody(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/request-123", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 62,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://xai.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-video-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"request-123","status":"completed"}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointVideoStatus, "request-123", nil, "")
	require.NoError(t, err)
	require.Equal(t, "https://xai.test/v1/videos/request-123", upstream.lastReq.URL.String())
	require.Equal(t, http.MethodGet, upstream.lastReq.Method)
	require.Equal(t, "Bearer api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, grok.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.Empty(t, upstream.lastReq.Header.Get("Content-Type"))
	require.Empty(t, upstream.lastBody)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"id":"request-123","status":"completed"}`, recorder.Body.String())
	require.Equal(t, "xai-video-req", result.RequestID)
}

func TestForwardGrokMediaVideoMutationEndpoints(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	tests := []struct {
		name     string
		endpoint grok.GrokMediaEndpoint
		path     string
	}{
		{name: "edit", endpoint: grok.GrokMediaEndpointVideosEdits, path: "/videos/edits"},
		{name: "extension", endpoint: grok.GrokMediaEndpointVideosExtensions, path: "/videos/extensions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := []byte(`{"model":"grok-imagine-video","prompt":"continue","video":{"url":"https://example.com/in.mp4"},"duration":6}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1"+tt.path, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 71, Name: "grok", Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{
					"api_key":       "api-key",
					"base_url":      "https://xai.test/v1",
					"model_mapping": map[string]any{"grok-imagine-video": "vendor-video-mutation"},
				}},
			}
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"request_id":"video-mutation-123"}`)),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

			result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, tt.endpoint, "", body, "application/json")
			require.NoError(t, err)
			require.Equal(t, "https://xai.test/v1"+tt.path, upstream.lastReq.URL.String())
			require.Equal(t, http.MethodPost, upstream.lastReq.Method)
			require.JSONEq(t, `{"model":"vendor-video-mutation","prompt":"continue","video":{"url":"https://example.com/in.mp4"},"duration":6}`, string(upstream.lastBody))
			require.Equal(t, "video-mutation-123", result.ResponseID)
			require.Equal(t, 0, result.VideoCount)
			require.Equal(t, 6, result.VideoDurationSeconds)
			require.Equal(t, "vendor-video-mutation", result.BillingModel)
			require.Equal(t, "vendor-video-mutation", result.UpstreamModel)
		})
	}
}

func TestGrokMediaVideoRequestBindingIsScopedToUserAndAPIKey(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/video-request-123", nil)
	c.Request.Header.Set("session_id", "shared-client-session")
	groupID := int64(7)
	cache := &sessiontestkit.StickyCache{}
	tasks := media.NewVideoTasks(cache, nil, media.VideoOptions{})
	const userID int64 = 41
	const apiKeyID int64 = 51
	require.NotEmpty(t, GenerateExplicitOpenAISessionHash(c, nil))
	ctx := c.Request.Context()

	hash := media.GrokMediaVideoRequestSessionHash("video-request-123", userID, apiKeyID)
	require.NotEmpty(t, hash)
	require.NoError(t, tasks.BindGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID, apiKeyID, 63))

	accountID, err := tasks.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID, apiKeyID)
	require.NoError(t, err)
	require.Equal(t, int64(63), accountID)

	accountID, err = tasks.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID+1, apiKeyID)
	require.Error(t, err)
	require.Zero(t, accountID)

	accountID, err = tasks.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, "video-request-123", userID, apiKeyID+1)
	require.Error(t, err)
	require.Zero(t, accountID)
}

func TestForwardGrokMedia429ReconcilesRateLimitBeforeCustomErrorBypass(t *testing.T) {
	t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-imagine","prompt":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 64,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":                    "api-key",
			"base_url":                   "https://xai.test/v1",
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusBadRequest)},
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-error-req"},
			"Retry-After":    []string{"45"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"do not expose this upstream detail"}}`)),
	}}
	repo := &grokQuotaAccountRepo{}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream, accounts: repo})

	result, err := svc.Grok.ForwardGrokMedia(context.Background(), c, account, grok.GrokMediaEndpointImagesGenerations, "", body, "application/json")
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream gateway error")
	require.NotContains(t, recorder.Body.String(), "do not expose")
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, httpFixtureRuntimeBlocked(svc, account))
}

func TestGrokMedia429FailoverPreservesRetryAfter(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 641, Name: "grok-oauth", Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusTooManyRequests)},
		}},
	}
	repo := &grokQuotaAccountRepo{}
	svc := newResponsesFixture(responsesFixtureInputs{accounts: repo})
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}

	result, err := mediaErrorResponseFixture(svc.Grok, context.Background(), resp, c, account, "request-id", "grok-imagine")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, "45", http.Header(failoverErr.ResponseHeaders).Get("Retry-After"))
}

func TestForwardAsChatCompletionsForGrokStopFallsBackToXAIChatCompletions(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false,"stop":"done","prompt_cache_key":"raw-client-cache-key"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5101})

	account := gatewaytestkit.HealthyGrokOAuthAccount(51, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{51: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"application/json"},
			"Xai-Request-Id":                 []string{"xai-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"9"},
			"X-Ratelimit-Limit-Tokens":       []string{"1000"},
			"X-Ratelimit-Remaining-Tokens":   []string{"990"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl","object":"chat.completion","model":"grok-4.3","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, grok.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.NotEmpty(t, upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.NotEqual(t, "raw-client-cache-key", upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
	require.Equal(t, "grok", result.Model)
	require.Equal(t, "grok-4.5", result.UpstreamModel)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.NotNil(t, repo.updates[51]["grok_usage_snapshot"])
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestForwardGrokResponsesStreamingDefaultsEmptyModelTo45AndSnapshots(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"input":"hi","stream":true,"reasoning_effort":"high"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("OpenAI-Beta", "responses=experimental")
	c.Set("api_key", &apikey.APIKey{ID: 5201})

	account := gatewaytestkit.HealthyGrokOAuthAccount(52, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{52: account},
		},
	}
	upstreamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_grok","model":"grok-4.3","usage":{"input_tokens":5,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"Xai-Request-Id":                 []string{"xai-stream-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"8"},
			"X-Ratelimit-Limit-Tokens":       []string{"1000"},
			"X-Ratelimit-Remaining-Tokens":   []string{"990"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "", true, time.Now())
	require.NoError(t, err)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "responses=experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "x_search", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.True(t, result.Stream)
	require.Equal(t, "resp_grok", result.ResponseID)
	require.Equal(t, "xai-stream-req", result.RequestID)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), "response.output_text.delta")
	require.NotNil(t, repo.updates[52]["grok_usage_snapshot"])
}

func TestForwardGrokResponsesAPIKeyUsesXAIResponses(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":"hi","metadata":{"session_id":"abc"},"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 53,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstreamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_grok_api_key","model":"grok-4.5","usage":{"input_tokens":2,"output_tokens":1}}}`,
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", true, time.Now())
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer xai-test-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, grok.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "metadata").Exists())
	require.Equal(t, "resp_grok_api_key", result.ResponseID)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
}

func TestForwardGrokResponsesUsesMetadataSessionForCacheIdentityWithoutForwardingMetadata(t *testing.T) {

	firstBody := []byte(`{"model":"grok","input":"first turn","metadata":{"user_id":"{\"session_id\":\"metadata-session\"}"},"stream":false}`)
	secondBody := []byte(`{"model":"grok","input":"different second turn","metadata":{"user_id":"{\"session_id\":\"metadata-session\"}"},"stream":false}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5401,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_first","object":"response","model":"grok-4.6","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_second","object":"response","model":"grok-4.6","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})
	newContext := func(body []byte) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Set("api_key", &apikey.APIKey{ID: 5401})
		return c
	}

	_, err := svc.Grok.ForwardResponses(context.Background(), newContext(firstBody), account, firstBody, "grok", false, time.Now())
	require.NoError(t, err)
	_, err = svc.Grok.ForwardResponses(context.Background(), newContext(secondBody), account, secondBody, "grok", false, time.Now())
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)

	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.False(t, gjson.GetBytes(upstream.bodies[0], "metadata").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "metadata").Exists())
}

func TestForwardGrokResponsesRetriesInvalidEncryptedContentOnce(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok",
		"previous_response_id":"resp_valid_history",
		"input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"keep this summary"}],"encrypted_content":"encrypted-reasoning"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
		],
		"metadata":{"large_id":9007199254740993},
		"stream":false
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 4535})

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4535,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "same-token",
			"base_url": "https://api.x.ai/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Xai-Request-Id": []string{"recoverable-first"},
			},
			Body: io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content. Ensure the value is unmodified."}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Xai-Request-Id": []string{"recovered-second"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"resp_recovered","object":"response","model":"grok-4.5","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_recovered", result.ResponseID)
	require.Equal(t, "recovered-second", result.RequestID)
	require.Len(t, upstream.requests, 2)
	require.Len(t, upstream.bodies, 2)

	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[0], "input.0.type").String())
	require.Equal(t, "resp_valid_history", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
	require.Equal(t, "encrypted-reasoning", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.Equal(t, "resp_valid_history", gjson.GetBytes(upstream.bodies[1], "previous_response_id").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
	require.Equal(t, "keep this summary", gjson.GetBytes(upstream.bodies[1], "input.0.summary.0.text").String())
	require.Equal(t, "message", gjson.GetBytes(upstream.bodies[1], "input.1.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "metadata").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "metadata").Exists())

	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	for _, req := range upstream.requests {
		require.Equal(t, "Bearer same-token", req.Header.Get("Authorization"))
		require.Equal(t, firstIdentity, req.Header.Get(GrokConversationIDHeader))
	}
	require.Equal(t, billing.StatusActive, account.Record.Status)
	_, hasUpstreamErrors := c.Get(OpsUpstreamErrorsKey)
	require.False(t, hasUpstreamErrors)
	_, hasTerminalStatus := c.Get(OpsUpstreamStatusCodeKey)
	require.False(t, hasTerminalStatus)
}

func TestForwardGrokResponsesInvalidEncryptedContentRecoveryDoesNotOvermatch(t *testing.T) {

	matchingError := `{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}`
	tests := []struct {
		name         string
		requestBody  string
		responseBody string
	}{
		{
			name:         "different top-level code",
			requestBody:  `{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"}],"stream":false}`,
			responseBody: `{"code":"bad-request","error":"Could not decrypt the provided encrypted_content."}`,
		},
		{
			name:         "message does not mention decryption",
			requestBody:  `{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"}],"stream":false}`,
			responseBody: `{"code":"invalid-argument","error":"The provided encrypted_content is invalid."}`,
		},
		{
			name:         "request has no encrypted reasoning",
			requestBody:  `{"model":"grok","input":[{"type":"message","role":"user","content":"hi"}],"stream":false}`,
			responseBody: matchingError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := []byte(tt.requestBody)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4536,
				Name:        "grok-api-key",
				Platform:    capability.PlatformGrok,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "token", "base_url": "https://api.x.ai/v1"}},
			}
			upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
			}}}
			svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

			result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
			require.Nil(t, result)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
			require.Len(t, upstream.bodies, 1)
		})
	}
}

func TestForwardGrokResponsesInvalidEncryptedContentRecoveryNestedErrorShape(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"message","role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4538,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "token", "base_url": "https://api.x.ai/v1"}},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":{"message":"Could not decrypt the provided encrypted_content."}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
}

func TestForwardGrokResponsesInvalidEncryptedContentRetryFailureIsTerminal(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"message","role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4537,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "same-token", "base_url": "https://api.x.ai/v1"}},
	}
	newInvalidEncryptedResponse := func(requestID string) *http.Response {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Xai-Request-Id": []string{requestID},
			},
			Body: io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}`)),
		}
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		newInvalidEncryptedResponse("recoverable-first"),
		newInvalidEncryptedResponse("terminal-second"),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{transport: upstream})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.Nil(t, result)
	require.Error(t, err)
	require.Len(t, upstream.requests, 2)
	require.Len(t, upstream.bodies, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], `input.#(type=="reasoning")`).Exists())

	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, events)
	for _, event := range events {
		require.NotEqual(t, "recoverable-first", event.UpstreamRequestID)
	}
	require.Equal(t, http.StatusBadRequest, c.GetInt(OpsUpstreamStatusCodeKey))
}

func TestForwardAsChatCompletionsForGrokAPIKeyUsesConfiguredRawEndpointWithoutOAuthIdentity(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 706,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "third-party-key",
			"base_url": "https://grok.example.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl","object":"chat.completion","model":"grok-4.5","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})

	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, "https://grok.example.test/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer third-party-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEqual(t, grok.DefaultGrokUpstreamUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
}

func TestForwardAsChatCompletionsForGrokAPIKeyRejectsNonStreamingResponseWithoutUsage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 707,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "third-party-key",
			"base_url": "https://grok.example.test/v1",
		}},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Xai-Request-Id": []string{"rid-grok-raw-missing-usage"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_missing_usage","object":"chat.completion","model":"grok-4.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
		)),
	}}
	service := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})

	result, err := service.Text.Chat(context.Background(), c, account, body, "", "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Equal(t, "grok_missing_usage", gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
	require.Equal(t, "rid-grok-raw-missing-usage", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestForwardAsChatCompletionsForGrokStreamingUsesRawXAIChatCompletions(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := gatewaytestkit.HealthyGrokOAuthAccount(53, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{53: account},
		},
	}
	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[],"usage":{"prompt_tokens":6,"completion_tokens":4,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":1}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-Request-Id":                   []string{"chat-stream-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"7"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), options: protocolHTTPOptions(), transport: upstream, accounts: repo})

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, grok.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, grok.CLIUserAgent(grok.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.True(t, result.Stream)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
	require.NotNil(t, repo.updates[53]["grok_usage_snapshot"])
}

func TestForwardGrokResponsesNonStreamingUsesCacheIdentityAndCachedUsage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":"hi","stream":false,"tools":[{"type":"namespace","name":"client_tools"}],"tool_choice":{"type":"namespace","name":"client_tools"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &apikey.APIKey{ID: 5202})

	account := gatewaytestkit.HealthyGrokOAuthAccount(56, "access-token")
	observedResetAt := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	observedLimitedAt := observedResetAt.Add(-grokRateLimitRepeatCooldown)
	account.Record.RateLimitedAt = &observedLimitedAt
	account.Record.RateLimitResetAt = &observedResetAt
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{56: account},
		},
		recoveryClearResult: true,
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Xai-Request-Id": []string{"xai-non-stream-req"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp_grok_non_stream","object":"response","model":"grok-4.3","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9,"input_tokens_details":{"cached_tokens":4}}}`)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "resp_grok_non_stream", result.ResponseID)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(GrokConversationIDHeader))
	// 清理器会移除不受支持的客户端工具，但明确的工具意图仍必须阻止注入原生缓存路由工具。
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
	require.Equal(t, "resp_grok_non_stream", gjson.Get(recorder.Body.String(), "id").String())
	require.Equal(t, 1, repo.recoveryClearCalls)
	require.Equal(t, observedLimitedAt, repo.recoveryObservedAt)
	require.Equal(t, observedResetAt, repo.recoveryObservedReset)
}

// 原生 Responses 的 Free OAuth 函数工具请求必须在实际转发前补齐可缓存原生工具。
func TestForwardGrokResponsesFreeFunctionToolsUseCacheCapableMixedRoute(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok","input":"look up alpha","stream":false,
		"tools":[
			{"type":"function","name":"lookup","parameters":{"type":"object"}},
			{"type":"function","name":"web_search","parameters":{"type":"object"}}
		],
		"tool_choice":"auto"
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5204})

	account := gatewaytestkit.HealthyGrokOAuthAccount(60, "access-token")
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{60: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_grok_tools","object":"response","model":"grok-4.5","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1}}`,
		)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
}

func TestForwardGrokResponsesFailoverKeepsCacheIdentityAcrossAccounts(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"role":"user","content":"stable prefix"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5203})

	newAccount := func(id int64, token string) *gatewayprovider.ExecutionAccount {
		account := gatewaytestkit.HealthyGrokOAuthAccount(id, token)
		account.Record.Name = fmt.Sprintf("grok-%d", id)
		return account
	}
	firstAccount := newAccount(58, "access-token-a")
	secondAccount := newAccount(59, "access-token-b")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{58: firstAccount, 59: secondAccount},
		},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_after_failover","object":"response","model":"grok-4.3","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1}}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	_, err := svc.Grok.ForwardResponses(context.Background(), c, firstAccount, body, "grok", false, time.Now())
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)

	result, err := svc.Grok.ForwardResponses(context.Background(), c, secondAccount, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Len(t, upstream.bodies, 2)
	firstIdentity := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondIdentity := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstIdentity)
	require.Equal(t, firstIdentity, secondIdentity)
	require.Equal(t, firstIdentity, upstream.requests[0].Header.Get(GrokConversationIDHeader))
	require.Equal(t, secondIdentity, upstream.requests[1].Header.Get(GrokConversationIDHeader))
	require.Equal(t, "Bearer access-token-a", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer access-token-b", upstream.requests[1].Header.Get("Authorization"))
}

func TestForwardAsChatCompletionsForGrokStreamingStopFallsBackToRawXAIChatCompletions(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true,"stop":"done"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set(GrokConversationIDHeader, "native-client-conversation")
	c.Set("api_key", &apikey.APIKey{ID: 5301})

	account := gatewaytestkit.HealthyGrokOAuthAccount(53, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{53: account},
		},
	}
	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_grok","object":"chat.completion.chunk","model":"grok-4.3","choices":[],"usage":{"prompt_tokens":6,"completion_tokens":4,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":1}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-Request-Id":                   []string{"chat-stream-req"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"7"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), options: protocolHTTPOptions(), transport: upstream, accounts: repo})

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, grok.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, grok.CLIUserAgent(grok.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, grok.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.NotEmpty(t, upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.NotEqual(t, "native-client-conversation", upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.True(t, result.Stream)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
	require.NotNil(t, repo.updates[53]["grok_usage_snapshot"])
}

func TestForwardAsChatCompletionsForGrokComposerBridgesImageInput(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok-composer-2.5-fast","messages":[{"role":"system","content":"You are concise."},{"role":"user","content":[{"type":"text","text":"What is shown?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}}]}],"metadata":{"large_id":9007199254740993},"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &apikey.APIKey{ID: 5501})

	account := gatewaytestkit.HealthyGrokOAuthAccount(55, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{55: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "xai-request-id": []string{"vision-req"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_vision","object":"response","model":"grok-build-0.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A small diagram with ABC letters."}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":                   []string{"application/json"},
				"X-Request-Id":                   []string{"composer-req"},
				"X-Ratelimit-Limit-Requests":     []string{"10"},
				"X-Ratelimit-Remaining-Requests": []string{"9"},
				"X-Ratelimit-Limit-Tokens":       []string{"1000"},
				"X-Ratelimit-Remaining-Tokens":   []string{"980"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_composer","object":"chat.completion","model":"grok-composer-2.5-fast","choices":[{"index":0,"message":{"role":"assistant","content":"It shows ABC."},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)),
		},
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), options: protocolHTTPOptions(), transport: upstream, accounts: repo})

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.requests[0].URL.String())
	require.Empty(t, upstream.requests[0].Header.Get(GrokConversationIDHeader))
	require.Equal(t, "grok-build-0.1", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "input_image", gjson.GetBytes(upstream.bodies[0], "input.0.content.1.type").String())
	require.Equal(t, grok.DefaultCLIBaseURL+"/chat/completions", upstream.requests[1].URL.String())
	require.NotEmpty(t, upstream.requests[1].Header.Get(GrokConversationIDHeader))
	require.Equal(t, "grok-composer-2.5-fast", gjson.GetBytes(upstream.bodies[1], "model").String())
	require.False(t, strings.Contains(string(upstream.bodies[1]), "image_url"))
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[1], "metadata.large_id").Raw)
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "messages.1.content").String(), "Image 1 description")
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "messages.1.content").String(), "A small diagram with ABC letters.")
	require.Equal(t, 14, result.Usage.InputTokens)
	require.Equal(t, 12, result.Usage.OutputTokens)
	require.Equal(t, "It shows ABC.", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	require.NotNil(t, repo.updates[55]["grok_usage_snapshot"])
}

// Codex 身份恢复只能作用于 OpenAI OAuth，Grok Messages 必须保持自己的请求头和端点。
func TestForwardAsAnthropicForGrokUsesXAIResponses(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","max_tokens":32,"stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("OpenAI-Beta", "grok-experimental")
	c.Request.Header.Set("originator", "opencode")
	c.Set("api_key", &apikey.APIKey{ID: 5401})

	account := gatewaytestkit.HealthyGrokOAuthAccount(54, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{54: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{resp: grokMessagesSSECompletedResponse("resp_grok_messages", 3)}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, grok.CLIUserAgent(grok.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "grok-experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Empty(t, upstream.lastReq.Header.Get("originator"))
	require.Empty(t, upstream.lastReq.Header.Get("version"))
	require.Equal(t, grok.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "x_search", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	require.Empty(t, upstream.lastReq.Header.Get("session_id"))
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.NotContains(t, string(upstream.lastBody), "chatgpt.com")
	require.Equal(t, "grok", result.Model)
	require.Equal(t, "grok", result.BillingModel)
	require.Equal(t, "grok-4.5", result.UpstreamModel)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Body.String(), `"type":"message"`)
	require.Equal(t, int64(3), gjson.Get(recorder.Body.String(), "usage.cache_read_input_tokens").Int())
	require.Contains(t, recorder.Body.String(), "ok")
}

// Grok Messages 在账号缓存身份变化后应剥离旧推理密文，并通过同一路由重试一次。
func TestForwardAsAnthropicForGrokRetriesInvalidEncryptedContentOnce(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok","max_tokens":32,"stream":false,
		"messages":[
			{"role":"user","content":"plan a command"},
			{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"enc-old-account"},{"type":"text","text":"run it"}]},
			{"role":"user","content":"continue"}
		]
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5404})

	account := gatewaytestkit.HealthyGrokOAuthAccount(59, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{59: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Could not decrypt the provided encrypted_content."}}`)),
		},
		grokMessagesSSECompletedResponse("resp_grok_messages_retry", 1),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.True(t, gatewayprovider.GrokBodyCodec().RequestHasGrokEncryptedReasoning(upstream.bodies[0]))
	require.False(t, gatewayprovider.GrokBodyCodec().RequestHasGrokEncryptedReasoning(upstream.bodies[1]))
	require.Equal(t, upstream.requests[0].URL.String(), upstream.requests[1].URL.String())
	require.Contains(t, recorder.Body.String(), "ok")
}

func TestForwardAsAnthropicForGrokFunctionToolUsesCacheCapableMixedRoute(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{
		"model":"grok","max_tokens":32,"stream":false,
		"messages":[{"role":"user","content":"look up alpha"}],
		"tools":[{"name":"lookup","description":"look up a key","input_schema":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}},{"name":"web_search","description":"search the web","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],
		"tool_choice":{"type":"auto"}
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5403})

	account := gatewaytestkit.HealthyGrokOAuthAccount(58, "access-token")
	account.Record.Extra = map[string]any{accountcore.GrokUsageBillingExtraKey: map[string]any{
		"status_code":        http.StatusOK,
		"source":             "billing_probe",
		"monthly_updated_at": "2026-07-15T05:00:00Z",
	}}
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{58: account},
		},
	}
	responseBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_grok_function","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"function_call","id":"fc_lookup","call_id":"call_lookup","name":"lookup","arguments":"{\"key\":\"alpha\"}","status":"completed"}],"usage":{"input_tokens":7000,"output_tokens":2,"total_tokens":7002,"input_tokens_details":{"cached_tokens":6144}}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, grok.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(GrokConversationIDHeader))
	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "object", tools[0].Get("parameters.type").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())

	require.Equal(t, 7000, result.Usage.InputTokens)
	require.Equal(t, 6144, result.Usage.CacheReadInputTokens)
	clientBody := recorder.Body.String()
	require.Equal(t, "tool_use", gjson.Get(clientBody, "content.0.type").String())
	require.Equal(t, "call_lookup", gjson.Get(clientBody, "content.0.id").String())
	require.Equal(t, "lookup", gjson.Get(clientBody, "content.0.name").String())
	require.Equal(t, "alpha", gjson.Get(clientBody, "content.0.input.key").String())
	require.Equal(t, "tool_use", gjson.Get(clientBody, "stop_reason").String())
	require.Equal(t, int64(856), gjson.Get(clientBody, "usage.input_tokens").Int())
	require.Equal(t, int64(6144), gjson.Get(clientBody, "usage.cache_read_input_tokens").Int())
}

func TestForwardAsAnthropicForGrokStreamingPreservesCacheUsage(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 5402})

	account := gatewaytestkit.HealthyGrokOAuthAccount(57, "access-token")
	repo := &grokQuotaAccountRepo{
		grokFixtureAccounts: &grokFixtureAccounts{
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{57: account},
		},
	}
	upstream := &auxiliaryHTTPRecorder{resp: grokMessagesSSECompletedResponse("resp_grok_messages_stream", 2)}
	svc := newResponsesFixture(responsesFixtureInputs{grokTokens: newHTTPGrokTokenFixture(repo, nil), transport: upstream, accounts: repo})

	result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(GrokConversationIDHeader))
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), `"cache_read_input_tokens":2`)
}

// grokPoolPolicyAccountRepo 记录 Grok 池模式错误策略产生的账号状态写入。
type grokPoolPolicyAccountRepo struct {
	*grokQuotaAccountRepo
	setErrorCalls            int
	overloadedCalls          int
	modelRateLimitCalls      int
	lastModelRateLimitScope  string
	lastModelRateLimitReason string
}

func (r *grokPoolPolicyAccountRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

func (r *grokPoolPolicyAccountRepo) SetOverloaded(_ context.Context, _ int64, _ time.Time) error {
	r.overloadedCalls++
	return nil
}

func (r *grokPoolPolicyAccountRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, reason ...string) error {
	r.modelRateLimitCalls++
	r.lastModelRateLimitScope = scope
	if len(reason) > 0 {
		r.lastModelRateLimitReason = reason[0]
	}
	return nil
}

// newGrokPoolPolicyGateway 构造接入真实通用错误策略的 Grok 网关测试实例。
func newGrokPoolPolicyGateway(account *gatewayprovider.ExecutionAccount) (*OpenAIResponsesExecutor, *grokPoolPolicyAccountRepo) {
	baseRepo := &grokFixtureAccounts{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}
	repo := &grokPoolPolicyAccountRepo{
		grokQuotaAccountRepo: &grokQuotaAccountRepo{grokFixtureAccounts: baseRepo},
	}
	cfg := &responsesFixtureOptions{}
	var svc *OpenAIResponsesExecutor

	healthObserver := newHTTPHealthFixture(repo, cfg, nil, accountcore.HealthOptions{Block: func(v *accountcore.Record, until time.Time, reason string) {
		svc.Output.Health.Runtime.BlockAccountScheduling(v, until, reason)
	}}, nil)

	svc = newResponsesFixture(responsesFixtureInputs{accounts: repo, health: healthObserver, options: cfg})
	healthObserver.Limits.RetryOpenAI = func(v *accountcore.Record, h http.Header, body []byte) bool {
		return accountprovider.CanRetryOpenAI429(svc.Output.Health.Runtime, v, h, body)
	}

	return svc, repo
}

// newGrokPoolAccount 返回开启池模式的 Grok API Key 账号。
func newGrokPoolAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}},
	}
}

func TestGrokMediaPoolModeRetryFlagFollowsExplicitPolicies(t *testing.T) {

	t.Run("default 429 remains retryable without local cooldown", func(t *testing.T) {
		account := newGrokPoolAccount(630)
		svc, repo := newGrokPoolPolicyGateway(account)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		resp := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"60"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		}

		result, err := mediaErrorResponseFixture(svc.Grok, context.Background(), resp, c, account, "request-id", "grok-imagine")

		require.Nil(t, result)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.True(t, failoverErr.RetryableOnSameAccount)
		require.Zero(t, repo.rateLimitedCalls)
		require.False(t, httpFixtureRuntimeBlocked(svc, account))
	})

	t.Run("explicit 401 policy stops same account retry", func(t *testing.T) {
		account := newGrokPoolAccount(631)
		account.Record.Credentials["custom_error_codes_enabled"] = true
		account.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnauthorized)}
		svc, repo := newGrokPoolPolicyGateway(account)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		resp := &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid api key"}}`)),
		}

		result, err := mediaErrorResponseFixture(svc.Grok, context.Background(), resp, c, account, "request-id", "grok-imagine")

		require.Nil(t, result)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.False(t, failoverErr.RetryableOnSameAccount)
		require.Equal(t, 1, repo.setErrorCalls)
		require.True(t, httpFixtureRuntimeBlocked(svc, account))
	})

	t.Run("mapped model is used by temporary policy", func(t *testing.T) {
		t.Setenv(grok.EnvAllowUnsafeURLOverrides, "true")
		account := newGrokPoolAccount(632)
		account.Record.Credentials["api_key"] = "api-key"
		account.Record.Credentials["base_url"] = "https://xai.test/v1"
		account.Record.Credentials["model_mapping"] = map[string]any{"image-alias": "vendor-image-model"}
		account.Record.Credentials["temp_unschedulable_enabled"] = true
		account.Record.Credentials["temp_unschedulable_rules"] = []any{
			map[string]any{
				"error_code":       float64(http.StatusServiceUnavailable),
				"keywords":         []any{"maintenance"},
				"duration_minutes": float64(30),
			},
		}
		svc, repo := newGrokPoolPolicyGateway(account)
		upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"maintenance in progress"}}`)),
		}}
		svc.Requests.Transport = upstream
		if svc.Grok != nil {
			svc.Grok.Transport = svc.Requests.Transport
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"image-alias","prompt":"draw a cat"}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")

		result, err := svc.Grok.ForwardGrokMedia(
			context.Background(),
			c,
			account,
			grok.GrokMediaEndpointImagesGenerations,
			"",
			body,
			"application/json",
		)

		require.Nil(t, result)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, 1, repo.modelRateLimitCalls)
		require.Equal(t, "vendor-image-model", repo.lastModelRateLimitScope)
		require.Equal(t, "vendor-image-model", gjson.GetBytes(upstream.lastBody, "model").String())
	})
}

func TestForwardGrokResponsesRejectsMappedImageModelWithClientError(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"image-alias","input":"draw a cat"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
		Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"image-alias": "grok-imagine-image-quality"},
		}},
	}

	result, err := (newResponsesFixture(responsesFixtureInputs{})).Grok.ForwardResponses(
		context.Background(), c, account, body, "image-alias", false, time.Now(),
	)

	require.ErrorContains(t, err, "use /v1/images/generations instead")
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Equal(t, "model", gjson.GetBytes(recorder.Body.Bytes(), "error.param").String())
	require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), "grok-imagine-image-quality")
}
