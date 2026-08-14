package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIInvalidFunctionParametersBody = `{"error":{` +
	`"message":"Invalid schema for function 'automation_update': expected an object.",` +
	`"type":"invalid_request_error",` +
	`"param":"input[8].tools[1].tools[2].parameters",` +
	`"code":"invalid_function_parameters"}}`

func newOpenAIUpstreamClientErrorTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func newOpenAIUpstreamClientErrorResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newOpenAIUpstreamClientErrorTestAccount() *Account {
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Name: "acct"}
}

func TestHandleErrorResponse_Deterministic400IsNotRewrappedAs502(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	svc := &OpenAIGatewayService{}

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		c, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, "invalid_function_parameters", gjson.Get(recorder.Body.String(), "error.code").String())
	require.Equal(t, "input[8].tools[1].tools[2].parameters", gjson.Get(recorder.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "automation_update")

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
}

func TestHandleErrorResponse_Deterministic400MatchesCompatSibling(t *testing.T) {
	svc := &OpenAIGatewayService{}
	nativeCtx, nativeRecorder := newOpenAIUpstreamClientErrorTestContext()
	_, nativeErr := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		nativeCtx, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)
	require.Error(t, nativeErr)

	compatCtx, _ := newOpenAIUpstreamClientErrorTestContext()
	var compatStatus int
	var compatType, compatMessage string
	writeError := func(_ *gin.Context, statusCode int, errType, message string) {
		compatStatus, compatType, compatMessage = statusCode, errType, message
	}
	_, compatErr := svc.handleCompatErrorResponse(
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		compatCtx, newOpenAIUpstreamClientErrorTestAccount(), writeError, writeChatCompletionsErrorBody,
	)
	require.Error(t, compatErr)
	require.Equal(t, compatStatus, nativeRecorder.Code)
	require.Equal(t, compatType, gjson.Get(nativeRecorder.Body.String(), "error.type").String())
	require.Equal(t, compatMessage, gjson.Get(nativeRecorder.Body.String(), "error.message").String())
}

func TestHandleErrorResponse_Transient400KeepsGenericGatewayError(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	svc := &OpenAIGatewayService{}
	body := `{"error":{"message":"An error occurred while processing your request. You can retry your request.","type":"invalid_request_error"}}`

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, body),
		c, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, "upstream_error", gjson.Get(recorder.Body.String(), "error.type").String())
}

func TestHandleErrorResponse_PoolRetryable400StillFailsOver(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	svc := &OpenAIGatewayService{}
	account := newOpenAIUpstreamClientErrorTestAccount()
	account.Type = AccountTypeAPIKey
	account.Credentials = map[string]any{
		"pool_mode":                    true,
		"pool_mode_retry_status_codes": []any{float64(http.StatusBadRequest)},
	}

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		c, account, nil,
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestHandleErrorResponse_PassthroughRuleWinsOverDeterministic400(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	ruleService := &ErrorPassthroughService{}
	ruleService.setLocalCache([]*model.ErrorPassthroughRule{
		newNonFailoverPassthroughRule(http.StatusBadRequest, "automation_update", http.StatusTeapot, "自定义文案"),
	})
	BindErrorPassthroughService(c, ruleService)
	svc := &OpenAIGatewayService{}

	_, err := svc.handleErrorResponse(
		context.Background(),
		newOpenAIUpstreamClientErrorResponse(http.StatusBadRequest, openAIInvalidFunctionParametersBody),
		c, newOpenAIUpstreamClientErrorTestAccount(), nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusTeapot, recorder.Code)
	require.Equal(t, "自定义文案", gjson.Get(recorder.Body.String(), "error.message").String())
}

func TestWriteOpenAIUpstreamClientError_UsesSafeFallbacks(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()

	writeOpenAIUpstreamClientError(c, http.StatusBadRequest, []byte(`<html>bad request</html>`), "")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, openAIUpstreamClientErrorFallbackType, gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, openAIUpstreamClientErrorFallbackMessage, gjson.Get(recorder.Body.String(), "error.message").String())
	require.False(t, gjson.Get(recorder.Body.String(), "error.code").Exists())
	require.False(t, gjson.Get(recorder.Body.String(), "error.param").Exists())
}

func TestWriteOpenAIUpstreamClientError_UsesSanitizedMessage(t *testing.T) {
	c, recorder := newOpenAIUpstreamClientErrorTestContext()
	body := []byte(`{"error":{"message":"failed at https://example.test?key=secret123","type":"invalid_request_error","code":"bad_value","param":"input"}}`)

	writeOpenAIUpstreamClientError(c, http.StatusBadRequest, body, "failed at https://example.test?key=***")

	require.Equal(t, "failed at https://example.test?key=***", gjson.Get(recorder.Body.String(), "error.message").String())
	require.Equal(t, "bad_value", gjson.Get(recorder.Body.String(), "error.code").String())
	require.Equal(t, "input", gjson.Get(recorder.Body.String(), "error.param").String())
	require.NotContains(t, recorder.Body.String(), "secret123")
}

func TestIsOpenAIDeterministicClientError(t *testing.T) {
	require.True(t, isOpenAIDeterministicClientError(http.StatusBadRequest, false))
	require.False(t, isOpenAIDeterministicClientError(http.StatusBadRequest, true))
	for _, statusCode := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
		http.StatusBadGateway,
	} {
		require.False(t, isOpenAIDeterministicClientError(statusCode, false))
	}
}
