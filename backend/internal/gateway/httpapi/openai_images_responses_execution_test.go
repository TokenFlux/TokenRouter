package httpapi

import (
	"context"
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
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	providerimages "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openAIImagesReadErrorBody returns one configured failure on every image
// body read.
type openAIImagesReadErrorBody struct {
	err error // configured image read failure
}

// countingModelRateLimitRepo captures image cooldown writes for
// provider-state assertions.
type countingModelRateLimitRepo struct {
	// ExecutionProviderStore leaves unused operations unset. Cooldown policy uses
	// the SetModelRateLimit override to observe its selected image scope.
	gatewayprovider.ExecutionProviderStore
	// calls and scopes describe the same SetModelRateLimit invocations. Each
	// accepted fixture call increments the count and appends its scope, allowing
	// tests to check both cardinality and capability isolation.
	calls  int      // observed cooldown write count
	scopes []string // captured cooldown scope names
}

// Read returns the configured failure before accepting any image response
// bytes.
func (b *openAIImagesReadErrorBody) Read([]byte) (int, error) { return 0, b.err }

// Close completes the stateless failing image body without another error.
func (b *openAIImagesReadErrorBody) Close() error { return nil }

// TestOpenAIImagesOAuthBodyReadTransportErrorFailover injects a classified
// HTTP/2 body failure after status 200 and before output. Policy returns a
// sanitized 502 failover envelope with independently cloned headers and
// correlated Ops diagnostics.
func TestOpenAIImagesOAuthBodyReadTransportErrorFailover(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Request-Id": []string{"req_h2_read_failure"},
			"X-Upstream":   []string{"preserved"},
		},
		Body: &openAIImagesReadErrorBody{err: errors.New("stream error: stream ID 11; INTERNAL_ERROR; received from peer")},
	}
	provider := &gatewayprovider.ExecutionProvider{Record: providerimages.Record{LoadLocation: time.LoadLocation, ID: 5400, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
	svc := newImagesFixture(imagesFixtureInputs{})

	_, _, _, readErr := upstreamopenai.ReadImagesOAuthNonStreaming(resp, ResponseSink{Writer: c.Writer}, svc.Output.ImageOptions(c), "b64_json", "gpt-image-2")
	require.Error(t, readErr)
	err := svc.handleOpenAIImagesOAuthResponseError(context.Background(), c, provider, "gpt-image-2", "https://api.openai.com/v1/responses", resp, HeartbeatAdjustedWrittenSize(c), readErr)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.JSONEq(t, `{"error":{"type":"upstream_error","code":"upstream_http2_stream_error","message":"Upstream HTTP/2 stream failed"}}`, string(failoverErr.ResponseBody))
	require.Equal(t, "req_h2_read_failure", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
	require.Equal(t, "preserved", http.Header(failoverErr.ResponseHeaders).Get("x-upstream"))
	resp.Header.Set("X-Upstream", "mutated")
	require.Equal(t, "preserved", http.Header(failoverErr.ResponseHeaders).Get("x-upstream"))

	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, "req_h2_read_failure", events[0].UpstreamRequestID)
	require.Equal(t, "Upstream HTTP/2 stream failed", events[0].Message)
}

// TestOpenAIImagesOAuthBodyReadErrorsNotMisclassified compares cancellation,
// body-size and semantic image failures. Each retains its original error
// identity and remains outside the tested transport-read failover conversion.
func TestOpenAIImagesOAuthBodyReadErrorsNotMisclassified(t *testing.T) {
	tests := []struct {
		name string // response error classification case
		err  error  // supplied image read error
	}{
		{name: "context canceled", err: context.Canceled},
		{name: "response too large", err: fmt.Errorf("%w: limit=1", httpclient.ErrResponseBodyTooLarge)},
		{name: "semantic error", err: &upstreamopenai.OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest, ErrorType: "invalid_request_error", Code: "invalid_value", Message: "bad image request"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
			err := tt.err
			if tt.name != "semantic error" && upstreamopenai.ShouldClassifyUpstreamStreamReadError(err, httpclient.ErrResponseBodyTooLarge, c.Request.Context()) {
				err = upstreamopenai.NewUpstreamStreamReadError(err)
			}

			got := newImagesFixture(imagesFixtureInputs{}).handleOpenAIImagesOAuthResponseError(context.Background(), c, &gatewayprovider.ExecutionProvider{Record: providerimages.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI}}, "gpt-image-2", "", resp, HeartbeatAdjustedWrittenSize(c), err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.False(t, errors.As(got, &failoverErr))
			require.ErrorIs(t, got, tt.err)
		})
	}
}

