package googleforward_test

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

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// geminiResponsesFailingStream supplies one content event before a configured
// read failure.
type geminiResponsesFailingStream struct {
	// read becomes true on the first invocation, so the fixture delivers
	// one fitting event prefix before subsequent reads fail.
	read bool // first-read completion marker
}

// TestGeminiForwardAsResponsesReturnsResponsesFormat maps an API key Responses
// request and returns reasoning, text, tool calls, and usage. Client output
// uses the logical model while completion and the captured endpoint retain the
// mapped Gemini target.
func TestGeminiForwardAsResponsesReturnsResponsesFormat(t *testing.T) {
	upstreamBody := `{
		"candidates":[{"content":{"parts":[
			{"text":"inspect inputs","thought":true},
			{"text":"calling tool"},
			{"functionCall":{"name":"get_weather","args":{"city":"Tokyo"}}}
		]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"thoughtsTokenCount":5}
	}`
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"X-Request-Id": []string{"gemini-response-1"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           201,
			Platform:     capability.PlatformGemini,
			Type:         capability.ProviderTypeAPIKey,
			Concurrency:  1,
			Credentials: map[string]any{
				"api_key": "gemini-key",
				"model_mapping": map[string]any{
					"group-model": "gemini-2.5-pro",
				},
			},
		},
	}
	body := []byte(`{"model":"group-model","input":"weather","tools":[{"type":"function","name":"get_weather","parameters":{"type":"object"}}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "group-model", result.Model)
	require.Equal(t, "gemini-2.5-pro", result.UpstreamModel)
	require.Equal(t, "gemini-response-1", result.RequestID)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, "response", gjson.GetBytes(recorder.Body.Bytes(), "object").String())
	require.Equal(t, "group-model", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
	require.Equal(t, "inspect inputs", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="reasoning").summary.0.text`).String())
	require.Equal(t, "get_weather", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="function_call").name`).String())
	require.Equal(t, "calling tool", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="message").content.0.text`).String())
	require.Contains(t, httpStub.lastReq.URL.String(), "/models/gemini-2.5-pro:generateContent")
}

// TestGeminiForwardAsResponsesOAuthCollectsReasoningTextAndTools collects
// several wrapped OAuth stream payloads for a buffered Responses client.
// Reasoning fragments join, text/tools remain separate output items, and final
// usage and v1internal route match the fixture.
func TestGeminiForwardAsResponsesOAuthCollectsReasoningTextAndTools(t *testing.T) {
	upstreamBody := strings.Join([]string{
		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"plan ","thought":true}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"thoughtsTokenCount":1}}}`,
		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"carefully","thought":true}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"thoughtsTokenCount":2}}}`,
		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"thoughtsTokenCount":2}}}`,
		`data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"id":42}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"thoughtsTokenCount":2}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{
		tokenProvider: newGeminiTokenSourceForTest(),

		httpUpstream: httpStub,

		cfg: &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           204,
			Platform:     capability.PlatformGemini,
			Type:         capability.ProviderTypeOAuth,
			Concurrency:  1,
			Credentials: map[string]any{
				"access_token": "ya29.test-token",
				"project_id":   "project-1",
			},
		},
	}
	body := []byte(`{"model":"gemini-2.5-pro","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, "plan carefully", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="reasoning").summary.0.text`).String())
	require.Equal(t, "answer", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="message").content.0.text`).String())
	require.Equal(t, "lookup", gjson.GetBytes(recorder.Body.Bytes(), `output.#(type=="function_call").name`).String())
	require.Contains(t, httpStub.lastReq.URL.String(), "/v1internal:streamGenerateContent?alt=sse")
}

