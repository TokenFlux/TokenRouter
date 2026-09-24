package service

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

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
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

	status, errType, errMsg, matched := httpapi.ApplyErrorPassthroughRule(
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

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Invalid schema for field messages"}}`)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)
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

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
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

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, string(respBody), rec.Body.String())
	require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	require.True(t, httpapi.IsResponseCommitted(c))
	require.Equal(t, http.StatusBadRequest, c.MustGet(httpapi.OpsUpstreamStatusCodeKey))
}

func TestOpenAIHandleErrorResponse_TransientInvalidRequest400KeepsGatewayError(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_123 in your message.","type":"invalid_request_error"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)

	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "Upstream request failed")
}

func TestOpenAIHandleErrorResponse_OtherInvalidRequest400PassesThroughDetails(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Unknown parameter: input[0].namespace","type":"invalid_request_error","param":"input[0].namespace","code":"unknown_parameter"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)

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

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	err := svc.handleErrorResponsePassthrough(context.Background(), resp, c, account, []byte(`{"model":"gpt-5.5"}`), respBody)

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

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	_, err := svc.handleCompatErrorResponse(resp, c, account, writeChatCompletionsError, writeChatCompletionsErrorBody)

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

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	_, err := svc.handleCompatErrorResponse(resp, c, account, httpapi.WriteForwardAnthropicError, httpapi.WriteForwardAnthropicErrorBody)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"type":"error","error":{"message":"Invalid property name in input arguments","type":"invalid_request_error","param":"input[35].arguments","code":"property_name_above_max_length"}}`, rec.Body.String())
}

func TestOpenAIHandleErrorResponse_ContextWindow502KeepsMessageWithoutFailover(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"upstream_error","code":null}}`)
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 14, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)
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

func TestGeminiWriteGeminiMappedError_NoRuleKeepsDefault(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := withSchedulerParametersForTest(&GeminiMessagesCompatService{})
	respBody := []byte(`{"error":{"code":422,"message":"Invalid schema for field messages","status":"INVALID_ARGUMENT"}}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}}

	err := svc.writeGeminiMappedError(c, account, http.StatusUnprocessableEntity, "req-2", respBody)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "invalid_request_error", errField["type"])
	assert.Equal(t, "Upstream request failed", errField["message"])
}

func TestOpenAIHandleErrorResponse_AppliesRuleFor422(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{newNonFailoverPassthroughRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "OpenAI上游失败")})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	respBody := []byte(`{"error":{"message":"Invalid schema for field messages"}}`)
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)
	require.Error(t, err)
	assert.Equal(t, http.StatusTeapot, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "OpenAI上游失败", errField["message"])
}

func TestGeminiWriteGeminiMappedError_AppliesRuleFor422(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{newNonFailoverPassthroughRule(http.StatusUnprocessableEntity, "invalid schema", http.StatusTeapot, "Gemini上游失败")})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	svc := withSchedulerParametersForTest(&GeminiMessagesCompatService{})
	respBody := []byte(`{"error":{"code":422,"message":"Invalid schema for field messages","status":"INVALID_ARGUMENT"}}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}}

	err := svc.writeGeminiMappedError(c, account, http.StatusUnprocessableEntity, "req-1", respBody)
	require.Error(t, err)
	assert.Equal(t, http.StatusTeapot, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	errField, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errField["type"])
	assert.Equal(t, "Gemini上游失败", errField["message"])
}

func TestApplyErrorPassthroughRule_SkipMonitoringSetsContextKey(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	rule := newNonFailoverPassthroughRule(http.StatusBadRequest, "prompt is too long", http.StatusBadRequest, "上下文超限")
	rule.SkipMonitoring = true

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{rule})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	_, _, _, matched := httpapi.ApplyErrorPassthroughRule(
		c,
		capability.PlatformAnthropic,
		http.StatusBadRequest,
		[]byte(`{"error":{"message":"prompt is too long"}}`),
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)

	assert.True(t, matched)
	v, exists := c.Get(httpapi.OpsSkipPassthroughKey)
	assert.True(t, exists, "OpsSkipPassthroughKey should be set when skip_monitoring=true")
	boolVal, ok := v.(bool)
	assert.True(t, ok, "value should be bool")
	assert.True(t, boolVal)
}

func TestApplyErrorPassthroughRule_NoSkipMonitoringDoesNotSetContextKey(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	rule := newNonFailoverPassthroughRule(http.StatusBadRequest, "prompt is too long", http.StatusBadRequest, "上下文超限")
	rule.SkipMonitoring = false

	ruleSvc := newErrorRulesTestService([]*errorpolicy.ErrorPassthroughRule{rule})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	_, _, _, matched := httpapi.ApplyErrorPassthroughRule(
		c,
		capability.PlatformAnthropic,
		http.StatusBadRequest,
		[]byte(`{"error":{"message":"prompt is too long"}}`),
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)

	assert.True(t, matched)
	_, exists := c.Get(httpapi.OpsSkipPassthroughKey)
	assert.False(t, exists, "OpsSkipPassthroughKey should NOT be set when skip_monitoring=false")
}

// ---- ResponseCommittedKey: service 层写完错误响应后标记，handler 层检查跳过兜底写入 ----

func TestOpenAIHandleErrorResponse_SetsResponseCommitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":{"message":"rate limit exceeded"}}`))),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 101, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil)
	require.Error(t, err)
	assert.True(t, httpapi.IsResponseCommitted(c), "OpenAI non-failover path must mark response committed")
}

func TestGeminiWriteGeminiMappedError_SetsResponseCommitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := withSchedulerParametersForTest(&GeminiMessagesCompatService{})
	body := []byte(`{"error":{"message":"invalid field"}}`)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 102, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}}

	err := svc.writeGeminiMappedError(c, account, http.StatusBadRequest, "req-99", body)
	require.Error(t, err)
	assert.True(t, httpapi.IsResponseCommitted(c), "Gemini path must mark response committed")
}

func newNonFailoverPassthroughRule(statusCode int, keyword string, respCode int, customMessage string) *errorpolicy.ErrorPassthroughRule {
	return &errorpolicy.ErrorPassthroughRule{
		ID:              1,
		Name:            "non-failover-rule",
		Enabled:         true,
		Priority:        1,
		ErrorCodes:      []int{statusCode},
		Keywords:        []string{keyword},
		MatchMode:       errorpolicy.MatchModeAll,
		PassthroughCode: false,
		ResponseCode:    &respCode,
		PassthroughBody: false,
		CustomMessage:   &customMessage,
	}
}
