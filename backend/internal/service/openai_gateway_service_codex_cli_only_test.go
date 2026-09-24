package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountpolicy "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGetAPIKeyIDFromContext(t *testing.T) {

	t.Run("context 为 nil", func(t *testing.T) {
		require.Equal(t, int64(0), gatewayhttp.APIKeyIDFromContext(nil))
	})

	t.Run("上下文没有 api_key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		require.Equal(t, int64(0), gatewayhttp.APIKeyIDFromContext(c))
	})

	t.Run("api_key 类型错误", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("api_key", "not-api-key")
		require.Equal(t, int64(0), gatewayhttp.APIKeyIDFromContext(c))
	})

	t.Run("api_key 指针为空", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		var k *apikey.APIKey
		c.Set("api_key", k)
		require.Equal(t, int64(0), gatewayhttp.APIKeyIDFromContext(c))
	})

	t.Run("正常读取 api_key_id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("api_key", &apikey.APIKey{ID: 12345})
		require.Equal(t, int64(12345), gatewayhttp.APIKeyIDFromContext(c))
	})
}

func TestLogCodexCLIOnlyDetection_NilSafety(t *testing.T) {
	// 不校验日志内容，仅保证在 nil 入参下不会 panic。
	require.NotPanics(t, func() {
		gatewayhttp.LogCodexCLIOnlyDetection(context.TODO(), nil, nil, 0, accountpolicy.CodexClientRestrictionDetectionResult{Enabled: true, Matched: false, Reason: "test"}, nil)
		gatewayhttp.LogCodexCLIOnlyDetection(context.Background(), nil, nil, 0, accountpolicy.CodexClientRestrictionDetectionResult{Enabled: false, Matched: false, Reason: "disabled"}, nil)
	})
}

func TestLogCodexCLIOnlyDetection_OnlyLogsRejected(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	account := &gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, ID: 1001}}
	gatewayhttp.LogCodexCLIOnlyDetection(context.Background(), nil, account, 2002, accountpolicy.CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: true,
		Reason:  accountpolicy.CodexClientRestrictionReasonMatchedUA,
	}, nil)
	gatewayhttp.LogCodexCLIOnlyDetection(context.Background(), nil, account, 2002, accountpolicy.CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: false,
		Reason:  accountpolicy.CodexClientRestrictionReasonNotMatchedUA,
	}, nil)

	require.False(t, logSink.ContainsMessage("OpenAI codex_cli_only 允许官方客户端请求"))
	require.True(t, logSink.ContainsMessage("OpenAI codex_cli_only 拒绝非官方客户端请求"))
}