// TestGeminiForwardAsResponsesStreamsReasoningTextToolAndUsage reads native SSE
// reasoning, text, and a function call for a streaming Responses client. Output
// uses Responses lifecycle/delta/terminal events, retains usage, and omits Chat
// DONE framing.
func TestGeminiForwardAsResponsesStreamsReasoningTextToolAndUsage(t *testing.T) {
	upstreamBody := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"plan","thought":true}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"thoughtsTokenCount":1}}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"thoughtsTokenCount":1}}`,
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"id":42}}}]} ,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"thoughtsTokenCount":1}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
		LoadLocation: time.LoadLocation,
		ID:           202,
		Platform:     capability.PlatformGemini,
		Type:         capability.ProviderTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Nil(t, result.Timings.TTFTMs())
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	streamBody := recorder.Body.String()
	require.Contains(t, streamBody, "event: response.created")
	require.Contains(t, streamBody, "event: response.reasoning_summary_text.delta")
	require.Contains(t, streamBody, `"delta":"plan"`)
	require.Contains(t, streamBody, "event: response.output_text.delta")
	require.Contains(t, streamBody, `"delta":"hello"`)
	require.Contains(t, streamBody, "event: response.function_call_arguments.done")
	require.Contains(t, streamBody, `"name":"lookup"`)
	require.Contains(t, streamBody, "event: response.completed")
	require.NotContains(t, streamBody, "data: [DONE]")
}

// TestGeminiForwardAsResponsesCommitsStreamBeforeReadFailure fails the upstream
// reader after its first content frame. Forwarding returns a stream-read error
// and the recorder confirms client bytes were already written before that
// failure.
func TestGeminiForwardAsResponsesCommitsStreamBeforeReadFailure(t *testing.T) {
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"text/event-stream"}},

		Body: &geminiResponsesFailingStream{},
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
		LoadLocation: time.LoadLocation,
		ID:           203,
		Platform:     capability.PlatformGemini,
		Type:         capability.ProviderTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello","stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	_, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body, nil)

	require.ErrorContains(t, err, "stream_read_error")
	require.Positive(t, recorder.Body.Len(), "首个字节写出后 handler 必须禁止 failover")
}

// TestGeminiForwardAsResponsesMapsUpstreamError returns a Gemini status-400
// generation error before output adaptation. The result is nil and client
// output uses the expected Responses invalid_request_error code with its safe
// message.
func TestGeminiForwardAsResponsesMapsUpstreamError(t *testing.T) {
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusBadRequest,

		Header: http.Header{"X-Goog-Request-Id": []string{"gemini-error-1"}},

		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":400,"message":"invalid generation request","status":"INVALID_ARGUMENT"}}`,
		)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
		LoadLocation: time.LoadLocation,
		ID:           205,
		Platform:     capability.PlatformGemini,
		Type:         capability.ProviderTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body, nil)

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
	require.Equal(t, "Invalid request", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
}