// TestOpenAIImagesOAuthTransportErrorAfterDownstreamWriteDoesNotFailover
// records image output before a classified body failure. The original error
// remains terminal for that attempt, and Ops marks the exhausted retry
// opportunity.
func TestOpenAIImagesOAuthTransportErrorAfterDownstreamWriteDoesNotFailover(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	before := HeartbeatAdjustedWrittenSize(c)
	_, writeErr := c.Writer.Write([]byte("downstream image bytes"))
	require.NoError(t, writeErr)
	classifiedErr := upstreamopenai.NewUpstreamStreamReadError(errors.New("unexpected EOF"))
	provider := &gatewayprovider.ExecutionProvider{Record: providerimages.Record{LoadLocation: time.LoadLocation, ID: 5401, Name: "openai-oauth", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
	resp := &http.Response{Header: http.Header{"X-Request-Id": []string{"req_after_write"}}}

	err := newImagesFixture(imagesFixtureInputs{}).handleOpenAIImagesOAuthResponseError(context.Background(), c, provider, "gpt-image-2", "", resp, before, classifiedErr)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.ErrorIs(t, err, classifiedErr)
	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "retry_exhausted_failover", events[0].Kind)
}

// TestBuildOpenAIImagesResponsesRequest_PassesThroughNForMultiImageModels
// converts a two-image parsed request into Responses tool input. The tool
// receives the count, selected image model and supplied prompt.
func TestBuildOpenAIImagesResponsesRequest_PassesThroughNForMultiImageModels(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "draw a cat",
		N:        2,
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", upstreamopenai.ImagesResponsesMainModel)
	require.NoError(t, err)
	require.NotNil(t, body)
	require.Equal(t, int64(2), gjson.GetBytes(body, "tools.0.n").Int())
	require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
	require.Equal(t, "draw a cat", gjson.GetBytes(body, "input.0.content.0.text").String())
}

// TestBuildOpenAIImagesResponsesRequest_ForcesImageToolChoice builds a
// prompt-only parsed generation request. Responses selects the
// image_generation tool and retains the same tool type and image model in its
// declaration.
func TestBuildOpenAIImagesResponsesRequest_ForcesImageToolChoice(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "draw a cat",
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", upstreamopenai.ImagesResponsesMainModel)
	require.NoError(t, err)
	require.NotNil(t, body)
	require.Equal(t, "image_generation", gjson.GetBytes(body, "tool_choice.type").String())
	require.Equal(t, "image_generation", gjson.GetBytes(body, "tools.0.type").String())
	require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
}

// TestBuildOpenAIImagesResponsesRequest_DoesNotPassNForDallE3 converts a
// DALL-E 3 request with count two. The native compatibility rule omits n
// while retaining the selected tool model.
func TestBuildOpenAIImagesResponsesRequest_DoesNotPassNForDallE3(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "dall-e-3",
		Prompt:   "draw a cat",
		N:        2,
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "dall-e-3", upstreamopenai.ImagesResponsesMainModel)
	require.NoError(t, err)
	require.NotNil(t, body)
	require.False(t, gjson.GetBytes(body, "tools.0.n").Exists())
	require.Equal(t, "dall-e-3", gjson.GetBytes(body, "tools.0.model").String())
}

// TestBuildOpenAIImagesResponsesRequest_StripsInputFidelity converts an
// editing request with explicit input fidelity and source URL. The OAuth tool
// uses edit action and omits the unsupported fidelity field.
func TestBuildOpenAIImagesResponsesRequest_StripsInputFidelity(t *testing.T) {
	parsed := &media.ImageRequest{
		Endpoint:      upstreamcore.OpenAIImagesEditsEndpoint,
		Model:         "gpt-image-2",
		Prompt:        "replace background",
		InputFidelity: "high",
		InputImageURLs: []string{
			"https://example.com/source.png",
		},
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", upstreamopenai.ImagesResponsesMainModel)
	require.NoError(t, err)
	require.NotNil(t, body)
	require.False(t, gjson.GetBytes(body, "tools.0.input_fidelity").Exists())
	require.Equal(t, "edit", gjson.GetBytes(body, "tools.0.action").String())
}

// TestBuildOpenAIImagesResponsesRequest_RequiresVerbatimUserPrompt supplies
// Unicode wording, capitalization and quotes in a parsed prompt. Construction
// retains that text and adds the dedicated verbatim-prompt instruction
// string.
func TestBuildOpenAIImagesResponsesRequest_RequiresVerbatimUserPrompt(t *testing.T) {
	prompt := "画一个蓝色马克杯，杯身只写“SkelOT”，保持大小写；白色背景，不要增加其他文字。"
	parsed := &media.ImageRequest{
		Endpoint: upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   prompt,
		N:        1,
	}

	body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", upstreamopenai.ImagesResponsesMainModel)
	require.NoError(t, err)
	require.Equal(t, upstreamopenai.ImagesVerbatimPromptInstructions, gjson.GetBytes(body, "instructions").String())
	require.Equal(t, prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
}

// SetModelRateLimit records call count and each supplied scope for cooldown
// assertions.
func (r *countingModelRateLimitRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, _ ...string) error {
	r.calls++
	r.scopes = append(r.scopes, scope)
	return nil
}

// newImagesCooldownContext creates a fresh image request context with a
// response recorder. The fixture has no request body because the tests
// exercise already observed error policy and output state.
func newImagesCooldownContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	return c, rec
}

// imagesCooldownProvider supplies an OpenAI OAuth target for image cooldown
// policy tests.
func imagesCooldownProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{Record: providerimages.Record{LoadLocation: time.LoadLocation, ID: 77, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Name: "img-oauth"}}
}