func TestLogCodexCLIOnlyDetection_RejectedIncludesRequestDetails(t *testing.T) {

	logSink, restore := captureStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?trace=1", bytes.NewReader(nil))
	c.Request.RemoteAddr = "172.18.0.1:54321"
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0 (Windows 10.0.19045; x86_64) unknown")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Real-IP", "203.0.113.42")
	c.Request.Header.Set("OpenAI-Beta", "assistants=v2")

	body := []byte(`{"model":"gpt-5.2","stream":false,"prompt_cache_key":"pc-123","access_token":"secret-token","input":[{"type":"text","text":"hello"}]}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, ID: 1001}}
	gatewayhttp.LogCodexCLIOnlyDetection(context.Background(), c, account, 2002, accountpolicy.CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: false,
		Reason:  accountpolicy.CodexClientRestrictionReasonNotMatchedUA,
	}, body)

	require.True(t, logSink.ContainsFieldValue("request_user_agent", "codex_cli_rs/0.98.0 (Windows 10.0.19045; x86_64) unknown"))
	require.True(t, logSink.ContainsFieldValue("request_model", "gpt-5.2"))
	require.True(t, logSink.ContainsFieldValue("request_query", "trace=1"))
	require.True(t, logSink.ContainsFieldValue("request_client_ip", "203.0.113.42"))
	require.True(t, logSink.ContainsFieldValue("request_remote_addr", "172.18.0.1:54321"))
	require.True(t, logSink.ContainsFieldValue("request_prompt_cache_key_sha256", upstreamcore.HashSensitiveValueForLog("pc-123")))
	require.True(t, logSink.ContainsFieldValue("request_headers", "openai-beta"))
	require.True(t, logSink.ContainsField("request_body_size"))
	require.False(t, logSink.ContainsField("request_body_preview"))
}

func TestLogOpenAIInstructionsRequiredDebug_LogsRequestDetails(t *testing.T) {

	logSink, restore := captureStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?trace=1", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "curl/8.0")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("OpenAI-Beta", "assistants=v2")

	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"prompt_cache_key":"pc-abc","access_token":"secret-token","input":[{"type":"text","text":"hello"}]}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, ID: 1001, Name: "codex max套餐"}}

	gatewayhttp.LogOpenAIInstructionsRequiredDebug(
		context.Background(),
		c,
		account,
		http.StatusBadRequest,
		"Instructions are required",
		body,
		[]byte(`{"error":{"message":"Instructions are required","type":"invalid_request_error","param":"instructions","code":"missing_required_parameter"}}`),
	)

	require.True(t, logSink.ContainsMessageAtLevel("OpenAI 上游返回 Instructions are required，已记录请求详情用于排查", "warn"))
	require.True(t, logSink.ContainsFieldValue("request_user_agent", "curl/8.0"))
	require.True(t, logSink.ContainsFieldValue("request_model", "gpt-5.1-codex"))
	require.True(t, logSink.ContainsFieldValue("request_query", "trace=1"))
	require.True(t, logSink.ContainsFieldValue("account_name", "codex max套餐"))
	require.True(t, logSink.ContainsFieldValue("request_headers", "openai-beta"))
	require.True(t, logSink.ContainsField("request_body_size"))
	require.False(t, logSink.ContainsField("request_body_preview"))
}

func TestLogOpenAIInstructionsRequiredDebug_NonTargetErrorSkipped(t *testing.T) {

	logSink, restore := captureStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "curl/8.0")
	body := []byte(`{"model":"gpt-5.1-codex","stream":false}`)

	gatewayhttp.LogOpenAIInstructionsRequiredDebug(
		context.Background(),
		c,
		&gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, ID: 1001}},
		http.StatusForbidden,
		"forbidden",
		body,
		[]byte(`{"error":{"message":"forbidden"}}`),
	)

	require.False(t, logSink.ContainsMessage("OpenAI 上游返回 Instructions are required，已记录请求详情用于排查"))
}

func TestIsOpenAITransientProcessingError(t *testing.T) {
	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadRequest,
		"An error occurred while processing your request.",
		nil,
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadRequest,
		"",
		[]byte(`{"error":{"message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_123 in your message."}}`),
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadRequest,
		"The selected model is at capacity. Please try again later.",
		nil,
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadRequest,
		"",
		[]byte(`{"error":{"code":"server_is_overloaded","message":"Please retry later.","type":"invalid_request_error"}}`),
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusServiceUnavailable,
		"",
		[]byte(`{"error":{"code":"slow_down","message":"Please retry later."}}`),
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusServiceUnavailable,
		"",
		[]byte(`{"error":{"message":"Our servers are currently overloaded. Please try again later."}}`),
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusServiceUnavailable,
		"Server is overloaded. Please try again later.",
		nil,
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadGateway,
		"",
		[]byte(`{"error":{"message":"Our servers are currently overloaded. Please try again later."}}`),
	))

	require.True(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadRequest,
		"",
		[]byte(`{"error":{"message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_123 in your message."}}`),
	))

	require.False(t, openai.IsOpenAITransientProcessingError(
		http.StatusBadRequest,
		"Missing required parameter: 'instructions'",
		[]byte(`{"error":{"message":"Missing required parameter: 'instructions'"}}`),
	))
}

func TestIsOpenAIContextWindowError(t *testing.T) {
	require.True(t, openai.IsOpenAIContextWindowError(
		"",
		[]byte(`{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"upstream_error","code":null}}`),
	))
	require.True(t, openai.IsOpenAIContextWindowError(
		"maximum context length exceeded",
		nil,
	))
	require.True(t, openai.IsOpenAIContextWindowError(
		"",
		[]byte(`maximum context length exceeded`),
	))
	require.False(t, openai.IsOpenAIContextWindowError(
		"context canceled",
		nil,
	))
	require.False(t, openai.IsOpenAIContextWindowError(
		"upstream unavailable",
		[]byte(`{"error":{"message":"upstream unavailable","code":"upstream_error"},"echo":"context_length_exceeded maximum context length"}`),
	))
}

func TestOpenAITransientAndCapacityClassificationIgnoresEchoedJSON(t *testing.T) {
	body := []byte(`{"error":{"message":"upstream unavailable","code":"upstream_error"},"echo":"server is overloaded; selected model is at capacity"}`)

	require.False(t, openai.IsOpenAITransientProcessingError(http.StatusBadRequest, "upstream unavailable", body))
	require.False(t, openai.IsOpenAIRequestScopedCapacityShed("upstream unavailable", body))

	plainText := []byte(`server is overloaded; please retry later`)
	require.True(t, openai.IsOpenAITransientProcessingError(http.StatusServiceUnavailable, "", plainText))
	require.True(t, openai.IsOpenAIRequestScopedCapacityShed("", plainText))
}

func TestShouldFailoverOpenAIUpstreamResponseContextWindow502(t *testing.T) {

	body := []byte(`{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"upstream_error","code":null}}`)

	require.False(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusBadGateway, "", body))
	require.True(t, gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusBadGateway, "temporary upstream outage", []byte(`{"error":{"message":"temporary upstream outage"}}`)))
	require.True(t, gatewayprovider.ShouldFailoverOpenAIResponse(
		http.StatusBadGateway,
		"temporary upstream outage",
		[]byte(`{"error":{"message":"temporary upstream outage"},"echo":"context_length_exceeded"}`),
	))
}

func TestOpenAIGatewayService_Forward_LogsInstructionsRequiredDetails(t *testing.T) {

	logSink, restore := captureStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?trace=1", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("OpenAI-Beta", "assistants=v2")

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-upstream"},
			},
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Missing required parameter: 'instructions'","type":"invalid_request_error","param":"instructions","code":"missing_required_parameter"}}`)),
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{ForceCodexCLI: false},
		},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, ID: 1001,
		Name:           "codex max套餐",
		Platform:       capability.PlatformOpenAI,
		Type:           capability.AccountTypeAPIKey,
		Concurrency:    1,
		Credentials:    map[string]any{"api_key": "sk-test"},
		Status:         billing.StatusActive,
		Schedulable:    true,
		RateMultiplier: f64p(1)},
	}
	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"input":[{"type":"text","text":"hello"}],"prompt_cache_key":"pc-forward","access_token":"secret-token"}`)

	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "missing_required_parameter", gjson.Get(rec.Body.String(), "error.code").String())
	require.Equal(t, "instructions", gjson.Get(rec.Body.String(), "error.param").String())
	require.Contains(t, err.Error(), "upstream error: 400")

	require.True(t, logSink.ContainsMessageAtLevel("OpenAI 上游返回 Instructions are required，已记录请求详情用于排查", "warn"))
	require.True(t, logSink.ContainsFieldValue("request_user_agent", "codex_cli_rs/0.1.0"))
	require.True(t, logSink.ContainsFieldValue("request_model", "gpt-5.1-codex"))
	require.True(t, logSink.ContainsFieldValue("request_headers", "openai-beta"))
	require.True(t, logSink.ContainsField("request_body_size"))
	require.False(t, logSink.ContainsField("request_body_preview"))
}

func TestOpenAIGatewayService_Forward_TransientProcessingErrorTriggersFailover(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.1.0")
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-processing-400"},
			},
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_123 in your message.","type":"invalid_request_error"}}`)),
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{ForceCodexCLI: false},
		},
		httpUpstream: upstream,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountpolicy.Record{LoadLocation: time.LoadLocation, ID: 1001,
		Name:           "codex max套餐",
		Platform:       capability.PlatformOpenAI,
		Type:           capability.AccountTypeAPIKey,
		Concurrency:    1,
		Credentials:    map[string]any{"api_key": "sk-test"},
		Status:         billing.StatusActive,
		Schedulable:    true,
		RateMultiplier: f64p(1)},
	}
	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"input":[{"type":"text","text":"hello"}]}`)

	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "An error occurred while processing your request")
	require.False(t, c.Writer.Written(), "service 层应返回 failover 错误给上层换号，而不是直接向客户端写响应")
}
