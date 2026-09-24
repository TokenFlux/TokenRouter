//go:build unit

package googleforward_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ErrorPolicySkipped 的客户端写出契约（与 OpenAI 网关路径对齐）：
//   - 池模式：不可 failover 的 4xx 按上游原始状态码/响应体保真写出，不改写成 5xx；
//   - 自定义错误码未命中：统一 500 + 固定文案，上游细节只进 ops 错误日志；
//   - 可 failover 的状态码（两种账号）一律换号，不透传。
// ---------------------------------------------------------------------------

const geminiSkippedTestUpstreamMsg = "antigravity executor: invalid Gemini function call history"

func geminiSkippedTestUpstreamBody() string {
	return `{"error":{"code":null,"message":"` + geminiSkippedTestUpstreamMsg + `","param":"","type":"invalid_request_error"}}`
}

func newGeminiErrorFixture(status int, body string) (*googleforward.Gemini, *geminiCompatHTTPUpstreamStub) {
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{

			StatusCode: status,

			Header: http.Header{"Content-Type": []string{"application/json"}},

			Body: io.NopCloser(strings.NewReader(body)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{

		httpUpstream: httpStub,

		cfg: &googleforward.Options{},

		healthObserver: newUpstreamHealthForTest(&gatewaytestkit.ErrorPolicyStore{}, &googleforward.Options{}, nil, accountcore.HealthOptions{}, nil),
	})
	return svc, httpStub
}

func geminiPoolModeAPIKeyAccount() *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           700,

		Platform: capability.PlatformGemini,

		Type: capability.AccountTypeAPIKey,

		Credentials: map[string]any{
			"api_key":   "test-key",
			"pool_mode": true,
		},
	},
	}
}

func geminiCustomCodesAPIKeyAccount() *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{
		LoadLocation: time.LoadLocation,
		ID:           701,

		Platform: capability.PlatformGemini,

		Type: capability.AccountTypeAPIKey,

		Credentials: map[string]any{

			"api_key": "test-key",

			"custom_error_codes_enabled": true,

			"custom_error_codes": []any{float64(429)},
		},
	},
	}
}

func newGeminiNativeTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", strings.NewReader("{}"))
	return c, rec
}

func TestGeminiForwardNative_PoolModeSkipped400PassthroughRealStatus(t *testing.T) {

	upstreamBody := geminiSkippedTestUpstreamBody()
	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, upstreamBody)
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiPoolModeAPIKeyAccount(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "池模式 400 不应换号")
	require.Contains(t, err.Error(), "gemini upstream error: 400")
	require.Equal(t, http.StatusBadRequest, rec.Code, "状态码应保真为上游 400")
	require.Equal(t, upstreamBody, rec.Body.String(), "响应体应原样透传")
}

func TestGeminiForwardNative_PoolModeSkipped503Failover(t *testing.T) {

	svc, _ := newGeminiErrorFixture(http.StatusServiceUnavailable, `{"error":{"message":"Upstream service temporarily unavailable"}}`)
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiPoolModeAPIKeyAccount(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "池模式 503 应换号")
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Zero(t, rec.Body.Len(), "换号场景不应写客户端响应")
}

func TestGeminiForwardNative_CustomCodesMiss400HiddenAs500(t *testing.T) {

	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, geminiSkippedTestUpstreamBody())
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiCustomCodesAPIKeyAccount(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not in custom error codes")
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Upstream gateway error", errObj["message"])
	require.NotContains(t, rec.Body.String(), geminiSkippedTestUpstreamMsg, "上游细节不应透传给客户端")
}

func TestGeminiForwardNative_CustomCodesMiss500Failover(t *testing.T) {

	svc, _ := newGeminiErrorFixture(http.StatusInternalServerError, `{"error":{"message":"internal"}}`)
	c, rec := newGeminiNativeTestContext(t)

	result, err := svc.ForwardNative(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiCustomCodesAPIKeyAccount(), "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "自定义错误码未命中的 500 应换号")
	require.Equal(t, http.StatusInternalServerError, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount, "非池模式不应同账号重试")
	require.Zero(t, rec.Body.Len())
}

func TestGeminiForwardAsChatCompletions_CustomCodesMiss400HiddenAs500(t *testing.T) {

	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, geminiSkippedTestUpstreamBody())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiCustomCodesAPIKeyAccount(), body)

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

func TestGeminiForwardAsChatCompletions_PoolMode400KeepsUpstreamMessage(t *testing.T) {

	svc, _ := newGeminiErrorFixture(http.StatusBadRequest, geminiSkippedTestUpstreamBody())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))

	result, err := svc.ForwardAsChatCompletions(context.Background(), gatewayhttp.NewGoogleBoundary(c, svc.Options, false), geminiPoolModeAPIKeyAccount(), body)

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

func TestWriteGeminiMappedError_400KeepsUpstreamMessage(t *testing.T) {

	svc := newGeminiFixture(geminiDependencies{cfg: &googleforward.Options{}})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	err := gatewayhttp.NewGoogleBoundary(c, svc.Options, false).GeminiMappedError(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 702, Platform: capability.PlatformGemini}}, http.StatusBadRequest, "req-1", []byte(geminiSkippedTestUpstreamBody()))

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, geminiSkippedTestUpstreamMsg, errObj["message"], "应回传上游 message")
}