// TestShouldCoolOpenAIImagesToolForError compares nil, text-derived and
// structured unavailable-tool observations. The predicate rejects absent or
// synthesized evidence and accepts the structured fixture for its caller's
// code-specific cooldown check.
func TestShouldCoolOpenAIImagesToolForError(t *testing.T) {
	cases := []struct {
		name string                                    // cooldown evidence case label
		err  *upstreamopenai.OpenAIImagesUpstreamError // supplied unavailable-image error evidence
		want bool                                      // expected structured-evidence predicate result
	}{
		{
			name: "nil_error",
			err:  nil,
			want: false,
		},
		{
			// Text-derived absence describes this attempt rather than confirmed provider
			// capability, so the unavailable-tool caller should not persist cooldown for it.
			name: "synthesized_from_model_text",
			err: &upstreamopenai.OpenAIImagesUpstreamError{
				StatusCode:               http.StatusBadGateway,
				Code:                     "image_generation_unavailable",
				SynthesizedFromModelText: true,
			},
			want: false,
		},
		{
			// Structured upstream data supplies the provider evidence required by the
			// unavailable-tool branch before it can persist an image capability cooldown.
			name: "structured_upstream_error_frame",
			err: &upstreamopenai.OpenAIImagesUpstreamError{
				StatusCode: http.StatusBadGateway,
				Code:       "image_generation_unavailable",
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldCoolOpenAIImagesToolForError(tc.err))
		})
	}
}

// TestHandleOpenAIImagesOAuthResponseError_TextFallbackDoesNotCoolProvider
// supplies missing image evidence inferred from model text. Policy writes no
// image cooldown and still returns the tested provider failover
// classification.
func TestHandleOpenAIImagesOAuthResponseError_TextFallbackDoesNotCoolProvider(t *testing.T) {
	c, _ := newImagesCooldownContext(t)
	repo := &countingModelRateLimitRepo{}
	svc := newImagesFixture(imagesFixtureInputs{store: repo})
	provider := imagesCooldownProvider()

	upstreamErr := upstreamopenai.OpenAIImagesTextFallbackErrorForText("Here's a polished image prompt for your request.")
	require.NotNil(t, upstreamErr)
	require.Equal(t, "image_generation_unavailable", upstreamErr.Code)

	err := svc.handleOpenAIImagesOAuthResponseError(
		context.Background(), c, provider, "gpt-image-2", "https://upstream.example/v1/responses",
		&http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, HeartbeatAdjustedWrittenSize(c), upstreamErr,
	)

	require.Zero(t, repo.calls, "模型闲聊不构成提供商级证据，不得写 30 分钟冷却")

	// Text-only output still permits provider failover.
	var failover *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failover), "仍应触发换号，got %T", err)
}

// TestHandleOpenAIImagesOAuthResponseError_StructuredUnavailableStillCoolsProvider
// supplies an explicit upstream image-tool-unavailable error. Policy writes
// one cooldown under the image-generation scope retained by the provider
// fixture.
func TestHandleOpenAIImagesOAuthResponseError_StructuredUnavailableStillCoolsProvider(t *testing.T) {
	c, _ := newImagesCooldownContext(t)
	repo := &countingModelRateLimitRepo{}
	svc := newImagesFixture(imagesFixtureInputs{store: repo})
	provider := imagesCooldownProvider()

	upstreamErr := &upstreamopenai.OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		ErrorType:  "upstream_error",
		Code:       "image_generation_unavailable",
		Message:    "image generation tool is not available for this provider",
	}

	// Discard the response verdict because this fixture checks persisted
	// capability state; the neighboring text-fallback test separately inspects
	// the returned failover classification.
	_ = svc.handleOpenAIImagesOAuthResponseError(
		context.Background(), c, provider, "gpt-image-2", "https://upstream.example/v1/responses",
		&http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, HeartbeatAdjustedWrittenSize(c), upstreamErr,
	)

	require.Equal(t, 1, repo.calls, "结构化上游证据仍须写冷却")
	require.Equal(t, []string{providerimages.OpenAIImageGenerationRateLimitKey}, repo.scopes)
}

