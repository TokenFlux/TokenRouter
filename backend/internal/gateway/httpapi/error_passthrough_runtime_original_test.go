package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyErrorPassthroughRule_NoBoundService(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c,
		capability.PlatformAnthropic,
		http.StatusUnprocessableEntity,
		[]byte(`{"error":{"message":"invalid schema"}}`),
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)

	assert.False(t, matched)
	assert.Equal(t, http.StatusBadGateway, status)
	assert.Equal(t, "upstream_error", errType)
	assert.Equal(t, "Upstream request failed", errMsg)
}

func TestOpenAIHandleErrorResponse_NoRuleKeepsDefault(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Invalid schema for field messages"}}`)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadGateway, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "Upstream request failed", errField["message"])
}

func TestOpenAIHandleErrorResponse_InvalidRequest400PassesThrough(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header: http.Header{
			"Content-Type": {"application/json; charset=utf-8"},
			"X-Request-Id": {"req_invalid_arguments"},
		},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, string(respBody), rec.Body.String())
	require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, http.StatusBadRequest, c.MustGet(OpsUpstreamStatusCodeKey))
}

func TestOpenAIHandleErrorResponse_TransientInvalidRequest400KeepsGatewayError(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_123 in your message.","type":"invalid_request_error"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)

	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "Upstream request failed")
}

func TestOpenAIHandleErrorResponse_OtherInvalidRequest400PassesThroughDetails(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Unknown parameter: input[0].namespace","type":"invalid_request_error","param":"input[0].namespace","code":"unknown_parameter"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "unknown_parameter", gjson.Get(rec.Body.String(), "error.code").String())
	require.Equal(t, "input[0].namespace", gjson.Get(rec.Body.String(), "error.param").String())
	require.Equal(t, "Unknown parameter: input[0].namespace", gjson.Get(rec.Body.String(), "error.message").String())
}

func TestOpenAIHandleErrorResponsePassthrough_InvalidRequest400PassesThrough(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	err := svc.PassthroughError(context.Background(), resp, c, account, []byte(`{"model":"gpt-5.5"}`), respBody)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, string(respBody), rec.Body.String())
	require.Contains(t, rec.Body.String(), "property_name_above_max_length")
	require.Contains(t, rec.Body.String(), "input[35].arguments")
}

func TestOpenAIHandleCompatErrorResponse_InvalidRequest400PreservesDetails(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	_, err := svc.CompatError(resp, c, account, WriteForwardChatError, WriteForwardChatErrorBody)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, string(respBody), rec.Body.String())
	require.Contains(t, rec.Body.String(), "property_name_above_max_length")
	require.Contains(t, rec.Body.String(), "input[35].arguments")
}

func TestOpenAIHandleCompatMessagesErrorResponse_InvalidRequest400PreservesDetails(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	_, err := svc.CompatError(resp, c, account, WriteForwardAnthropicError, WriteForwardAnthropicErrorBody)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"type":"error","error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`, rec.Body.String())
}

func TestOpenAIHandleErrorResponse_ContextWindow502KeepsMessageWithoutFailover(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"upstream_error","code":null}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 14, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	assert.Equal(t, http.StatusBadGateway, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "Your input exceeds the context window of this model. Please adjust your input and try again.", errField["message"])
}

func TestOpenAIHandleErrorResponse_AppliesRuleFor422(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{gatewaytestkit.NonFailoverRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "OpenAI上游失败")})
	BindErrorPassthroughService(c, ruleSvc)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	respBody := []byte(`{"error":{"message":"Invalid schema for field messages"}}`)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)
	require.Error(t, err)
	assert.Equal(t, http.StatusTeapot, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "OpenAI上游失败", errField["message"])
}

func TestApplyErrorPassthroughRule_SkipMonitoringSetsContextKey(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	rule := gatewaytestkit.NonFailoverRule(http.StatusBadRequest, "prompt is too long", http.StatusBadRequest, "上下文超限")
	rule.SkipMonitoring = true

	ruleSvc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, ruleSvc)

	_, _, _, matched := ApplyErrorPassthroughRule(
		c,
		capability.PlatformAnthropic,
		http.StatusBadRequest,
		[]byte(`{"error":{"message":"prompt is too long"}}`),
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)

	assert.True(t, matched)
	v, exists := c.Get(OpsSkipPassthroughKey)
	assert.True(t, exists, "OpsSkipPassthroughKey should be set when skip_monitoring=true")
	boolVal, ok := v.(bool)
	assert.True(t, ok, "value should be bool")
	assert.True(t, boolVal)
}

func TestApplyErrorPassthroughRule_NoSkipMonitoringDoesNotSetContextKey(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	rule := gatewaytestkit.NonFailoverRule(http.StatusBadRequest, "prompt is too long", http.StatusBadRequest, "上下文超限")
	rule.SkipMonitoring = false

	ruleSvc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, ruleSvc)

	_, _, _, matched := ApplyErrorPassthroughRule(
		c,
		capability.PlatformAnthropic,
		http.StatusBadRequest,
		[]byte(`{"error":{"message":"prompt is too long"}}`),
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)

	assert.True(t, matched)
	_, exists := c.Get(OpsSkipPassthroughKey)
	assert.False(t, exists, "OpsSkipPassthroughKey should NOT be set when skip_monitoring=false")
}

// ---- ResponseCommittedKey: service 层写完错误响应后标记，handler 层检查跳过兜底写入 ----

func TestOpenAIHandleErrorResponse_SetsResponseCommitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := newResponseOutputForTest(OpenAIResponseOptions{})
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":{"message":"rate limit exceeded"}}`))),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 101, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.ResponseError(context.Background(), resp, c, account, nil)
	require.Error(t, err)
	assert.True(t, IsResponseCommitted(c), "OpenAI non-failover path must mark response committed")
}