// TestGeminiForwardAsResponsesReturnsFailoverBeforeResponseStarts returns an
// insufficient-scope status 403 through an API key Responses attempt. Typed
// failover retains the status and the client body remains empty for outer
// provider selection.
func TestGeminiForwardAsResponsesReturnsFailoverBeforeResponseStarts(t *testing.T) {
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusForbidden,

		Header: http.Header{
			"Www-Authenticate": []string{`Bearer error="insufficient_scope"`},

			"X-Goog-Request-Id": []string{"gemini-failover-1"},
		},

		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":403,"message":"insufficient authentication scope","status":"PERMISSION_DENIED"}}`,
		)),
	}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: httpStub, cfg: &googleforward.Options{}})
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{
		LoadLocation: time.LoadLocation,
		ID:           206,
		Platform:     capability.PlatformGemini,
		Type:         capability.ProviderTypeAPIKey,
		Concurrency:  1,
		Credentials:  map[string]any{"api_key": "gemini-key"},
	}}
	body := []byte(`{"model":"gemini-2.5-flash","input":"hello"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body, nil)

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
	require.Zero(t, recorder.Body.Len())
}

// TestGeminiForwardAsChatCompletions_CustomCodesMiss400HiddenAs500 returns a
// status-400 error outside an enabled custom-code set. Chat output uses the
// generic status-500 api_error message and forwarding returns no completion
// result.
func TestGeminiForwardAsChatCompletions_CustomCodesMiss400HiddenAs500(t *testing.T) {
	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, geminiSkippedTestUpstreamBody())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiCustomCodesAPIKeyProvider(), body)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not in custom error codes")
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "api_error", errObj["type"])
	require.Equal(t, "Upstream gateway error", errObj["message"])
}

// TestGeminiForwardAsChatCompletions_PoolMode400KeepsUpstreamMessage returns
// status 400 through a pool API key provider. Chat output retains the upstream
// message with invalid_request_error classification and no completion result is
// returned.
func TestGeminiForwardAsChatCompletions_PoolMode400KeepsUpstreamMessage(t *testing.T) {
	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, geminiSkippedTestUpstreamBody())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiPoolModeAPIKeyProvider(), body)

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code, "状态码应保真为上游 400")

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", errObj["type"])
	require.Equal(t, geminiSkippedTestUpstreamMsg, errObj["message"], "应回传上游 message")
}

// TestGeminiForwardAsChatCompletions_OAuthRoutesToGeminiAndReturnsChatFormat
// maps an OAuth Chat request and collects a wrapped stream into buffered
// output. Captured v1internal authorization/project/model and client Chat
// envelope retain their separate expected values.
func TestGeminiForwardAsChatCompletions_OAuthRoutesToGeminiAndReturnsChatFormat(t *testing.T) {
	upstreamBody := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello from gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,

			Header: http.Header{"Content-Type": []string{"text/event-stream"}},

			Body: io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{
		tokenProvider: newGeminiTokenSourceForTest(),

		httpUpstream: httpStub,

		cfg: &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           101,
			Platform:     capability.PlatformGemini,
			Type:         capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"access_token": "ya29.test-token",

				"project_id": "project-1",

				"model_mapping": map[string]any{
					"gemini-2.5-flash": "gemini-2.5-flash-upstream",
				},
			},
			Concurrency: 1,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gemini-2.5-flash", result.Model)
	require.Equal(t, "gemini-2.5-flash-upstream", result.UpstreamModel)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, "hello from gemini", gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String())

	require.NotNil(t, httpStub.lastReq)
	require.Contains(t, httpStub.lastReq.URL.String(), "/v1internal:streamGenerateContent?alt=sse")
	require.Equal(t, "Bearer ya29.test-token", httpStub.lastReq.Header.Get("Authorization"))
	require.Empty(t, httpStub.lastReq.Header.Get("x-api-key"))
	require.Empty(t, httpStub.lastReq.Header.Get("anthropic-version"))

	var sent map[string]any
	sentBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(sentBody, &sent))
	require.Equal(t, "gemini-2.5-flash-upstream", sent["model"])
	require.Equal(t, "project-1", sent["project"])
	require.Contains(t, fmt.Sprint(sent["request"]), "hi")

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "chat.completion", got["object"])
	require.Equal(t, "gemini-2.5-flash", got["model"])
	choices, ok := got["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	choice, ok := choices[0].(map[string]any)
	require.True(t, ok)
	message, ok := choice["message"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "assistant", message["role"])
	require.Equal(t, "hello from gemini", message["content"])
	usage, ok := got["usage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(7), usage["prompt_tokens"])
	require.Equal(t, float64(3), usage["completion_tokens"])
	require.Equal(t, float64(10), usage["total_tokens"])
}

// TestGeminiForwardAsChatCompletions_StreamsOpenAIChunksFromGeminiSSE reads
// cumulative native text under a streaming Chat request with usage enabled.
// Client chunks contain incremental suffixes, assistant role, final usage, and
// the Chat DONE marker.
func TestGeminiForwardAsChatCompletions_StreamsOpenAIChunksFromGeminiSSE(t *testing.T) {
	upstreamBody := `data: {"candidates":[{"content":{"parts":[{"text":"hel"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,

			Header: http.Header{"Content-Type": []string{"text/event-stream"}},

			Body: io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{
		httpUpstream: httpStub,
		cfg:          &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           102,
			Platform:     capability.PlatformGemini,
			Type:         capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key": "gemini-api-key",
			},
			Concurrency: 1,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, result.Stream)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)

	require.NotNil(t, httpStub.lastReq)
	require.Contains(t, httpStub.lastReq.URL.String(), "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse")
	require.Equal(t, "gemini-api-key", httpStub.lastReq.Header.Get("x-goog-api-key"))

	out := rec.Body.String()
	require.Contains(t, out, `"object":"chat.completion.chunk"`)
	require.Contains(t, out, `"role":"assistant"`)
	require.Contains(t, out, `"content":"hel"`)
	require.Contains(t, out, `"content":"lo"`)
	require.Contains(t, out, `"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}`)
	require.Contains(t, out, "data: [DONE]")
}

// TestGeminiForwardAsChatCompletions_FunctionNamedWebSearchStaysClientSide
// supplies two Chat function tools including one named web_search. Captured
// native tools retain both function declarations and contain no built-in Google
// search object.
func TestGeminiForwardAsChatCompletions_FunctionNamedWebSearchStaysClientSide(t *testing.T) {
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,

			Header: http.Header{"Content-Type": []string{"application/json"}},

			Body: io.NopCloser(strings.NewReader(
				`{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`,
			)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{
		httpUpstream: httpStub,
		cfg:          &googleforward.Options{},
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           103,
			Platform:     capability.PlatformGemini,
			Type:         capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"api_key": "gemini-api-key",
			},
			Concurrency: 1,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{
		"model":"gemini-3.6-flash-high",
		"messages":[{"role":"user","content":"search and read"}],
		"tools":[
			{"type":"function","function":{"name":"web_search","description":"Search through the Hermes client","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}},
			{"type":"function","function":{"name":"read_file","description":"Read a local file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}
		]
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, httpStub.lastReq)

	postedBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)

	var posted map[string]any
	require.NoError(t, json.Unmarshal(postedBody, &posted))
	tools, ok := posted["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1, "Chat Completions function tools must not be promoted to Gemini built-ins by name")

	functionTool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	functionDecls, ok := functionTool["functionDeclarations"].([]any)
	require.True(t, ok)
	require.Len(t, functionDecls, 2)
	webSearchDecl, ok := functionDecls[0].(map[string]any)
	require.True(t, ok)
	readFileDecl, ok := functionDecls[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search", webSearchDecl["name"])
	require.Equal(t, "read_file", readFileDecl["name"])
	require.NotContains(t, functionTool, "googleSearch")
	require.NotContains(t, functionTool, "google_search")
}

// Read copies the fitting content-frame prefix once, then returns the fixture
// stream error. The first invocation sets read even if its buffer truncates the
// payload; later calls report failure.
func (r *geminiResponsesFailingStream) Read(p []byte) (int, error) {
	if r.read {
		return 0, errors.New("upstream stream failed")
	}
	r.read = true
	return copy(p, []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")), nil
}

// Close returns success because this failing-stream fixture owns no external
// resource.
func (r *geminiResponsesFailingStream) Close() error { return nil }

// TestGeminiCompatibilityForwardsResolvedThinking crosses budget, level,
// disabled, and unknown reasoning controls across Chat/Responses routes.
// Captured thinkingConfig matches final mapped model capability and the client
// receives response text.
func TestGeminiCompatibilityForwardsResolvedThinking(t *testing.T) {
	for _, tc := range []struct {
		// name identifies the case, model selects the mapped Gemini
		// target, body supplies client controls, and want is the
		// expected thinkingConfig JSON for that same request.
		//
		// name: scenario label; model: wire target; body: client JSON;
		// want: thinking JSON
		name, model, body, want string
		// Responses fixture invocation flag
		responses bool
	}{
		{"chat budget", "gemini-2.5-flash", `{"model":"alias","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"max"}`, `{"includeThoughts":true,"thinkingBudget":24576}`, false},
		{"chat level", "gemini-3.1-pro-preview", `{"model":"alias","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`, `{"includeThoughts":true,"thinkingLevel":"high"}`, false},
		{"responses disabled", "gemini-2.5-flash", `{"model":"alias","input":"hi","reasoning":{"effort":"none"}}`, `{"includeThoughts":false,"thinkingBudget":0}`, true},
		{"unknown effort", "unlisted-model", `{"model":"alias","input":"hi","reasoning":{"effort":"future"}}`, `{"includeThoughts":true,"thinkingLevel":"future"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`))}}
			svc := newGeminiFixture(geminiDependencies{httpUpstream: stub, cfg: &googleforward.Options{}})
			svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 1, Platform: "gemini", Type: "apikey", Credentials: map[string]any{"api_key": "test", "model_mapping": map[string]any{"alias": tc.model}}}}
			body := []byte(tc.body)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			output := gatewayhttp.NewGoogleBoundary(c, svc.Options, false)
			var err error
			if tc.responses {
				_, err = svc.ForwardAsResponses(context.Background(), output, provider, body, nil)
			} else {
				_, err = svc.ForwardAsChatCompletions(context.Background(), output, provider, body)
			}
			require.NoError(t, err)
			require.Equal(t, 1, stub.calls)
			wire, err := io.ReadAll(stub.lastReq.Body)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, gjson.GetBytes(wire, "generationConfig.thinkingConfig").Raw)
			require.Contains(t, stub.lastReq.URL.Path, tc.model)
			require.Contains(t, recorder.Body.String(), "answer")
		})
	}
}

// TestGeminiCompatibilityForwardsReasoningWithUnknownOptions supplies a catalog
// model that supports reasoning while configurable options are absent. The
// outgoing thinkingConfig retains the client high effort as thinkingLevel on
// this Gemini compatibility request.
func TestGeminiCompatibilityForwardsReasoningWithUnknownOptions(t *testing.T) {
	catalog, err := modelcatalog.Parse([]byte(`{"providers":{"google":{"models":{"private-model":{"reasoning":true}}}}}`))
	require.NoError(t, err)
	stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`))}}
	svc := newGeminiFixture(geminiDependencies{httpUpstream: stub, cfg: &googleforward.Options{}})
	svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: func() modelrules.View { return modelrules.NewView(catalog.Publication()) }}}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 1, Platform: "gemini", Type: "apikey", Credentials: map[string]any{"api_key": "fixture"}}}
	body := []byte(`{"model":"private-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	_, err = svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), provider, body)
	require.NoError(t, err)
	require.Equal(t, 1, stub.calls)
	sent, err := io.ReadAll(stub.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(sent, "generationConfig.thinkingConfig.thinkingLevel").String())
}