// TestOpenAIImagesTextFallback_MarksSynthesizedVerdicts compares text-derived
// missing-image errors with policy refusal and blank text. Only missing-image
// inference receives the synthesis marker, while refusal retains its separate
// tested code.
func TestOpenAIImagesTextFallback_MarksSynthesizedVerdicts(t *testing.T) {
	t.Run("plain_text_reply_is_synthesized", func(t *testing.T) {
		err := upstreamopenai.OpenAIImagesTextFallbackErrorForText("Here's a polished image prompt for your request.")
		require.NotNil(t, err)
		require.True(t, err.SynthesizedFromModelText)
		require.Equal(t, "image_generation_unavailable", err.Code)
		require.Equal(t, http.StatusBadGateway, err.StatusCode)
	})

	t.Run("body_entrypoint_is_synthesized", func(t *testing.T) {
		body := []byte("event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"r","status":"completed",` +
			`"output":[{"type":"message","content":[{"type":"output_text","text":"I drafted a prompt for you."}]}]}}` +
			"\n\n")
		err := upstreamopenai.OpenAIImagesTextFallbackError(body)
		require.NotNil(t, err)
		require.True(t, err.SynthesizedFromModelText)
	})

	t.Run("content_policy_branch_unchanged", func(t *testing.T) {
		err := upstreamopenai.OpenAIImagesTextFallbackErrorForText("Blocked by our content policy.")
		require.NotNil(t, err)
		require.Equal(t, "content_policy_violation", err.Code)
		require.Equal(t, http.StatusBadRequest, err.StatusCode)
		// Policy refusals have a different code and do not enter image-unavailable cooldown.
		// Their synthesized-from-text marker remains false.
		require.False(t, err.SynthesizedFromModelText)
	})

	t.Run("empty_text_yields_no_error", func(t *testing.T) {
		require.Nil(t, upstreamopenai.OpenAIImagesTextFallbackErrorForText("   "))
	})
}

// TestOpenAIImagesTextFallback_RemainsRetryableAndThusCascades builds a
// missing-image error from plain model text. Its 502 classification is
// retryable, supporting request recovery while other tests separately protect
// the absence of provider cooldown.
func TestOpenAIImagesTextFallback_RemainsRetryableAndThusCascades(t *testing.T) {
	err := upstreamopenai.OpenAIImagesTextFallbackErrorForText("Here's a polished image prompt for your request.")
	require.NotNil(t, err)
	require.True(t, upstreamopenai.IsOpenAIImagesRetryableUpstreamError(err),
		"文字兜底判据是可重试的——正因如此，写提供商冷却会沿号池级联")
}

// TestImageStreamFailureUsesResponseDispatch reports an older image stream error
// after a replacement exchange starts. The production error adapter must attach
// its response reference so only the interrupted attempt receives the failure.
func TestImageStreamFailureUsesResponseDispatch(t *testing.T) {
	var saved telemetry.RequestRecord
	ctx := telemetry.WithRequestCapture(t.Context(), telemetry.RequestRecord{RequestID: "local"}, func(r telemetry.RequestRecord) { saved = r })
	ctx = telemetry.WithRequestProvider(ctx, 5400, "X-Custom")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(ctx)
	first := telemetry.BeginRequestAttempt(ctx, 0)
	headers := http.Header{"X-Custom": {"shared"}, "X-Request-Id": {"unselected"}}
	telemetry.ObserveRequestHeaders(first, telemetry.RequestAttemptFromContext(first), headers, 200)
	second := telemetry.BeginRequestAttempt(ctx, 0)
	telemetry.ObserveRequestHeaders(second, telemetry.RequestAttemptFromContext(second), headers, 200)
	resp := &http.Response{
		StatusCode: 200,
		Header:     headers,
		Request:    httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(first),
		Body:       &openAIImagesReadErrorBody{err: errors.New("stream error: stream ID 11; INTERNAL_ERROR; received from peer")},
	}
	target := &gatewayprovider.ExecutionProvider{Record: providerimages.Record{LoadLocation: time.LoadLocation, ID: 5400, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
	svc := newImagesFixture(imagesFixtureInputs{})
	_, _, _, readErr := upstreamopenai.ReadImagesOAuthNonStreaming(resp, ResponseSink{Writer: c.Writer}, svc.Output.ImageOptions(c), "b64_json", "gpt-image-2")
	require.Error(t, readErr)
	require.Error(t, svc.handleOpenAIImagesOAuthResponseError(ctx, c, target, "gpt-image-2", "", resp, HeartbeatAdjustedWrittenSize(c), readErr))
	require.Equal(t, "failed", saved.Attempts[0].Outcome)
	require.Equal(t, 502, saved.Attempts[0].Status)
	require.Equal(t, "running", saved.Attempts[1].Outcome)
	require.Equal(t, 200, saved.Attempts[1].Status)
	require.Equal(t, "shared", saved.Attempts[0].RequestID)
	require.Len(t, saved.Aliases, 2)
}

// TestForwardOpenAIImagesOAuthPreservesTLSRouterMatchAcrossAgentTaskRecovery
// triggers Agent Identity task recovery during an OAuth image request with an
// explicit TLS router match. Both the rejected attempt and the recovered retry
// keep the routed User-Agent, originator and TLS fingerprint profile.
func TestForwardOpenAIImagesOAuthPreservesTLSRouterMatchAcrossAgentTaskRecovery(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providerimages.Record{
			LoadLocation: time.LoadLocation,
			ID:           2301,
			Name:         "agent-images-tls",
			Platform:     capability.PlatformOpenAI,
			Type:         capability.ProviderTypeOAuth,
			Status:       billing.StatusActive,
			Schedulable:  true,
			Concurrency:  1,
			Credentials: map[string]any{
				"auth_mode":          providerimages.OpenAIAuthModeAgentIdentity,
				"agent_runtime_id":   key.RuntimeID,
				"agent_private_key":  privateKey,
				"task_id":            "task-old",
				"chatgpt_account_id": "provider-agent-images",
			},
			Extra: map[string]any{"enable_tls_fingerprint": true},
		},
	}
	repo := &agentIdentityForwardRepo{provider: provider}
	registerCalls := 0
	registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		registerCalls++
		_, _ = io.WriteString(w, `{"task_id":"task-new"}`)
	}))
	// Close the local registration endpoint after the recovered retry completes
	// so task replacement runs against an active HTTP listener.
	defer registerServer.Close()

	onePixelPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	sseSuccess := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_img","status":"completed","model":"gpt-5.4","output":[{"type":"image_generation_call","id":"ig_1","status":"completed","result":"` + onePixelPNG + `"}],"usage":{"input_tokens":5,"output_tokens":10,"total_tokens":15}}}` +
		"\n\ndata: [DONE]\n\n"
	upstream := &auxiliaryHTTPRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sseSuccess)),
		},
	}}

	svc := newImagesFixture(imagesFixtureInputs{store: repo, transport: upstream})
	svc.Requests.Profiles = &egressprovider.TLSProfiles{}
	svc.Requests.Identity = gatewayprovider.NewExecutionAgentIdentity(
		&providerimages.OpenAITaskCoordinator{},
		repo,
		func(ctx context.Context, value *providerimages.Record) (string, error) {
			return provideradapter.RegisterAgentIdentityTask(ctx, value, registerServer.URL)
		},
		nil,
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	parsed := &media.ImageRequest{
		Endpoint:       upstreamcore.OpenAIImagesGenerationsEndpoint,
		Model:          "gpt-image-2",
		Prompt:         "draw a square",
		N:              1,
		ResponseFormat: "b64_json",
	}
	const routedUA = "codex-tui/9.9.9 (Mac OS X 14.0; arm64) iTerm (codex-tui; 9.9.9)"
	routerMatch := egress.TLSFingerprintRouterMatchResult{
		Matched:                 true,
		UpstreamUserAgent:       routedUA,
		UpstreamOriginator:      "codex-tui",
		TLSFingerprintProfileID: 0,
	}

	result, err := svc.forwardOpenAIImagesOAuth(context.Background(), c, provider, parsed, "", routerMatch)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, registerCalls)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "task-new", decodeAgentAssertionTask(t, upstream.requests[1].Header.Get("Authorization")))
	for _, req := range upstream.requests {
		require.Equal(t, routedUA, req.Header.Get("User-Agent"))
		require.Equal(t, "codex-tui", req.Header.Get("Originator"))
	}
	require.NotNil(t, upstream.lastTLSProfile)
	require.Equal(t, "Built-in Default (Node.js 24.x)", upstream.lastTLSProfile.Name)
}
